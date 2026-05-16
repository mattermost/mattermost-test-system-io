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
		remoteAddr string // peer IP:port; default loopback
		wantStatus int
	}{
		// Happy paths — trusted peer with https header.
		{"https header from loopback passes", "/api/v1/anything", "https", "127.0.0.1:1234", http.StatusTeapot},
		{"HTTPS uppercase from loopback passes", "/api/v1/anything", "HTTPS", "127.0.0.1:1234", http.StatusTeapot},
		{"https from RFC1918 (10/8) passes", "/api/v1/anything", "https", "10.0.0.5:1234", http.StatusTeapot},
		{"https from IPv6 loopback passes", "/api/v1/anything", "https", "[::1]:1234", http.StatusTeapot},

		// Header alone is not enough — peer must be trusted.
		{"https header from PUBLIC IP rejected (spoof prevention)", "/api/v1/anything", "https", "8.8.8.8:443", http.StatusUpgradeRequired},
		{"http header rejected", "/api/v1/anything", "http", "127.0.0.1:1234", http.StatusUpgradeRequired},
		{"missing header rejected", "/api/v1/anything", "", "127.0.0.1:1234", http.StatusUpgradeRequired},

		// Health endpoints bypass the entire check.
		{"/health bypasses (no header, public peer)", "/health", "", "8.8.8.8:443", http.StatusTeapot},
		{"/healthz bypasses (no header)", "/healthz", "", "127.0.0.1:1234", http.StatusTeapot},
		{"/ready bypasses with http header", "/ready", "http", "127.0.0.1:1234", http.StatusTeapot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.RemoteAddr = tc.remoteAddr
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
