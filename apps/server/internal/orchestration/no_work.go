package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RecordNoWork records that a worker is leaving the run without ever having
// held a lease on it: it started after the queue drained, or gave up polling
// an empty queue. It ran no tests and uploads no report, so the run's report
// group counts it toward total_reports_expected once the run completes.
//
// Returns ErrWorkerHasLeases when the worker was issued any lease on the run:
// it ran tests, and its report (or the lack of one) must decide the group.
// Idempotent on (run, gh_job_id). Returns the id of the run's report group,
// or uuid.Nil when no group matches the run's identity.
func (s *Store) RecordNoWork(ctx context.Context, run *Run, worker WorkerIdentity) (uuid.UUID, error) {
	var groupID uuid.UUID
	txErr := s.inTx(ctx, func(tx pgx.Tx) error {
		var leased bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM leases WHERE run_id = $1 AND gh_job_id = $2)`,
			run.ID, worker.GHJobID,
		).Scan(&leased); err != nil {
			return fmt.Errorf("check worker leases: %w", err)
		}
		if leased {
			return ErrWorkerHasLeases
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO orchestration_no_work_workers (run_id, gh_job_name, gh_job_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (run_id, gh_job_id) DO NOTHING
		`, run.ID, worker.GHJobName, worker.GHJobID); err != nil {
			return fmt.Errorf("insert no-work worker: %w", err)
		}
		err := tx.QueryRow(ctx, `
			SELECT id FROM report_groups
			 WHERE repository = $1 AND commit_sha = $2 AND gh_run_id = $3 AND name = $4 AND gh_run_attempt = $5
		`, run.Identity.Repository, run.Identity.CommitSHA, run.Identity.GHRunID,
			run.Identity.Name, run.Identity.GHRunAttempt).Scan(&groupID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find report group: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return uuid.Nil, txErr
	}
	logEvent(ctx, s.Logger, "orchestration.worker.no_work", "orchestration worker left without work", run,
		slog.String("gh_job_id", worker.GHJobID),
		slog.String("gh_job_name", worker.GHJobName),
	)
	return groupID, nil
}
