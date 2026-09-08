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
	"strings"
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

// attemptFailureStatuses is the SQL list of per-attempt statuses that count as
// a failed attempt, kept in one place so the rollup and the fallback agree.
// It mirrors isAttemptFailure in internal/ingest.
const attemptFailureStatuses = `('failed', 'timedOut', 'interrupted')`

// groupRollupSQL is the shared CTE: every report group that ran the requested
// test, with the test's rows collapsed to a single outcome per group.
//
// The rollup is two levels, and the levels mean different things:
//
//	per_shard  one report (one shard) within the group. A test's rows here are
//	           its ATTEMPTS in that shard's run, and every attempt row repeats
//	           the same run-level rollup, so max() reads that run's value
//	           rather than summing a repeated one.
//	rolled     the whole group. Distinct shards are distinct runs of the test,
//	           so their attempt counts add.
//
// Rates downstream are computed over the groups this produces — over runs,
// never over attempts. A retried attempt shares the leaked state or the slow
// container that failed the first one, so a run's attempts are not independent
// draws and counting them as such overstates both the sample size and the
// failure rate.
//
// Args, in order: $1 stable_key, $2 repository, $3 branch, $4 framework,
// $5 run_group, $6 since, $7 baseline, $8 name, $9 before. String filters
// treat an empty string as "any"; nullable timestamps omit that bound.
const groupRollupSQL = `
	WITH matched AS (
		SELECT g.id, g.commit_sha, g.gh_run_id, g.gh_pr_number, g.branch,
		       g.name, g.run_group, g.created_at, r.id AS report_id,
		       tc.status, tc.duration_ms,
		       tc.attempts, tc.attempts_failed, tc.run_failed
		FROM report_groups g
		JOIN reports r ON r.report_group_id = g.id
		JOIN suites s ON s.report_id = r.id
		JOIN test_cases tc ON tc.suite_id = s.id
		WHERE tc.stable_key = $1
		  AND (g.repository = $2 OR split_part(g.repository, '/', 2) = $2)
		  AND ($3 = '' OR g.branch = $3)
          AND ($3 <> 'master' OR g.gh_pr_number IS NULL)
          AND ($8 = '' OR g.name = $8)
          AND ($9::timestamptz IS NULL OR g.created_at <= $9::timestamptz)
          AND (NOT $7 OR (
              g.repository = $2 AND g.gh_pr_number IS NULL AND g.status = 'completed'
              AND g.total_reports_expected > 0
              AND (SELECT count(*) FROM reports cr WHERE cr.report_group_id = g.id) = g.total_reports_expected
              AND NOT EXISTS (SELECT 1 FROM reports cr WHERE cr.report_group_id = g.id AND cr.status <> 'complete')
          ))
		  AND ($4 = '' OR g.framework = $4)
		  AND ($5 = '' OR g.run_group = $5)
		  AND ($6::timestamptz IS NULL OR g.created_at >= $6::timestamptz)
	),
	per_shard AS (
		SELECT id, commit_sha, gh_run_id, gh_pr_number, branch, name, run_group, created_at,
		       count(*)::int                                            AS shard_rows,
		       sum(coalesce(duration_ms, 0))::bigint                    AS duration_ms,
		       bool_or(status IN ('passed', 'flaky'))                   AS ever_passed,
		       bool_or(status IN ` + attemptFailureStatuses + `)        AS ever_failed,
		       -- Rows ingested before the retry-semantics fix stamped every
		       -- attempt of a retried Playwright test 'flaky', the failed
		       -- attempt included, so such a group can be flaky with no
		       -- 'failed' row at all. Current rows carry each attempt's own
		       -- status and reach 'flaky' through ever_passed AND ever_failed
		       -- instead; this branch keeps the older rows reading correctly.
		       bool_or(status = 'flaky')                                AS ever_flaky,
		       -- Every attempt row of one run repeats the run's rollup, so
		       -- max() reads it rather than multiplying it by the attempt
		       -- count. coalesce covers rows written before migration 29,
		       -- which have no stored rollup: they are treated as one attempt,
		       -- failed iff their own status is a failure.
		       max(coalesce(attempts, 1))::int                          AS attempts,
		       max(coalesce(attempts_failed,
		           CASE WHEN status IN ` + attemptFailureStatuses + `
		                THEN 1 ELSE 0 END))::int                        AS attempts_failed,
		       bool_and(coalesce(run_failed,
		           status IN ` + attemptFailureStatuses + `))           AS run_failed
		FROM matched
		GROUP BY id, commit_sha, gh_run_id, gh_pr_number, branch, name, run_group,
		         created_at, report_id
	),
	rolled AS (
		SELECT id, commit_sha, gh_run_id, gh_pr_number, branch, name, run_group, created_at,
		       sum(shard_rows)::int                  AS shard_rows,
		       sum(duration_ms)::bigint              AS duration_ms,
		       bool_or(ever_passed)                  AS ever_passed,
		       bool_or(ever_failed)                  AS ever_failed,
		       bool_or(ever_flaky)                   AS ever_flaky,
		       sum(attempts)::int                    AS attempts,
		       sum(attempts_failed)::int             AS attempts_failed,
		       -- The group failed outright only if no shard's run survived.
		       bool_and(run_failed)                  AS run_failed
		FROM per_shard
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
	GroupID             string          `json:"group_id"`
	Repository          string          `json:"repository"`
	Framework           string          `json:"framework"`
	GHRunAttempt        string          `json:"gh_run_attempt"`
	EnvironmentMetadata json.RawMessage `json:"environment_metadata,omitempty"`
	Commit              string          `json:"commit"`
	GHRunID             string          `json:"gh_run_id"`
	GHPRNumber          *int            `json:"gh_pr_number,omitempty"`
	Branch              string          `json:"branch"`
	Name                string          `json:"name"`
	RunGroup            *string         `json:"run_group,omitempty"`
	Outcome             string          `json:"outcome"`
	ShardRows           int             `json:"shard_rows"`
	DurationMs          int64           `json:"duration_ms"`
	CreatedAt           time.Time       `json:"created_at"`

	// Attempts and AttemptsFailed describe how the run reached its outcome:
	// both CI configs retry once, so a failed-then-passed run reports
	// (2, 1) and a run that never survived reports (2, 2). RunFailed is the
	// single boolean that means the same thing across frameworks — every
	// attempt failed — and is what a consumer should read rather than
	// inferring a failure from the attempt counts.
	Attempts       int  `json:"attempts"`
	AttemptsFailed int  `json:"attempts_failed"`
	RunFailed      bool `json:"run_failed"`
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
	window := q.Get("window")
	if window == "" {
		window = "30d"
	}
	since, err := parseSince(window)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	baseline := false
	if value := q.Get("baseline"); value != "" {
		baseline, err = strconv.ParseBool(value)
		if err != nil {
			api.WriteError(w, r, fmt.Errorf("%w: baseline must be a boolean", api.ErrBadRequest))
			return
		}
	}
	branch := q.Get("branch")
	if baseline {
		if strings.Count(repo, "/") != 1 || strings.HasPrefix(repo, "/") || strings.HasSuffix(repo, "/") || (branch != "" && branch != "master") {
			api.WriteError(w, r, fmt.Errorf("%w: baseline requires an owner/repo slug and the master branch", api.ErrBadRequest))
			return
		}
		branch = "master"
	}
	var before *time.Time
	if value := q.Get("before"); value != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, value)
		if parseErr != nil {
			api.WriteError(w, r, fmt.Errorf("%w: before must be an RFC3339 timestamp", api.ErrBadRequest))
			return
		}
		before = &parsed
	}
	entries, summary, total, err := loadHistory(r.Context(), h.Pool, testID, repo,
		branch, q.Get("framework"), q.Get("run_group"), since, limit, baseline, q.Get("name"), before)
	if err != nil {
		h.logError("tests history", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"test_id": testID, "repo": repo, "entries": entries, "summary": summary,
		"total_entries": total, "truncated": total > len(entries), "window": window,
		"baseline": baseline,
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

// loadHistory reads the complete bounded time window. Only the requested page
// carries detailed metadata; rates and streak boundaries include every row.
func loadHistory(ctx context.Context, pool *pgxpool.Pool, testID, repo, branch, framework, runGroup string, since *time.Time, limit int, baseline bool, name string, before *time.Time) ([]historyEntry, HistorySummary, int, error) {
	rows, err := pool.Query(ctx, groupRollupSQL+`
  SELECT o.commit_sha, o.gh_run_id, o.gh_pr_number, o.branch, o.name, o.run_group,
         o.outcome, o.shard_rows, o.duration_ms, o.created_at,
         o.attempts, o.attempts_failed, o.run_failed,
         g.id::text, g.repository, g.framework, g.gh_run_attempt, g.environment_metadata
  FROM outcomes o JOIN report_groups g ON g.id = o.id
  ORDER BY o.created_at DESC, o.id DESC
 `, testID, repo, branch, framework, runGroup, since, baseline, name, before)
	if err != nil {
		return nil, HistorySummary{}, 0, fmt.Errorf("history query: %w", err)
	}
	defer rows.Close()
	entries := make([]historyEntry, 0, limit)
	var observations []historyEntry
	for rows.Next() {
		var e historyEntry
		if err := rows.Scan(&e.Commit, &e.GHRunID, &e.GHPRNumber, &e.Branch, &e.Name,
			&e.RunGroup, &e.Outcome, &e.ShardRows, &e.DurationMs, &e.CreatedAt,
			&e.Attempts, &e.AttemptsFailed, &e.RunFailed, &e.GroupID, &e.Repository,
			&e.Framework, &e.GHRunAttempt, &e.EnvironmentMetadata); err != nil {
			return nil, HistorySummary{}, 0, fmt.Errorf("history scan: %w", err)
		}
		if len(entries) < limit {
			entries = append(entries, e)
		}
		observations = append(observations, historyEntry{Commit: e.Commit, Outcome: e.Outcome})
	}
	if err := rows.Err(); err != nil {
		return nil, HistorySummary{}, 0, fmt.Errorf("history rows: %w", err)
	}
	summary := summarize(observations)
	// series is a compact rendering of entries, not a second unbounded payload.
	if len(summary.Series) > limit {
		summary.Series = summary.Series[:limit]
	}
	return entries, summary, len(observations), nil
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
	var unit time.Duration
	switch window[len(window)-1] {
	case 'd':
		unit = 24 * time.Hour
	case 'h':
		unit = time.Hour
	case 'm':
		unit = time.Minute
	default:
		return nil, fmt.Errorf("%w: window unit must be d, h or m", api.ErrBadRequest)
	}
	// Reject rather than silently clamp: a clamped window would make the
	// response claim more data than it covers.
	if int64(n) > int64(maxWindow/unit) {
		return nil, fmt.Errorf("%w: window may not exceed 180d", api.ErrBadRequest)
	}
	t := time.Now().Add(-time.Duration(n) * unit)
	return &t, nil
}
