package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// RefreshGroupSummary recomputes the denormalized projection columns on
// report_groups for the given group, using the existing per-group
// aggregation helpers as the single source of truth. Called from every
// state-changing path:
//
//   - reports.Begin / reports.Register (initial baseline)
//   - reports.UploadJSON / UploadScreenshots (per-shard data arrives)
//   - orchestration.RecordCompletion (per-unit terminal state change)
//   - orchestration reaper (run terminal transition)
//
// Idempotent — re-running against the same state writes the same values.
// Runs as autocommit against the supplied pool; intentionally not inside
// the parent transaction so a projection write failure cannot roll back
// a successful upload. The idle catch-up job picks up rows where
// last_summary_at lags the source data and re-syncs.
//
// Returns a non-nil error only for unrecoverable failures; transient DB
// errors are returned to the caller, which is expected to log-and-ignore
// (the caller's primary work has already succeeded).
func RefreshGroupSummary(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) error {
	// Captured before the SELECTs so it brackets the source state we're
	// about to summarize. Used both as the new last_summary_at value and
	// as a compare-and-swap guard on the UPDATE: if a concurrent refresh
	// has already written a newer snapshot, our older one is dropped
	// rather than overwriting it.
	snapshotAt := time.Now().UTC()

	// Pull the group's identity (orchestration lookup needs it).
	g, err := fetchGroup(ctx, pool, groupID)
	if errors.Is(err, api.ErrNotFound) {
		// Group deleted between the parent commit and the projection
		// call; nothing to refresh.
		return nil
	}
	if err != nil {
		return fmt.Errorf("refresh_group_summary: load group: %w", err)
	}

	// Reuse the existing helpers. They are pure reads; calling them once
	// at write time costs the same as calling them once per /reports/grouped
	// request (the old path), but now scales with write volume instead of
	// poll volume.
	stats, err := aggregateGroupStats(ctx, pool, groupID)
	if err != nil {
		return fmt.Errorf("refresh_group_summary: stats: %w", err)
	}
	orch, err := newOrchestrationLookup().getForGroup(ctx, pool, g)
	if err != nil {
		return fmt.Errorf("refresh_group_summary: orchestration: %w", err)
	}
	stats = mergeOrchestrationTestsIntoStats(stats, orch)

	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("refresh_group_summary: marshal stats: %w", err)
	}
	var orchJSON []byte
	if orch != nil {
		orchJSON, err = json.Marshal(orch)
		if err != nil {
			return fmt.Errorf("refresh_group_summary: marshal orchestration: %w", err)
		}
	}

	// Total wall-clock = last_test_at − begin_at when both are known.
	// Phases (setup, first-pass, retest) can overlap due to per-failure
	// re-dispatch during the first pass, so summing them overcounts.
	// The end-to-end subtraction is always correct. See specs/reports-grouped-redesign.md.
	var totalDurationMs *int64
	if orch != nil && orch.Durations != nil {
		d := orch.Durations
		if d.LastTestAt != nil {
			ms := d.LastTestAt.Sub(d.BeginAt).Milliseconds()
			if ms < 0 {
				ms = 0
			}
			totalDurationMs = &ms
		}
	}

	// Drop a stale snapshot rather than overwriting a newer one: if two
	// refreshes for the same group race, the loser's read started earlier
	// and may be missing rows that the winner already captured.
	_, err = pool.Exec(ctx, `
		UPDATE report_groups
		   SET test_stats_json    = $2,
		       orchestration_json = $3,
		       reports_count      = $4,
		       total_duration_ms  = $5,
		       last_summary_at    = $6
		 WHERE id = $1
		   AND (last_summary_at IS NULL OR last_summary_at < $6)
	`, groupID, statsJSON, nullableJSON(orchJSON), stats.ReportsCount, totalDurationMs, snapshotAt)
	if err != nil {
		return fmt.Errorf("refresh_group_summary: update: %w", err)
	}
	return nil
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// Fill test_stats from orchestration when no shards uploaded.
func mergeOrchestrationTestsIntoStats(stats groupStats, orch *orchestrationSummary) groupStats {
	if orch == nil || orch.Tests == nil || stats.TotalCases > 0 {
		return stats
	}
	t := orch.Tests
	stats.TotalCases = t.Total
	stats.Passed = t.Passed
	stats.Failed = t.Failed
	stats.Skipped = t.Skipped
	stats.Flaky = t.Flaky
	return stats
}

// report_groups id for orchestration identity.
func GroupIDForOrchestrationIdentity(
	ctx context.Context,
	pool *pgxpool.Pool,
	repository, commitSHA, ghRunID, name, ghRunAttempt string,
) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("reports: nil pool")
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT id
		  FROM report_groups
		 WHERE repository = $1
		   AND commit_sha = $2
		   AND gh_run_id = $3
		   AND name = $4
		   AND gh_run_attempt = $5
		 LIMIT 1
	`, repository, commitSHA, ghRunID, name, ghRunAttempt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// Refresh projection; log failures.
func RefreshGroupSummaryBestEffort(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, groupID uuid.UUID) {
	if groupID == uuid.Nil {
		return
	}
	if err := RefreshGroupSummary(ctx, pool, groupID); err != nil && logger != nil {
		logger.Warn("refresh_group_summary failed",
			"group_id", groupID.String(),
			"error", err.Error())
	}
}

// Backfill stale projection rows.
func RefreshStaleGroupSummaries(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, limit int) error {
	if pool == nil || limit <= 0 {
		return nil
	}
	rows, err := pool.Query(ctx, `
		SELECT id
		  FROM report_groups
		 WHERE last_summary_at IS NULL
		    OR test_stats_json IS NULL
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		RefreshGroupSummaryBestEffort(ctx, pool, logger, id)
	}
	return rows.Err()
}

// refreshGroupSummaryBestEffort is the handler-side wrapper: calls
// RefreshGroupSummary, logs any error at WARN, never returns an error
// to the caller. Use from write paths where the projection update is
// secondary to the primary work.
func (h *Handlers) refreshGroupSummaryBestEffort(ctx context.Context, groupID uuid.UUID) {
	RefreshGroupSummaryBestEffort(ctx, h.Pool, h.Logger, groupID)
}
