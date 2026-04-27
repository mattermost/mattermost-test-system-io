package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// AtomicCheckout performs the work-queue dispatch: SELECT ... FOR UPDATE
// SKIP LOCKED on dispatch_units WHERE state='pending', INSERT one leases row
// for the calling worker, INSERT one attempts row per spec in each picked
// unit, and UPDATE the picked units to state='leased' with current_lease_id
// set. Bounded by batchSize.
//
// Returns ErrRunNotInProgress when the run has reached a terminal state and
// ErrWorkerHasActiveLease when the partial unique index on
// leases(run_id, gh_job_id) WHERE released_at IS NULL rejects the insert.
// When the run is in progress but no pending units remain, returns the
// zero-value (lease=nil, units=nil, isRetest=false, err=nil) — the caller
// maps this to a 200 with an empty assignment (handlers may also fall through
// to AtomicRetestCheckout when retest-on-fail is enabled for the run).
func (s *Store) AtomicCheckout(
	ctx context.Context,
	identity CompositeIdentity,
	worker WorkerIdentity,
	batchSize int,
) (*Lease, []*DispatchUnit, bool, error) {
	if batchSize <= 0 {
		batchSize = 1
	}

	run, err := s.FindRunByIdentity(ctx, identity)
	if err != nil {
		return nil, nil, false, err
	}
	if run.Status != RunStatusInProgress {
		return nil, nil, false, ErrRunNotInProgress
	}

	// Lazy expiration: clean up any overdue leases on this run before picking
	// new units. Failures here are non-fatal; the periodic reaper is the
	// authoritative backstop.
	if expireErr := s.expireOverdueLeasesForRun(ctx, run.ID); expireErr != nil {
		// Best-effort. Log via slog default; do not bubble.
		slog.Default().DebugContext(ctx, "lazy lease expiration failed",
			slog.String("run_id", run.ID.String()),
			slog.String("err", expireErr.Error()))
	}

	var (
		lease *Lease
		units []*DispatchUnit
	)

	txErr := s.inTx(ctx, func(tx pgx.Tx) error {
		l, err := insertLeaseTx(ctx, tx, run, worker)
		if err != nil {
			return err
		}

		dispatched, err := dispatchPendingUnitsTx(ctx, tx, run.ID, l.ID, batchSize)
		if err != nil {
			return err
		}
		if len(dispatched) == 0 {
			// Nothing to lease — discard the empty lease row so the partial
			// unique index does not retain a phantom active lease.
			if _, delErr := tx.Exec(ctx, `DELETE FROM leases WHERE id = $1`, l.ID); delErr != nil {
				return fmt.Errorf("delete empty lease: %w", delErr)
			}
			lease = nil
			units = nil
			return nil
		}

		// Update the lease's unit_ids array now that we know what got picked.
		unitIDs := make([]uuid.UUID, 0, len(dispatched))
		for _, u := range dispatched {
			unitIDs = append(unitIDs, u.ID)
		}
		if _, err := tx.Exec(ctx, `UPDATE leases SET unit_ids = $1 WHERE id = $2`, unitIDs, l.ID); err != nil {
			return fmt.Errorf("update lease unit_ids: %w", err)
		}
		l.UnitIDs = unitIDs

		// Insert one attempts row per spec_path in each dispatched unit.
		if err := insertInitialAttemptsTx(ctx, tx, run.ID, l.ID, dispatched); err != nil {
			return err
		}

		// Update run counters: pending -= len, leased += len.
		if _, err := tx.Exec(ctx, `
			UPDATE orchestration_runs
			   SET pending_count = pending_count - $2,
			       leased_count = leased_count + $2,
			       updated_at = now()
			 WHERE id = $1
		`, run.ID, len(dispatched)); err != nil {
			return fmt.Errorf("update run counters: %w", err)
		}

		lease = l
		units = dispatched
		return nil
	})
	if txErr != nil {
		// Map the partial-unique-index conflict to a clear sentinel.
		var pgErr *pgconn.PgError
		if errors.As(txErr, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "leases_active_worker_uq" {
			return nil, nil, false, ErrWorkerHasActiveLease
		}
		return nil, nil, false, txErr
	}
	if lease == nil {
		logEvent(ctx, s.Logger, "orchestration.checkout.empty", "orchestration checkout empty", run,
			slog.String("gh_job_id", worker.GHJobID),
			slog.String("gh_job_name", worker.GHJobName),
			slog.Bool("is_retest", false),
		)
		logMetric(ctx, s.Logger, "orchestration_checkouts_total", "empty", 1)
		return nil, nil, false, nil
	}
	logEvent(ctx, s.Logger, "orchestration.checkout.dispatched", "orchestration checkout dispatched", run,
		slog.String("gh_job_id", worker.GHJobID),
		slog.String("gh_job_name", worker.GHJobName),
		slog.String("lease_id", lease.ID.String()),
		slog.Int("unit_count", len(units)),
		slog.Bool("is_retest", false),
	)
	logMetric(ctx, s.Logger, "orchestration_checkouts_total", "dispatched", 1)
	return lease, units, false, nil
}

// AtomicRetestCheckout is the retest variant of AtomicCheckout — re-leases a
// previously-failed unit (within retest_budget). Any worker that calls this
// path is eligible, including the worker that previously failed the unit;
// the simpler "any worker may retest" semantics keep single-worker queues
// from stalling on a failed unit. Lazy and gated on first-pass completion:
// the run must have zero pending and zero leased units before any retest
// is dispatched. The retest budget is per-unit (fail_count <= retest_budget).
//
// Returns the zero value (lease=nil, units=nil, isRetest=false, err=nil)
// when there is no retest-eligible work — either because retest is
// disabled, first-pass is not yet complete, or no eligible units remain.
// The caller maps this to a queue_empty: true response.
//
// Returns ErrRunNotInProgress when the run has reached a terminal state
// and ErrWorkerHasActiveLease when the partial unique index on
// leases(run_id, gh_job_id) WHERE released_at IS NULL rejects the insert.
func (s *Store) AtomicRetestCheckout(
	ctx context.Context,
	identity CompositeIdentity,
	worker WorkerIdentity,
	batchSize int,
) (*Lease, []*DispatchUnit, bool, error) {
	if batchSize <= 0 {
		batchSize = 1
	}

	run, err := s.FindRunByIdentity(ctx, identity)
	if err != nil {
		return nil, nil, false, err
	}
	if run.Status != RunStatusInProgress {
		return nil, nil, false, ErrRunNotInProgress
	}
	// Retest gating: must be enabled, first-pass complete, and retest pool non-empty.
	if !run.RetestOnFail {
		return nil, nil, false, nil
	}
	if run.Counts.Pending != 0 || run.Counts.Leased != 0 {
		return nil, nil, false, nil
	}
	if run.Counts.RetestEligible <= 0 {
		return nil, nil, false, nil
	}

	var (
		lease *Lease
		units []*DispatchUnit
	)

	txErr := s.inTx(ctx, func(tx pgx.Tx) error {
		l, err := insertLeaseTx(ctx, tx, run, worker)
		if err != nil {
			return err
		}

		dispatched, err := dispatchRetestUnitsTx(ctx, tx, run.ID, l.ID, batchSize)
		if err != nil {
			return err
		}
		if len(dispatched) == 0 {
			// No eligible retest unit remains. Discard the empty lease row so
			// the partial unique index doesn't retain a phantom.
			if _, delErr := tx.Exec(ctx, `DELETE FROM leases WHERE id = $1`, l.ID); delErr != nil {
				return fmt.Errorf("delete empty retest lease: %w", delErr)
			}
			lease = nil
			units = nil
			return nil
		}

		// Update the lease's unit_ids array.
		unitIDs := make([]uuid.UUID, 0, len(dispatched))
		for _, u := range dispatched {
			unitIDs = append(unitIDs, u.ID)
		}
		if _, err := tx.Exec(ctx, `UPDATE leases SET unit_ids = $1 WHERE id = $2`, unitIDs, l.ID); err != nil {
			return fmt.Errorf("update retest lease unit_ids: %w", err)
		}
		l.UnitIDs = unitIDs

		// Insert one attempts row per spec_path in each dispatched unit.
		if err := insertInitialAttemptsTx(ctx, tx, run.ID, l.ID, dispatched); err != nil {
			return err
		}

		// Counter math for the retest state transition (completed_fail -> leased):
		//   completed_fail_count -= len(dispatched)   (units leaving the terminal-ish bucket)
		//   leased_count        += len(dispatched)   (units entering the leased bucket)
		//   retest_eligible_count -= len(dispatched) (no longer waiting for retest)
		//
		// The CHECK invariant counters-sum-to-total is preserved because
		// retest_eligible_count is a derived subset of completed_fail_count and
		// is NOT included in the sum.
		if _, err := tx.Exec(ctx, `
			UPDATE orchestration_runs
			   SET completed_fail_count = completed_fail_count - $2,
			       leased_count = leased_count + $2,
			       retest_eligible_count = retest_eligible_count - $2,
			       updated_at = now()
			 WHERE id = $1
		`, run.ID, len(dispatched)); err != nil {
			return fmt.Errorf("update run counters on retest dispatch: %w", err)
		}

		lease = l
		units = dispatched
		return nil
	})
	if txErr != nil {
		var pgErr *pgconn.PgError
		if errors.As(txErr, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "leases_active_worker_uq" {
			return nil, nil, false, ErrWorkerHasActiveLease
		}
		return nil, nil, false, txErr
	}
	if lease == nil {
		return nil, nil, false, nil
	}
	logEvent(ctx, s.Logger, "orchestration.retest.dispatched", "orchestration retest dispatched", run,
		slog.String("gh_job_id", worker.GHJobID),
		slog.String("gh_job_name", worker.GHJobName),
		slog.String("lease_id", lease.ID.String()),
		slog.Int("unit_count", len(units)),
		slog.Bool("is_retest", true),
	)
	logMetric(ctx, s.Logger, "orchestration_retests_total", "", 1)
	logMetric(ctx, s.Logger, "orchestration_checkouts_total", "retest", 1)
	return lease, units, true, nil
}

// dispatchRetestUnitsTx is the retest-dispatch CTE: pick `completed_fail`
// units whose fail_count is still within the run's retest_budget, order
// them by dispatch_seq, lock with FOR UPDATE SKIP LOCKED, and flip them
// back to `leased` under the supplied leaseID. Any caller is eligible —
// retest no longer excludes the worker that previously failed the unit,
// so single-worker queues continue to make progress on retest.
func dispatchRetestUnitsTx(
	ctx context.Context,
	tx pgx.Tx,
	runID, leaseID uuid.UUID,
	batchSize int,
) ([]*DispatchUnit, error) {
	rows, err := tx.Query(ctx, `
		WITH retestable AS (
		    SELECT du.id
		      FROM dispatch_units du
		      JOIN orchestration_runs r ON r.id = du.run_id
		     WHERE du.run_id = $1
		       AND r.retest_on_fail = TRUE
		       AND du.state = 'completed_fail'
		       AND du.fail_count <= r.retest_budget
		       AND r.pending_count = 0 AND r.leased_count = 0
		     ORDER BY du.dispatch_seq
		     LIMIT $2
		     FOR UPDATE SKIP LOCKED
		)
		UPDATE dispatch_units du
		   SET state = 'leased',
		       current_lease_id = $3,
		       lease_count = lease_count + 1,
		       outcome_set_at = NULL,
		       updated_at = now()
		  FROM retestable rt
		 WHERE du.id = rt.id
		RETURNING du.id, du.run_id, du.dispatch_seq, du.spec_path,
		          du.lease_count, du.fail_count
	`, runID, batchSize, leaseID)
	if err != nil {
		return nil, fmt.Errorf("dispatch retest units: %w", err)
	}
	defer rows.Close()

	var out []*DispatchUnit
	for rows.Next() {
		u := &DispatchUnit{State: UnitStateLeased, CurrentLeaseID: &leaseID}
		if err := rows.Scan(&u.ID, &u.RunID, &u.DispatchSeq, &u.SpecPath,
			&u.LeaseCount, &u.FailCount); err != nil {
			return nil, fmt.Errorf("scan retest unit: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// insertLeaseTx inserts a leases row for the given run + worker with deadline
// = now() + run.lease_timeout_ms. unit_ids is initialized to the empty array
// here and overwritten once the dispatch CTE has chosen units.
//
// IMPORTANT: the leases_active_worker_uq partial UNIQUE constraint is
// evaluated on this INSERT — a conflict here is what surfaces as
// ErrWorkerHasActiveLease.
func insertLeaseTx(ctx context.Context, tx pgx.Tx, run *Run, worker WorkerIdentity) (*Lease, error) {
	// Use an empty-but-valid array placeholder; the table CHECK is
	// cardinality(unit_ids) >= 1 so we cannot actually leave it empty. We
	// insert with a single zero UUID then immediately overwrite via UPDATE
	// later. To avoid that, defer unit_ids to the same INSERT by issuing a
	// temporary self-referential value: just use a non-empty array with a
	// fresh UUID we are about to attach. Since we have no unit IDs yet, we
	// pre-generate a placeholder lease id of the lease itself (idempotent
	// from cardinality's perspective; the column is rewritten with the
	// real unit ids before commit).
	leaseID := uuid.Must(uuid.NewV7())
	placeholder := []uuid.UUID{leaseID}

	var (
		issuedAt time.Time
		deadline time.Time
	)
	err := tx.QueryRow(ctx, `
		INSERT INTO leases (id, run_id, gh_job_name, gh_job_id, unit_ids, deadline)
		VALUES ($1, $2, $3, $4, $5,
		        now() + make_interval(secs => $6::double precision / 1000.0))
		RETURNING issued_at, deadline
	`, leaseID, run.ID, worker.GHJobName, worker.GHJobID, placeholder, run.LeaseTimeoutMs).
		Scan(&issuedAt, &deadline)
	if err != nil {
		return nil, fmt.Errorf("insert lease: %w", err)
	}
	return &Lease{
		ID:       leaseID,
		RunID:    run.ID,
		Worker:   worker,
		UnitIDs:  nil,
		IssuedAt: issuedAt,
		Deadline: deadline,
	}, nil
}

// dispatchPendingUnitsTx runs the FIFO CTE that picks up to batchSize pending
// dispatch_units in dispatch_seq order, marks them leased under leaseID, and
// returns the picked units. SKIP LOCKED keeps concurrent checkouts disjoint.
func dispatchPendingUnitsTx(
	ctx context.Context,
	tx pgx.Tx,
	runID, leaseID uuid.UUID,
	batchSize int,
) ([]*DispatchUnit, error) {
	rows, err := tx.Query(ctx, `
		WITH picked AS (
		    SELECT id FROM dispatch_units
		     WHERE run_id = $1 AND state = 'pending'
		     ORDER BY dispatch_seq
		     LIMIT $2
		     FOR UPDATE SKIP LOCKED
		)
		UPDATE dispatch_units du
		   SET state = 'leased',
		       current_lease_id = $3,
		       lease_count = lease_count + 1,
		       updated_at = now()
		  FROM picked p
		 WHERE du.id = p.id
		RETURNING du.id, du.run_id, du.dispatch_seq, du.spec_path,
		          du.lease_count, du.fail_count
	`, runID, batchSize, leaseID)
	if err != nil {
		return nil, fmt.Errorf("dispatch pending units: %w", err)
	}
	defer rows.Close()

	var out []*DispatchUnit
	for rows.Next() {
		u := &DispatchUnit{State: UnitStateLeased, CurrentLeaseID: &leaseID}
		if err := rows.Scan(&u.ID, &u.RunID, &u.DispatchSeq, &u.SpecPath,
			&u.LeaseCount, &u.FailCount); err != nil {
			return nil, fmt.Errorf("scan dispatched unit: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// insertInitialAttemptsTx inserts one attempts row per dispatched unit with
// status NULL and reported_at NULL. The unique index on (lease_id, spec_path)
// provides idempotency; each unit has exactly one spec_path so the row count
// equals the unit count.
func insertInitialAttemptsTx(
	ctx context.Context,
	tx pgx.Tx,
	runID, leaseID uuid.UUID,
	units []*DispatchUnit,
) error {
	if len(units) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, u := range units {
		batch.Queue(`
			INSERT INTO attempts (lease_id, dispatch_unit_id, run_id, spec_path)
			VALUES ($1, $2, $3, $4)
		`, leaseID, u.ID, runID, u.SpecPath)
	}
	br := tx.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()
	for i := range units {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("insert attempts row %d: %w", i, err)
		}
	}
	return nil
}

// expireOverdueLeasesForRun runs the same lease-expiry sweep the reaper does
// but scoped to a single run. Used by AtomicCheckout for tighter response
// latency on reclaim. Best-effort: errors are returned but the caller may
// log-and-continue.
func (s *Store) expireOverdueLeasesForRun(ctx context.Context, runID uuid.UUID) error {
	rows, err := s.Pool.Query(ctx, `
		SELECT id FROM leases
		 WHERE run_id = $1 AND released_at IS NULL AND deadline < now()
	`, runID)
	if err != nil {
		return fmt.Errorf("scan overdue leases: %w", err)
	}
	defer rows.Close()
	var leaseIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		leaseIDs = append(leaseIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range leaseIDs {
		if _, err := s.expireOneLease(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
