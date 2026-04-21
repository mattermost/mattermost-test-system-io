package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

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
