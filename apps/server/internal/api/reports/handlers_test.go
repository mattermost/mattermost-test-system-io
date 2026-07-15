package reports

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// newRouter mounts the report handlers on a chi router for tests. Pool is nil —
// every test in this file exercises a code path that short-circuits before any
// DB access.
func newRouter() chi.Router {
	h := &Handlers{
		Pool:             nil,
		Store:            nil,
		Publisher:        nil,
		MaxUploadBytes:   1 << 20,
		MaxArtifactBytes: 1 << 20,
		PresignTTL:       5 * time.Minute,
	}
	r := chi.NewRouter()
	r.Get("/api/v1/reports/{id}", h.Detail)
	r.Delete("/api/v1/reports/{id}", h.Delete)
	r.Get("/api/v1/reports/{id}/suites", h.Suites)
	r.Get("/api/v1/reports/{id}/cases", h.Cases)
	r.Get("/api/v1/reports/{id}/json", h.JSONFile)
	return r
}

func TestBadUUID_returns400(t *testing.T) {
	r := newRouter()
	paths := []struct {
		method string
		url    string
	}{
		{http.MethodGet, "/api/v1/reports/not-a-uuid"},
		{http.MethodDelete, "/api/v1/reports/not-a-uuid"},
		{http.MethodGet, "/api/v1/reports/not-a-uuid/suites"},
		{http.MethodGet, "/api/v1/reports/not-a-uuid/cases"},
		{http.MethodGet, "/api/v1/reports/not-a-uuid/json"},
	}
	for _, p := range paths {
		req := httptest.NewRequest(p.method, p.url, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d, want 400 (body=%s)", p.method, p.url, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s %s: content-type = %q, want application/json", p.method, p.url, got)
		}
	}
}

func TestParseLimit(t *testing.T) {
	cases := []struct {
		in        string
		dflt, max int
		want      int
	}{
		{"", 50, 200, 50},     // empty → default
		{"0", 50, 200, 50},    // non-positive → default
		{"-5", 50, 200, 50},   // negative → default
		{"abc", 50, 200, 50},  // non-numeric → default
		{"25", 50, 200, 25},   // in-range
		{"500", 50, 200, 200}, // over max → capped
		{"200", 50, 200, 200}, // exact max → max
	}
	for _, c := range cases {
		if got := parseLimit(c.in, c.dflt, c.max); got != c.want {
			t.Errorf("parseLimit(%q, %d, %d) = %d, want %d", c.in, c.dflt, c.max, got, c.want)
		}
	}
}
