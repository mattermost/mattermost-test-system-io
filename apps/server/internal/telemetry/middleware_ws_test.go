package telemetry

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The telemetry middleware wraps ResponseWriter with statusRecorder. If
// Unwrap() is missing, http.ResponseController can't reach the underlying
// Hijacker and a WebSocket upgrade fails with 1006 (websocket closed before
// connection established). Regression test for that path.
func TestMiddleware_allowsWebSocketUpgrade(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Middleware(logger)

	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("websocket.Accept: %v", err)
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "ok")
	})

	srv := httptest.NewServer(mw(wsHandler))
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
