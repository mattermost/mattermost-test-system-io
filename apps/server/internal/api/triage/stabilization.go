// W14 — the stabilization queue: the ranked list of what to fix first.
//
// The queue is entirely derived from ingested history, and it leads on blast
// radius — the number of distinct PRs a test broke — because that is realized
// developer cost rather than "most broken on master". A test leaves the queue
// when its failures stop, which is the only definition of fixed that cannot be
// asserted into being true.
//
// Three things that used to live here are gone, all for the same reason —
// nothing called them. The release-cut guard (W5) was built and callable but
// the release automation it was meant to pause was never located. The SLA
// clocks (W15c) tracked a review latency no code can enforce. Manual promote
// and resolve let a human reorder the queue by hand, but had no caller and no
// interface once the release guard went, and the ranking is the mechanism —
// a hand-maintained parallel ordering is the thing it exists to replace.

package triage

import (
	"net/http"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// StabilizationQueueDepth is the working-queue scope: the organic ranking
// serves at most this many entries (plan W14; promotions ride above it).
const StabilizationQueueDepth = 10

// ---------- W14: the derived stabilization queue ----------

type queueEntry struct {
	TestID      string   `json:"test_id"`
	Titles      []string `json:"titles,omitempty"`
	Runs        int      `json:"runs"`
	Failed      int      `json:"failed"`
	Flaky       int      `json:"flaky"`
	Flips       int      `json:"flips"`
	FailureRate float64  `json:"failure_rate"`
	FlakeRate   float64  `json:"flake_rate"`
	// R7-L2 blast radius — how many DISTINCT open PRs this test failed on in
	// the window. Master counters say how broken a test is; this says how many
	// developers it actually cost. When the queue drains slower than flakes
	// arrive (measured: drain 0.10-0.37/day vs arrival 1.5/day), what you fix
	// matters more than how fast, so this leads the ranking.
	AffectedPRs  int     `json:"affected_prs"`
	FailingSince *string `json:"failing_since_commit,omitempty"`

	// What happened the last time the agent fix loop tried this test. Present
	// on the queue rather than behind a second call because "has anything
	// tried this yet, and did it work" is part of choosing what to work on —
	// both for the loop and for the human reading the list.
	FixAttempts *fixAttemptSummary `json:"fix_attempts,omitempty"`
}

type queueResponse struct {
	Repo   string       `json:"repo"`
	Window string       `json:"window"`
	Depth  int          `json:"depth"`
	Ranked []queueEntry `json:"ranked"`
}

// StabilizationQueue serves GET /api/v1/triage/stabilization/queue?repo=
// Public read: the stabilization loop (an action in the tested repo's CI)
// needs it without a credential round-trip.
func (h *Handlers) StabilizationQueue(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	window := orDefault(r.URL.Query().Get("window"), "30d")
	since, err := parseSince(window)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	// M2 fix: the
	// rollup is per (test, report group) FIRST — the same shape the flakiness
	// leaderboard uses — and only then aggregated per test, so runs = groups
	// the test actually executed in and failure_rate is a real rate instead of
	// degenerating to 0.0/1.0.
	//
	// R7-L2: ranked by BLAST RADIUS first — distinct PRs the test failed on in
	// the window — then by master failure count, then flips.
	//
	// Why the order changed. The queue provably cannot drain: with arrival at
	// 1.5 new flaky tests/day, a 7-day re-measurement window and review-bound
	// concurrency capped at 5, drain is 0.10-0.37/day (see the throughput
	// formula in the strategy doc). When you can only fix a fraction of what
	// arrives, ranking by "most broken on master" optimizes the wrong thing: a
	// 40% flake in a spec every PR runs costs far more developer time than a
	// 40% flake in a rarely-executed suite, yet master counters rate them
	// equally. Distinct affected PRs is the realized cost, measured not
	// modeled.
	//
	// Fallback is deliberate and load-bearing: with no PR runs in the window
	// (fresh install, PR reports not uploaded) every affected_prs is 0 and the
	// ordering degenerates exactly to the previous master-only ranking, so the
	// queue never returns nothing just because PR data is absent.
	rows, err := h.Pool.Query(r.Context(), `
		WITH matched AS (
			SELECT g.id AS group_id, tc.external_test_id, tc.status
			FROM report_groups g
			JOIN reports r ON r.report_group_id = g.id
			JOIN suites s ON s.report_id = r.id
			JOIN test_cases tc ON tc.suite_id = s.id
			WHERE tc.external_test_id IS NOT NULL
			  AND (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
			  AND g.branch IN ('main', 'master')
			  AND g.created_at >= $2::timestamptz
		),
		rolled AS (
			SELECT group_id, external_test_id,
			       bool_or(status IN ('passed', 'flaky'))                   AS ever_passed,
			       bool_or(status IN ('failed', 'timedOut', 'interrupted')) AS ever_failed,
			       bool_or(status = 'flaky')                                AS had_flaky
			FROM matched
			GROUP BY group_id, external_test_id
		),
		master_agg AS (
			SELECT external_test_id,
			       count(*)::int                                                AS runs,
			       count(*) FILTER (WHERE r2.ever_failed)::int                   AS failed,
			       count(*) FILTER (WHERE r2.had_flaky)::int                     AS flaky,
			       count(*) FILTER (WHERE r2.ever_passed AND r2.ever_failed)::int AS flips
			FROM rolled r2
			GROUP BY external_test_id
			HAVING bool_or(r2.ever_failed)
		),
		-- Blast radius: distinct PRs this test failed on. Rolled per (PR, group)
		-- first so a test failing across many shards of one PR run counts once,
		-- then distinct on the PR number so repeated pushes to the same PR are
		-- one affected developer, not several.
		pr_rolled AS (
			SELECT g.gh_pr_number, g.id AS group_id, tc.external_test_id,
			       bool_or(tc.status IN ('failed', 'timedOut', 'interrupted')) AS ever_failed
			FROM report_groups g
			JOIN reports r ON r.report_group_id = g.id
			JOIN suites s ON s.report_id = r.id
			JOIN test_cases tc ON tc.suite_id = s.id
			WHERE tc.external_test_id IS NOT NULL
			  AND (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
			  AND g.gh_pr_number IS NOT NULL
			  AND g.created_at >= $2::timestamptz
			GROUP BY g.gh_pr_number, g.id, tc.external_test_id
		),
		blast AS (
			SELECT external_test_id,
			       count(DISTINCT gh_pr_number)::int AS affected_prs
			FROM pr_rolled
			WHERE ever_failed
			GROUP BY external_test_id
		)
		SELECT m.external_test_id, m.runs, m.failed, m.flaky, m.flips,
		       coalesce(b.affected_prs, 0) AS affected_prs
		FROM master_agg m
		LEFT JOIN blast b ON b.external_test_id = m.external_test_id
		ORDER BY coalesce(b.affected_prs, 0) DESC,
		         m.failed DESC,
		         m.flips DESC
		LIMIT $3
		`, repo, since, StabilizationQueueDepth)
	if err != nil {
		h.logError("stabilization queue ranking", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	// One round trip for the whole repo: the queue is capped at ten entries,
	// but a per-entry lookup would still be ten queries to answer a question
	// the caller always asks.
	attempts, err := h.loadFixAttempts(r, repo)
	if err != nil {
		// The ranking is the answer; the annotation is a bonus. Failing the
		// whole queue because the attempt tally could not be read would take
		// away the more important half.
		h.logError("stabilization queue fix attempts", err)
		attempts = map[string]fixAttemptSummary{}
	}

	ranked := []queueEntry{}
	for rows.Next() {
		var e queueEntry
		// B8 fix: no titles in the ranking projection — round-2 major 2: the
		// CROSS JOIN LATERAL unnest multiplied every counter by the title
		// count; the ranking is counters-only now (the agent reads titles
		// from the spec files themselves).
		if err := rows.Scan(&e.TestID, &e.Runs, &e.Failed, &e.Flaky, &e.Flips, &e.AffectedPRs); err != nil {
			h.logError("stabilization queue scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		if e.Runs > 0 {
			e.FailureRate = float64(e.Failed) / float64(e.Runs)
			e.FlakeRate = float64(e.Flips) / float64(e.Runs)
		}
		if a, ok := attempts[e.TestID]; ok {
			e.FixAttempts = &a
		}
		ranked = append(ranked, e)
		if len(ranked) >= StabilizationQueueDepth {
			break
		}
	}

	writeJSON(w, http.StatusOK, queueResponse{
		Repo:   repo,
		Window: window,
		Depth:  StabilizationQueueDepth,
		Ranked: ranked,
	})
}
