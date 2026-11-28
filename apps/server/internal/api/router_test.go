package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRouter_SetsSecurityHeaders(t *testing.T) {
	r := NewRouter(Deps{Logger: slog.New(slog.DiscardHandler)})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", rec.Code)
	}
	for hdr, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(hdr); got != want {
			t.Errorf("%s = %q, want %q", hdr, got, want)
		}
	}
}
