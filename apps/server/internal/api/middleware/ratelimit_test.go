package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestIPRateLimiter_BurstRefillAndBoundedMap(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	curNow := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	lim := NewIPRateLimiter(60, 3) // 1 req/sec refill, burst 3
	lim.now = curNow

	okH := lim.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 3 requests consume the burst cleanly.
	for i := range 3 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/kpi/summary", nil)
		req.RemoteAddr = "192.0.2.10:12345"
		okH.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("burst req %d status = %d, want 200", i, rec.Code)
		}
		if rec.Header().Get("X-RateLimit-Limit") != "60" {
			t.Fatalf("X-RateLimit-Limit = %q, want 60", rec.Header().Get("X-RateLimit-Limit"))
		}
	}

	// 4th immediate request is rejected with 429 JSON + headers.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/kpi/summary", nil)
	req.RemoteAddr = "192.0.2.10:12346"
	okH.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th req status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" || rec.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("missing rate-limit headers on 429: %+v", rec.Header())
	}

	// Advancing clock by 2s refills tokens and allows again.
	mu.Lock()
	now = now.Add(2 * time.Second)
	mu.Unlock()
	rec2 := httptest.NewRecorder()
	okH.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("after refill status = %d, want 200", rec2.Code)
	}

	// Different IP uses independent bucket and SSR path gets text/plain 429 after burst.
	for range 3 {
		r := httptest.NewRecorder()
		q := httptest.NewRequest(http.MethodGet, "/kpi", nil)
		q.RemoteAddr = "198.51.100.5:999"
		okH.ServeHTTP(r, q)
		if r.Code != http.StatusOK {
			t.Fatalf("other IP status = %d, want 200", r.Code)
		}
	}
	rDeny := httptest.NewRecorder()
	qDeny := httptest.NewRequest(http.MethodGet, "/kpi", nil)
	qDeny.RemoteAddr = "198.51.100.5:999"
	okH.ServeHTTP(rDeny, qDeny)
	if rDeny.Code != http.StatusTooManyRequests {
		t.Fatalf("other IP 4th status = %d, want 429", rDeny.Code)
	}
}
