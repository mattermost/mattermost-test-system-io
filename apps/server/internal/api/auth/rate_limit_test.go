package authapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func asKey(id uuid.UUID) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/limited", nil)
	return req.WithContext(context.WithValue(req.Context(), subjectKey, Subject{Kind: "apikey", APIKeyID: id, Role: policy.RoleViewer}))
}

func TestRateLimit_perMinuteBudgetRefills(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)}
	limited := newRateLimiter(3, 10, clock.Now).middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	key := uuid.New()
	for i := range 3 {
		rec := httptest.NewRecorder()
		limited.ServeHTTP(rec, asKey(key))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	limited.ServeHTTP(rec, asKey(key))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("4th request: status = %d, Retry-After %q; want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}

	// Another key has its own budget.
	other := httptest.NewRecorder()
	limited.ServeHTTP(other, asKey(uuid.New()))
	if other.Code != http.StatusOK {
		t.Errorf("another key: status = %d, want 200", other.Code)
	}

	// A third of a minute refills one request.
	clock.advance(20 * time.Second)
	rec = httptest.NewRecorder()
	limited.ServeHTTP(rec, asKey(key))
	if rec.Code != http.StatusOK {
		t.Errorf("after refill: status = %d, want 200", rec.Code)
	}
}

func TestRateLimit_concurrencyCap(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 2)
	limited := newRateLimiter(100, 1, time.Now).middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	key := uuid.New()

	done := make(chan int)
	go func() {
		rec := httptest.NewRecorder()
		limited.ServeHTTP(rec, asKey(key))
		done <- rec.Code
	}()
	<-entered // the first request holds the only slot

	rec := httptest.NewRecorder()
	limited.ServeHTTP(rec, asKey(key))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("concurrent request: status = %d, want 429", rec.Code)
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Errorf("first request: status = %d, want 200", code)
	}

	// The slot is free again.
	rec = httptest.NewRecorder()
	go func() { <-entered }()
	limited.ServeHTTP(rec, asKey(key))
	if rec.Code != http.StatusOK {
		t.Errorf("after release: status = %d, want 200", rec.Code)
	}
}

func TestRateLimit_withoutSubjectIs401(t *testing.T) {
	limited := newRateLimiter(1, 1, time.Now).middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	limited.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/limited", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
