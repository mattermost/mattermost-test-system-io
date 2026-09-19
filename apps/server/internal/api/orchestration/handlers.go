// Package orchestration serves /api/v1/orchestration/* endpoints. Workers in
// a CI matrix register a run with an ordered list of dispatch units, check
// out units against a server-issued lease, report completion, optionally
// upload failure screenshots, and poll the run's status. Domain logic lives
// in apps/server/internal/orchestration; this package is the HTTP surface.
package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/cache"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

// defaultMaxSpecsPerRun caps the total spec count summed across every
// dispatch unit in a single begin-run request. Mirrors the OpenAPI's
// dispatch_units.maxItems=5000 ceiling at the spec level — a 5000-unit
// run with one spec each remains accepted; one with 5000 multi-spec
// units that aggregate to more than the cap is rejected with
// TOO_MANY_SPECS. Override via TSIO_ORCH_MAX_SPECS_PER_RUN.
const defaultMaxSpecsPerRun = 5000

// maxSpecsPerRunEnvVar is the env override knob name. Non-positive or
// unparseable values fall back to defaultMaxSpecsPerRun.
const maxSpecsPerRunEnvVar = "TSIO_ORCH_MAX_SPECS_PER_RUN"

// checkoutRetryAfterBaseMs / checkoutRetryAfterJitterMs form the polling
// hint returned to a worker that called /checkout with no work available
// but more that might still arrive (other workers leased, retest pool
// non-empty). The worker sleeps this long and re-polls instead of exiting.
//
// Jitter is randomized per response, not fixed, so concurrently-polling
// workers desynchronize instead of staying phase-locked on the same
// interval indefinitely.
const (
	checkoutRetryAfterBaseMs   = 5000
	checkoutRetryAfterJitterMs = 2000
)

// checkoutRetryAfterMs returns a randomized hint in
// [checkoutRetryAfterBaseMs, checkoutRetryAfterBaseMs+checkoutRetryAfterJitterMs).
func checkoutRetryAfterMs() int {
	return checkoutRetryAfterBaseMs + rand.IntN(checkoutRetryAfterJitterMs) //nolint:gosec // non-cryptographic jitter
}

// defaultStatusCacheTTLMs bounds staleness of the /orchestration/status
// snapshot. It sits below the dashboard's ~5s poll cadence so hundreds of
// concurrent viewers of one live run collapse onto a single backing query per
// window, while remaining fresh enough that the poll (a WebSocket safety net)
// still reflects progress promptly. Override via TSIO_ORCH_STATUS_CACHE_TTL_MS;
// a value <= 0 disables caching (used by tests that assert read-after-write).
const defaultStatusCacheTTLMs = 2000

// statusCacheTTLEnvVar is the override knob for defaultStatusCacheTTLMs.
const statusCacheTTLEnvVar = "TSIO_ORCH_STATUS_CACHE_TTL_MS"

// resolveStatusCacheTTL reads TSIO_ORCH_STATUS_CACHE_TTL_MS. Missing/invalid
// falls back to the default; an explicit non-positive value disables caching
// and returns a non-positive duration.
func resolveStatusCacheTTL() time.Duration {
	raw := os.Getenv(statusCacheTTLEnvVar)
	if raw == "" {
		return defaultStatusCacheTTLMs * time.Millisecond
	}
	ms, err := strconv.Atoi(raw)
	if err != nil {
		return defaultStatusCacheTTLMs * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}

// Handlers bundles the orchestration HTTP handlers. All fields are populated
// by server.Build; nil-checks are the responsibility of individual handler
// methods once they move beyond stubs.
type Handlers struct {
	Pool        *pgxpool.Pool
	Store       *orchestration.Store
	ObjectStore storage.ObjectStore
	Publisher   *orchestration.Publisher
	// ReportsPublisher publishes the report_created event when BeginRun
	// seeds a new report_groups row, so the existing /reports index pages
	// pick up an in-flight orchestration run as soon as it starts. Optional;
	// nil-check at the call site so the orchestration flow keeps working
	// when wiring is incomplete.
	ReportsPublisher *events.Publisher
	Logger           *slog.Logger

	// LeaseRetentionMs is how long after a lease's release_at a worker may
	// still upload screenshots or retry /complete. Default 60_000.
	LeaseRetentionMs int64

	// MaxScreenshotBytes caps the size of a single multipart screenshot
	// upload. Default 10 * 1024 * 1024.
	MaxScreenshotBytes int64

	// statusCache memoizes /orchestration/status response bodies per
	// (identity, view) for a short TTL so concurrent polling dashboards
	// collapse onto one DB read per window. Lazily initialized; nil when
	// caching is disabled (TTL <= 0).
	statusCacheOnce sync.Once
	statusCache     *cache.TTLCache
}

func (h *Handlers) initStatusCache() {
	h.statusCacheOnce.Do(func() {
		if ttl := resolveStatusCacheTTL(); ttl > 0 {
			h.statusCache = cache.New(ttl)
		}
	})
}

// ---------- request/response bodies ----------

// identityFields is the JSON projection of orchestration.CompositeIdentity.
// Embedded into the per-endpoint request bodies via composition.
type identityFields struct {
	Repository   string `json:"repository"`
	CommitSHA    string `json:"commit_sha"`
	GHRunID      string `json:"gh_run_id"`
	Name         string `json:"name"`
	GHRunAttempt string `json:"gh_run_attempt"`
	Branch       string `json:"branch"`
	GHPRNumber   *int   `json:"gh_pr_number,omitempty"`
	Framework    string `json:"framework"`
}

type workerFields struct {
	GHJobName string `json:"gh_job_name"`
	GHJobID   string `json:"gh_job_id"`
}

type beginUnitBody struct {
	SpecPath string `json:"spec_path"`
}

type beginRunBody struct {
	identityFields
	PlaywrightProject    string          `json:"playwright_project"`
	LeaseTimeoutMs       int64           `json:"lease_timeout_ms"`
	IdleTimeoutMs        int64           `json:"idle_timeout_ms"`
	RetestOnFail         bool            `json:"retest_on_fail"`
	RetestBudget         int             `json:"retest_budget"`
	TotalReportsExpected int             `json:"total_reports_expected"`
	DispatchUnits        []beginUnitBody `json:"dispatch_units"`
}

type checkoutBody struct {
	identityFields
	workerFields
	BatchSize int `json:"batch_size"`
}

type completeResultBody struct {
	SpecPath         string          `json:"spec_path"`
	Status           string          `json:"status"`
	ActualDurationMs *int64          `json:"actual_duration_ms,omitempty"`
	ErrorMessage     *string         `json:"error_message,omitempty"`
	ErrorStack       *string         `json:"error_stack,omitempty"`
	TestCases        json.RawMessage `json:"test_cases,omitempty"`
}

type completeBody struct {
	identityFields
	workerFields
	Results []completeResultBody `json:"results"`
}

// ---------- handlers ----------

// BeginRun serves POST /api/v1/orchestration/begin. Registers a new run with
// the suite's spec files (idempotent on the composite identity).
func (h *Handlers) BeginRun(w http.ResponseWriter, r *http.Request) {
	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}

	var body beginRunBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	identity, ierr := identityFromFields(body.identityFields)
	if ierr != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", ierr.Error())
		return
	}
	if len(body.DispatchUnits) == 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "dispatch_units must not be empty")
		return
	}
	specPaths := make([]string, 0, len(body.DispatchUnits))
	for i, u := range body.DispatchUnits {
		if u.SpecPath == "" {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
				fmt.Sprintf("dispatch_units[%d].spec_path must not be empty", i))
			return
		}
		specPaths = append(specPaths, u.SpecPath)
	}
	maxSpecs := resolveMaxSpecsPerRun()
	if len(specPaths) > maxSpecs {
		api.WriteErrorCode(w, http.StatusBadRequest, "TOO_MANY_SPECS",
			fmt.Sprintf("total spec count %d exceeds the per-run limit of %d", len(specPaths), maxSpecs))
		return
	}

	if body.TotalReportsExpected <= 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
			"total_reports_expected is required and must be > 0")
		return
	}
	options := orchestration.BeginRunOptions{
		LeaseTimeoutMs:       body.LeaseTimeoutMs,
		IdleTimeoutMs:        body.IdleTimeoutMs,
		RetestOnFail:         body.RetestOnFail,
		RetestBudget:         body.RetestBudget,
		TotalReportsExpected: body.TotalReportsExpected,
		PlaywrightProject:    body.PlaywrightProject,
		Branch:               body.Branch,
		GHPRNumber:           body.GHPRNumber,
	}
	owner := ownerFromSubject(subject)
	if owner.OIDCSubject == nil && owner.APIKeyID == nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}

	run, created, seeded, err := h.Store.BeginRun(r.Context(), identity, options, specPaths, owner)
	if err != nil {
		switch {
		case errors.Is(err, orchestration.ErrConflict):
			api.WriteErrorCode(w, http.StatusConflict, "BEGIN_RUN_HASH_MISMATCH",
				"composite identity already exists with a different dispatch unit list")
		default:
			api.WriteError(w, r, err)
		}
		return
	}

	if created && h.Publisher != nil {
		h.Publisher.RunStarted(r.Context(), run.Identity, run.Counts.Total, run.IdleTimeoutMs, run.LeaseTimeoutMs)
	}
	// Notify the reports-side WebSocket subscribers so the /reports index
	// pages pick up the run-as-report_group as soon as begin commits. Only
	// fire when this call's transaction actually inserted the report_groups
	// row — the upload flow may have created it earlier (in which case it
	// already published its own event).
	if created && seeded != nil && seeded.Created && h.ReportsPublisher != nil {
		ref := refFromBranch(seeded.Branch)
		actor := actorFromSubject(subject)
		h.ReportsPublisher.ReportCreated(
			seeded.ID, seeded.Framework, seeded.Repository, ref,
			seeded.CommitSHA, actor, seeded.GHRunID, seeded.GHPRNumber,
			time.Now().UTC(),
		)
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	out := runSnapshotPayload(run)
	if snap, fetchErr := h.Store.GetRunWithUnits(r.Context(), run.Identity); fetchErr == nil {
		out["units"] = unitsPayload(snap.Units)
	}
	writeJSON(w, status, out)
}

// Checkout serves POST /api/v1/orchestration/checkout. Atomically dispatches
// up to batch_size pending units from the run's queue to the calling worker.
func (h *Handlers) Checkout(w http.ResponseWriter, r *http.Request) {
	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}

	var body checkoutBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	identity, ierr := identityFromFields(body.identityFields)
	if ierr != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", ierr.Error())
		return
	}
	worker, werr := workerFromFields(body.workerFields)
	if werr != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", werr.Error())
		return
	}
	batchSize := body.BatchSize
	if batchSize <= 0 {
		batchSize = 1
	}
	if batchSize > 64 {
		batchSize = 64
	}

	run, err := h.Store.FindRunByIdentity(r.Context(), identity)
	if err != nil {
		if errors.Is(err, orchestration.ErrNotFound) {
			api.WriteError(w, r, api.ErrNotFound)
			return
		}
		api.WriteError(w, r, err)
		return
	}
	if err := orchestration.CheckRunOwner(r.Context(), run, subject); err != nil {
		api.WriteError(w, r, api.ErrForbidden)
		return
	}
	if run.Status != orchestration.RunStatusInProgress {
		api.WriteErrorCode(w, http.StatusConflict, "RUN_NOT_IN_PROGRESS",
			"run is not in progress")
		return
	}

	lease, units, _, err := h.Store.AtomicCheckout(r.Context(), identity, worker, batchSize)
	if err != nil {
		switch {
		case errors.Is(err, orchestration.ErrWorkerHasActiveLease):
			api.WriteErrorCode(w, http.StatusConflict, "WORKER_HAS_ACTIVE_LEASE",
				"worker already holds an active lease on this run")
		case errors.Is(err, orchestration.ErrRunNotInProgress):
			api.WriteErrorCode(w, http.StatusConflict, "RUN_NOT_IN_PROGRESS",
				"run is not in progress")
		default:
			api.WriteError(w, r, err)
		}
		return
	}

	isRetest := false
	// Primary pool empty: when retest is enabled and first-pass is complete,
	// fall through to the retest dispatch. AtomicRetestCheckout returns
	// (nil, nil, false, nil) when there is nothing for this caller — we then
	// surface a clean queue_empty: true so the worker exits.
	if lease == nil && run.RetestOnFail {
		retestLease, retestUnits, retestIsRetest, retestErr := h.Store.AtomicRetestCheckout(
			r.Context(), identity, worker, batchSize)
		if retestErr != nil {
			switch {
			case errors.Is(retestErr, orchestration.ErrWorkerHasActiveLease):
				api.WriteErrorCode(w, http.StatusConflict, "WORKER_HAS_ACTIVE_LEASE",
					"worker already holds an active lease on this run")
			case errors.Is(retestErr, orchestration.ErrRunNotInProgress):
				api.WriteErrorCode(w, http.StatusConflict, "RUN_NOT_IN_PROGRESS",
					"run is not in progress")
			default:
				api.WriteError(w, r, retestErr)
			}
			return
		}
		lease = retestLease
		units = retestUnits
		isRetest = retestIsRetest
	}

	if lease == nil {
		// Re-read counts to decide whether the worker should poll again or
		// exit cleanly. Counts loaded earlier (line ~252) are stale after
		// AtomicCheckout / AtomicRetestCheckout. When something is still
		// in flight (leased > 0) or queued for retest (retest_eligible > 0),
		// the worker should sleep and re-check rather than exit.
		resp := map[string]any{
			"queue_empty": true,
			"is_retest":   false,
			"units":       []any{},
		}
		if freshRun, ferr := h.Store.FindRunByIdentity(r.Context(), identity); ferr == nil {
			c := freshRun.Counts
			if c.Leased > 0 || c.RetestEligible > 0 {
				resp["retry_after_ms"] = checkoutRetryAfterMs()
			}
			// Lets a polling worker log queue depth alongside its own
			// "queue empty; sleeping" line.
			resp["counts"] = queueCountsPayload(c)
			if wc, werr := h.Store.CountWorkers(r.Context(), freshRun.ID); werr == nil {
				resp["workers"] = workersPayload(wc)
			}
		}
		resp["db_pool"] = dbPoolPayload(h.Pool)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	unitIDs := make([]uuid.UUID, len(units))
	for i, u := range units {
		unitIDs[i] = u.ID
	}
	if h.Publisher != nil {
		h.Publisher.UnitLeased(r.Context(), identity, worker.GHJobName, worker.GHJobID,
			unitIDs, lease.Deadline, isRetest)
	}

	resp := map[string]any{
		"deadline":    lease.Deadline.UTC(),
		"queue_empty": false,
		"is_retest":   isRetest,
		"units":       checkoutUnitsPayload(units, isRetest),
	}
	// Best-effort: a failure here doesn't fail the checkout — the units
	// are already leased and durable.
	if freshRun, ferr := h.Store.FindRunByIdentity(r.Context(), identity); ferr == nil {
		resp["counts"] = queueCountsPayload(freshRun.Counts)
		if wc, werr := h.Store.CountWorkers(r.Context(), freshRun.ID); werr == nil {
			resp["workers"] = workersPayload(wc)
		}
	}
	resp["db_pool"] = dbPoolPayload(h.Pool)
	writeJSON(w, http.StatusOK, resp)
}

// workersPayload projects WorkerCounts into the response's "workers"
// object. `active` is workers currently holding an unreleased lease on
// this run; `seen_total` is every worker that has ever held one, active or
// released. A worker that crashed before its first successful checkout
// appears in neither.
func workersPayload(w orchestration.WorkerCounts) map[string]any {
	return map[string]any{
		"active":     w.Active,
		"seen_total": w.SeenTotal,
	}
}

// queueCountsPayload projects RunCounts using the same field names as
// runSnapshotPayload's "counts" object (plus "total").
func queueCountsPayload(c orchestration.RunCounts) map[string]any {
	return map[string]any{
		"pending":           c.Pending,
		"leased":            c.Leased,
		"completed_pass":    c.CompletedPass,
		"completed_fail":    c.CompletedFail,
		"completed_skipped": c.CompletedSkipped,
		"abandoned":         c.Abandoned,
		"retest_eligible":   c.RetestEligible,
		"total":             c.Total,
	}
}

// dbPoolPayload snapshots the shared pgxpool.Pool's in-memory counters (no
// DB round trip). The pool is server-wide, not scoped to a single run.
func dbPoolPayload(pool *pgxpool.Pool) map[string]any {
	if pool == nil {
		return nil
	}
	s := pool.Stat()
	return map[string]any{
		"total_conns":         s.TotalConns(),
		"acquired_conns":      s.AcquiredConns(),
		"idle_conns":          s.IdleConns(),
		"max_conns":           s.MaxConns(),
		"empty_acquire_count": s.EmptyAcquireCount(),
	}
}

// Complete serves POST /api/v1/orchestration/complete. The worker reports
// the outcome of every spec in its currently-held lease.
func (h *Handlers) Complete(w http.ResponseWriter, r *http.Request) {
	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}

	var body completeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	identity, ierr := identityFromFields(body.identityFields)
	if ierr != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", ierr.Error())
		return
	}
	worker, werr := workerFromFields(body.workerFields)
	if werr != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", werr.Error())
		return
	}
	if len(body.Results) == 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
			"results must not be empty")
		return
	}
	results := make([]orchestration.SpecResult, 0, len(body.Results))
	for i, item := range body.Results {
		if item.SpecPath == "" {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
				fmt.Sprintf("results[%d].spec_path is required", i))
			return
		}
		if item.Status == "" {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
				fmt.Sprintf("results[%d].status is required", i))
			return
		}
		results = append(results, orchestration.SpecResult{
			SpecPath:         item.SpecPath,
			Status:           item.Status,
			ActualDurationMs: item.ActualDurationMs,
			ErrorMessage:     item.ErrorMessage,
			ErrorStack:       item.ErrorStack,
			TestCases:        item.TestCases,
		})
	}

	run, err := h.Store.FindRunByIdentity(r.Context(), identity)
	if err != nil {
		if errors.Is(err, orchestration.ErrNotFound) {
			api.WriteError(w, r, api.ErrNotFound)
			return
		}
		api.WriteError(w, r, err)
		return
	}
	if err := orchestration.CheckRunOwner(r.Context(), run, subject); err != nil {
		api.WriteError(w, r, api.ErrForbidden)
		return
	}

	outcome, err := h.Store.RecordCompletion(r.Context(), identity, worker, results)
	if err != nil {
		switch {
		case errors.Is(err, orchestration.ErrUnknownLease):
			api.WriteError(w, r, api.ErrNotFound)
		case errors.Is(err, orchestration.ErrPartialReport):
			api.WriteErrorCode(w, http.StatusBadRequest, "PARTIAL_REPORT", err.Error())
		default:
			api.WriteError(w, r, err)
		}
		return
	}

	// Best-effort; computed once and reused by both response branches below.
	var workers map[string]any
	if wc, werr := h.Store.CountWorkers(r.Context(), run.ID); werr == nil {
		workers = workersPayload(wc)
	}

	if outcome.Idempotent {
		resp := map[string]any{
			"accepted":            true,
			"late_report":         outcome.LateReport,
			"unit_states_changed": []any{},
			// outcome.RunCounts is populated on every RecordCompletion path,
			// idempotent replay included — no extra query needed here.
			"counts":  queueCountsPayload(outcome.RunCounts),
			"db_pool": dbPoolPayload(h.Pool),
		}
		if workers != nil {
			resp["workers"] = workers
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if h.Publisher != nil {
		for _, change := range outcome.UnitStatesChanged {
			h.Publisher.UnitCompleted(r.Context(), identity, change.UnitID,
				change.ToState, outcome.LateReport, len(results))
		}
		if outcome.RunNowCompleted {
			terminalAt := time.Now().UTC()
			h.Publisher.RunCompleted(r.Context(), identity, terminalAt, outcome.RunCounts)
		}
	}

	resp := map[string]any{
		"accepted":            true,
		"late_report":         outcome.LateReport,
		"unit_states_changed": unitStateChangesPayload(outcome.UnitStatesChanged),
		"counts":              queueCountsPayload(outcome.RunCounts),
		"db_pool":             dbPoolPayload(h.Pool),
	}
	if workers != nil {
		resp["workers"] = workers
	}
	writeJSON(w, http.StatusOK, resp)
}

// Status serves GET /api/v1/orchestration/status. Returns the run's current
// status and unit-count breakdown for the supplied composite identity.
//
// Auth: the route is mounted under RequireAuth so a valid subject is always
// present. The endpoint is a public read alongside the other report-read
// endpoints — anonymous callers (dashboard, status pollers, CI controllers)
// can fetch the snapshot. The composite identity is required as input, so
// no enumeration of unrelated runs is possible without already knowing it.
func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	h.initStatusCache()
	q := r.URL.Query()
	fields := identityFields{
		Repository:   q.Get("repository"),
		CommitSHA:    q.Get("commit_sha"),
		GHRunID:      q.Get("gh_run_id"),
		Name:         q.Get("name"),
		GHRunAttempt: q.Get("gh_run_attempt"),
		Branch:       q.Get("branch"),
		Framework:    q.Get("framework"),
	}
	identity, ierr := identityFromFields(fields)
	if ierr != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", ierr.Error())
		return
	}
	// view=summary omits the (potentially multi-MB) per-unit + per-attempt
	// detail, returning only the run snapshot, counts, tests, and durations.
	// CI consumers and live progress indicators that just track counts should
	// prefer it; the default keeps the full units[] for the dashboard's grid.
	summary := q.Get("view") == "summary"

	// Cache key: identity tuple + view. Collapses concurrent polls of the
	// same run onto one backing query per TTL window. When caching is
	// disabled (TTL <= 0) the compute runs inline for a fresh read.
	var (
		body []byte
		err  error
	)
	if h.statusCache != nil {
		key := statusCacheKey(identity, summary)
		body, err = h.statusCache.Get(r.Context(), key, func(ctx context.Context) ([]byte, error) {
			return h.computeStatus(ctx, identity, summary)
		})
	} else {
		body, err = h.computeStatus(r.Context(), identity, summary)
	}
	if err != nil {
		if errors.Is(err, orchestration.ErrNotFound) {
			api.WriteError(w, r, api.ErrNotFound)
			return
		}
		api.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=2, stale-while-revalidate=5")
	_, _ = w.Write(body) //nolint:gosec // G705 — JSON response, not HTML
}

// statusCacheKey builds the per-(identity, view) cache key. Uses cache.Key's
// length-prefixed encoding so query values containing embedded separators/NUL
// bytes cannot collapse distinct identities onto one entry.
func statusCacheKey(identity orchestration.CompositeIdentity, summary bool) string {
	view := "full"
	if summary {
		view = "summary"
	}
	return cache.Key(
		view, identity.Repository, identity.CommitSHA, identity.GHRunID,
		identity.Name, identity.GHRunAttempt,
	)
}

// computeStatus runs the DB reads for a status snapshot and marshals the
// response body. The `units` relation (and its per-attempt test_cases JSONB,
// which dominates the payload and query cost on large runs) is fetched only
// for the full view. Returns orchestration.ErrNotFound when the run does not
// exist so the caller can map it to 404 without the error being cached.
func (h *Handlers) computeStatus(
	ctx context.Context, identity orchestration.CompositeIdentity, summary bool,
) ([]byte, error) {
	var run *orchestration.Run
	var out map[string]any
	if summary {
		r, err := h.Store.FindRunByIdentity(ctx, identity)
		if err != nil {
			return nil, err
		}
		run = r
		out = runSnapshotPayload(run)
	} else {
		snap, err := h.Store.GetRunWithUnits(ctx, identity)
		if err != nil {
			return nil, err
		}
		run = snap.Run
		out = runSnapshotPayload(run)
		out["units"] = unitsPayload(snap.Units)
	}

	// Best-effort enrichment with the test-case rollup. CI consumers
	// (test-system-io-summary action → commit-status description) want
	// test-level counts (e.g. "457/472"), not unit-level counts. Returns
	// nil when no attempts have reported test_cases yet — the response
	// just omits the field in that case, matching the listing endpoints'
	// `tests` shape. A failure here doesn't fail the request: status is
	// the load-bearing field and was already produced above.
	if t, terr := aggregateTestCounts(ctx, h.Pool, run.ID); terr == nil && t != nil {
		out["tests"] = t
	} else if terr != nil {
		h.Logger.Warn("status: aggregateTestCounts failed",
			slog.String("run_id", run.ID.String()),
			slog.Any("err", terr))
	}

	// First-pass / retest wall-clock split. Used by the summary action
	// to render `first-pass + retest` durations in the commit-status
	// description. Best-effort: surface 0 / null when computation fails
	// rather than failing the request.
	if d, derr := aggregateDurations(ctx, h.Pool, run.ID, run.StartedAt); derr == nil && d != nil {
		out["durations"] = d
	} else if derr != nil {
		h.Logger.Warn("status: aggregateDurations failed",
			slog.String("run_id", run.ID.String()),
			slog.Any("err", derr))
	}

	return json.Marshal(out)
}

// testCounts is the test-case-level rollup the /status endpoint exposes
// alongside the unit-level counts. Mirrors orchestrationTestCounts in the
// reports package — kept duplicated here to avoid a cross-package import
// cycle for a 6-field DTO.
type testCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Flaky   int `json:"flaky"`
	Skipped int `json:"skipped"`
	Total   int `json:"total"`
}

// runDurations is the first-pass vs retest wall-clock split exposed on
// /status for CI consumers, plus the absolute timestamps the dashboard
// renders as a timeline (begin → first test → first retest → last test).
// All ms fields are nullable; timestamp fields are nullable too except
// `BeginAt`, which is always the run's started_at.
type runDurations struct {
	FirstPassMs     *int64     `json:"first_pass_ms,omitempty"`
	RetestMs        *int64     `json:"retest_ms,omitempty"`
	RetestUnitCount int        `json:"retest_unit_count"`
	BeginAt         time.Time  `json:"begin_at"`
	FirstTestAt     *time.Time `json:"first_test_at,omitempty"`
	FirstRetestAt   *time.Time `json:"first_retest_at,omitempty"`
	LastTestAt      *time.Time `json:"last_test_at,omitempty"`
}

// aggregateDurations splits the run's wall-clock into a first-pass and a
// retest portion using the attempts table. "First attempt per unit" is
// MIN(created_at) for each dispatch_unit_id; everything later on the same
// unit is a retest. Also counts how many distinct units were re-leased so
// the consumer can render `:repeat: re-run N spec(s)`, and surfaces the
// absolute timestamps behind the durations so the dashboard's timeline
// row can show begin → first test → first retest → last test. Returns
// (nil, nil) when no attempts have reported yet.
func aggregateDurations(
	ctx context.Context, pool *pgxpool.Pool, runID uuid.UUID, runStartedAt time.Time,
) (*runDurations, error) {
	var (
		firstAttemptStart *time.Time
		firstPassEnd      *time.Time
		retestStart       *time.Time
		retestEnd         *time.Time
		retestUnitCount   int
	)
	err := pool.QueryRow(ctx, `
		WITH first_per_unit AS (
			SELECT DISTINCT ON (dispatch_unit_id)
				dispatch_unit_id, created_at, reported_at
			  FROM attempts
			 WHERE run_id = $1
			 ORDER BY dispatch_unit_id, created_at ASC
		),
		retests AS (
			SELECT a.dispatch_unit_id, a.created_at, a.reported_at
			  FROM attempts a
			  JOIN first_per_unit f ON a.dispatch_unit_id = f.dispatch_unit_id
			 WHERE a.run_id = $1
			   AND a.created_at > f.created_at
		)
		SELECT
			(SELECT MIN(created_at)  FROM first_per_unit)                                AS first_attempt_start,
			(SELECT MAX(reported_at) FROM first_per_unit WHERE reported_at IS NOT NULL)  AS first_pass_end,
			(SELECT MIN(created_at)  FROM retests)                                       AS retest_start,
			(SELECT MAX(reported_at) FROM retests WHERE reported_at IS NOT NULL)         AS retest_end,
			(SELECT COUNT(DISTINCT dispatch_unit_id) FROM retests)                       AS retest_unit_count
	`, runID).Scan(&firstAttemptStart, &firstPassEnd, &retestStart, &retestEnd, &retestUnitCount)
	if err != nil {
		return nil, err
	}
	if firstAttemptStart == nil && firstPassEnd == nil && retestStart == nil {
		return nil, nil
	}
	out := &runDurations{
		RetestUnitCount: retestUnitCount,
		BeginAt:         runStartedAt.UTC(),
		FirstTestAt:     utcOrNil(firstAttemptStart),
		FirstRetestAt:   utcOrNil(retestStart),
		LastTestAt:      utcOrNil(latestNonNil(firstPassEnd, retestEnd)),
	}
	if firstPassEnd != nil {
		ms := firstPassEnd.Sub(runStartedAt).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		out.FirstPassMs = &ms
	}
	if retestStart != nil && retestEnd != nil {
		ms := retestEnd.Sub(*retestStart).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		out.RetestMs = &ms
	}
	return out, nil
}

func utcOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func latestNonNil(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case b.After(*a):
		return b
	default:
		return a
	}
}

// aggregateTestCounts walks every reported test_case across all attempts of
// every dispatch_unit on the run and rolls them up to per-test-case
// statuses, applying the same any-passed-AND-any-failed → flaky rule the
// listing rollup uses (see reports.aggregateOrchestrationTestCounts).
// Returns (nil, nil) when no attempts have yet reported test_cases.
func aggregateTestCounts(ctx context.Context, pool *pgxpool.Pool, runID uuid.UUID) (*testCounts, error) {
	var t testCounts
	err := pool.QueryRow(ctx, `
		WITH per_test AS (
			SELECT
				a.dispatch_unit_id,
				tc->>'full_title' AS full_title,
				BOOL_OR(tc->>'status' IN ('passed', 'flaky')) AS ever_passed,
				BOOL_OR(tc->>'status' IN ('failed', 'timedOut', 'interrupted')) AS ever_failed,
				BOOL_OR(tc->>'status' = 'skipped') AS ever_skipped
			  FROM attempts a
			 CROSS JOIN LATERAL jsonb_array_elements(a.test_cases) AS tc
			 WHERE a.run_id = $1
			   AND a.test_cases IS NOT NULL
			 GROUP BY a.dispatch_unit_id, tc->>'full_title'
		)
		SELECT
			COUNT(*) FILTER (WHERE ever_passed AND NOT ever_failed) AS passed,
			COUNT(*) FILTER (WHERE ever_failed AND NOT ever_passed) AS failed,
			COUNT(*) FILTER (WHERE ever_passed AND ever_failed) AS flaky,
			COUNT(*) FILTER (WHERE ever_skipped AND NOT ever_passed AND NOT ever_failed) AS skipped,
			COUNT(*) AS total
		  FROM per_test
	`, runID).Scan(&t.Passed, &t.Failed, &t.Flaky, &t.Skipped, &t.Total)
	if err != nil {
		return nil, err
	}
	if t.Total == 0 {
		return nil, nil
	}
	return &t, nil
}

// ListRuns serves GET /api/v1/orchestration/runs. Returns every run matching
// the supplied display identity (repository may be the trailing segment alone,
// matching aggregations.go's suffix-match convention; commit_sha may be a
// 7-char short SHA). The dashboard uses this to resolve a single bare URL
// like /reports/<repo>/<branch>/<commit>/<name> to a specific gh_run_id when
// the URL doesn't carry one — avoiding the "page is empty until shards
// upload" gap that the consolidated-report path otherwise introduces.
//
// Returned shape is run summaries only — no per-unit detail. Clients that
// need the unit list call /orchestration/status with a fully-qualified
// identity once they've picked a run.
func (h *Handlers) ListRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repository := strings.TrimSpace(q.Get("repository"))
	commitSHA := strings.TrimSpace(q.Get("commit_sha"))
	name := strings.TrimSpace(q.Get("name"))
	if repository == "" || commitSHA == "" || name == "" {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
			"repository, commit_sha, and name are required")
		return
	}
	branch := strings.TrimSpace(q.Get("branch"))

	runs, err := h.Store.ListRunsByDisplayIdentity(r.Context(), repository, commitSHA, name, branch)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	out := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		out = append(out, runSnapshotPayload(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

// Note for clients: when a single (repo, branch, commit, name) display
// identity matches multiple gh_run_ids, the dashboard should auto-select if
// exactly one match exists, otherwise present a chooser (one entry per
// gh_run_id). The runs[] array surfaces enough metadata (status, started_at,
// counts) for the chooser to be readable without a follow-up call.

// Screenshots serves POST /api/v1/orchestration/screenshots. Streaming
// multipart upload of a single screenshot tied to the worker's current or
// most recent lease for the run.
func (h *Handlers) Screenshots(w http.ResponseWriter, r *http.Request) {
	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}

	maxBytes := h.MaxScreenshotBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	if err := r.ParseMultipartForm(maxBytes); err != nil {
		// http.MaxBytesReader produces an error string containing
		// "http: request body too large" when the cap is exceeded.
		if strings.Contains(err.Error(), "request body too large") {
			api.WriteError(w, r, api.ErrTooLarge)
			return
		}
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
			"invalid multipart body")
		return
	}

	fields := identityFields{
		Repository:   r.FormValue("repository"),
		CommitSHA:    r.FormValue("commit_sha"),
		GHRunID:      r.FormValue("gh_run_id"),
		Name:         r.FormValue("name"),
		GHRunAttempt: r.FormValue("gh_run_attempt"),
		Branch:       r.FormValue("branch"),
		Framework:    r.FormValue("framework"),
	}
	identity, ierr := identityFromFields(fields)
	if ierr != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", ierr.Error())
		return
	}
	worker, werr := workerFromFields(workerFields{
		GHJobName: r.FormValue("gh_job_name"),
		GHJobID:   r.FormValue("gh_job_id"),
	})
	if werr != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", werr.Error())
		return
	}
	specPath := r.FormValue("spec_path")
	relativePath := r.FormValue("relative_path")
	if specPath == "" {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "spec_path is required")
		return
	}
	if relativePath == "" {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "relative_path is required")
		return
	}

	run, err := h.Store.FindRunByIdentity(r.Context(), identity)
	if err != nil {
		if errors.Is(err, orchestration.ErrNotFound) {
			api.WriteError(w, r, api.ErrNotFound)
			return
		}
		api.WriteError(w, r, err)
		return
	}
	if err := orchestration.CheckRunOwner(r.Context(), run, subject); err != nil {
		api.WriteError(w, r, api.ErrForbidden)
		return
	}

	if r.MultipartForm == nil || r.MultipartForm.File == nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "file part is required")
		return
	}
	headers := r.MultipartForm.File["file"]
	if len(headers) == 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "file part is required")
		return
	}
	header := headers[0]
	if header.Size <= 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "file is empty")
		return
	}
	if header.Size > maxBytes {
		api.WriteError(w, r, api.ErrTooLarge)
		return
	}
	file, err := header.Open()
	if err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "cannot read file part")
		return
	}
	defer func() { _ = file.Close() }()

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	key, err := h.Store.StoreScreenshot(r.Context(), identity, worker,
		specPath, relativePath, file, contentType, header.Size, h.ObjectStore)
	if err != nil {
		switch {
		case errors.Is(err, orchestration.ErrUnknownLease):
			api.WriteError(w, r, api.ErrNotFound)
		case errors.Is(err, orchestration.ErrSpecNotInLease):
			api.WriteErrorCode(w, http.StatusBadRequest, "SPEC_NOT_IN_LEASE",
				"spec_path is not part of the worker's lease")
		default:
			api.WriteError(w, r, err)
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"key":        key,
		"size_bytes": header.Size,
	})
}

// ---------- helpers ----------

// identityFromFields validates required identity fields and applies the
// project's defaults: framework defaults to "playwright", gh_run_attempt
// to "1". An unknown framework value is rejected here in addition to the
// OpenAPI-middleware enum check, so direct callers (tests, internal code)
// stay honest.
func identityFromFields(f identityFields) (orchestration.CompositeIdentity, error) {
	framework := f.Framework
	if framework == "" {
		framework = orchestration.DefaultFramework
	}
	if !orchestration.IsSupportedFramework(framework) {
		return orchestration.CompositeIdentity{},
			fmt.Errorf("framework %q is not supported (must be one of: %s)", framework, strings.Join(orchestration.SupportedFrameworksList(), ", "))
	}
	attempt := f.GHRunAttempt
	if attempt == "" {
		attempt = "1"
	}
	identity := orchestration.CompositeIdentity{
		Repository:   strings.TrimSpace(f.Repository),
		CommitSHA:    strings.TrimSpace(f.CommitSHA),
		GHRunID:      strings.TrimSpace(f.GHRunID),
		Name:         strings.TrimSpace(f.Name),
		GHRunAttempt: attempt,
		Branch:       f.Branch,
		GHPRNumber:   f.GHPRNumber,
		Framework:    framework,
	}
	if err := identity.Validate(); err != nil {
		return orchestration.CompositeIdentity{}, err
	}
	return identity, nil
}

// workerFromFields validates the worker identity tuple. Both gh_job_name and
// gh_job_id are required.
func workerFromFields(f workerFields) (orchestration.WorkerIdentity, error) {
	name := strings.TrimSpace(f.GHJobName)
	id := strings.TrimSpace(f.GHJobID)
	if name == "" {
		return orchestration.WorkerIdentity{}, errors.New("gh_job_name is required")
	}
	if id == "" {
		return orchestration.WorkerIdentity{}, errors.New("gh_job_id is required")
	}
	return orchestration.WorkerIdentity{GHJobName: name, GHJobID: id}, nil
}

// ownerFromSubject projects the auth subject onto the orchestration owner
// tuple. Sessions are not valid run owners (the begin endpoint is for CI
// callers) — the resulting OwnerInfo will fail BeginRun's owner check.
func ownerFromSubject(s authapi.Subject) orchestration.OwnerInfo {
	switch s.Kind {
	case "apikey":
		id := s.APIKeyID
		return orchestration.OwnerInfo{APIKeyID: &id}
	case "oidc":
		sub := s.OIDCSubject
		if sub == "" {
			return orchestration.OwnerInfo{}
		}
		return orchestration.OwnerInfo{OIDCSubject: &sub}
	}
	return orchestration.OwnerInfo{}
}

// actorFromSubject extracts an actor string for the report_created event
// payload. Matches the convention used by reports.actorFromContext: prefer
// the OIDC subject, fall back to empty when no usable identity is present.
func actorFromSubject(s authapi.Subject) string {
	if s.OIDCClaims != nil {
		return s.OIDCClaims.Subject
	}
	return s.OIDCSubject
}

// refFromBranch turns "main" → "refs/heads/main" so the report_created
// payload looks like a real GitHub OIDC ref. Mirrors the helper in the
// reports-side stateless flow.
func refFromBranch(branch string) string {
	if branch == "" {
		return ""
	}
	if strings.HasPrefix(branch, "refs/") {
		return branch
	}
	return "refs/heads/" + branch
}

// runSnapshotPayload builds the JSON body returned by /begin and /status.
// Layout matches the OpenAPI RunSnapshot schema.
func runSnapshotPayload(run *orchestration.Run) map[string]any {
	out := map[string]any{
		"repository":       run.Identity.Repository,
		"commit_sha":       run.Identity.CommitSHA,
		"gh_run_id":        run.Identity.GHRunID,
		"name":             run.Identity.Name,
		"gh_run_attempt":   run.Identity.GHRunAttempt,
		"framework":        run.Identity.Framework,
		"status":           run.Status,
		"total_units":      run.Counts.Total,
		"started_at":       run.StartedAt.UTC(),
		"last_activity_at": run.LastActivityAt.UTC(),
		"idle_timeout_ms":  run.IdleTimeoutMs,
		"counts": map[string]any{
			"pending":           run.Counts.Pending,
			"leased":            run.Counts.Leased,
			"completed_pass":    run.Counts.CompletedPass,
			"completed_fail":    run.Counts.CompletedFail,
			"completed_skipped": run.Counts.CompletedSkipped,
			"abandoned":         run.Counts.Abandoned,
			"retest_eligible":   run.Counts.RetestEligible,
		},
		// run_id is exposed for convenience even though workers don't need it.
		"run_id": run.ID.String(),
	}
	if run.Identity.Branch != "" {
		out["branch"] = run.Identity.Branch
	}
	if run.Identity.GHPRNumber != nil {
		out["gh_pr_number"] = *run.Identity.GHPRNumber
	}
	if run.TerminalAt != nil {
		out["terminal_at"] = run.TerminalAt.UTC()
	} else {
		out["terminal_at"] = nil
	}
	return out
}

// checkoutUnitsPayload builds the per-unit JSON array returned by /checkout.
// Per the OpenAPI contract, fail_count is surfaced only when the unit is
// being dispatched as a retest — first-pass dispatches never carry it.
func checkoutUnitsPayload(units []*orchestration.DispatchUnit, isRetest bool) []map[string]any {
	out := make([]map[string]any, 0, len(units))
	for _, u := range units {
		entry := map[string]any{
			"unit_id":      u.ID.String(),
			"dispatch_seq": u.DispatchSeq,
			"spec_path":    u.SpecPath,
		}
		if isRetest {
			entry["fail_count"] = u.FailCount
		}
		out = append(out, entry)
	}
	return out
}

// resolveMaxSpecsPerRun reads TSIO_ORCH_MAX_SPECS_PER_RUN and falls back to
// defaultMaxSpecsPerRun on missing/invalid/non-positive values. Resolved at
// request time so tests can flip the cap without a restart.
func resolveMaxSpecsPerRun() int {
	raw := os.Getenv(maxSpecsPerRunEnvVar)
	if raw == "" {
		return defaultMaxSpecsPerRun
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultMaxSpecsPerRun
	}
	return n
}

// unitStateChangesPayload mirrors the CompleteResponse.unit_states_changed
// schema: one object per dispatched-unit transition.
func unitStateChangesPayload(changes []orchestration.UnitStateChange) []map[string]any {
	out := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		out = append(out, map[string]any{
			"unit_id":   c.UnitID.String(),
			"new_state": c.ToState,
		})
	}
	return out
}

// unitsPayload serializes the run's dispatch units (with current lease and
// attempt history) for the /status and /begin response bodies. The shape
// matches the OpenAPI Unit / Attempt / CurrentLease schemas.
func unitsPayload(units []orchestration.UnitView) []map[string]any {
	out := make([]map[string]any, 0, len(units))
	for _, u := range units {
		entry := map[string]any{
			"id":             u.ID.String(),
			"dispatch_seq":   u.DispatchSeq,
			"spec_path":      u.SpecPath,
			"state":          u.State,
			"lease_count":    u.LeaseCount,
			"fail_count":     u.FailCount,
			"outcome_set_at": nilOrTime(u.OutcomeSetAt),
		}
		if u.CurrentLease != nil {
			entry["current_lease"] = map[string]any{
				"id":          u.CurrentLease.ID.String(),
				"gh_job_name": u.CurrentLease.GHJobName,
				"gh_job_id":   u.CurrentLease.GHJobID,
				"issued_at":   u.CurrentLease.IssuedAt.UTC(),
				"deadline":    u.CurrentLease.Deadline.UTC(),
			}
		} else {
			entry["current_lease"] = nil
		}
		entry["attempts"] = attemptsPayload(u.Attempts)
		out = append(out, entry)
	}
	return out
}

// attemptsPayload serializes the per-spec attempt history of a single unit.
// Status is nullable until the worker reports; null indicates the lease is
// still in flight (or expired without a late report landing yet).
//
// test_cases is the framework-specific per-test-case detail the worker
// supplied on /complete (see SpecResultTestCase in the OpenAPI). When
// present it includes the attachments object referencing screenshot
// storage keys; the dashboard renders those inline. Emitted as raw JSON
// so the dashboard sees the same shape the worker sent.
func attemptsPayload(attempts []orchestration.AttemptView) []map[string]any {
	out := make([]map[string]any, 0, len(attempts))
	for _, a := range attempts {
		entry := map[string]any{
			"id":                 a.ID.String(),
			"lease_id":           a.LeaseID.String(),
			"spec_path":          a.SpecPath,
			"status":             a.Status,
			"actual_duration_ms": a.ActualDurationMs,
			"error_message":      a.ErrorMessage,
			"reported_at":        nilOrTime(a.ReportedAt),
			"late_report":        a.LateReport,
			"expired":            a.Expired,
			"created_at":         a.CreatedAt.UTC(),
			"gh_job_name":        a.GHJobName,
			"gh_job_id":          a.GHJobID,
		}
		if len(a.TestCases) > 0 {
			entry["test_cases"] = json.RawMessage(a.TestCases)
		} else {
			entry["test_cases"] = nil
		}
		out = append(out, entry)
	}
	return out
}

// nilOrTime returns nil for a nil *time.Time and the UTC ISO timestamp
// otherwise. Used to keep payloads consistent — JSON encoders skip-vs-emit
// nil pointers in different ways depending on whether the field is omitted.
func nilOrTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

// writeJSON encodes v as application/json with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
