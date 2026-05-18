package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
)

// Anonymous connections are accepted — the dashboard's WebSocket never
// attaches credentials.
func TestEvents_anonymousHandshakeSucceeds(t *testing.T) {
	hub := events.NewHub()
	h := &Handler{Hub: hub}

	srv := httptest.NewServer(http.HandlerFunc(h.Events))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")
}

// A non-WebSocket request (plain GET) must not panic — the upgrade fails and
// the handler returns silently.
func TestEvents_plainGETDoesNotPanic(t *testing.T) {
	h := &Handler{Hub: events.NewHub()}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	rec := httptest.NewRecorder()
	h.Events(rec, req) // must not panic
}

// fakeHub records SubscribeIdentity calls and exposes a way to fire events to
// each subscriber, plus tracks whether each subscription's cancel func has
// been invoked. Used by the inbound-frame tests so they can assert directly
// on the handler-to-Hub contract without standing up the real broadcaster.
type fakeHub struct {
	mu              sync.Mutex
	defaultCh       chan events.Event
	defaultCanceled bool
	identitySubs    []*fakeIdentitySub
}

type fakeIdentitySub struct {
	identity events.CompositeIdentity
	ch       chan events.Event
	canceled bool
}

func newFakeHub() *fakeHub {
	return &fakeHub{defaultCh: make(chan events.Event, 8)}
}

func (f *fakeHub) Subscribe(_ *uuid.UUID, _ string) (<-chan events.Event, func()) {
	return f.defaultCh, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.defaultCanceled {
			f.defaultCanceled = true
			close(f.defaultCh)
		}
	}
}

func (f *fakeHub) SubscribeIdentity(identity events.CompositeIdentity) (<-chan events.Event, func()) {
	sub := &fakeIdentitySub{
		identity: identity,
		ch:       make(chan events.Event, 8),
	}
	f.mu.Lock()
	f.identitySubs = append(f.identitySubs, sub)
	f.mu.Unlock()
	return sub.ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !sub.canceled {
			sub.canceled = true
			close(sub.ch)
		}
	}
}

func (f *fakeHub) snapshotIdentitySubs() []*fakeIdentitySub {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*fakeIdentitySub, len(f.identitySubs))
	copy(out, f.identitySubs)
	return out
}

// runHandler stands up an httptest server that drives Handler.serve with the
// caller-supplied hubAPI. Returning the WebSocket dial result lets tests send
// frames over a real wire so we exercise the actual JSON parser and reader
// loop.
func runHandler(t *testing.T, hub hubAPI) (*websocket.Conn, func()) {
	t.Helper()
	h := &Handler{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		h.serve(r.Context(), conn, hub)
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial: %v", err)
	}
	return conn, func() {
		_ = conn.Close(websocket.StatusNormalClosure, "done")
		srv.Close()
	}
}

// waitFor polls cond until it returns true or the deadline elapses. Used to
// bridge the brief gap between a client send and the handler-side Subscribe
// call, which happens on a separate goroutine.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition did not become true within deadline")
}

func sendFrame(t *testing.T, conn *websocket.Conn, frameType string, identity events.CompositeIdentity) {
	t.Helper()
	b, err := json.Marshal(inboundFrame{Type: frameType, Identity: &identity})
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func readEvent(t *testing.T, conn *websocket.Conn, timeout time.Duration) (events.Event, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return events.Event{}, false
	}
	var ev events.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	return ev, true
}

// A subscribe.orchestration frame results in a Hub.SubscribeIdentity call
// keyed by the same composite identity, and events pushed onto that
// subscription's channel reach the wire.
func TestEvents_subscribeOrchestrationTriggersHubSubscribe(t *testing.T) {
	hub := newFakeHub()
	conn, cleanup := runHandler(t, hub)
	defer cleanup()

	id := events.CompositeIdentity{
		Repository: "mattermost/mattermost", CommitSHA: "abc123",
		GHRunID: "111", Name: "playwright-full-enterprise", GHRunAttempt: "1",
	}
	sendFrame(t, conn, frameSubscribeOrchestration, id)

	// Wait for the handler to register the subscription with the Hub.
	waitFor(t, func() bool { return len(hub.snapshotIdentitySubs()) == 1 })

	subs := hub.snapshotIdentitySubs()
	if subs[0].identity != id {
		t.Fatalf("expected identity %v, got %v", id, subs[0].identity)
	}

	// Push an event onto the subscription's channel and confirm it reaches
	// the websocket wire (the forwarder goroutine should be running).
	subs[0].ch <- events.Event{Type: "orchestration.run.started", Timestamp: time.Now()}
	ev, ok := readEvent(t, conn, time.Second)
	if !ok {
		t.Fatalf("expected event on wire, got none")
	}
	if ev.Type != "orchestration.run.started" {
		t.Fatalf("unexpected event type: %q", ev.Type)
	}
}

// An unsubscribe.orchestration frame for a previously-subscribed identity
// invokes the Hub-returned cancel func.
func TestEvents_unsubscribeOrchestrationTriggersCancel(t *testing.T) {
	hub := newFakeHub()
	conn, cleanup := runHandler(t, hub)
	defer cleanup()

	id := events.CompositeIdentity{
		Repository: "mattermost/mattermost", CommitSHA: "abc123",
		GHRunID: "222", Name: "playwright-full-enterprise", GHRunAttempt: "1",
	}
	sendFrame(t, conn, frameSubscribeOrchestration, id)
	waitFor(t, func() bool { return len(hub.snapshotIdentitySubs()) == 1 })

	sendFrame(t, conn, frameUnsubscribeOrchestration, id)
	waitFor(t, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.identitySubs[0].canceled
	})
}

// Multiple concurrent orchestration subscriptions on a single connection
// must each route events independently: subscribing to A and B, publishing
// to A only, must surface exactly one event on the wire.
func TestEvents_multipleConcurrentOrchestrationSubscriptions(t *testing.T) {
	hub := newFakeHub()
	conn, cleanup := runHandler(t, hub)
	defer cleanup()

	idA := events.CompositeIdentity{
		Repository: "mattermost/mattermost", CommitSHA: "aaa",
		GHRunID: "100", Name: "groupA", GHRunAttempt: "1",
	}
	idB := events.CompositeIdentity{
		Repository: "mattermost/mattermost", CommitSHA: "bbb",
		GHRunID: "200", Name: "groupB", GHRunAttempt: "1",
	}

	sendFrame(t, conn, frameSubscribeOrchestration, idA)
	sendFrame(t, conn, frameSubscribeOrchestration, idB)
	waitFor(t, func() bool { return len(hub.snapshotIdentitySubs()) == 2 })

	subs := hub.snapshotIdentitySubs()
	var subA, subB *fakeIdentitySub
	for _, s := range subs {
		switch s.identity {
		case idA:
			subA = s
		case idB:
			subB = s
		}
	}
	if subA == nil || subB == nil {
		t.Fatalf("expected subs for both identities, got %+v", subs)
	}

	// Publish only on A's channel; the wire should see exactly one event.
	subA.ch <- events.Event{Type: "orchestration.unit.leased", Timestamp: time.Now()}
	ev, ok := readEvent(t, conn, time.Second)
	if !ok {
		t.Fatalf("expected one event from sub A, got none")
	}
	if ev.Type != "orchestration.unit.leased" {
		t.Fatalf("unexpected event type: %q", ev.Type)
	}

	// Now publish on B's channel; that event must also surface independently.
	subB.ch <- events.Event{Type: "orchestration.run.completed", Timestamp: time.Now()}
	ev, ok = readEvent(t, conn, time.Second)
	if !ok {
		t.Fatalf("expected event from sub B, got none")
	}
	if ev.Type != "orchestration.run.completed" {
		t.Fatalf("unexpected event type: %q", ev.Type)
	}

	// Cancel A; B's subscription must still be active.
	sendFrame(t, conn, frameUnsubscribeOrchestration, idA)
	waitFor(t, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return subA.canceled && !subB.canceled
	})
}
