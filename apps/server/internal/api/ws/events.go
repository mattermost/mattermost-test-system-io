// Package ws serves the /api/v1/ws WebSocket endpoint for live ingest progress
// updates. Anonymous connections are accepted; any authenticated session or
// OIDC subject on the request is scoped to the matching event stream, otherwise
// the subscriber receives the public (global) feed.
//
// In addition to the default subject/userID-scoped subscription that is
// installed at upgrade time, a connected client may register one or more
// per-run orchestration subscriptions by sending an inbound frame:
//
//	{ "type": "subscribe.orchestration", "identity": { ...composite identity... } }
//
// The server forwards only events whose Scope.Identity matches that composite
// identity to the connection. A matching `unsubscribe.orchestration` frame
// (same identity) tears that subscription down. A single connection may hold
// multiple orchestration subscriptions concurrently alongside the default
// feed; each is canceled independently.
package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
)

// wsPingInterval is how often the server sends a WebSocket ping. It must stay
// well below any proxy/load-balancer idle timeout (the ALB is 120s) so an
// otherwise-quiet subscription (no events for a while) isn't dropped as idle.
const wsPingInterval = 30 * time.Second

// hubAPI is the slice of *events.Hub the handler actually depends on. Defining
// it as an interface lets unit tests substitute a recording fake without
// pulling in the real Hub's broadcast semantics.
type hubAPI interface {
	Subscribe(userID *uuid.UUID, subject string) (<-chan events.Event, func())
	SubscribeIdentity(identity events.CompositeIdentity) (<-chan events.Event, func())
}

// Handler upgrades HTTP connections to WebSocket and streams events from the Hub.
type Handler struct {
	Hub *events.Hub
}

// inboundFrame is the JSON envelope for client-to-server control frames.
// `type` selects the action; `identity` is required for orchestration
// subscribe/unsubscribe and ignored otherwise. Unknown types are silently
// dropped so future frame types can be added without breaking older servers.
type inboundFrame struct {
	Type     string                    `json:"type"`
	Identity *events.CompositeIdentity `json:"identity,omitempty"`
}

const (
	frameSubscribeOrchestration   = "subscribe.orchestration"
	frameUnsubscribeOrchestration = "unsubscribe.orchestration"
)

// Events serves /api/v1/ws.
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	h.serve(r.Context(), conn, h.Hub)
}

// serve runs the read+forward loops for a single connection. Split out from
// Events so unit tests can drive it with a fake hub and an in-memory pair of
// websocket conns.
func (h *Handler) serve(ctx context.Context, conn *websocket.Conn, hub hubAPI) {
	ctx, cancelCtx := context.WithCancel(ctx)
	defer cancelCtx()

	// Single writer mutex: coder/websocket requires writes to be serialized
	// across goroutines. Every send to the wire goes through writeJSON.
	var writeMu sync.Mutex
	writeJSON := func(ev events.Event) error {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.Write(ctx, websocket.MessageText, b)
	}

	// forwardChan reads from a hub channel and relays each event to the wire
	// until the channel closes or the connection ctx is canceled. Tracked in
	// a WaitGroup so we can cleanly shut down on disconnect.
	var wg sync.WaitGroup
	forwardChan := func(ch <-chan events.Event) {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if err := writeJSON(ev); err != nil {
					cancelCtx()
					return
				}
			}
		}
	}

	// onceCancel wraps a hub cancel func so it's idempotent — the hub's
	// cancel closes the underlying channel, which would panic on a second
	// invocation. We may invoke a cancel via the unsubscribe frame and again
	// via the connection-shutdown sweep; sync.Once collapses both paths.
	onceCancel := func(c func()) func() {
		var once sync.Once
		return func() { once.Do(c) }
	}

	// Default subscription: subject/userID-scoped feed (or the global feed
	// when the connection is anonymous). Same behavior as before this handler
	// learned about orchestration subscriptions.
	var (
		userID      *uuid.UUID
		oidcSubject string
	)
	if sub, err := authapi.SubjectFromContext(ctx); err == nil {
		if sub.Kind == "session" {
			u := sub.UserID
			userID = &u
		}
		oidcSubject = sub.OIDCSubject
	}
	defaultCh, defaultCancelRaw := hub.Subscribe(userID, oidcSubject)
	defaultCancel := onceCancel(defaultCancelRaw)
	wg.Add(1)
	go forwardChan(defaultCh)

	// Keepalive: ping periodically so a connection with no events to forward
	// isn't dropped by an idle proxy / load-balancer timeout before the next
	// event arrives. coder/websocket's Ping is safe to call concurrently with
	// the forwarder writes. A failed ping tears the connection down.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pingCtx)
				cancel()
				if err != nil {
					cancelCtx()
					return
				}
			}
		}
	}()

	// Per-connection orchestration subscriptions. Keyed by composite identity
	// so a duplicate subscribe.orchestration frame for the same run is a
	// no-op (rather than leaking another subscriber).
	var (
		orchMu  sync.Mutex
		orchSub = map[events.CompositeIdentity]func(){}
	)
	subscribeOrch := func(id events.CompositeIdentity) {
		orchMu.Lock()
		if _, ok := orchSub[id]; ok {
			orchMu.Unlock()
			return
		}
		chRaw, cancelRaw := hub.SubscribeIdentity(id)
		cancel := onceCancel(cancelRaw)
		orchSub[id] = cancel
		orchMu.Unlock()
		wg.Add(1)
		go forwardChan(chRaw)
	}
	unsubscribeOrch := func(id events.CompositeIdentity) {
		orchMu.Lock()
		cancel, ok := orchSub[id]
		if ok {
			delete(orchSub, id)
		}
		orchMu.Unlock()
		if ok {
			cancel()
		}
	}
	cancelAllOrch := func() {
		orchMu.Lock()
		cancels := make([]func(), 0, len(orchSub))
		for id, c := range orchSub {
			cancels = append(cancels, c)
			delete(orchSub, id)
		}
		orchMu.Unlock()
		for _, c := range cancels {
			c()
		}
	}

	// Reader loop: parse client control frames and act on them. A failed
	// read (peer closed, ctx canceled) tears down the connection so the
	// forwarder goroutines unblock and exit.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			cancelCtx()
			break
		}
		var frame inboundFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			// Malformed frame: ignore and keep the connection open.
			continue
		}
		switch frame.Type {
		case frameSubscribeOrchestration:
			if frame.Identity == nil {
				continue
			}
			subscribeOrch(*frame.Identity)
		case frameUnsubscribeOrchestration:
			if frame.Identity == nil {
				continue
			}
			unsubscribeOrch(*frame.Identity)
		default:
			// Unknown frame type — ignore to keep the protocol forward-compatible.
		}
	}

	// Cancel all subscriptions so their channels close, then wait for
	// forwarder goroutines to drain. Without the wait, the caller's deferred
	// CloseNow could race with an in-flight Write.
	cancelAllOrch()
	defaultCancel()
	wg.Wait()
}
