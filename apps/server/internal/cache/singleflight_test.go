package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTLCache_singleFlightCollapsesConcurrentCallers(t *testing.T) {
	c := New(time.Minute)
	var computes atomic.Int32
	release := make(chan struct{})

	compute := func(context.Context) ([]byte, error) {
		computes.Add(1)
		<-release // hold all callers in-flight together
		return []byte("value"), nil
	}

	const n = 20
	var wg sync.WaitGroup
	results := make([][]byte, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, err := c.Get(context.Background(), "k", compute)
			if err != nil {
				t.Errorf("Get: %v", err)
			}
			results[i] = b
		}(i)
	}
	// Give goroutines time to arrive and block on the single in-flight compute.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := computes.Load(); got != 1 {
		t.Fatalf("compute ran %d times, want 1 (single-flight)", got)
	}
	for i, b := range results {
		if string(b) != "value" {
			t.Fatalf("result[%d] = %q, want \"value\"", i, string(b))
		}
	}
}

func TestTTLCache_servesCachedWithinTTL(t *testing.T) {
	c := New(time.Minute)
	var computes atomic.Int32
	compute := func(context.Context) ([]byte, error) {
		computes.Add(1)
		return []byte("v"), nil
	}
	for range 5 {
		if _, err := c.Get(context.Background(), "k", compute); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if got := computes.Load(); got != 1 {
		t.Fatalf("compute ran %d times, want 1 (cached within TTL)", got)
	}
}

func TestTTLCache_recomputesAfterTTL(t *testing.T) {
	c := New(20 * time.Millisecond)
	var computes atomic.Int32
	compute := func(context.Context) ([]byte, error) {
		computes.Add(1)
		return []byte("v"), nil
	}
	if _, err := c.Get(context.Background(), "k", compute); err != nil {
		t.Fatalf("Get: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := c.Get(context.Background(), "k", compute); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := computes.Load(); got != 2 {
		t.Fatalf("compute ran %d times, want 2 (recompute after TTL)", got)
	}
}

func TestTTLCache_doesNotCacheErrors(t *testing.T) {
	c := New(time.Minute)
	var computes atomic.Int32
	wantErr := errors.New("boom")
	compute := func(context.Context) ([]byte, error) {
		n := computes.Add(1)
		if n == 1 {
			return nil, wantErr
		}
		return []byte("ok"), nil
	}
	if _, err := c.Get(context.Background(), "k", compute); !errors.Is(err, wantErr) {
		t.Fatalf("first Get err = %v, want %v", err, wantErr)
	}
	// Second call must recompute (error was not cached) and succeed.
	b, err := c.Get(context.Background(), "k", compute)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if string(b) != "ok" {
		t.Fatalf("second Get = %q, want \"ok\"", string(b))
	}
	if got := computes.Load(); got != 2 {
		t.Fatalf("compute ran %d times, want 2", got)
	}
}
