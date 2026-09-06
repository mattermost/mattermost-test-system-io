// Package testhistory serves /api/v1/tests/* — what Test System IO stores about
// a test across runs, branches and pull requests, read back the way an
// automated triage consumer needs it.
//
// Two reads, no opinions:
//
//   - /tests/history: where and when one test passed or failed. One entry per
//     report group that ran the test, newest first, plus counts derived from
//     that series so every caller derives them the same way.
//   - /tests/evidence: what one run's failures looked like — error, stack and
//     screenshots — grouped by normalized error so identical causes read as one.
//
// Whether a failure is flaky, a regression, or caused by the pull request is
// decided by the consumer, which can build and run the product and prove its
// answer. This package only stores and serves the record.
//
// Every history query rolls a test's per-shard rows up to one outcome per report
// group using the same rule the dashboard applies client-side: a test that both
// passed and failed within a group is flaky (a retry survived), not failed.
package testhistory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// Handlers bundles the per-test history and evidence handlers.
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
// Args, in order: $1 stable_key, $2 repository, $3 branch, $4 framework,
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
		WHERE tc.stable_key = $1
		  AND (g.repository = $2 OR split_part(g.repository, '/', 2) = $2)
		  AND ($3 = '' OR g.branch = $3)
		  AND ($4 = '' OR g.framework = $4)
		  AND ($5 = '' OR g.run_group = $5)
		  AND ($6::timestamptz IS NULL OR g.created_at >= $6::timestamptz)
	),
	rolled AS (
		SELECT id, commit_sha, gh_run_id, gh_pr_number, branch, name, run_group, created_at,
		       count(*)::int                                                   AS shard_rows,
		       sum(coalesce(duration_ms, 0))::bigint                           AS duration_ms,
		       bool_or(status IN ('passed', 'flaky'))                          AS ever_passed,
		       bool_or(status IN ('failed', 'timedOut', 'interrupted'))        AS ever_failed,
		       -- Playwright stores every attempt of a retried test as 'flaky',
		       -- the failed attempt included, so a group can be flaky with no
		       -- 'failed' row at all. Without this a retry-survivor rolled up as
		       -- a clean pass and history never saw the flake evidence did.
		       bool_or(status = 'flaky')                                       AS ever_flaky
		FROM matched
		GROUP BY id, commit_sha, gh_run_id, gh_pr_number, branch, name, run_group, created_at
	),
	outcomes AS (
		SELECT *,
		       CASE
		           WHEN ever_flaky                  THEN 'flaky'
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
// newest first, across every branch and pull request unless `branch` narrows it.
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

	entries, err := loadEntries(r.Context(), h.Pool, testID, repo,
		q.Get("branch"), q.Get("framework"), q.Get("run_group"), since, limit)
	if err != nil {
		h.logError("tests history", err)
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

// HistorySummary is the series reduced to counts, computed server-side so every
// caller derives them the same way.
type HistorySummary struct {
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
// FailureRate counts flaky runs as failures because a run that needed a retry
// did not cleanly pass; FlakeRate isolates just those. Flips counts adjacent
// outcome changes over pass/fail only — skipped runs are carried over rather
// than counted as a transition, since a skip says nothing about stability.
func summarize(entries []historyEntry) HistorySummary {
	s := HistorySummary{Series: make([]string, 0, len(entries))}
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
	// Runs excludes skipped groups: a skipped run says nothing about stability
	// and must not dilute the rate.
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
		c := e.Commit
		s.FailingSinceCommit = &c
	}
	for _, e := range entries {
		s.Series = append(s.Series, e.Outcome)
	}
	return s
}

func loadEntries(ctx context.Context, pool *pgxpool.Pool, testID, repo, branch, framework, runGroup string, since *time.Time, limit int) ([]historyEntry, error) {
	rows, err := pool.Query(ctx, groupRollupSQL+`
		SELECT commit_sha, gh_run_id, gh_pr_number, branch, name, run_group,
		       outcome, shard_rows, duration_ms, created_at
		FROM outcomes
		ORDER BY created_at DESC
		LIMIT $7
	`, testID, repo, branch, framework, runGroup, since, limit)
	if err != nil {
		return nil, fmt.Errorf("history query: %w", err)
	}
	defer rows.Close()

	entries := make([]historyEntry, 0, limit)
	for rows.Next() {
		var e historyEntry
		if err := rows.Scan(&e.Commit, &e.GHRunID, &e.GHPRNumber, &e.Branch, &e.Name,
			&e.RunGroup, &e.Outcome, &e.ShardRows, &e.DurationMs, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("history scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history rows: %w", err)
	}
	return entries, nil
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

// maxWindow bounds every history lookback. Long windows are expensive and
// nobody decides anything on six-month-old flake data.
const maxWindow = 180 * 24 * time.Hour

// parseSince turns a window like "30d", "24h" or "90m" into an absolute lower
// bound. Empty means unbounded (nil), which the queries treat as "no time filter".
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
	// Reject rather than silently clamp: a clamped window would make the
	// response claim more data than it covers.
	if d > maxWindow {
		return nil, fmt.Errorf("%w: window may not exceed 180d", api.ErrBadRequest)
	}
	t := time.Now().Add(-d)
	return &t, nil
}
