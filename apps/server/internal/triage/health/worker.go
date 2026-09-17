package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// Worker subscribes before returning so finalization events cannot race startup.
// Recovery scans cover the hub's deliberately lossy buffers and process downtime.
type Worker struct {
	Refresher *Refresher
	Logger    *slog.Logger
	Interval  time.Duration
	cancel    context.CancelFunc
	done      chan struct{}
	mu        sync.Mutex
}

// Start launches the in-binary projection worker. Calling it twice is harmless.
func (w *Worker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return
	}
	ctx, w.cancel = context.WithCancel(ctx)
	w.done = make(chan struct{})
	ch, unsubscribe := w.Refresher.Hub.Subscribe(nil, "")
	go func() {
		defer close(w.done)
		defer unsubscribe()
		interval := w.Interval
		if interval <= 0 {
			interval = time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		nightly := time.NewTicker(24 * time.Hour)
		defer nightly.Stop()
		refresh := func(since time.Time) {
			if err := w.Refresher.RefreshAll(ctx, since); err != nil && ctx.Err() == nil {
				logger := w.Logger
				if logger == nil {
					logger = slog.Default()
				}
				logger.ErrorContext(ctx, "triage health refresh failed", slog.String("error", err.Error()))
			}
		}
		refresh(time.Time{})
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				if e.Type != "report_updated" {
					continue
				}
				var p struct {
					Status string `json:"status"`
				}
				if json.Unmarshal(e.Payload, &p) == nil && p.Status == "completed" {
					refresh(time.Now().Add(-24 * time.Hour))
				}
			case <-ticker.C:
				refresh(time.Now().Add(-24 * time.Hour))
			case <-nightly.C:
				refresh(time.Time{})
			}
		}
	}()
}

// Stop cancels in-flight database work and waits for the goroutine to exit.
func (w *Worker) Stop() {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
