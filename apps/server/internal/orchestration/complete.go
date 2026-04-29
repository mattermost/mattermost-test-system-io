package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CompleteOutcome summarizes the side-effects of a successful RecordCompletion
// call. Handlers consume this to emit live events and to construct the HTTP
// response.
//
// Counter invariants maintained across every state transition the
// orchestration domain performs. All transitions are atomic with their
// owning transaction; the per-row state column on dispatch_units is updated
// in lockstep with the run-level counters on orchestration_runs. The
// orchestration_runs_counts_ck CHECK constraint
//
//	pending_count + leased_count + completed_pass_count + completed_fail_count
//	  + completed_skipped_count + abandoned_count = total_units
//
// is preserved at the end of every transition. retest_eligible_count is a
// derived SUBSET of completed_fail_count (units waiting to be re-leased) and
// is intentionally NOT included in the sum — it is bookkeeping for the
// run-completion gate (a run completes only when this count is zero).
//
// State transitions and the counter deltas they apply:
//
//	pending  -> leased         (AtomicCheckout):
//	             pending--, leased++
//	leased   -> pending        (Reaper.expireOneLease, on lease timeout):
//	             leased--, pending++
//	leased   -> completed_pass / completed_skipped (RecordCompletion):
//	             leased--, <target>_count++
//	leased   -> completed_fail (RecordCompletion):
//	             leased--, completed_fail++
//	             unit.fail_count++
//	             when retest_on_fail AND new fail_count <= retest_budget:
//	               retest_eligible_count++
//	completed_fail -> leased   (AtomicRetestCheckout, retest dispatch):
//	             completed_fail--, leased++, retest_eligible_count--
//	pending|leased -> abandoned (Reaper.markRunTimedOut):
//	             pending=0, leased=0, abandoned += sum of moved
//
// A timeout-driven re-lease (lease expired without a report) does NOT
// increment fail_count — it is not a definitive failure. Only an explicit
// `failed`/`timedOut`/`interrupted` status from a worker bumps fail_count.
type CompleteOutcome struct {
	LeaseID           uuid.UUID
	LateReport        bool
	Idempotent        bool // true when the lease was already released-completed (first answer wins)
	UnitStatesChanged []UnitStateChange
	RunNowCompleted   bool
	RunCounts         RunCounts
}

// RecordCompletion applies a worker's complete payload against the worker's
// most recent lease for the run. Late reports are accepted; duplicate reports
// against an already-completed lease are idempotent (200, no-op).
//
// The composite identity uniquely names the run; the (run, gh_job_id) tuple
// uniquely names the worker — together they resolve the lease without the
// worker needing to remember a server-issued lease id.
func (s *Store) RecordCompletion(
	ctx context.Context,
	identity CompositeIdentity,
	worker WorkerIdentity,
	results []SpecResult,
) (*CompleteOutcome, error) {
	run, err := s.FindRunByIdentity(ctx, identity)
	if err != nil {
		return nil, err
	}

	lease, err := s.FindLeaseByWorker(ctx, run.ID, worker)
	if err != nil {
		return nil, err
	}

	// Idempotent path: if the lease is already released as 'completed', first
	// answer wins (no DB mutation, no events).
	if lease.ReleasedAt != nil && lease.ReleaseReason != nil && *lease.ReleaseReason == LeaseReleaseCompleted {
		logEvent(ctx, s.Logger, "orchestration.complete.idempotent", "orchestration complete idempotent replay", run,
			slog.String("gh_job_id", worker.GHJobID),
			slog.String("lease_id", lease.ID.String()),
		)
		logMetric(ctx, s.Logger, "orchestration_completions_total", "late_idempotent", 1)
		return &CompleteOutcome{
			LeaseID:    lease.ID,
			Idempotent: true,
			LateReport: false,
			RunCounts:  run.Counts,
		}, nil
	}

	// All-or-nothing: results must cover every (lease, spec_path) attempt row.
	specsInLease, err := s.specsForLease(ctx, lease.ID)
	if err != nil {
		return nil, err
	}
	if err := validateResultsCoverLease(results, specsInLease); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	lateReport := isLateReport(now, lease.Deadline)

	var (
		stateChanges    []UnitStateChange
		runNowCompleted bool
		updatedCounts   RunCounts
	)

	txErr := s.inTx(ctx, func(tx pgx.Tx) error {
		// Update the per-spec attempts rows.
		if err := updateAttemptsForCompletionTx(ctx, tx, lease.ID, results, lateReport, now); err != nil {
			return err
		}

		// Group results by dispatch_unit so we can compute per-unit outcomes.
		unitOutcomes, err := groupResultsByUnitTx(ctx, tx, lease.ID, results)
		if err != nil {
			return err
		}

		retestCfg := runRetestConfig{
			RetestOnFail: run.RetestOnFail,
			RetestBudget: run.RetestBudget,
		}
		for _, uo := range unitOutcomes {
			change, applied, err := finalizeUnitStateTx(ctx, tx, run.ID, uo, now, lateReport, retestCfg)
			if err != nil {
				return err
			}
			if applied {
				stateChanges = append(stateChanges, change)
			}
		}

		// Release the lease (idempotent guard: only when still active).
		if _, err := tx.Exec(ctx, `
			UPDATE leases
			   SET released_at = COALESCE(released_at, $2),
			       release_reason = COALESCE(release_reason, $3)
			 WHERE id = $1
		`, lease.ID, now, LeaseReleaseCompleted); err != nil {
			return fmt.Errorf("release lease: %w", err)
		}

		// Re-read run counters and decide on completion transition.
		var (
			pending        int
			leased         int
			retestEligible int
			passed         int
			failed         int
			skipped        int
			abandoned      int
			total          int
			status         string
		)
		err = tx.QueryRow(ctx, `
			SELECT pending_count, leased_count, retest_eligible_count,
			       completed_pass_count, completed_fail_count, completed_skipped_count,
			       abandoned_count, total_units, status
			  FROM orchestration_runs
			 WHERE id = $1
		`, run.ID).Scan(&pending, &leased, &retestEligible,
			&passed, &failed, &skipped,
			&abandoned, &total, &status)
		if err != nil {
			return fmt.Errorf("re-read run counters: %w", err)
		}
		updatedCounts = RunCounts{
			Pending:          pending,
			Leased:           leased,
			CompletedPass:    passed,
			CompletedFail:    failed,
			CompletedSkipped: skipped,
			Abandoned:        abandoned,
			RetestEligible:   retestEligible,
			Total:            total,
		}

		if status == RunStatusInProgress && pending == 0 && leased == 0 && retestEligible == 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE orchestration_runs
				   SET status = 'completed',
				       terminal_at = $2,
				       updated_at = now()
				 WHERE id = $1 AND status = 'in_progress'
			`, run.ID, now); err != nil {
				return fmt.Errorf("transition run completed: %w", err)
			}
			runNowCompleted = true
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}

	for _, change := range stateChanges {
		logEvent(ctx, s.Logger, "orchestration.complete.applied", "orchestration complete applied", run,
			slog.String("gh_job_id", worker.GHJobID),
			slog.String("lease_id", lease.ID.String()),
			slog.String("unit_id", change.UnitID.String()),
			slog.String("from_state", change.FromState),
			slog.String("to_state", change.ToState),
			slog.Bool("late_report", lateReport),
		)
		logMetric(ctx, s.Logger, "orchestration_completions_total", attemptOutcomeFromUnitState(change.ToState), 1)
	}
	if runNowCompleted {
		durationMs := time.Since(run.StartedAt).Milliseconds()
		logEvent(ctx, s.Logger, "orchestration.run.completed", "orchestration run completed", run,
			slog.String("from_state", RunStatusInProgress),
			slog.String("to_state", RunStatusCompleted),
			slog.Int64("duration_ms", durationMs),
			slog.Int("unit_count", updatedCounts.Total),
		)
	}

	return &CompleteOutcome{
		LeaseID:           lease.ID,
		LateReport:        lateReport,
		Idempotent:        false,
		UnitStatesChanged: stateChanges,
		RunNowCompleted:   runNowCompleted,
		RunCounts:         updatedCounts,
	}, nil
}

// attemptOutcomeFromUnitState maps a terminal dispatch_units state onto the
// outcome label used by orchestration_completions_total. The metric label
// space follows the project's attempt-status enum where possible (passed,
// failed, skipped) and falls back to the unit-state name otherwise.
func attemptOutcomeFromUnitState(state string) string {
	switch state {
	case UnitStateCompletedPass:
		return "passed"
	case UnitStateCompletedFail:
		return "failed"
	case UnitStateCompletedSkipped:
		return "skipped"
	}
	return state
}

// specsForLease returns every (dispatch_unit_id, spec_path) pair attached to
// the lease. Used both to validate the worker's payload and to drive the
// per-unit outcome rollup.
func (s *Store) specsForLease(ctx context.Context, leaseID uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT spec_path, dispatch_unit_id
		  FROM attempts
		 WHERE lease_id = $1
	`, leaseID)
	if err != nil {
		return nil, fmt.Errorf("load lease specs: %w", err)
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var sp string
		var unit uuid.UUID
		if err := rows.Scan(&sp, &unit); err != nil {
			return nil, err
		}
		out[sp] = unit
	}
	return out, rows.Err()
}

// validateResultsCoverLease enforces all-or-nothing completion: the worker's
// payload must cover every spec in the lease. Extra entries (specs not in the
// lease) and missing entries both produce ErrPartialReport.
func validateResultsCoverLease(results []SpecResult, leaseSpecs map[string]uuid.UUID) error {
	if len(results) != len(leaseSpecs) {
		return fmt.Errorf("%w: lease covers %d specs, got %d",
			ErrPartialReport, len(leaseSpecs), len(results))
	}
	seen := map[string]bool{}
	for _, r := range results {
		if r.SpecPath == "" {
			return fmt.Errorf("%w: empty spec_path in results", ErrPartialReport)
		}
		if _, ok := leaseSpecs[r.SpecPath]; !ok {
			return fmt.Errorf("%w: spec_path %q not in lease", ErrPartialReport, r.SpecPath)
		}
		if seen[r.SpecPath] {
			return fmt.Errorf("%w: duplicate spec_path %q in results", ErrPartialReport, r.SpecPath)
		}
		seen[r.SpecPath] = true
		if !validAttemptStatus(r.Status) {
			return fmt.Errorf("%w: invalid status %q for %q", ErrPartialReport, r.Status, r.SpecPath)
		}
	}
	for sp := range leaseSpecs {
		if !seen[sp] {
			return fmt.Errorf("%w: spec_path %q missing from results", ErrPartialReport, sp)
		}
	}
	return nil
}

func validAttemptStatus(s string) bool {
	switch s {
	case AttemptStatusPassed, AttemptStatusFailed, AttemptStatusSkipped,
		AttemptStatusFlaky, AttemptStatusTimedOut, AttemptStatusInterrupted:
		return true
	}
	return false
}

// updateAttemptsForCompletionTx writes the per-spec status, duration, error,
// test-cases JSON, reported_at, and late_report flag for every attempts row
// keyed by (lease_id, spec_path).
func updateAttemptsForCompletionTx(
	ctx context.Context,
	tx pgx.Tx,
	leaseID uuid.UUID,
	results []SpecResult,
	lateReport bool,
	now time.Time,
) error {
	batch := &pgx.Batch{}
	for _, r := range results {
		var testCases interface{}
		if len(r.TestCases) > 0 {
			testCases = []byte(r.TestCases)
		}
		batch.Queue(`
			UPDATE attempts
			   SET status = $3,
			       actual_duration_ms = $4,
			       error_message = $5,
			       error_stack = $6,
			       test_cases = $7,
			       reported_at = $8,
			       late_report = $9
			 WHERE lease_id = $1 AND spec_path = $2
		`, leaseID, r.SpecPath, r.Status, r.ActualDurationMs, r.ErrorMessage, r.ErrorStack, testCases, now, lateReport)
	}
	br := tx.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()
	for i := 0; i < len(results); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("update attempt row %d: %w", i, err)
		}
	}
	return nil
}

// unitOutcome groups every spec result reported on the lease that maps to one
// dispatch_unit, plus the unit's current state read inside the same tx.
type unitOutcome struct {
	UnitID       uuid.UUID
	CurrentState string
	FailCount    int
	SpecStatuses []string
}

// runRetestConfig is the subset of orchestration_runs used by complete to
// decide whether a fail transition makes the unit retest-eligible.
type runRetestConfig struct {
	RetestOnFail bool
	RetestBudget int
}

// groupResultsByUnitTx joins the worker's payload to the lease's
// dispatch_units, returning one unitOutcome per leased unit.
func groupResultsByUnitTx(
	ctx context.Context,
	tx pgx.Tx,
	leaseID uuid.UUID,
	results []SpecResult,
) ([]unitOutcome, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.dispatch_unit_id, a.spec_path, du.state, du.fail_count
		  FROM attempts a
		  JOIN dispatch_units du ON du.id = a.dispatch_unit_id
		 WHERE a.lease_id = $1
	`, leaseID)
	if err != nil {
		return nil, fmt.Errorf("group results by unit: %w", err)
	}
	defer rows.Close()

	statusByPath := map[string]string{}
	for _, r := range results {
		statusByPath[r.SpecPath] = r.Status
	}

	byUnit := map[uuid.UUID]*unitOutcome{}
	order := []uuid.UUID{}
	for rows.Next() {
		var (
			unitID    uuid.UUID
			specPath  string
			state     string
			failCount int
		)
		if err := rows.Scan(&unitID, &specPath, &state, &failCount); err != nil {
			return nil, err
		}
		uo, ok := byUnit[unitID]
		if !ok {
			uo = &unitOutcome{UnitID: unitID, CurrentState: state, FailCount: failCount}
			byUnit[unitID] = uo
			order = append(order, unitID)
		}
		uo.SpecStatuses = append(uo.SpecStatuses, statusByPath[specPath])
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]unitOutcome, 0, len(order))
	for _, id := range order {
		out = append(out, *byUnit[id])
	}
	return out, nil
}

// finalizeUnitStateTx applies a unit's outcome under the most-recent-non-expired
// completion rule. The unit's current state must be 'leased' for any
// transition to occur; both first-pass leases and retest leases land here
// (retest dispatch has already flipped the unit's state from completed_fail
// back to 'leased'). A completion for a unit that is no longer 'leased' is
// necessarily a late report against an expired lease; attempt rows are still
// updated by the caller, but the unit's outcome is preserved.
//
// Counter math (single transaction, atomic with the unit-row update):
//
//	leased -> completed_pass / completed_skipped:
//	  leased_count -= 1
//	  <target>_count += 1
//	leased -> completed_fail:
//	  leased_count -= 1
//	  completed_fail_count += 1
//	  fail_count++ on the unit
//	  when retest_on_fail AND new fail_count <= retest_budget:
//	    retest_eligible_count += 1   (subset of completed_fail_count;
//	                                  not added to the counters-sum invariant)
//
// Returns (change, applied, err): applied=false when the unit-state update is
// a no-op (e.g. already terminal).
func finalizeUnitStateTx(
	ctx context.Context,
	tx pgx.Tx,
	runID uuid.UUID,
	uo unitOutcome,
	now time.Time,
	lateReport bool,
	retestCfg runRetestConfig,
) (UnitStateChange, bool, error) {
	if uo.CurrentState != UnitStateLeased {
		// A non-'leased' unit means this is a late report (the lease
		// timed out and the unit either reached terminal state via a
		// later lease or is awaiting one). Attempt rows are still
		// updated by the caller; the unit's outcome is preserved.
		_ = lateReport
		return UnitStateChange{}, false, nil
	}

	target := mapStatusesToUnitState(uo.SpecStatuses)
	failed := target == UnitStateCompletedFail
	failIncrement := 0
	if failed {
		failIncrement = 1
	}

	// Update the unit row. Capture the post-update fail_count so we can
	// decide retest-eligibility transactionally without a second read.
	var newFailCount int
	if err := tx.QueryRow(ctx, `
		UPDATE dispatch_units
		   SET state = $2,
		       outcome_set_at = $3,
		       current_lease_id = NULL,
		       fail_count = fail_count + $4,
		       updated_at = now()
		 WHERE id = $1
		RETURNING fail_count
	`, uo.UnitID, target, now, failIncrement).Scan(&newFailCount); err != nil {
		return UnitStateChange{}, false, fmt.Errorf("transition unit %s -> %s: %w", uo.UnitID, target, err)
	}

	// Update run counters: leased--, completed_*++.
	counterCol := unitStateCounterColumn(target)
	if counterCol == "" {
		return UnitStateChange{}, false, fmt.Errorf("no counter column for state %q", target)
	}

	// When a unit transitions to completed_fail under retest-on-fail and the
	// new fail_count is still within budget, the unit is retest-eligible.
	// retest_eligible_count is a derived counter (subset of completed_fail_count)
	// — it is NOT included in the run's counters-sum check constraint, so
	// incrementing it here does not break the invariant.
	retestEligibleDelta := 0
	if failed && retestCfg.RetestOnFail && newFailCount <= retestCfg.RetestBudget {
		retestEligibleDelta = 1
	}

	_, err := tx.Exec(ctx, fmt.Sprintf(`
		UPDATE orchestration_runs
		   SET leased_count = leased_count - 1,
		       %s = %s + 1,
		       retest_eligible_count = retest_eligible_count + $2,
		       last_activity_at = now(),
		       updated_at = now()
		 WHERE id = $1
	`, counterCol, counterCol), runID, retestEligibleDelta)
	if err != nil {
		return UnitStateChange{}, false, fmt.Errorf("update run counters for unit transition: %w", err)
	}

	return UnitStateChange{
		UnitID:    uo.UnitID,
		FromState: UnitStateLeased,
		ToState:   target,
		Outcome:   target,
		At:        now,
	}, true, nil
}

// isLateReport returns true when the report time is strictly after the
// lease deadline. Extracted as a pure helper so the late-report rule can be
// unit-tested without a database. Reporting AT the deadline is treated as
// on-time (consistent with time.Time.After's strict-greater-than semantics).
func isLateReport(now, deadline time.Time) bool {
	return now.After(deadline)
}

// mapStatusesToUnitState collapses a unit's per-spec statuses into the
// narrower per-unit state enum:
//
//	any failed/timedOut/interrupted -> completed_fail
//	else any passed/flaky           -> completed_pass
//	else (all skipped)              -> completed_skipped
func mapStatusesToUnitState(statuses []string) string {
	hasPassOrFlaky := false
	for _, s := range statuses {
		switch s {
		case AttemptStatusFailed, AttemptStatusTimedOut, AttemptStatusInterrupted:
			return UnitStateCompletedFail
		case AttemptStatusPassed, AttemptStatusFlaky:
			hasPassOrFlaky = true
		}
	}
	if hasPassOrFlaky {
		return UnitStateCompletedPass
	}
	return UnitStateCompletedSkipped
}

// unitStateCounterColumn maps a terminal unit state to the orchestration_runs
// counter column it increments. Returns "" on an unknown state.
func unitStateCounterColumn(state string) string {
	switch state {
	case UnitStateCompletedPass:
		return "completed_pass_count"
	case UnitStateCompletedFail:
		return "completed_fail_count"
	case UnitStateCompletedSkipped:
		return "completed_skipped_count"
	case UnitStateAbandoned:
		return "abandoned_count"
	}
	return ""
}
