package middleware

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const rateLimitMaxBuckets = 10_000

type ipBucket struct {
	tokens   float64
	lastSeen time.Time
}

// IPRateLimiter is a bounded in-memory per-IP token bucket middleware.
type IPRateLimiter struct {
	ratePerSec  float64
	burst       float64
	limitPerMin int
	now         func() time.Time

	mu      sync.Mutex
	buckets map[string]*ipBucket
}

// NewIPRateLimiter creates a per-IP token bucket limiter allowing limitPerMin requests/min
// with burst capacity. A non-positive limitPerMin disables throttling.
func NewIPRateLimiter(limitPerMin, burst int) *IPRateLimiter {
	if burst <= 0 && limitPerMin > 0 {
		burst = max(limitPerMin/6, 10)
	}
	return &IPRateLimiter{
		ratePerSec:  float64(limitPerMin) / 60.0,
		burst:       float64(burst),
		limitPerMin: limitPerMin,
		now:         time.Now,
		buckets:     make(map[string]*ipBucket),
	}
}

// Middleware enforces the token bucket per client IP, returning 429 on overflow.
func (l *IPRateLimiter) Middleware(next http.Handler) http.Handler {
	if l == nil || l.limitPerMin <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIPKey(r.RemoteAddr)
		allowed, remaining, retryAfter := l.allow(ip)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(l.limitPerMin))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		if !allowed {
			sec := max(int(math.Ceil(retryAfter.Seconds())), 1)
			w.Header().Set("Retry-After", strconv.Itoa(sec))
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprintf(w, `{"error":"RATE_LIMITED","message":"rate limit exceeded; retry after %ds"}`+"\n", sec)
				return
			}
			http.Error(w, "rate limit exceeded; please retry shortly", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *IPRateLimiter) allow(ip string) (allowed bool, remaining int, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[ip]
	if !ok {
		if len(l.buckets) >= rateLimitMaxBuckets {
			l.evictStaleLocked(now)
		}
		b = &ipBucket{tokens: l.burst, lastSeen: now}
		l.buckets[ip] = b
	} else {
		elapsed := now.Sub(b.lastSeen).Seconds()
		if elapsed > 0 {
			b.tokens = min(l.burst, b.tokens+elapsed*l.ratePerSec)
		}
		b.lastSeen = now
	}

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true, int(b.tokens), 0
	}
	need := (1.0 - b.tokens) / l.ratePerSec
	return false, 0, time.Duration(need * float64(time.Second))
}

func (l *IPRateLimiter) evictStaleLocked(now time.Time) {
	cutoff := now.Add(-5 * time.Minute)
	var oldestIP string
	var oldestSeen time.Time
	hasOldest := false
	for ip, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, ip)
			continue
		}
		if !hasOldest || b.lastSeen.Before(oldestSeen) {
			oldestIP = ip
			oldestSeen = b.lastSeen
			hasOldest = true
		}
	}
	if len(l.buckets) >= rateLimitMaxBuckets && hasOldest {
		delete(l.buckets, oldestIP)
	}
}

func clientIPKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil && host != "" {
		return host
	}
	if trimmed := strings.TrimSpace(remoteAddr); trimmed != "" {
		return trimmed
	}
	return "unknown"
}
