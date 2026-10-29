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
	// Results, when set, re-reads the results of shards whose extraction failed (see RetryStalledResults).
	Results *Handlers

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
			r.RetryStalledResults(ctx)
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
		RETURNING id, updated_at
	`, thresholdMs, reaperBatchLimit)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var updatedAt time.Time
		if err := rows.Scan(&id, &updatedAt); err != nil {
			return err
		}
		var reportsCount int
		_ = r.Pool.QueryRow(ctx,
			`SELECT count(*) FROM reports WHERE report_group_id = $1`, id).Scan(&reportsCount)
		r.Publisher.ReportUpdated(id, "incomplete", reportsCount, nil, updatedAt)
		r.Logger.Info("report group reaped to incomplete",
			slog.String("group_id", id.String()),
			slog.Int("reports_count", reportsCount))
	}
	return rows.Err()
}

// stalledResultsAge is how long a shard whose results could not be read waits before the reaper tries again.
const stalledResultsAge = time.Minute

// RetryStalledResults re-reads the stored results of shards whose extraction failed after its inline retries
// (an object-store outage), so they complete once the store is back without a re-upload. A failed attempt
// records the reason again, which puts the shard back at the end of the wait.
func (r *Reaper) RetryStalledResults(ctx context.Context) {
	if r.Results == nil {
		return
	}
	rows, err := r.Pool.Query(ctx, `
		SELECT id, report_group_id FROM reports
		 WHERE status = 'processing'
		   AND json_upload_status = 'completed'
		   AND starts_with(error_message, $1)
		   AND updated_at < now() - make_interval(secs => $2::double precision)
		 ORDER BY updated_at ASC
		 LIMIT $3`, resultsErrorPrefix, stalledResultsAge.Seconds(), reaperBatchLimit)
	if err != nil {
		r.Logger.Error("reports reaper: list stalled results", slog.String("error", err.Error()))
		return
	}
	type shard struct{ id, group uuid.UUID }
	var stalled []shard
	for rows.Next() {
		var s shard
		if err := rows.Scan(&s.id, &s.group); err != nil {
			rows.Close()
			r.Logger.Error("reports reaper: scan stalled results", slog.String("error", err.Error()))
			return
		}
		stalled = append(stalled, s)
	}
	rows.Close()
	for _, s := range stalled {
		if ctx.Err() != nil {
			return
		}
		r.Results.finalizeResults(s.group, s.id)
	}
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
