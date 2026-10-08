// Package cache provides a small in-process, single-flight TTL cache for
// memoizing computed HTTP response bodies keyed by an arbitrary string.
//
// It generalizes the pattern already used for /reports/grouped: when many
// concurrent requests (e.g. hundreds of polling dashboard tabs) arrive for the
// same key while the entry is cold or expired, exactly one runs the expensive
// computation and the rest block on its result — collapsing a thundering herd
// into a single backing query per TTL window.
//
// Per-process and in-memory. Multi-pod deployments each keep their own cache;
// cross-pod consistency is not required because the cache only shapes load, it
// is not authoritative state. Errors are never cached: a failed compute drops
// the entry so the next caller retries.
package cache

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// computeTimeout bounds a shared compute run on its detached context so a
// wedged backing query can't pin a cache entry (and its waiters) forever.
const computeTimeout = 30 * time.Second

// Key builds an unambiguous cache key from parts using a length prefix per
// part. Unlike joining with a separator (e.g. "\x00"), no combination of
// attacker-controlled field contents — including embedded separators or NUL
// bytes — can collapse two distinct part lists onto the same key.
func Key(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(strconv.Itoa(len(p)))
		b.WriteByte(':')
		b.WriteString(p)
	}
	return b.String()
}

// TTLCache memoizes []byte bodies per string key for a fixed TTL, with
// single-flight semantics per key.
type TTLCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	// done is closed when the in-flight computation completes. Readers that
	// arrive while a compute is in flight wait on done, then check expiresAt.
	done      chan struct{}
	expiresAt time.Time

	body []byte
	err  error
}

// New returns a TTLCache with the given freshness window.
func New(ttl time.Duration) *TTLCache {
	return &TTLCache{ttl: ttl, entries: map[string]*entry{}}
}

// Get returns the cached body for key, or computes one via compute() when no
// fresh entry exists. Concurrent callers for the same key share a single
// compute(); the others block on its result. Errors are returned to all
// waiters and not cached.
func (c *TTLCache) Get(
	ctx context.Context,
	key string,
	compute func(context.Context) ([]byte, error),
) ([]byte, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		c.mu.Unlock()
		select {
		case <-e.done:
			// Re-check freshness after the wait, which may be arbitrarily long.
			if time.Now().Before(e.expiresAt) || e.err != nil {
				return e.body, e.err
			}
			// Expired: evict this stale entry, then retry with a clean miss.
			c.mu.Lock()
			if cur, ok := c.entries[key]; ok && cur == e {
				delete(c.entries, key)
			}
			c.mu.Unlock()
			return c.Get(ctx, key, compute)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Cache miss: install a pending entry, release the lock, compute.
	e := &entry{done: make(chan struct{})}
	c.entries[key] = e
	c.mu.Unlock()

	// Run the shared compute on a context detached from this caller's ctx: the
	// result is shared with every waiter on this key, so if the caller that
	// won the race disconnects, its cancellation must not poison the entry for
	// the others. A dedicated timeout keeps a wedged compute from pinning the
	// entry indefinitely. Waiters still honor their own ctx in the wait path.
	computeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
	defer cancel()
	body, err := compute(computeCtx)
	e.body = body
	e.err = err
	e.expiresAt = time.Now().Add(c.ttl)
	close(e.done)

	if err != nil {
		// Don't cache failures: drop the entry so the next caller retries.
		c.mu.Lock()
		if c.entries[key] == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, err
	}

	// Schedule eviction so the map doesn't grow unbounded across many keys.
	time.AfterFunc(c.ttl, func() {
		c.mu.Lock()
		if cur, ok := c.entries[key]; ok && cur == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	})

	return body, nil
}
