package kpi

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKPICache_SingleflightCollapsesConcurrentCalls(t *testing.T) {
	s := &Store{CacheTTL: 5 * time.Second, CacheMaxEntries: 16}

	var calls atomic.Int32
	gate := make(chan struct{})
	const workers = 50
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, err := cachedQuery(t.Context(), s, "same-key", func(context.Context) (int, error) {
				if calls.Add(1) == 1 {
					<-gate
				}
				return 42, nil
			})
			if err != nil {
				errs <- err
				return
			}
			if val != 42 {
				errs <- fmt.Errorf("got %d, want 42", val)
			}
		}()
	}

	time.Sleep(30 * time.Millisecond)
	close(gate)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("compute calls = %d, want 1", got)
	}
}

func TestKPICache_BoundedEvictionAndErrorNonCaching(t *testing.T) {
	s := &Store{CacheTTL: 5 * time.Second, CacheMaxEntries: 8}

	// Failed compute must not populate cache.
	boom := errors.New("db down")
	var attempts atomic.Int32
	for range 2 {
		_, err := cachedQuery(t.Context(), s, "err-key", func(context.Context) (int, error) {
			if attempts.Add(1) == 1 {
				return 0, boom
			}
			return 7, nil
		})
		if attempts.Load() == 1 && !errors.Is(err, boom) {
			t.Fatalf("first call err = %v, want boom", err)
		}
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2 (errors must not be cached)", attempts.Load())
	}

	// Inserting maxEntries + 20 keys must bound map size.
	for i := range 28 {
		key := fmt.Sprintf("key-%d", i)
		v, err := cachedQuery(t.Context(), s, key, func(context.Context) (int, error) { return i, nil })
		if err != nil || v != i {
			t.Fatalf("cachedQuery(%s) = (%d, %v)", key, v, err)
		}
	}
	c := s.getCache()
	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()
	if n > 8 {
		t.Fatalf("cache entries = %d, want <= 8", n)
	}
}

func TestKPIBulkhead_CapsConcurrencyAndRejectsTimeout(t *testing.T) {
	s := &Store{MaxConcurrentQueries: 2}

	var active, peak atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup

	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.withBulkhead(t.Context(), func(context.Context) (any, error) {
				cur := active.Add(1)
				for {
					prev := peak.Load()
					if cur <= prev || peak.CompareAndSwap(prev, cur) {
						break
					}
				}
				<-release
				active.Add(-1)
				return nil, nil
			})
			if err != nil {
				t.Errorf("holder err = %v", err)
			}
		}()
	}

	// Wait until both bulkhead slots are occupied.
	deadline := time.Now().Add(time.Second)
	for active.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if active.Load() != 2 {
		t.Fatalf("active = %d, want 2", active.Load())
	}

	// Third caller with short deadline must fail with ErrOverloaded.
	shortCtx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	_, err := s.withBulkhead(shortCtx, func(context.Context) (any, error) {
		t.Fatal("third caller entered full bulkhead")
		return nil, nil
	})
	if !errors.Is(err, ErrOverloaded) {
		t.Fatalf("blocked caller err = %v, want ErrOverloaded", err)
	}

	close(release)
	wg.Wait()
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak.Load())
	}
}
