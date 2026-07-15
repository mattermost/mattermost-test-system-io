package reports

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
)

// DefaultStalenessTimeout is the idle window after which an in-flight report
// group is flipped to `incomplete`. Pairs with the bursty nature of report
// uploads — shards upload only at queue-empty, so the gap between healthy
// shards' uploads can legitimately reach tens of minutes. 1 hour is well
// past the worst legitimate case while still surfacing actually-stuck runs
// within a single working hour.
const DefaultStalenessTimeout = 1 * time.Hour

// DefaultReaperInterval is how often the reaper sweeps for stale groups.
// Coarser than the orchestration reaper (5s) because the staleness threshold
// is hours, not seconds.
const DefaultReaperInterval = 1 * time.Minute

// stalenessEnvVar is the env override knob name. Value is interpreted as
// milliseconds; non-positive values fall back to DefaultStalenessTimeout.
const stalenessEnvVar = "TSIO_REPORTS_STALENESS_TIMEOUT_MS"

// reaperBatchLimit caps how many groups a single tick processes. Bounded
// work keeps a runaway tick from monopolising the pool.
const reaperBatchLimit = 100

// Reaper periodically flips report_groups whose last_upload_at is older
// than the configured staleness window from `in_progress` to `incomplete`.
// It is the ground truth for "this run is stuck"; the UI may render a
// shorter-window heuristic optimistically, but the DB only commits to the
// terminal state after the reaper has spoken.
type Reaper struct {
	Pool      *pgxpool.Pool
	Publisher *events.Publisher
	Logger    *slog.Logger
	Timeout   time.Duration
	Interval  time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// Start launches the reaper goroutine. Returns immediately; the goroutine
// runs until ctx is canceled or Stop is called. Calling Start twice is a
// no-op.
func (r *Reaper) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cancel != nil {
		return nil
	}

	if r.Timeout <= 0 {
		r.Timeout = resolveStalenessTimeout()
	}
	if r.Interval <= 0 {
		r.Interval = DefaultReaperInterval
	}
	if r.Logger == nil {
		r.Logger = slog.Default()
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.done = make(chan struct{})

	go r.loop(runCtx)
	return nil
}

// Stop cancels the reaper goroutine and blocks until it exits. Safe to call
// multiple times.
func (r *Reaper) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.cancel = nil
	r.done = nil
	r.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func (r *Reaper) loop(ctx context.Context) {
	defer close(r.done)

	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()

	r.Logger.Info("reports reaper started",
		slog.Duration("interval", r.Interval),
		slog.Duration("staleness_timeout", r.Timeout))

	for {
		select {
		case <-ctx.Done():
			r.Logger.Info("reports reaper stopped")
			return
		case <-ticker.C:
			if err := r.markIncomplete(ctx); err != nil {
				r.Logger.Error("reports reaper: mark incomplete", slog.String("error", err.Error()))
			}
		}
	}
}

// markIncomplete transitions in-progress groups whose last upload activity
// is older than the staleness window. Each transition publishes a
// ReportUpdated event so dashboards see the new badge without polling.
func (r *Reaper) markIncomplete(ctx context.Context) error {
	thresholdMs := int64(r.Timeout / time.Millisecond)
	// The per-group reports_count is folded into RETURNING so the event
	// payload needs no follow-up query per reaped row (avoids an N+1 and the
	// error it previously discarded).
	rows, err := r.Pool.Query(ctx, `
		UPDATE report_groups
		   SET status = 'incomplete', updated_at = now()
		 WHERE id IN (
		     SELECT id FROM report_groups
		      WHERE status = 'in_progress'
		        AND now() - last_upload_at > make_interval(secs => $1::double precision / 1000)
		      ORDER BY last_upload_at ASC
		      LIMIT $2
		 )
		RETURNING id, updated_at,
		          (SELECT count(*) FROM reports WHERE report_group_id = report_groups.id)
	`, thresholdMs, reaperBatchLimit)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var updatedAt time.Time
		var reportsCount int
		if err := rows.Scan(&id, &updatedAt, &reportsCount); err != nil {
			return err
		}
		r.Publisher.ReportUpdated(id, "incomplete", reportsCount, nil, updatedAt)
		r.Logger.Info("report group reaped to incomplete",
			slog.String("group_id", id.String()),
			slog.Int("reports_count", reportsCount))
	}
	return rows.Err()
}

// resolveStalenessTimeout reads TSIO_REPORTS_STALENESS_TIMEOUT_MS, falling
// back to DefaultStalenessTimeout on missing/invalid/non-positive values.
func resolveStalenessTimeout() time.Duration {
	v := os.Getenv(stalenessEnvVar)
	if v == "" {
		return DefaultStalenessTimeout
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	if err != nil || ms <= 0 {
		return DefaultStalenessTimeout
	}
	return time.Duration(ms) * time.Millisecond
}
