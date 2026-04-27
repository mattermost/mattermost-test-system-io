package orchestration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BeginRunOptions captures the configurable knobs supplied (or defaulted) by
// the caller at begin run. Counterparts to the corresponding columns on
// orchestration_runs.
type BeginRunOptions struct {
	LeaseTimeoutMs    int64
	RunTimeoutMs      int64
	RetestOnFail      bool
	RetestBudget      int
	PlaywrightProject string
	Branch            string
	GHPRNumber        *int
}

// OwnerInfo identifies the caller that opened the run. Exactly one of the
// fields is set, mirroring the orchestration_runs.owner_oidc_subject /
// owner_api_key_id columns.
type OwnerInfo struct {
	OIDCSubject *string
	APIKeyID    *uuid.UUID
}

// BeginRun inserts a new orchestration_runs row with its dispatch_units, or
// returns the existing snapshot when the composite identity is already known
// and the dispatch-units hash matches.
//
// Idempotency: identity collision with a byte-equivalent dispatch list (same
// hash) is a successful no-op replay; identity collision with a different
// list is ErrConflict and no DB mutation occurs.
//
// The boolean return is true only when a new row was inserted (the caller
// uses it to decide between HTTP 201 and HTTP 200).
//
// As a side effect, BeginRun also seeds a matching report_groups row inside
// the same transaction (ON CONFLICT DO NOTHING) so the report-index pages
// pick up the run as soon as it starts. The returned SeededReportGroup
// captures whether a new row was created so the caller can publish a
// report_created event after the transaction commits. SeededReportGroup
// is returned for both new and replay BeginRun calls; .Created is true only
// when this call's transaction inserted the report_groups row.
func (s *Store) BeginRun(
	ctx context.Context,
	identity CompositeIdentity,
	options BeginRunOptions,
	specPaths []string,
	owner OwnerInfo,
) (*Run, bool, *SeededReportGroup, error) {
	if err := identity.Validate(); err != nil {
		return nil, false, nil, fmt.Errorf("orchestration begin: %w", err)
	}
	if len(specPaths) == 0 {
		return nil, false, nil, errors.New("orchestration begin: dispatch_units must not be empty")
	}
	for i, p := range specPaths {
		if p == "" {
			return nil, false, nil, fmt.Errorf("orchestration begin: dispatch_units[%d] spec_path is empty", i)
		}
	}
	if owner.OIDCSubject == nil && owner.APIKeyID == nil {
		return nil, false, nil, errors.New("orchestration begin: owner is required")
	}

	hash := identity.HashUnits(specPaths)

	var existing *Run
	var created bool
	var seeded *SeededReportGroup

	txErr := s.inTx(ctx, func(tx pgx.Tx) error {
		row, err := scanRunRow(tx.QueryRow(ctx, runSelectByIdentity,
			identity.Repository, identity.CommitSHA, identity.GHRunID,
			identity.Name, identity.GHRunAttempt))
		switch {
		case err == nil:
			if !bytes.Equal(row.DispatchUnitsHash, hash) {
				return ErrConflict
			}
			existing = row
			return nil
		case errors.Is(err, pgx.ErrNoRows):
			// fall through to insert
		default:
			return fmt.Errorf("lookup existing run: %w", err)
		}

		newRun, insertErr := insertRunTx(ctx, tx, identity, options, specPaths, owner, hash)
		if insertErr != nil {
			return insertErr
		}
		existing = newRun
		created = true

		seedID, seedCreated, seedErr := seedReportGroupTx(ctx, tx, newRun.Identity)
		if seedErr != nil {
			return seedErr
		}
		seeded = &SeededReportGroup{
			ID:         seedID,
			Created:    seedCreated,
			Repository: newRun.Identity.Repository,
			Branch:     newRun.Identity.Branch,
			CommitSHA:  newRun.Identity.CommitSHA,
			GHRunID:    newRun.Identity.GHRunID,
			GHPRNumber: newRun.Identity.GHPRNumber,
			Framework:  newRun.Identity.Framework,
		}
		return nil
	})
	if txErr != nil {
		return nil, false, nil, txErr
	}
	if created {
		logEvent(ctx, s.Logger, "orchestration.run.begin", "orchestration run started", existing,
			slog.Int("unit_count", existing.Counts.Total),
			slog.Int64("lease_timeout_ms", existing.LeaseTimeoutMs),
			slog.Int64("run_timeout_ms", existing.RunTimeoutMs),
			slog.Bool("retest_on_fail", existing.RetestOnFail),
		)
		logMetric(ctx, s.Logger, "orchestration_runs_started_total", "", 1)
	}
	return existing, created, seeded, nil
}

// seedReportGroupTx inserts (or no-ops on conflict) the report_groups row
// matching the orchestration run's composite identity so the report-index
// pages can render the run while orchestration is still in flight. Returns
// the resulting row's id and whether this call created it. The unique key
// is (repository, commit_sha, gh_run_id, name, gh_run_attempt) — see
// migrations/000002_report_groups.up.sql.
func seedReportGroupTx(ctx context.Context, tx pgx.Tx, identity CompositeIdentity) (uuid.UUID, bool, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO report_groups (
			repository, branch, commit_sha, gh_run_id, gh_run_attempt,
			framework, name, gh_pr_number, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'in_progress')
		ON CONFLICT (repository, commit_sha, gh_run_id, name, gh_run_attempt)
		DO UPDATE SET updated_at = now()
		RETURNING id, (xmax = 0) AS created
	`,
		identity.Repository, identity.Branch, identity.CommitSHA,
		identity.GHRunID, identity.GHRunAttempt,
		identity.Framework, identity.Name, identity.GHPRNumber,
	)
	var id uuid.UUID
	var created bool
	if err := row.Scan(&id, &created); err != nil {
		return uuid.Nil, false, fmt.Errorf("seed report_group: %w", err)
	}
	return id, created, nil
}

// GetRunSnapshot returns a stable read of the run identified by ci. Used by
// the run-status endpoint and the live-events bootstrap path.
func (s *Store) GetRunSnapshot(ctx context.Context, ci CompositeIdentity) (*Run, error) {
	row := s.Pool.QueryRow(ctx, runSelectByIdentity,
		ci.Repository, ci.CommitSHA, ci.GHRunID, ci.Name, ci.GHRunAttempt)
	run, err := scanRunRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get run snapshot: %w", err)
	}
	return run, nil
}

// FindRunByIdentity is the same lookup used internally by other store methods.
// Returns ErrNotFound when no row matches.
func (s *Store) FindRunByIdentity(ctx context.Context, ci CompositeIdentity) (*Run, error) {
	return s.GetRunSnapshot(ctx, ci)
}

// GetRunWithUnits returns the run plus its dispatch units, current leases
// (when a unit is leased), and the full per-spec attempt history. The
// dashboard renders one row per (unit, spec) so it needs all three relations
// in one read.
func (s *Store) GetRunWithUnits(ctx context.Context, ci CompositeIdentity) (*RunWithUnits, error) {
	run, err := s.GetRunSnapshot(ctx, ci)
	if err != nil {
		return nil, err
	}

	units, err := s.fetchUnitsWithLeases(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if err := s.attachAttempts(ctx, run.ID, units); err != nil {
		return nil, err
	}

	return &RunWithUnits{Run: run, Units: units}, nil
}

// fetchUnitsWithLeases returns units in dispatch_seq order with their current
// lease attached when state = 'leased'.
func (s *Store) fetchUnitsWithLeases(ctx context.Context, runID uuid.UUID) ([]UnitView, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT du.id, du.run_id, du.dispatch_seq, du.spec_path, du.state,
		       du.current_lease_id, du.lease_count, du.fail_count, du.outcome_set_at,
		       l.gh_job_name, l.gh_job_id, l.issued_at, l.deadline
		  FROM dispatch_units du
		  LEFT JOIN leases l ON l.id = du.current_lease_id
		 WHERE du.run_id = $1
		 ORDER BY du.dispatch_seq
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("fetch units: %w", err)
	}
	defer rows.Close()

	var out []UnitView
	for rows.Next() {
		var (
			u             UnitView
			leaseGHName   *string
			leaseGHID     *string
			leaseIssuedAt *time.Time
			leaseDeadline *time.Time
		)
		if err := rows.Scan(
			&u.ID, &u.RunID, &u.DispatchSeq, &u.SpecPath, &u.State,
			&u.CurrentLeaseID, &u.LeaseCount, &u.FailCount, &u.OutcomeSetAt,
			&leaseGHName, &leaseGHID, &leaseIssuedAt, &leaseDeadline,
		); err != nil {
			return nil, fmt.Errorf("scan unit: %w", err)
		}
		if u.CurrentLeaseID != nil && leaseGHName != nil && leaseGHID != nil &&
			leaseIssuedAt != nil && leaseDeadline != nil {
			u.CurrentLease = &CurrentLeaseView{
				ID:        *u.CurrentLeaseID,
				GHJobName: *leaseGHName,
				GHJobID:   *leaseGHID,
				IssuedAt:  *leaseIssuedAt,
				Deadline:  *leaseDeadline,
			}
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// attachAttempts loads every attempt for the run in one query and assigns
// each to its parent unit. Attempts are ordered by created_at so the
// dashboard can render history left-to-right per spec.
func (s *Store) attachAttempts(ctx context.Context, runID uuid.UUID, units []UnitView) error {
	if len(units) == 0 {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT a.id, a.lease_id, a.dispatch_unit_id, a.run_id, a.spec_path,
		       a.status, a.actual_duration_ms, a.error_message, a.error_stack,
		       a.test_cases, a.reported_at, a.late_report, a.expired, a.created_at,
		       l.gh_job_name, l.gh_job_id
		  FROM attempts a
		  JOIN leases l ON l.id = a.lease_id
		 WHERE a.run_id = $1
		 ORDER BY a.created_at, a.id
	`, runID)
	if err != nil {
		return fmt.Errorf("fetch attempts: %w", err)
	}
	defer rows.Close()

	byUnit := make(map[uuid.UUID]int, len(units))
	for i := range units {
		byUnit[units[i].ID] = i
	}

	for rows.Next() {
		var v AttemptView
		if err := rows.Scan(
			&v.ID, &v.LeaseID, &v.DispatchUnitID, &v.RunID, &v.SpecPath,
			&v.Status, &v.ActualDurationMs, &v.ErrorMessage, &v.ErrorStack,
			&v.TestCases, &v.ReportedAt, &v.LateReport, &v.Expired, &v.CreatedAt,
			&v.GHJobName, &v.GHJobID,
		); err != nil {
			return fmt.Errorf("scan attempt: %w", err)
		}
		if idx, ok := byUnit[v.DispatchUnitID]; ok {
			units[idx].Attempts = append(units[idx].Attempts, v)
		}
	}
	return rows.Err()
}

// runSelectByIdentity is the canonical SELECT used by every identity-keyed
// run lookup. The column list maps onto scanRunRow.
//
// commit_sha accepts exactly two forms: the full 40-char SHA (used by CI on
// begin/checkout/complete) or the first 7 chars (used by browser URL path
// segments). Other lengths intentionally miss so callers cannot search by
// arbitrary prefixes. Stored values are always full SHAs, so the equality
// branch and the substr branch never overlap for valid input.
const runSelectByIdentity = `
	SELECT id, repository, commit_sha, gh_run_id, name, gh_run_attempt,
	       framework, branch, gh_pr_number, playwright_project,
	       lease_timeout_ms, run_timeout_ms,
	       retest_on_fail, retest_budget, retest_eligible_count,
	       status,
	       pending_count, leased_count, completed_pass_count, completed_fail_count,
	       completed_skipped_count, abandoned_count, total_units,
	       dispatch_units_hash, started_at, deadline, terminal_at,
	       owner_oidc_subject, owner_api_key_id, created_at, updated_at
	  FROM orchestration_runs
	 WHERE repository = $1
	   AND (commit_sha = $2 OR substr(commit_sha, 1, 7) = $2)
	   AND gh_run_id = $3
	   AND name = $4 AND gh_run_attempt = $5
	 LIMIT 1
`

// scanRunRow scans a row produced by runSelectByIdentity (or any equivalent
// SELECT with the same column order) into a Run.
func scanRunRow(row pgx.Row) (*Run, error) {
	var (
		r                Run
		ghPRNumber       *int
		playwrightProj   *string
		retestEligible   int
		terminalAt       *time.Time
		ownerOIDCSubject *string
		ownerAPIKeyID    *uuid.UUID
	)
	err := row.Scan(
		&r.ID, &r.Identity.Repository, &r.Identity.CommitSHA, &r.Identity.GHRunID,
		&r.Identity.Name, &r.Identity.GHRunAttempt,
		&r.Identity.Framework, &r.Identity.Branch, &ghPRNumber, &playwrightProj,
		&r.LeaseTimeoutMs, &r.RunTimeoutMs,
		&r.RetestOnFail, &r.RetestBudget, &retestEligible,
		&r.Status,
		&r.Counts.Pending, &r.Counts.Leased, &r.Counts.CompletedPass, &r.Counts.CompletedFail,
		&r.Counts.CompletedSkipped, &r.Counts.Abandoned, &r.Counts.Total,
		&r.DispatchUnitsHash, &r.StartedAt, &r.Deadline, &terminalAt,
		&ownerOIDCSubject, &ownerAPIKeyID, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	r.Identity.GHPRNumber = ghPRNumber
	if playwrightProj != nil {
		r.PlaywrightProject = *playwrightProj
	}
	r.Counts.RetestEligible = retestEligible
	r.TerminalAt = terminalAt
	r.OwnerOIDCSubject = ownerOIDCSubject
	r.OwnerAPIKeyID = ownerAPIKeyID
	return &r, nil
}

// insertRunTx performs the orchestration_runs INSERT and the bulk-insert of
// dispatch_units within an existing transaction. Returns the populated Run.
func insertRunTx(
	ctx context.Context,
	tx pgx.Tx,
	identity CompositeIdentity,
	options BeginRunOptions,
	specPaths []string,
	owner OwnerInfo,
	hash []byte,
) (*Run, error) {
	leaseTimeoutMs := options.LeaseTimeoutMs
	if leaseTimeoutMs <= 0 {
		leaseTimeoutMs = 5 * 60 * 1000
	}
	runTimeoutMs := options.RunTimeoutMs
	if runTimeoutMs <= 0 {
		runTimeoutMs = 20 * 60 * 1000
	}
	retestBudget := options.RetestBudget
	if retestBudget < 0 {
		retestBudget = 0
	}

	branch := identity.Branch
	if branch == "" {
		branch = options.Branch
	}
	ghPR := identity.GHPRNumber
	if ghPR == nil {
		ghPR = options.GHPRNumber
	}
	var playwrightProj *string
	if options.PlaywrightProject != "" {
		v := options.PlaywrightProject
		playwrightProj = &v
	}

	totalUnits := len(specPaths)

	var (
		runID      uuid.UUID
		startedAt  time.Time
		deadline   time.Time
		createdAt  time.Time
		updatedAt  time.Time
		ghPROut    *int
		projectOut *string
	)
	err := tx.QueryRow(ctx, `
		INSERT INTO orchestration_runs (
			repository, commit_sha, gh_run_id, name, gh_run_attempt,
			framework, branch, gh_pr_number, playwright_project,
			lease_timeout_ms, run_timeout_ms,
			retest_on_fail, retest_budget, retest_eligible_count,
			status,
			pending_count, leased_count,
			completed_pass_count, completed_fail_count,
			completed_skipped_count, abandoned_count, total_units,
			dispatch_units_hash,
			started_at, deadline,
			owner_oidc_subject, owner_api_key_id
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11,
			$12, $13, 0,
			'in_progress',
			$14, 0,
			0, 0,
			0, 0, $14,
			$15,
			now(), now() + make_interval(secs => $18),
			$16, $17
		)
		RETURNING id, started_at, deadline, gh_pr_number, playwright_project,
		          created_at, updated_at
	`,
		identity.Repository, identity.CommitSHA, identity.GHRunID,
		identity.Name, identity.GHRunAttempt,
		identity.Framework, branch, ghPR, playwrightProj,
		leaseTimeoutMs, runTimeoutMs,
		options.RetestOnFail, retestBudget,
		totalUnits,
		hash,
		owner.OIDCSubject, owner.APIKeyID,
		float64(runTimeoutMs)/1000.0,
	).Scan(&runID, &startedAt, &deadline, &ghPROut, &projectOut, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert orchestration_runs: %w", err)
	}

	if err := bulkInsertDispatchUnitsTx(ctx, tx, runID, specPaths); err != nil {
		return nil, err
	}

	run := &Run{
		ID:                runID,
		Identity:          identity,
		PlaywrightProject: options.PlaywrightProject,
		LeaseTimeoutMs:    leaseTimeoutMs,
		RunTimeoutMs:      runTimeoutMs,
		RetestOnFail:      options.RetestOnFail,
		RetestBudget:      retestBudget,
		Status:            RunStatusInProgress,
		Counts: RunCounts{
			Pending: totalUnits,
			Total:   totalUnits,
		},
		DispatchUnitsHash: hash,
		StartedAt:         startedAt,
		Deadline:          deadline,
		OwnerOIDCSubject:  owner.OIDCSubject,
		OwnerAPIKeyID:     owner.APIKeyID,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}
	run.Identity.Branch = branch
	run.Identity.GHPRNumber = ghPROut
	if projectOut != nil {
		run.PlaywrightProject = *projectOut
	}
	return run, nil
}

// bulkInsertDispatchUnitsTx batches INSERTs of dispatch_units rows for the
// given run. Each unit gets dispatch_seq = its position in the input slice
// (0-indexed) and starts in 'pending' state.
func bulkInsertDispatchUnitsTx(
	ctx context.Context,
	tx pgx.Tx,
	runID uuid.UUID,
	specPaths []string,
) error {
	batch := &pgx.Batch{}
	for i, sp := range specPaths {
		batch.Queue(`
			INSERT INTO dispatch_units (run_id, dispatch_seq, spec_path, state)
			VALUES ($1, $2, $3, 'pending')
		`, runID, i, sp)
	}
	br := tx.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()
	for i := range specPaths {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("insert dispatch_units[%d]: %w", i, err)
		}
	}
	return nil
}
