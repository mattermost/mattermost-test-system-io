package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireHTTPS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := RequireHTTPS(next)

	cases := []struct {
		name       string
		path       string
		proto      string // X-Forwarded-Proto value; empty = unset
		wantStatus int
	}{
		{"https header passes", "/api/v1/anything", "https", http.StatusTeapot},
		{"HTTPS header (uppercase) passes", "/api/v1/anything", "HTTPS", http.StatusTeapot},
		{"http header rejected", "/api/v1/anything", "http", http.StatusUpgradeRequired},
		{"missing header rejected", "/api/v1/anything", "", http.StatusUpgradeRequired},
		{"/health bypasses with missing header", "/health", "", http.StatusTeapot},
		{"/healthz bypasses with missing header", "/healthz", "", http.StatusTeapot},
		{"/ready bypasses with http header", "/ready", "http", http.StatusTeapot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}
