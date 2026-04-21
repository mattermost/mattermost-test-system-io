// Package ws serves the /api/v1/ws WebSocket endpoint for live ingest progress
// updates. Anonymous connections are accepted; any authenticated session or
// OIDC subject on the request is scoped to the matching event stream, otherwise
// the subscriber receives the public (global) feed.
package ws

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
)

// Handler upgrades HTTP connections to WebSocket and streams events from the Hub.
type Handler struct {
	Hub *events.Hub
}

// Events serves /api/v1/ws.
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	var (
		userID      *uuid.UUID
		oidcSubject string
	)
	// Opportunistic: if the request carries a valid session/OIDC subject (because
	// RequireAuth ran upstream on a protected variant), surface it for scoping.
	// When the WS is mounted on the public router, SubjectFromContext returns
	// ErrNotAuthenticated and both scopers stay zero-valued (global feed).
	if sub, err := authapi.SubjectFromContext(r.Context()); err == nil {
		if sub.Kind == "session" {
			u := sub.UserID
			userID = &u
		}
		oidcSubject = sub.OIDCSubject
	}

	ch, cancel := h.Hub.Subscribe(userID, oidcSubject)
	defer cancel()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
				return
			}
		}
	}
}
