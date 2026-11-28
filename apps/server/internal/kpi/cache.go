package kpi

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const defaultKPICacheMaxEntries = 512

type kpiCache struct {
	ttl        time.Duration
	maxEntries int

	mu      sync.Mutex
	entries map[string]*kpiCacheEntry
}

type kpiCacheEntry struct {
	done      chan struct{}
	expiresAt time.Time
	val       any
	err       error
}

func newKPICache(ttl time.Duration, maxEntries int) *kpiCache {
	if maxEntries <= 0 {
		maxEntries = defaultKPICacheMaxEntries
	}
	return &kpiCache{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    make(map[string]*kpiCacheEntry),
	}
}

func (c *kpiCache) get(
	ctx context.Context,
	key string,
	compute func(context.Context) (any, error),
) (any, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		c.mu.Unlock()
		select {
		case <-e.done:
			if time.Now().Before(e.expiresAt) && e.err == nil {
				return e.val, nil
			}
			c.mu.Lock()
			if cur, ok := c.entries[key]; ok && cur == e {
				delete(c.entries, key)
			}
			c.mu.Unlock()
			return c.get(ctx, key, compute)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if len(c.entries) >= c.maxEntries {
		c.evictLocked(time.Now())
	}
	e := &kpiCacheEntry{done: make(chan struct{})}
	c.entries[key] = e
	c.mu.Unlock()

	val, err := compute(ctx)
	e.val = val
	e.err = err
	e.expiresAt = time.Now().Add(c.ttl)
	close(e.done)

	if err != nil {
		c.mu.Lock()
		if c.entries[key] == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, err
	}

	time.AfterFunc(c.ttl, func() {
		c.mu.Lock()
		if cur, ok := c.entries[key]; ok && cur == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	})

	return val, nil
}

// evictLocked removes expired entries first, then the soonest-expiring completed entry.
func (c *kpiCache) evictLocked(now time.Time) {
	var oldestKey string
	var oldestExp time.Time
	hasOldest := false

	for k, e := range c.entries {
		select {
		case <-e.done:
			if !now.Before(e.expiresAt) || e.err != nil {
				delete(c.entries, k)
				continue
			}
			if !hasOldest || e.expiresAt.Before(oldestExp) {
				oldestKey = k
				oldestExp = e.expiresAt
				hasOldest = true
			}
		default:
		}
	}
	if len(c.entries) >= c.maxEntries && hasOldest {
		delete(c.entries, oldestKey)
	}
}

func (s *Store) getCache() *kpiCache {
	if s == nil || s.CacheTTL <= 0 {
		return nil
	}
	s.cacheOnce.Do(func() {
		s.cache = newKPICache(s.CacheTTL, s.CacheMaxEntries)
	})
	return s.cache
}

func (s *Store) getSem() chan struct{} {
	if s == nil || s.MaxConcurrentQueries <= 0 {
		return nil
	}
	s.semOnce.Do(func() {
		s.sem = make(chan struct{}, s.MaxConcurrentQueries)
	})
	return s.sem
}

func (s *Store) withBulkhead(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	sem := s.getSem()
	if sem == nil {
		return fn(ctx)
	}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
		return fn(ctx)
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrOverloaded, ctx.Err())
	}
}

func cachedQuery[T any](
	ctx context.Context,
	s *Store,
	key string,
	compute func(context.Context) (T, error),
) (T, error) {
	cache := s.getCache()
	if cache == nil {
		raw, err := s.withBulkhead(ctx, func(ctx context.Context) (any, error) {
			return compute(ctx)
		})
		if err != nil {
			var zero T
			return zero, err
		}
		return raw.(T), nil
	}

	raw, err := cache.get(ctx, key, func(ctx context.Context) (any, error) {
		return s.withBulkhead(ctx, func(ctx context.Context) (any, error) {
			return compute(ctx)
		})
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return raw.(T), nil
}

func filterCacheKey(prefix string, f FilterParams, extra string) string {
	return fmt.Sprintf(
		"%s:%s:%s:%q:%q:%q:%q:%q:%s:%s:%d:%d:%s",
		prefix,
		f.CurFromDate.Format("2006-01-02"),
		f.CurToDate.Format("2006-01-02"),
		f.App, f.Repo, f.Stage, f.Lane, f.Platform,
		f.Window, f.Sort, f.Page, f.Limit, extra,
	)
}
