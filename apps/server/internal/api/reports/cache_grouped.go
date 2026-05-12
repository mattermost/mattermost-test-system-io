package reports

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// groupedCache memoizes computed /reports/grouped response bodies per
// (limit, offset) pagination window for a short TTL. Two design points:
//
//  1. Single-flight per key: when the cache is cold (or expired) and N
//     concurrent goroutines arrive for the same key, exactly one runs
//     the expensive computation while the rest block on its result.
//     Avoids the thundering-herd a polling dashboard would otherwise
//     produce against the DB on a popular page.
//
//  2. Bounded freshness: a 5-second TTL is short enough that staleness
//     is bounded by one polling tick (the dashboard polls every 5s
//     while in-progress runs exist), and long enough that hundreds of
//     concurrent tabs collapse onto one DB hit per TTL window.
//
// Per-pod, in-memory. Pod restarts cold-start; a single missed cache hit
// is fine. Cross-pod consistency is not required — the cache merely
// shapes load, not authoritative state.
type groupedCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]*groupedCacheEntry // key = "limit:offset"
}

type groupedCacheEntry struct {
	// done is closed when the in-flight computation completes. Readers
	// arriving while compute is in-flight wait on done; writers
	// (post-compute) check expiresAt to decide whether to recompute.
	done      chan struct{}
	expiresAt time.Time

	body []byte // marshaled JSON response body
	err  error
}

func newGroupedCache(ttl time.Duration) *groupedCache {
	return &groupedCache{
		ttl:     ttl,
		entries: map[string]*groupedCacheEntry{},
	}
}

// get returns the cached body for (limit, offset), or computes one via
// compute() if no fresh entry exists. Concurrent callers for the same
// key share one compute(); the others block on its result.
func (c *groupedCache) get(
	ctx context.Context,
	limit, offset int,
	compute func(context.Context) ([]byte, error),
) ([]byte, error) {
	key := fmt.Sprintf("%d:%d", limit, offset)

	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		// In-flight or fresh: wait on done, then return.
		c.mu.Unlock()
		select {
		case <-e.done:
			// Compare against time.Now() after the wait, not the
			// pre-wait clock — the wait can be arbitrarily long.
			if time.Now().Before(e.expiresAt) || e.err != nil {
				return e.body, e.err
			}
			// Expired. Evict this stale entry before retrying so the
			// recursive call sees a clean miss and starts a new
			// compute; otherwise it would race the time.AfterFunc
			// eviction and could loop on the same expired entry.
			c.mu.Lock()
			if cur, ok := c.entries[key]; ok && cur == e {
				delete(c.entries, key)
			}
			c.mu.Unlock()
			return c.get(ctx, limit, offset, compute)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Cache miss: install a pending entry, release the lock, compute.
	e := &groupedCacheEntry{done: make(chan struct{})}
	c.entries[key] = e
	c.mu.Unlock()

	body, err := compute(ctx)
	e.body = body
	e.err = err
	e.expiresAt = time.Now().Add(c.ttl)
	close(e.done)

	// On error, drop the entry so the next caller retries instead of
	// caching the failure for 5s.
	if err != nil {
		c.mu.Lock()
		if c.entries[key] == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, err
	}

	// Schedule eviction so the map doesn't grow unbounded across many
	// (limit, offset) keys over time.
	time.AfterFunc(c.ttl, func() {
		c.mu.Lock()
		if cur, ok := c.entries[key]; ok && cur == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	})

	return body, nil
}
