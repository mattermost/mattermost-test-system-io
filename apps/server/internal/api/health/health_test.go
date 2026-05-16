package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHealth_alwaysOK(t *testing.T) {
	healthHandler, _ := Handlers(nil, "v-test", nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	healthHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q", got)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q", body["status"])
	}
	if body["version"] != "v-test" {
		t.Errorf("version = %q", body["version"])
	}
}

// TestReady_failureDoesNotLeakError ensures the public /ready endpoint reports
// the failure category ("postgres") without disclosing the underlying error
// text (which can include host/port/auth detail).
func TestReady_failureDoesNotLeakError(t *testing.T) {
	// 127.0.0.1:1 is reserved and never bound, so Ping fails fast.
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	_, readyHandler := Handlers(pool, "v-test", nil)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	readyHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	var parsed map[string]string
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if parsed["status"] != "not-ready" {
		t.Errorf("status = %q, want not-ready", parsed["status"])
	}
	if parsed["reason"] != "postgres" {
		t.Errorf("reason = %q, want postgres", parsed["reason"])
	}
	if _, ok := parsed["detail"]; ok {
		t.Errorf("response leaks `detail` field: %q", body)
	}
	if strings.Contains(strings.ToLower(body), "127.0.0.1") {
		t.Errorf("response leaks connection target: %q", body)
	}
}
