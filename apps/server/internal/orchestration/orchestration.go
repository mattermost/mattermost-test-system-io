// Package orchestration is the domain layer for the Test Shard Orchestration
// feature: stateless API addressing by composite identity, atomic dispatch of
// spec-file units to GitHub Actions worker shards, lease/run-level timeouts,
// per-unit retest, and live progress events.
//
// The package contains only types, enums, and small pure helpers. Database
// I/O lives in store.go; HTTP handlers, route wiring, and event publishing
// live in sibling packages.
package orchestration

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Framework label constants. Membership of a value in supportedFrameworks
// below implies the rest of the orchestration stack (queue, leases,
// retest, screenshot upload) is exercised against it; the
// orchestration_runs.framework CHECK constraint in the database mirrors
// this set and MUST be kept in sync when a new framework is added.
const (
	FrameworkPlaywright = "playwright"
	FrameworkCypress    = "cypress"
	FrameworkDetox      = "detox"
	FrameworkMaestro    = "maestro"

	// DefaultFramework is the value used when a request omits framework.
	// Kept at playwright for backward compatibility with consumers that
	// pre-date Cypress support.
	DefaultFramework = FrameworkPlaywright
)

// supportedFrameworks is the closed set of accepted framework labels.
// Lookup is membership-only; callers do not need to know the order.
var supportedFrameworks = map[string]struct{}{
	FrameworkPlaywright: {},
	FrameworkCypress:    {},
	FrameworkDetox:      {},
	FrameworkMaestro:    {},
}

// IsSupportedFramework reports whether s is one of the orchestration-
// supported framework labels. Empty input returns false — callers apply
// DefaultFramework before consulting this.
func IsSupportedFramework(s string) bool {
	_, ok := supportedFrameworks[s]
	return ok
}

// SupportedFrameworksList returns accepted framework labels in a stable
// order for error messages and docs.
func SupportedFrameworksList() []string {
	order := []string{FrameworkPlaywright, FrameworkCypress, FrameworkDetox, FrameworkMaestro}
	out := make([]string, 0, len(order))
	for _, f := range order {
		if _, ok := supportedFrameworks[f]; ok {
			out = append(out, f)
		}
	}
	return out
}

// Run status enum values.
const (
	RunStatusInProgress = "in_progress"
	RunStatusCompleted  = "completed"
	RunStatusTimedOut   = "timed_out"
)

// DispatchUnit state enum values.
const (
	UnitStatePending          = "pending"
	UnitStateLeased           = "leased"
	UnitStateCompletedPass    = "completed_pass"
	UnitStateCompletedFail    = "completed_fail"
	UnitStateCompletedSkipped = "completed_skipped"
	UnitStateAbandoned        = "abandoned"
)

// Lease release reason enum values.
const (
	LeaseReleaseCompleted   = "completed"
	LeaseReleaseExpired     = "expired"
	LeaseReleaseRunTimedOut = "run_timed_out"
)

// Attempt status enum values. Mirrors test_cases.status.
const (
	AttemptStatusPassed      = "passed"
	AttemptStatusFailed      = "failed"
	AttemptStatusSkipped     = "skipped"
	AttemptStatusFlaky       = "flaky"
	AttemptStatusTimedOut    = "timedOut"
	AttemptStatusInterrupted = "interrupted"
)

// Sentinel errors returned from the domain and store layers. Handlers map
// these to HTTP responses; the domain package is HTTP-agnostic.
var (
	// ErrConflict signals that a request conflicts with persisted state
	// (e.g. begin run with a different dispatch list, or checkout while the
	// worker already has an active lease).
	ErrConflict = errors.New("orchestration: conflict")

	// ErrNotFound signals that the addressed run / lease / unit does not
	// exist.
	ErrNotFound = errors.New("orchestration: not found")

	// ErrWorkerHasActiveLease signals a checkout from a worker that already
	// has an unreleased lease on the same run.
	ErrWorkerHasActiveLease = errors.New("orchestration: worker already has an active lease")

	// ErrRunNotInProgress signals that an operation requiring an active run
	// was issued against a run that has reached a terminal state.
	ErrRunNotInProgress = errors.New("orchestration: run is not in progress")

	// ErrPartialReport signals that a complete call covered fewer specs
	// than the lease's unit set.
	ErrPartialReport = errors.New("orchestration: partial report not allowed")

	// ErrUnknownLease signals that no lease exists for the given worker
	// identity on the given run (active or released).
	ErrUnknownLease = errors.New("orchestration: unknown lease for worker")
)

// CompositeIdentity is the public, stateless addressing tuple for an
// orchestration run. Mirrors the columns used by the existing report_groups
// table so the UI can join the two systems at query time without a foreign
// key.
type CompositeIdentity struct {
	Repository   string
	CommitSHA    string
	GHRunID      string
	Name         string
	GHRunAttempt string // defaults to "1" when omitted by the caller
	Branch       string
	GHPRNumber   *int
	Framework    string // pinned to the package-level Framework constant
}

// WorkerIdentity is the structured worker identifier carried on checkout and
// complete. Both fields are required and mirror reports.gh_job_name /
// reports.gh_job_id used by the per-shard upload flow.
type WorkerIdentity struct {
	GHJobName string
	GHJobID   string
}

// RunCounts is the materialized counter snapshot stored on orchestration_runs.
// The six state counters always sum to Total; RetestEligible is a derived
// subset of CompletedFail (units waiting to be re-leased) and is NOT included
// in the sum.
type RunCounts struct {
	Pending          int
	Leased           int
	CompletedPass    int
	CompletedFail    int
	CompletedSkipped int
	Abandoned        int
	RetestEligible   int
	Total            int
}

// Run is the in-memory view of an orchestration_runs row plus its
// materialized counters and owner.
type Run struct {
	ID                uuid.UUID // internal PK; never exposed in the API
	Identity          CompositeIdentity
	PlaywrightProject string
	LeaseTimeoutMs    int64
	// IdleTimeoutMs is the inactivity window: a run is reaped when no
	// checkout/complete has touched it for this many milliseconds.
	IdleTimeoutMs     int64
	RetestOnFail      bool
	RetestBudget      int
	Status            string
	Counts            RunCounts
	DispatchUnitsHash []byte
	StartedAt         time.Time
	// LastActivityAt is bumped to now() on every successful checkout and
	// complete. Combined with IdleTimeoutMs by the reaper to decide when
	// the run transitions to 'timed_out'.
	LastActivityAt   time.Time
	TerminalAt       *time.Time
	OwnerOIDCSubject *string
	OwnerAPIKeyID    *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// DispatchUnit is the in-memory view of a dispatch_units row. Each unit
// addresses exactly one spec file; the orchestrator does not bundle specs.
type DispatchUnit struct {
	ID             uuid.UUID
	RunID          uuid.UUID
	DispatchSeq    int
	SpecPath       string
	State          string
	CurrentLeaseID *uuid.UUID
	LeaseCount     int
	FailCount      int
	OutcomeSetAt   *time.Time
}

// Lease is the in-memory view of a leases row.
type Lease struct {
	ID              uuid.UUID
	RunID           uuid.UUID
	Worker          WorkerIdentity
	UnitIDs         []uuid.UUID
	IssuedAt        time.Time
	Deadline        time.Time
	ReleasedAt      *time.Time
	ReleaseReason   *string
	AuthOIDCSubject *string
	AuthAPIKeyID    *uuid.UUID
}

// Attempt is the in-memory view of an attempts row.
type Attempt struct {
	ID               uuid.UUID
	LeaseID          uuid.UUID
	DispatchUnitID   uuid.UUID
	RunID            uuid.UUID
	SpecPath         string
	Status           *string
	ActualDurationMs *int64
	ErrorMessage     *string
	ErrorStack       *string
	TestCases        json.RawMessage
	ReportedAt       *time.Time
	LateReport       bool
	Expired          bool
	CreatedAt        time.Time
}

// AttemptView is an Attempt denormalized with the worker name from the
// joined leases row. Exposed in the run-status payload so the dashboard
// can show "spec X was run by worker Y" without a per-row lookup.
type AttemptView struct {
	Attempt
	GHJobName string
	GHJobID   string
}

// CurrentLeaseView captures the minimal lease info shown on a unit while it
// is in the leased state. Populated only when DispatchUnit.CurrentLeaseID is
// non-nil; nil otherwise.
type CurrentLeaseView struct {
	ID        uuid.UUID
	GHJobName string
	GHJobID   string
	IssuedAt  time.Time
	Deadline  time.Time
}

// UnitView bundles a DispatchUnit with the data the dashboard needs to
// render its current state: the active lease (when leased) and the full
// attempt history. Attempts are ordered oldest-first.
type UnitView struct {
	DispatchUnit
	CurrentLease *CurrentLeaseView
	Attempts     []AttemptView
}

// RunWithUnits is the full read-model returned by Store.GetRunWithUnits and
// serialized into the /orchestration/status and /orchestration/begin
// response bodies. Units are ordered by dispatch_seq.
type RunWithUnits struct {
	Run   *Run
	Units []UnitView
}

// Validate enforces required fields and the framework pin on a composite
// identity. It does NOT mutate (Framework defaulting is the caller's job —
// typically the HTTP handler).
func (ci CompositeIdentity) Validate() error {
	if ci.Repository == "" {
		return errors.New("composite identity: repository is required")
	}
	if ci.CommitSHA == "" {
		return errors.New("composite identity: commit_sha is required")
	}
	if ci.GHRunID == "" {
		return errors.New("composite identity: gh_run_id is required")
	}
	if ci.Name == "" {
		return errors.New("composite identity: name is required")
	}
	if ci.GHRunAttempt == "" {
		return errors.New("composite identity: gh_run_attempt is required")
	}
	if !IsSupportedFramework(ci.Framework) {
		return fmt.Errorf("composite identity: framework %q is not supported (must be one of: %s)", ci.Framework, strings.Join(SupportedFrameworksList(), ", "))
	}
	return nil
}

// HashUnits returns a SHA-256 digest of the canonical-form serialization of
// the dispatch-units list. Canonical form: the spec_path strings are kept
// in submission order (no sort) and JSON-marshaled, then hashed. Used by
// the begin-run idempotency check — a retry whose hash matches the stored
// value is treated as a no-op replay; a retry with a different hash on
// the same composite identity is a conflict.
//
// Note: caller submission order is part of the canonical form because
// dispatch_seq drives FIFO checkout — re-ordering changes run behavior
// and must therefore be a different run.
func (ci CompositeIdentity) HashUnits(specPaths []string) []byte {
	buf, _ := json.Marshal(specPaths)
	sum := sha256.Sum256(buf)
	return sum[:]
}
