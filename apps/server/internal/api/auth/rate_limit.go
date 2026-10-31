package authapi

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimit bounds each authenticated subject to perMinute requests (a token bucket that refills
// continuously) and to concurrent requests in flight. Over either limit the request gets 429 with
// Retry-After. It runs after RequireAuth; budgets are per server process and kept in memory.
func RateLimit(perMinute, concurrent int) func(http.Handler) http.Handler {
	return newRateLimiter(perMinute, concurrent, time.Now).middleware
}

type rateLimiter struct {
	perMinute  float64
	concurrent int
	now        func() time.Time

	mu       sync.Mutex
	subjects map[string]*subjectBudget
}

type subjectBudget struct {
	tokens   float64
	refilled time.Time
	inFlight int
}

func newRateLimiter(perMinute, concurrent int, now func() time.Time) *rateLimiter {
	return &rateLimiter{perMinute: float64(perMinute), concurrent: concurrent, now: now, subjects: map[string]*subjectBudget{}}
}

func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, err := SubjectFromContext(r.Context())
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		key := subjectID(sub)
		if wait, ok := l.acquire(key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		defer l.release(key)
		next.ServeHTTP(w, r)
	})
}

// acquire takes one token and one in-flight slot; when it can't, it says how long to wait.
func (l *rateLimiter) acquire(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.subjects[key]
	if b == nil {
		b = &subjectBudget{tokens: l.perMinute, refilled: now}
		l.subjects[key] = b
	}
	b.tokens = math.Min(l.perMinute, b.tokens+now.Sub(b.refilled).Minutes()*l.perMinute)
	b.refilled = now
	if b.inFlight >= l.concurrent {
		return time.Second, false
	}
	if b.tokens < 1 {
		return time.Duration((1 - b.tokens) / l.perMinute * float64(time.Minute)), false
	}
	b.tokens--
	b.inFlight++
	return 0, true
}

func (l *rateLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.subjects[key].inFlight--
}

// subjectID names the credential a budget belongs to.
func subjectID(s Subject) string {
	switch s.Kind {
	case "apikey":
		return "apikey:" + s.APIKeyID.String()
	case "oidc":
		return "oidc:" + s.OIDCSubject
	default:
		return s.Kind + ":" + s.UserID.String()
	}
}
