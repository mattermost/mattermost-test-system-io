package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DefaultReaperInterval is the fallback tick used when neither the Reaper
// struct nor the TSIO_ORCH_REAPER_INTERVAL_MS env var supplies a value. The
// 5 s cadence pairs with lazy lease expiration on each checkout: a tighter
// reaper would burn cycles for little gain, while a looser one would let
// reclaim latency creep above the second-level target.
const DefaultReaperInterval = 5 * time.Second

// reaperEnvVar is the env override knob name. Value is interpreted as a
// signed integer number of milliseconds; non-positive values fall back to
// DefaultReaperInterval.
const reaperEnvVar = "TSIO_ORCH_REAPER_INTERVAL_MS"

// reaperBatchLimit caps how many leases / runs a single tick processes.
// Bounded work keeps a runaway tick from monopolising the pool.
const reaperBatchLimit = 100

// Reaper periodically expires overdue leases and times out runs whose
// idle window has elapsed (no checkout / complete activity for
// idle_timeout_ms). It is a backstop: every checkout call also lazily
// expires matching leases inline for tighter latency.
type Reaper struct {
	Store     *Store
	Publisher *Publisher
	Logger    *slog.Logger
	Interval  time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// Start launches the reaper goroutine. It returns immediately; the goroutine
// runs until the supplied ctx is canceled or Stop is called. Calling Start
// twice is a no-op (the existing goroutine continues).
func (r *Reaper) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cancel != nil {
		return nil
	}

	if r.Interval <= 0 {
		r.Interval = resolveInterval()
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
// multiple times; calls after the first are no-ops.
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

	r.Logger.Info("orchestration reaper started", slog.Duration("interval", r.Interval))

	for {
		select {
		case <-ctx.Done():
			r.Logger.Info("orchestration reaper stopped")
			return
		case <-ticker.C:
			if err := r.expireOverdueLeases(ctx); err != nil {
				r.Logger.Error("reaper: expire overdue leases", slog.String("error", err.Error()))
			}
			if err := r.markTimedOutRuns(ctx); err != nil {
				r.Logger.Error("reaper: mark timed-out runs", slog.String("error", err.Error()))
			}
		}
	}
}

// expireOverdueLeases finds leases whose deadline has passed and marks them
// expired (returning their units to pending). One transaction per lease so a
// transient failure on one does not stall the rest.
func (r *Reaper) expireOverdueLeases(ctx context.Context) error {
	if r.Store == nil {
		return nil
	}
	rows, err := r.Store.Pool.Query(ctx, `
		SELECT id FROM leases
		 WHERE released_at IS NULL AND deadline < now()
		 ORDER BY deadline
		 LIMIT $1
	`, reaperBatchLimit)
	if err != nil {
		return fmt.Errorf("scan overdue leases: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range ids {
		expired, err := r.Store.expireOneLease(ctx, id)
		if err != nil {
			r.Logger.Warn("reaper: expire lease",
				slog.String("lease_id", id.String()),
				slog.String("error", err.Error()))
			continue
		}
		if expired != nil && expired.Lease != nil {
			logEventIdentity(ctx, r.Logger, "orchestration.lease.expired", "orchestration lease expired",
				expired.Lease.RunID, expired.Identity,
				slog.String("gh_job_id", expired.Lease.Worker.GHJobID),
				slog.String("gh_job_name", expired.Lease.Worker.GHJobName),
				slog.String("lease_id", expired.Lease.ID.String()),
				slog.Int("unit_count", len(expired.ReclaimedUnitIDs)),
			)
			logMetric(ctx, r.Logger, "orchestration_reclaims_total", "", 1)
			if r.Publisher != nil {
				r.Publisher.LeaseExpired(ctx, expired.Identity,
					expired.Lease.Worker.GHJobName, expired.Lease.Worker.GHJobID,
					*expired.Lease.ReleasedAt, expired.ReclaimedUnitIDs)
			}
		}
	}
	return nil
}

// markTimedOutRuns finds in-progress runs whose idle window has elapsed
// (last_activity_at + idle_timeout_ms < now()) and transitions them to
// 'timed_out'. Activity = a successful checkout or complete; the
// inactivity window resets on every such call.
func (r *Reaper) markTimedOutRuns(ctx context.Context) error {
	if r.Store == nil {
		return nil
	}
	rows, err := r.Store.Pool.Query(ctx, `
		SELECT id FROM orchestration_runs
		 WHERE status = 'in_progress'
		   AND last_activity_at + (idle_timeout_ms || ' milliseconds')::interval < now()
		 ORDER BY last_activity_at
		 LIMIT $1
	`, reaperBatchLimit)
	if err != nil {
		return fmt.Errorf("scan timed-out runs: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range ids {
		out, err := r.Store.markRunTimedOut(ctx, id)
		if err != nil {
			r.Logger.Warn("reaper: mark run timed-out",
				slog.String("run_id", id.String()),
				slog.String("error", err.Error()))
			continue
		}
		if out != nil && out.TerminalAt != nil {
			logEventIdentity(ctx, r.Logger, "orchestration.run.timed_out", "orchestration run timed out",
				id, out.Identity,
				slog.String("from_state", RunStatusInProgress),
				slog.String("to_state", RunStatusTimedOut),
				slog.Int("abandoned_count", out.AbandonedAdded),
				slog.Int("unit_count", out.Counts.Total),
			)
			if r.Publisher != nil {
				r.Publisher.RunTimedOut(ctx, out.Identity, *out.TerminalAt, out.Counts, out.AbandonedAdded)
			}
		}
	}
	return nil
}

// expireLeaseOutcome describes the side-effects of a single lease expiration.
type expireLeaseOutcome struct {
	Lease            *Lease
	Identity         CompositeIdentity
	ReclaimedUnitIDs []uuid.UUID
}

// expireOneLease releases one lease as 'expired', flips its leased units back
// to 'pending', and updates the run's counters. No-op if the lease is already
// released.
func (s *Store) expireOneLease(ctx context.Context, leaseID uuid.UUID) (*expireLeaseOutcome, error) {
	var outcome *expireLeaseOutcome

	txErr := s.inTx(ctx, func(tx pgx.Tx) error {
		// Lock + check the lease.
		row := tx.QueryRow(ctx, `
			SELECT id, run_id, gh_job_name, gh_job_id, unit_ids,
			       issued_at, deadline, released_at, release_reason,
			       auth_oidc_subject, auth_api_key_id
			  FROM leases
			 WHERE id = $1
			 FOR UPDATE
		`, leaseID)
		lease, err := scanLeaseRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnknownLease
		}
		if err != nil {
			return fmt.Errorf("load lease: %w", err)
		}
		if lease.ReleasedAt != nil {
			return nil
		}

		now := time.Now().UTC()
		if _, err := tx.Exec(ctx, `
			UPDATE leases SET released_at = $2, release_reason = 'expired' WHERE id = $1
		`, leaseID, now); err != nil {
			return fmt.Errorf("release lease: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE attempts
			   SET expired = TRUE
			 WHERE lease_id = $1 AND reported_at IS NULL
		`, leaseID); err != nil {
			return fmt.Errorf("mark attempts expired: %w", err)
		}

		// Reset only units still pointing at THIS lease — a previously-
		// re-leased unit must not be disturbed.
		resetRows, err := tx.Query(ctx, `
			UPDATE dispatch_units
			   SET state = 'pending',
			       current_lease_id = NULL,
			       updated_at = now()
			 WHERE current_lease_id = $1
			RETURNING id
		`, leaseID)
		if err != nil {
			return fmt.Errorf("reset reclaimed units: %w", err)
		}
		var reclaimed []uuid.UUID
		for resetRows.Next() {
			var id uuid.UUID
			if err := resetRows.Scan(&id); err != nil {
				resetRows.Close()
				return err
			}
			reclaimed = append(reclaimed, id)
		}
		resetRows.Close()
		if err := resetRows.Err(); err != nil {
			return err
		}

		if n := len(reclaimed); n > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE orchestration_runs
				   SET pending_count = pending_count + $2,
				       leased_count = leased_count - $2,
				       updated_at = now()
				 WHERE id = $1
			`, lease.RunID, n); err != nil {
				return fmt.Errorf("update run counters on lease expire: %w", err)
			}
		}

		identity, err := loadRunIdentityTx(ctx, tx, lease.RunID)
		if err != nil {
			return err
		}

		releasedAt := now
		lease.ReleasedAt = &releasedAt
		reason := LeaseReleaseExpired
		lease.ReleaseReason = &reason

		outcome = &expireLeaseOutcome{
			Lease:            lease,
			Identity:         identity,
			ReclaimedUnitIDs: reclaimed,
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return outcome, nil
}

// runTimedOutOutcome describes the result of a run-timeout transition.
type runTimedOutOutcome struct {
	Identity       CompositeIdentity
	Counts         RunCounts
	TerminalAt     *time.Time
	AbandonedAdded int
}

// markRunTimedOut flips remaining pending and leased units to 'abandoned',
// releases all open leases on the run with release_reason='run_timed_out',
// and transitions the run's status to 'timed_out'. No-op when already
// terminal.
func (s *Store) markRunTimedOut(ctx context.Context, runID uuid.UUID) (*runTimedOutOutcome, error) {
	var outcome *runTimedOutOutcome

	txErr := s.inTx(ctx, func(tx pgx.Tx) error {
		var status string
		err := tx.QueryRow(ctx, `
			SELECT status FROM orchestration_runs WHERE id = $1 FOR UPDATE
		`, runID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock run: %w", err)
		}
		if status != RunStatusInProgress {
			return nil
		}

		now := time.Now().UTC()

		// Release all still-active leases on this run as 'run_timed_out' and
		// flag their attempts as expired.
		leaseRows, err := tx.Query(ctx, `
			UPDATE leases
			   SET released_at = $2, release_reason = 'run_timed_out'
			 WHERE run_id = $1 AND released_at IS NULL
			RETURNING id
		`, runID, now)
		if err != nil {
			return fmt.Errorf("release run leases: %w", err)
		}
		var leaseIDs []uuid.UUID
		for leaseRows.Next() {
			var id uuid.UUID
			if err := leaseRows.Scan(&id); err != nil {
				leaseRows.Close()
				return err
			}
			leaseIDs = append(leaseIDs, id)
		}
		leaseRows.Close()
		if err := leaseRows.Err(); err != nil {
			return err
		}
		for _, lid := range leaseIDs {
			if _, err := tx.Exec(ctx, `
				UPDATE attempts
				   SET expired = TRUE
				 WHERE lease_id = $1 AND reported_at IS NULL
			`, lid); err != nil {
				return fmt.Errorf("mark attempts expired on timeout: %w", err)
			}
		}

		// Transition all still-active units (pending or leased) to abandoned.
		var abandoned int
		err = tx.QueryRow(ctx, `
			WITH updated AS (
			    UPDATE dispatch_units
			       SET state = 'abandoned',
			           outcome_set_at = $2,
			           current_lease_id = NULL,
			           updated_at = now()
			     WHERE run_id = $1 AND state IN ('pending','leased')
			    RETURNING 1
			)
			SELECT COUNT(*) FROM updated
		`, runID, now).Scan(&abandoned)
		if err != nil {
			return fmt.Errorf("abandon active units: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE orchestration_runs
			   SET status = 'timed_out',
			       terminal_at = $2,
			       pending_count = 0,
			       leased_count = 0,
			       abandoned_count = abandoned_count + $3,
			       updated_at = now()
			 WHERE id = $1 AND status = 'in_progress'
		`, runID, now, abandoned); err != nil {
			return fmt.Errorf("transition run timed_out: %w", err)
		}

		// Re-read for the returned outcome.
		run, err := loadRunByIDTx(ctx, tx, runID)
		if err != nil {
			return err
		}
		outcome = &runTimedOutOutcome{
			Identity:       run.Identity,
			Counts:         run.Counts,
			TerminalAt:     run.TerminalAt,
			AbandonedAdded: abandoned,
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return outcome, nil
}

// loadRunIdentityTx fetches just the composite identity for a run within a
// transaction. Used by the reaper to populate event payloads.
func loadRunIdentityTx(ctx context.Context, tx pgx.Tx, runID uuid.UUID) (CompositeIdentity, error) {
	var ci CompositeIdentity
	var ghPRNumber *int
	err := tx.QueryRow(ctx, `
		SELECT repository, commit_sha, gh_run_id, name, gh_run_attempt,
		       framework, branch, gh_pr_number
		  FROM orchestration_runs WHERE id = $1
	`, runID).Scan(&ci.Repository, &ci.CommitSHA, &ci.GHRunID, &ci.Name, &ci.GHRunAttempt,
		&ci.Framework, &ci.Branch, &ghPRNumber)
	if err != nil {
		return CompositeIdentity{}, fmt.Errorf("load run identity: %w", err)
	}
	ci.GHPRNumber = ghPRNumber
	return ci, nil
}

// loadRunByIDTx selects an orchestration_runs row by its internal PK within a
// transaction.
func loadRunByIDTx(ctx context.Context, tx pgx.Tx, runID uuid.UUID) (*Run, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, repository, commit_sha, gh_run_id, name, gh_run_attempt,
		       framework, branch, gh_pr_number, playwright_project,
		       lease_timeout_ms, idle_timeout_ms,
		       retest_on_fail, retest_budget, retest_eligible_count,
		       status,
		       pending_count, leased_count, completed_pass_count, completed_fail_count,
		       completed_skipped_count, abandoned_count, total_units,
		       dispatch_units_hash, started_at, last_activity_at, terminal_at,
		       owner_oidc_subject, owner_api_key_id, created_at, updated_at
		  FROM orchestration_runs WHERE id = $1
	`, runID)
	return scanRunRow(row)
}

// resolveInterval reads TSIO_ORCH_REAPER_INTERVAL_MS and falls back to
// DefaultReaperInterval on missing/invalid/non-positive values.
func resolveInterval() time.Duration {
	raw := os.Getenv(reaperEnvVar)
	if raw == "" {
		return DefaultReaperInterval
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		return DefaultReaperInterval
	}
	return time.Duration(ms) * time.Millisecond
}
