// Package triage serves /api/v1/triage/* — the verdict ledger written by
// automated E2E failure triage, the flake-amnesty decision derived from it, and
// the accuracy metrics that gate how much authority triage is given.
//
// The ledger exists so that auto-greening a flake is accountable rather than
// silent. Three things depend on it:
//
//   - Amnesty: a test cannot be waived indefinitely. Past waivers are counted,
//     and past a threshold the test loses amnesty and goes hard red.
//   - Accuracy: every human correction of a verdict is a labelled example, and
//     the false-green count (a waived verdict a human later reclassified as a
//     real bug) is the one metric that can disqualify the whole system.
//   - Audit: every AI-granted green is queryable after the fact.
package triage

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// Handlers bundles the triage-ledger handlers.
type Handlers struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

// validVerdicts mirrors the CHECK constraint on triage_verdicts.verdict. Kept as
// a map so a malformed payload is rejected at the edge with a useful message
// rather than as a generic constraint violation from Postgres.
var validVerdicts = map[string]bool{
	"PR_REGRESSION":      true,
	"MAIN_REGRESSION":    true,
	"FLAKY_TEST":         true,
	"FLAKY_INFRA":        true,
	"FLAKY_SERVER":       true,
	"BUILD_OR_ENV_ERROR": true,
	"TEST_DEBT":          true,
	"INCONCLUSIVE":       true,
}

// realBugVerdicts are the corrected values that make a prior *waiver* a
// false green — triage said "ignore this", a human said "this was a real bug".
var realBugVerdicts = map[string]bool{
	"PR_REGRESSION":      true,
	"MAIN_REGRESSION":    true,
	"BUILD_OR_ENV_ERROR": true,
	"TEST_DEBT":          true,
}

var validCheckStates = map[string]bool{
	"success": true, "failure": true, "pending": true, "error": true,
}

// ---------- POST /api/v1/triage/verdicts ----------

type verdictInput struct {
	ExternalTestID   *string           `json:"external_test_id"`
	ClusterSignature *string           `json:"cluster_signature"`
	MemberCount      *int              `json:"member_count"`
	Verdict          string            `json:"verdict"`
	Confidence       float64           `json:"confidence"`
	RootCause        *string           `json:"root_cause"`
	Evidence         []json.RawMessage `json:"evidence"`
	SuspectCommit    *string           `json:"suspect_commit"`
	CheckState       *string           `json:"check_state"`
	Waived           bool              `json:"waived"`
}

type verdictBatch struct {
	Repository string         `json:"repository"`
	Branch     string         `json:"branch"`
	CommitSHA  string         `json:"commit_sha"`
	GHRunID    string         `json:"gh_run_id"`
	GHPRNumber *int           `json:"gh_pr_number"`
	Model      *string        `json:"model"`
	Tier       *int           `json:"tier"`
	Verdicts   []verdictInput `json:"verdicts"`
}

// CreateVerdicts serves POST /api/v1/triage/verdicts — upserts a run's triage
// decisions.
//
// Upsert rather than insert because triage re-runs (a retry of the triage job, a
// re-triage after a rerun produced better evidence) must correct the record for
// that run rather than append a second, contradictory row.
func (h *Handlers) CreateVerdicts(w http.ResponseWriter, r *http.Request) {
	var batch verdictBatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&batch); err != nil {
		api.WriteError(w, r, fmt.Errorf("%w: malformed JSON body", api.ErrBadRequest))
		return
	}
	if batch.Repository == "" || batch.CommitSHA == "" {
		api.WriteError(w, r, fmt.Errorf("%w: repository and commit_sha are required", api.ErrBadRequest))
		return
	}
	if len(batch.Verdicts) == 0 {
		api.WriteError(w, r, fmt.Errorf("%w: verdicts must not be empty", api.ErrBadRequest))
		return
	}
	if len(batch.Verdicts) > 500 {
		api.WriteError(w, r, fmt.Errorf("%w: at most 500 verdicts per request", api.ErrBadRequest))
		return
	}
	for i, v := range batch.Verdicts {
		if !validVerdicts[v.Verdict] {
			api.WriteError(w, r, fmt.Errorf("%w: verdicts[%d].verdict %q is not a known verdict", api.ErrBadRequest, i, v.Verdict))
			return
		}
		if v.Confidence < 0 || v.Confidence > 1 {
			api.WriteError(w, r, fmt.Errorf("%w: verdicts[%d].confidence must be between 0 and 1", api.ErrBadRequest, i))
			return
		}
		if v.CheckState != nil && !validCheckStates[*v.CheckState] {
			api.WriteError(w, r, fmt.Errorf("%w: verdicts[%d].check_state %q is invalid", api.ErrBadRequest, i, *v.CheckState))
			return
		}
		if v.ExternalTestID == nil && v.ClusterSignature == nil {
			api.WriteError(w, r, fmt.Errorf("%w: verdicts[%d] needs external_test_id or cluster_signature", api.ErrBadRequest, i))
			return
		}
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		h.logError("triage verdicts begin", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ids := make([]uuid.UUID, 0, len(batch.Verdicts))
	for _, v := range batch.Verdicts {
		evidence, marshalErr := json.Marshal(orEmptyArray(v.Evidence))
		if marshalErr != nil {
			api.WriteError(w, r, fmt.Errorf("%w: evidence is not serializable", api.ErrBadRequest))
			return
		}
		memberCount := 1
		if v.MemberCount != nil && *v.MemberCount >= 0 {
			memberCount = *v.MemberCount
		}
		checkState := "failure"
		if v.CheckState != nil {
			checkState = *v.CheckState
		}

		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO triage_verdicts (
				repository, branch, commit_sha, gh_run_id, gh_pr_number,
				external_test_id, cluster_signature, member_count,
				verdict, confidence, tier, root_cause, evidence,
				suspect_commit, check_state, waived, model
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			ON CONFLICT (repository, commit_sha, gh_run_id, cluster_signature, external_test_id)
			DO UPDATE SET
				branch        = EXCLUDED.branch,
				gh_pr_number  = EXCLUDED.gh_pr_number,
				member_count  = EXCLUDED.member_count,
				verdict       = EXCLUDED.verdict,
				confidence    = EXCLUDED.confidence,
				tier          = EXCLUDED.tier,
				root_cause    = EXCLUDED.root_cause,
				evidence      = EXCLUDED.evidence,
				suspect_commit = EXCLUDED.suspect_commit,
				check_state   = EXCLUDED.check_state,
				waived        = EXCLUDED.waived,
				model         = EXCLUDED.model
			RETURNING id
		`,
			batch.Repository, batch.Branch, batch.CommitSHA, batch.GHRunID, batch.GHPRNumber,
			v.ExternalTestID, v.ClusterSignature, memberCount,
			v.Verdict, v.Confidence, batch.Tier, v.RootCause, evidence,
			v.SuspectCommit, checkState, v.Waived, batch.Model,
		).Scan(&id); err != nil {
			h.logError("triage verdicts upsert", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		ids = append(ids, id)
	}

	if err := tx.Commit(ctx); err != nil {
		h.logError("triage verdicts commit", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"ids": ids, "count": len(ids)})
}

// ---------- POST /api/v1/triage/verdicts/{id}/correction ----------

type correctionInput struct {
	CorrectedVerdict string `json:"corrected_verdict"`
	CorrectedBy      string `json:"corrected_by"`
	CorrectedReason  string `json:"corrected_reason"`
}

// Correct serves POST /api/v1/triage/verdicts/{id}/correction — records that a
// human disagreed with a verdict.
//
// Corrections are the only ground truth this system gets. They feed the accuracy
// metrics and, when they land on a waived verdict, the false-green count.
func (h *Handlers) Correct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, r, fmt.Errorf("%w: id must be a UUID", api.ErrBadRequest))
		return
	}
	var in correctionInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		api.WriteError(w, r, fmt.Errorf("%w: malformed JSON body", api.ErrBadRequest))
		return
	}
	if !validVerdicts[in.CorrectedVerdict] {
		api.WriteError(w, r, fmt.Errorf("%w: corrected_verdict %q is not a known verdict", api.ErrBadRequest, in.CorrectedVerdict))
		return
	}
	if in.CorrectedBy == "" {
		api.WriteError(w, r, fmt.Errorf("%w: corrected_by is required", api.ErrBadRequest))
		return
	}

	tag, err := h.Pool.Exec(r.Context(), `
		UPDATE triage_verdicts
		SET corrected_verdict = $2,
		    corrected_by      = $3,
		    corrected_reason  = nullif($4, ''),
		    corrected_at      = now()
		WHERE id = $1
	`, id, in.CorrectedVerdict, in.CorrectedBy, in.CorrectedReason)
	if err != nil {
		h.logError("triage correction update", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	if tag.RowsAffected() == 0 {
		api.WriteError(w, r, api.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "corrected_verdict": in.CorrectedVerdict})
}

// ---------- GET /api/v1/triage/amnesty ----------

type amnestyResponse struct {
	TestID          string     `json:"test_id"`
	Repository      string     `json:"repository"`
	Granted         bool       `json:"granted"`
	Reason          string     `json:"reason"`
	WaiversInWindow int        `json:"waivers_in_window"`
	MaxWaivers      int        `json:"max_waivers"`
	WaiverWindow    string     `json:"waiver_window"`
	FirstWaiverAt   *time.Time `json:"first_waiver_at,omitempty"`
	LastWaiverAt    *time.Time `json:"last_waiver_at,omitempty"`
	FailureRate     float64    `json:"failure_rate"`
	MaxFailureRate  float64    `json:"max_failure_rate"`
	RateWindow      string     `json:"rate_window"`
	Runs            int        `json:"runs"`
}

// Amnesty serves GET /api/v1/triage/amnesty — may this test be auto-waived again?
//
// The decision lives server-side, not in each caller, so that "how much flakiness
// is tolerated" has exactly one definition. Two independent limits, either of
// which revokes amnesty:
//
//   - waiver count: a test waived more than max_waivers times in waiver_window is
//     no longer noise, it is unmaintained.
//   - failure rate: a test failing more than max_failure_rate of its runs on the
//     baseline branch is broken regardless of how often triage happened to look.
//
// Thresholds are query params with defaults so they can be tuned from real data
// without a redeploy.
func (h *Handlers) Amnesty(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	testID := q.Get("test_id")
	repo := q.Get("repo")
	if testID == "" || repo == "" {
		api.WriteError(w, r, fmt.Errorf("%w: test_id and repo are required", api.ErrBadRequest))
		return
	}
	waiverWindow := orDefault(q.Get("waiver_window"), "14d")
	rateWindow := orDefault(q.Get("rate_window"), "30d")
	maxWaivers := parseInt(q.Get("max_waivers"), 3)
	maxFailureRate := parseFloat(q.Get("max_failure_rate"), 0.10)
	baselineBranch := orDefault(q.Get("branch"), "main")

	waiverSince, err := parseSince(waiverWindow)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	rateSince, err := parseSince(rateWindow)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	resp := amnestyResponse{
		TestID:         testID,
		Repository:     repo,
		MaxWaivers:     maxWaivers,
		WaiverWindow:   waiverWindow,
		MaxFailureRate: maxFailureRate,
		RateWindow:     rateWindow,
	}

	if err := h.Pool.QueryRow(r.Context(), `
		SELECT count(*), min(created_at), max(created_at)
		FROM triage_verdicts
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND external_test_id = $2
		  AND waived
		  AND created_at >= $3::timestamptz
	`, repo, testID, waiverSince).Scan(&resp.WaiversInWindow, &resp.FirstWaiverAt, &resp.LastWaiverAt); err != nil {
		h.logError("triage amnesty waiver count", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	// Failure rate on the baseline branch. Same group rollup the history endpoint
	// uses: a test that both passed and failed inside one group is flaky, and
	// flaky counts toward the failure rate because it did not cleanly pass.
	if err := h.Pool.QueryRow(r.Context(), `
		WITH matched AS (
			SELECT g.id, tc.status
			FROM report_groups g
			JOIN reports r ON r.report_group_id = g.id
			JOIN suites s ON s.report_id = r.id
			JOIN test_cases tc ON tc.suite_id = s.id
			WHERE tc.external_test_id = $2
			  AND (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
			  AND g.branch = $3
			  AND g.created_at >= $4::timestamptz
		),
		rolled AS (
			SELECT id,
			       bool_or(status IN ('passed', 'flaky'))                   AS ever_passed,
			       bool_or(status IN ('failed', 'timedOut', 'interrupted')) AS ever_failed
			FROM matched
			GROUP BY id
		)
		SELECT count(*)::int,
		       coalesce(
		           count(*) FILTER (WHERE ever_failed)::float
		               / nullif(count(*), 0)::float,
		           0)
		FROM rolled
	`, repo, testID, baselineBranch, rateSince).Scan(&resp.Runs, &resp.FailureRate); err != nil {
		h.logError("triage amnesty failure rate", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	switch {
	case resp.WaiversInWindow >= maxWaivers:
		resp.Granted = false
		resp.Reason = fmt.Sprintf("waived %d times in %s (limit %d) — fix or quarantine explicitly",
			resp.WaiversInWindow, waiverWindow, maxWaivers)
	case resp.Runs > 0 && resp.FailureRate > maxFailureRate:
		resp.Granted = false
		resp.Reason = fmt.Sprintf("fails %.0f%% of %s runs on %s over %s (limit %.0f%%)",
			resp.FailureRate*100, baselineBranch, baselineBranch, rateWindow, maxFailureRate*100)
	default:
		resp.Granted = true
		resp.Reason = "within flake tolerance"
	}

	writeJSON(w, http.StatusOK, resp)
}

// ---------- GET /api/v1/triage/verdicts ----------

type verdictRow struct {
	ID               uuid.UUID  `json:"id"`
	Repository       string     `json:"repository"`
	Branch           string     `json:"branch"`
	CommitSHA        string     `json:"commit_sha"`
	GHRunID          string     `json:"gh_run_id"`
	GHPRNumber       *int       `json:"gh_pr_number,omitempty"`
	ExternalTestID   *string    `json:"external_test_id,omitempty"`
	ClusterSignature *string    `json:"cluster_signature,omitempty"`
	MemberCount      int        `json:"member_count"`
	Verdict          string     `json:"verdict"`
	Confidence       float64    `json:"confidence"`
	Tier             *int       `json:"tier,omitempty"`
	RootCause        *string    `json:"root_cause,omitempty"`
	SuspectCommit    *string    `json:"suspect_commit,omitempty"`
	CheckState       string     `json:"check_state"`
	Waived           bool       `json:"waived"`
	Model            *string    `json:"model,omitempty"`
	CorrectedVerdict *string    `json:"corrected_verdict,omitempty"`
	CorrectedBy      *string    `json:"corrected_by,omitempty"`
	CorrectedAt      *time.Time `json:"corrected_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// ListVerdicts serves GET /api/v1/triage/verdicts — the audit trail, filterable by
// commit, PR, test, or verdict class.
func (h *Handlers) ListVerdicts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		api.WriteError(w, r, fmt.Errorf("%w: repo is required", api.ErrBadRequest))
		return
	}
	prNumber := -1
	if v := q.Get("pr"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			api.WriteError(w, r, fmt.Errorf("%w: pr must be an integer", api.ErrBadRequest))
			return
		}
		prNumber = n
	}
	limit := parseInt(q.Get("limit"), 100)
	if limit > 500 {
		limit = 500
	}

	rows, err := h.Pool.Query(r.Context(), `
		SELECT id, repository, branch, commit_sha, gh_run_id, gh_pr_number,
		       external_test_id, cluster_signature, member_count, verdict, confidence,
		       tier, root_cause, suspect_commit, check_state, waived, model,
		       corrected_verdict, corrected_by, corrected_at, created_at
		FROM triage_verdicts
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND ($2 = '' OR commit_sha = $2)
		  AND ($3 = -1 OR gh_pr_number = $3)
		  AND ($4 = '' OR external_test_id = $4)
		  AND ($5 = '' OR verdict = $5)
		ORDER BY created_at DESC
		LIMIT $6
	`, repo, q.Get("commit"), prNumber, q.Get("test_id"), q.Get("verdict"), limit)
	if err != nil {
		h.logError("triage list verdicts", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	out := make([]verdictRow, 0, limit)
	for rows.Next() {
		var v verdictRow
		if err := rows.Scan(&v.ID, &v.Repository, &v.Branch, &v.CommitSHA, &v.GHRunID,
			&v.GHPRNumber, &v.ExternalTestID, &v.ClusterSignature, &v.MemberCount,
			&v.Verdict, &v.Confidence, &v.Tier, &v.RootCause, &v.SuspectCommit,
			&v.CheckState, &v.Waived, &v.Model, &v.CorrectedVerdict, &v.CorrectedBy,
			&v.CorrectedAt, &v.CreatedAt); err != nil {
			h.logError("triage list verdicts scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		h.logError("triage list verdicts rows", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"verdicts": out})
}

// ---------- GET /api/v1/triage/accuracy ----------

// Accuracy serves GET /api/v1/triage/accuracy — the rollout gate.
//
// FalseGreens is the number the whole design hangs on: verdicts that waived a red
// result and were later corrected to a real-bug class. It must stay at zero
// before triage is given any gating authority; a non-zero value means the system
// shipped a bug.
func (h *Handlers) Accuracy(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		api.WriteError(w, r, fmt.Errorf("%w: repo is required", api.ErrBadRequest))
		return
	}
	since, err := parseSince(orDefault(q.Get("window"), "30d"))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	type bucket struct {
		Verdict   string `json:"verdict"`
		Count     int    `json:"count"`
		Waived    int    `json:"waived"`
		Corrected int    `json:"corrected"`
	}

	rows, err := h.Pool.Query(r.Context(), `
		SELECT verdict,
		       count(*)::int,
		       count(*) FILTER (WHERE waived)::int,
		       count(*) FILTER (WHERE corrected_verdict IS NOT NULL)::int
		FROM triage_verdicts
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND created_at >= $2::timestamptz
		GROUP BY verdict
		ORDER BY count(*) DESC
	`, repo, since)
	if err != nil {
		h.logError("triage accuracy buckets", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	buckets := make([]bucket, 0, len(validVerdicts))
	var total, waived, corrected int
	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.Verdict, &b.Count, &b.Waived, &b.Corrected); err != nil {
			h.logError("triage accuracy scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		total += b.Count
		waived += b.Waived
		corrected += b.Corrected
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		h.logError("triage accuracy rows", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	realBugList := make([]string, 0, len(realBugVerdicts))
	for v := range realBugVerdicts {
		realBugList = append(realBugList, v)
	}
	var falseGreens int
	if err := h.Pool.QueryRow(r.Context(), `
		SELECT count(*)::int
		FROM triage_verdicts
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND created_at >= $2::timestamptz
		  AND waived
		  AND corrected_verdict = ANY($3)
	`, repo, since, realBugList).Scan(&falseGreens); err != nil {
		h.logError("triage accuracy false greens", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	precision := 1.0
	if total > 0 {
		precision = float64(total-corrected) / float64(total)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repo":            repo,
		"window":          orDefault(q.Get("window"), "30d"),
		"total_verdicts":  total,
		"waived":          waived,
		"corrected":       corrected,
		"false_greens":    falseGreens,
		"precision":       precision,
		"by_verdict":      buckets,
		"gate_satisfied":  falseGreens == 0 && total > 0,
		"gate_definition": "false_greens == 0 over the window",
	})
}

// ---------- helpers ----------

func (h *Handlers) logError(msg string, err error) {
	if h.Logger != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.Logger.Error(msg, slog.String("error", err.Error()))
	}
}

func orEmptyArray(v []json.RawMessage) []json.RawMessage {
	if v == nil {
		return []json.RawMessage{}
	}
	return v
}

func orDefault(v, dflt string) string {
	if v == "" {
		return dflt
	}
	return v
}

func parseInt(v string, dflt int) int {
	if v == "" {
		return dflt
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return dflt
	}
	return n
}

func parseFloat(v string, dflt float64) float64 {
	if v == "" {
		return dflt
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 1 {
		return dflt
	}
	return f
}

// parseSince mirrors the window parsing in the testhistory package: "30d", "24h",
// "90m" → an absolute lower bound, capped at 180 days.
func parseSince(window string) (*time.Time, error) {
	if window == "" {
		return nil, nil
	}
	if len(window) < 2 {
		return nil, fmt.Errorf("%w: window must look like 30d, 24h or 90m", api.ErrBadRequest)
	}
	n, err := strconv.Atoi(window[:len(window)-1])
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("%w: window must look like 30d, 24h or 90m", api.ErrBadRequest)
	}
	var d time.Duration
	switch window[len(window)-1] {
	case 'd':
		d = time.Duration(n) * 24 * time.Hour
	case 'h':
		d = time.Duration(n) * time.Hour
	case 'm':
		d = time.Duration(n) * time.Minute
	default:
		return nil, fmt.Errorf("%w: window unit must be d, h or m", api.ErrBadRequest)
	}
	if d > 180*24*time.Hour {
		d = 180 * 24 * time.Hour
	}
	t := time.Now().Add(-d)
	return &t, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
