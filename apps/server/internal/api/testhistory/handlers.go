// Package testhistory serves /api/v1/tests/* — per-test outcome history across
// commits, flakiness aggregates, and the "is this test failing on other PRs right
// now" lookup.
//
// These exist for automated failure triage. The rest of the API answers "what
// happened in this run"; triage needs "what usually happens to this test", which
// is a different query shape: keyed on a stable external test ID, ordered across
// report groups, and rolled up per group rather than per shard.
//
// Every endpoint here rolls a test's per-shard rows up to one outcome per report
// group using the same rule the dashboard applies client-side: a test that both
// passed and failed within a group is flaky (a retry survived), not failed.
package testhistory

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// Handlers bundles the per-test history handlers.
type Handlers struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

// Outcome values returned by the group-level rollup.
const (
	outcomePassed  = "passed"
	outcomeFailed  = "failed"
	outcomeFlaky   = "flaky"
	outcomeSkipped = "skipped"
)

// groupRollupSQL is the shared CTE: every report group that ran the requested
// test, with the test's per-shard rows collapsed to a single outcome.
//
// Args, in order: $1 external_test_id, $2 repository, $3 branch, $4 framework,
// $5 run_group, $6 since. The three filter args treat an empty string as "any";
// since treats NULL as unbounded.
const groupRollupSQL = `
	WITH matched AS (
		SELECT g.id, g.commit_sha, g.gh_run_id, g.gh_pr_number, g.branch,
		       g.name, g.run_group, g.created_at, tc.status, tc.duration_ms
		FROM report_groups g
		JOIN reports r ON r.report_group_id = g.id
		JOIN suites s ON s.report_id = r.id
		JOIN test_cases tc ON tc.suite_id = s.id
		WHERE tc.external_test_id = $1
		  AND (g.repository = $2 OR split_part(g.repository, '/', 2) = $2)
		  AND ($3 = '' OR g.branch = $3)
		  AND ($4 = '' OR g.framework = $4)
		  AND ($5 = '' OR g.run_group = $5)
		  AND ($6::timestamptz IS NULL OR g.created_at >= $6::timestamptz)
	),
	rolled AS (
		SELECT id, commit_sha, gh_run_id, gh_pr_number, branch, name, run_group, created_at,
		       -- count() returns bigint and sum(bigint) returns numeric. Both
		       -- casts are cosmetic, not load-bearing: pgx v5 scans either into
		       -- the Go int/int64 these land in without them (verified against
		       -- PG 18.4), which is why the flakiness aggregate below leaves its
		       -- own count()s uncast. Kept only so the column types read as what
		       -- the Go struct declares.
		       count(*)::int                                                   AS shard_rows,
		       sum(coalesce(duration_ms, 0))::bigint                           AS duration_ms,
		       bool_or(status IN ('passed', 'flaky'))                          AS ever_passed,
		       bool_or(status IN ('failed', 'timedOut', 'interrupted'))        AS ever_failed
		FROM matched
		GROUP BY id, commit_sha, gh_run_id, gh_pr_number, branch, name, run_group, created_at
	),
	outcomes AS (
		SELECT *,
		       CASE
		           WHEN ever_passed AND ever_failed THEN 'flaky'
		           WHEN ever_failed                 THEN 'failed'
		           WHEN ever_passed                 THEN 'passed'
		           ELSE 'skipped'
		       END AS outcome
		FROM rolled
	)
`

type historyEntry struct {
	Commit     string    `json:"commit"`
	GHRunID    string    `json:"gh_run_id"`
	GHPRNumber *int      `json:"gh_pr_number,omitempty"`
	Branch     string    `json:"branch"`
	Name       string    `json:"name"`
	RunGroup   *string   `json:"run_group,omitempty"`
	Outcome    string    `json:"outcome"`
	ShardRows  int       `json:"shard_rows"`
	DurationMs int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// History serves GET /api/v1/tests/history — the outcome series for one test,
// newest first. This is the query that lets triage answer "was this already
// failing before the PR?" without spending runner minutes on a baseline rerun.
func (h *Handlers) History(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	testID := q.Get("test_id")
	repo := q.Get("repo")
	if testID == "" || repo == "" {
		api.WriteError(w, r, fmt.Errorf("%w: test_id and repo are required", api.ErrBadRequest))
		return
	}
	limit := parseLimit(q.Get("limit"), 20, 200)
	since, err := parseSince(q.Get("window"))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	rows, err := h.Pool.Query(r.Context(), groupRollupSQL+`
		SELECT commit_sha, gh_run_id, gh_pr_number, branch, name, run_group,
		       outcome, shard_rows, duration_ms, created_at
		FROM outcomes
		ORDER BY created_at DESC
		LIMIT $7
	`, testID, repo, q.Get("branch"), q.Get("framework"), q.Get("run_group"), since, limit)
	if err != nil {
		h.logError("tests history query", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	entries := make([]historyEntry, 0, limit)
	for rows.Next() {
		var e historyEntry
		if err := rows.Scan(&e.Commit, &e.GHRunID, &e.GHPRNumber, &e.Branch, &e.Name,
			&e.RunGroup, &e.Outcome, &e.ShardRows, &e.DurationMs, &e.CreatedAt); err != nil {
			h.logError("tests history scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		h.logError("tests history rows", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"test_id": testID,
		"repo":    repo,
		"entries": entries,
		"summary": summarize(entries),
	})
}

// historySummary is the part of the series triage actually branches on, computed
// server-side so every caller derives it the same way.
type historySummary struct {
	Runs               int      `json:"runs"`
	Passed             int      `json:"passed"`
	Failed             int      `json:"failed"`
	Flaky              int      `json:"flaky"`
	Skipped            int      `json:"skipped"`
	Flips              int      `json:"flips"`
	FailureRate        float64  `json:"failure_rate"`
	FlakeRate          float64  `json:"flake_rate"`
	LastPassCommit     *string  `json:"last_pass_commit,omitempty"`
	FailingSinceCommit *string  `json:"failing_since_commit,omitempty"`
	Series             []string `json:"series"`
}

// summarize walks the newest-first series once.
//
// FailureRate counts flaky runs as failures because for gating purposes a run
// that needed a retry did not cleanly pass; FlakeRate isolates just those. Flips
// counts adjacent outcome changes over pass/fail only — skipped runs are carried
// over rather than counted as a transition, since a skip says nothing about
// stability.
func summarize(entries []historyEntry) historySummary {
	s := historySummary{Series: make([]string, 0, len(entries))}
	var prevStable string
	// Walk oldest→newest for flips so the series reads chronologically.
	for i := len(entries) - 1; i >= 0; i-- {
		o := entries[i].Outcome
		switch o {
		case outcomePassed:
			s.Passed++
		case outcomeFailed:
			s.Failed++
		case outcomeFlaky:
			s.Flaky++
		case outcomeSkipped:
			s.Skipped++
		}
		if o != outcomeSkipped {
			stable := o
			if o == outcomeFlaky {
				stable = outcomeFailed
			}
			if prevStable != "" && prevStable != stable {
				s.Flips++
			}
			prevStable = stable
		}
	}
	// Runs excludes skipped groups, matching /tests/flakiness (whose aggregate
	// filters them out) and the amnesty failure rate. The two endpoints used
	// different denominators for the same word, so a test skipped half the time
	// reported two different failure_rates depending on which one you asked —
	// and the diluted one was the one gating waivers.
	s.Runs = len(entries) - s.Skipped
	if s.Runs > 0 {
		s.FailureRate = float64(s.Failed+s.Flaky) / float64(s.Runs)
		s.FlakeRate = float64(s.Flaky) / float64(s.Runs)
	}

	// Newest-first walk for the streak boundaries.
	for _, e := range entries {
		if e.Outcome == outcomePassed || e.Outcome == outcomeFlaky {
			c := e.Commit
			s.LastPassCommit = &c
			break
		}
	}
	// FailingSinceCommit is the oldest commit in the *current* unbroken failing
	// streak — i.e. the first run after the last pass. Nil when the newest run
	// passed (there is no active streak).
	for _, e := range entries {
		if e.Outcome == outcomePassed || e.Outcome == outcomeFlaky {
			break
		}
		if e.Outcome != outcomeFailed {
			continue
		}
		// Keep advancing; the last failing entry before a pass (or the end of the
		// series) is the oldest one in the streak.
		c := e.Commit
		s.FailingSinceCommit = &c
	}
	for _, e := range entries {
		s.Series = append(s.Series, e.Outcome)
	}
	return s
}

type flakinessRow struct {
	TestID             string   `json:"test_id"`
	Runs               int      `json:"runs"`
	Passed             int      `json:"passed"`
	Failed             int      `json:"failed"`
	Flaky              int      `json:"flaky"`
	Flips              int      `json:"flips"`
	FailureRate        float64  `json:"failure_rate"`
	FlakeRate          float64  `json:"flake_rate"`
	LastPassCommit     *string  `json:"last_pass_commit,omitempty"`
	FailingSinceCommit *string  `json:"failing_since_commit,omitempty"`
	Titles             []string `json:"titles"`
}

// Flakiness serves GET /api/v1/tests/flakiness — per-test stability aggregates
// over a window, most-unstable first. Drives the flake-amnesty thresholds and the
// weekly flake rollup.
//
// Unlike History this is not scoped to a single test, so the rollup is done in
// SQL: returning every run row for every test in the window would be unbounded.
func (h *Handlers) Flakiness(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		api.WriteError(w, r, fmt.Errorf("%w: repo is required", api.ErrBadRequest))
		return
	}
	since, err := parseSince(defaultWindow(q.Get("window"), "30d"))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	limit := parseLimit(q.Get("limit"), 50, 200)
	minRuns := parseLimit(q.Get("min_runs"), 5, 1000)

	rows, err := h.Pool.Query(r.Context(), `
		WITH matched AS (
			SELECT g.id, g.commit_sha, g.created_at, tc.external_test_id,
			       tc.status, tc.full_title
			FROM report_groups g
			JOIN reports r ON r.report_group_id = g.id
			JOIN suites s ON s.report_id = r.id
			JOIN test_cases tc ON tc.suite_id = s.id
			WHERE tc.external_test_id IS NOT NULL
			  AND (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
			  AND ($2 = '' OR g.branch = $2)
			  AND ($3 = '' OR g.framework = $3)
			  AND ($4 = '' OR g.run_group = $4)
			  AND g.created_at >= $5::timestamptz
		),
		rolled AS (
			SELECT external_test_id, id AS group_id, commit_sha, created_at,
			       (array_agg(full_title ORDER BY full_title))[1]           AS title,
			       bool_or(status IN ('passed', 'flaky'))                   AS ever_passed,
			       bool_or(status IN ('failed', 'timedOut', 'interrupted')) AS ever_failed
			FROM matched
			GROUP BY external_test_id, id, commit_sha, created_at
		),
		outcomes AS (
			SELECT *,
			       CASE
			           WHEN ever_passed AND ever_failed THEN 'flaky'
			           WHEN ever_failed                 THEN 'failed'
			           WHEN ever_passed                 THEN 'passed'
			           ELSE 'skipped'
			       END AS outcome
			FROM rolled
		),
		flagged AS (
			SELECT *,
			       -- Collapse to a two-state series for flip counting: a run that
			       -- needed a retry is not a clean pass.
			       CASE WHEN outcome = 'passed' THEN 'pass' ELSE 'fail' END AS stable,
			       lag(CASE WHEN outcome = 'passed' THEN 'pass' ELSE 'fail' END)
			           OVER (PARTITION BY external_test_id ORDER BY created_at) AS prev_stable
			FROM outcomes
			WHERE outcome <> 'skipped'
		),
		agg AS (
			SELECT external_test_id,
			       count(*)                                                AS runs,
			       count(*) FILTER (WHERE outcome = 'passed')              AS passed,
			       count(*) FILTER (WHERE outcome = 'failed')              AS failed,
			       count(*) FILTER (WHERE outcome = 'flaky')               AS flaky,
			       count(*) FILTER (WHERE prev_stable IS NOT NULL
			                          AND prev_stable <> stable)           AS flips,
			       max(created_at) FILTER (WHERE outcome IN ('passed', 'flaky'))
			                                                               AS last_pass_at,
			       (array_agg(commit_sha ORDER BY created_at DESC)
			            FILTER (WHERE outcome IN ('passed', 'flaky')))[1]  AS last_pass_commit,
			       (array_agg(DISTINCT title))[1:3]                        AS titles
			FROM flagged
			GROUP BY external_test_id
			HAVING count(*) >= $6
		)
		SELECT a.external_test_id, a.runs, a.passed, a.failed, a.flaky, a.flips,
		       a.last_pass_commit, a.titles,
		       (SELECT f.commit_sha
		          FROM flagged f
		         WHERE f.external_test_id = a.external_test_id
		           AND f.outcome <> 'passed'
		           AND (a.last_pass_at IS NULL OR f.created_at > a.last_pass_at)
		         ORDER BY f.created_at ASC
		         LIMIT 1) AS failing_since_commit
		FROM agg a
		WHERE a.failed + a.flaky > 0
		ORDER BY (a.failed + a.flaky)::float / a.runs DESC, a.flips DESC, a.runs DESC
		LIMIT $7
	`, repo, q.Get("branch"), q.Get("framework"), q.Get("run_group"), since, minRuns, limit)
	if err != nil {
		h.logError("tests flakiness query", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	out := make([]flakinessRow, 0, limit)
	for rows.Next() {
		var fr flakinessRow
		if err := rows.Scan(&fr.TestID, &fr.Runs, &fr.Passed, &fr.Failed, &fr.Flaky,
			&fr.Flips, &fr.LastPassCommit, &fr.Titles, &fr.FailingSinceCommit); err != nil {
			h.logError("tests flakiness scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		if fr.Runs > 0 {
			fr.FailureRate = float64(fr.Failed+fr.Flaky) / float64(fr.Runs)
			fr.FlakeRate = float64(fr.Flaky) / float64(fr.Runs)
		}
		out = append(out, fr)
	}
	if err := rows.Err(); err != nil {
		h.logError("tests flakiness rows", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repo":  repo,
		"tests": out,
	})
}

type elsewhereRow struct {
	Branch     string    `json:"branch"`
	GHPRNumber *int      `json:"gh_pr_number,omitempty"`
	Commit     string    `json:"commit"`
	Outcome    string    `json:"outcome"`
	CreatedAt  time.Time `json:"created_at"`
}

// FailingElsewhere serves GET /api/v1/tests/failing-elsewhere — other branches and
// PRs where the same test failed inside the window.
//
// This is the cheapest high-value "not your fault" signal there is: if a test is
// failing on five unrelated PRs right now, it is not the sixth PR's change. No
// rerun, no model call, one indexed query.
func (h *Handlers) FailingElsewhere(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	testID := q.Get("test_id")
	repo := q.Get("repo")
	if testID == "" || repo == "" {
		api.WriteError(w, r, fmt.Errorf("%w: test_id and repo are required", api.ErrBadRequest))
		return
	}
	since, err := parseSince(defaultWindow(q.Get("window"), "24h"))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	// Exclude the caller's own PR so the answer is strictly "elsewhere".
	excludePR := -1
	if v := q.Get("exclude_pr"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			api.WriteError(w, r, fmt.Errorf("%w: exclude_pr must be an integer", api.ErrBadRequest))
			return
		}
		excludePR = n
	}

	rows, err := h.Pool.Query(r.Context(), groupRollupSQL+`
		SELECT branch, gh_pr_number, commit_sha, outcome, created_at
		FROM outcomes
		WHERE outcome IN ('failed', 'flaky')
		  AND (gh_pr_number IS NULL OR gh_pr_number <> $7)
		ORDER BY created_at DESC
		LIMIT 50
	`, testID, repo, q.Get("branch"), q.Get("framework"), q.Get("run_group"), since, excludePR)
	if err != nil {
		h.logError("tests failing-elsewhere query", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	out := make([]elsewhereRow, 0, 16)
	for rows.Next() {
		var er elsewhereRow
		if err := rows.Scan(&er.Branch, &er.GHPRNumber, &er.Commit, &er.Outcome, &er.CreatedAt); err != nil {
			h.logError("tests failing-elsewhere scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		out = append(out, er)
	}
	if err := rows.Err(); err != nil {
		h.logError("tests failing-elsewhere rows", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	// Counted over every matching occurrence, not over the 50 returned above.
	// Derived from the capped page, "this test is failing on 50 other PRs" was
	// the most it could ever say, however widespread the failure — and these two
	// numbers are exactly the signal that separates a main regression from
	// something the PR under test broke. Undercounting them biases triage toward
	// blaming the PR author.
	var distinctPRs, distinctBranches int
	if err := h.Pool.QueryRow(r.Context(), groupRollupSQL+`
		SELECT count(DISTINCT gh_pr_number)::int,
		       count(DISTINCT nullif(branch, ''))::int
		FROM outcomes
		WHERE outcome IN ('failed', 'flaky')
		  AND (gh_pr_number IS NULL OR gh_pr_number <> $7)
	`, testID, repo, q.Get("branch"), q.Get("framework"), q.Get("run_group"), since, excludePR).
		Scan(&distinctPRs, &distinctBranches); err != nil {
		h.logError("tests failing-elsewhere distinct counts", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"test_id":         testID,
		"repo":            repo,
		"distinct_prs":    distinctPRs,
		"distinct_branch": distinctBranches,
		"occurrences":     out,
	})
}

// ---------- helpers ----------

func (h *Handlers) logError(msg string, err error) {
	if h.Logger != nil {
		h.Logger.Error(msg, slog.String("error", err.Error()))
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func parseLimit(v string, dflt, maxN int) int {
	if v == "" {
		return dflt
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return dflt
	}
	if n > maxN {
		return maxN
	}
	return n
}

func defaultWindow(v, dflt string) string {
	if v == "" {
		return dflt
	}
	return v
}

// parseSince turns a window like "30d", "24h" or "90m" into an absolute lower
// bound. Empty means unbounded (nil), which the queries treat as "no time filter".
// maxWindow bounds every history/ledger lookback. Long windows are expensive and
// nobody makes a merge decision on six-month-old flake data.
const maxWindow = 180 * 24 * time.Hour

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
	// Cap the lookback so a typo cannot turn into a full-table scan.
	// Reject rather than silently clamp. The caller echoes the window it asked
	// for back into the response (waiver_window, rate_window, the accuracy
	// "window" field), so a silent clamp made the response claim a 365d window
	// over 180d of data — a wrong answer that reads as a correct one.
	if d > maxWindow {
		return nil, fmt.Errorf("%w: window may not exceed 180d", api.ErrBadRequest)
	}
	t := time.Now().Add(-d)
	return &t, nil
}
