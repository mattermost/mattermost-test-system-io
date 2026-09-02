//go:build e2e

// R7-L2 gate: the stabilization queue ranks by BLAST RADIUS — how many distinct
// PRs a test failed on — ahead of how broken it is on master.
//
// Why this ordering is load-bearing. The queue provably cannot drain: arrival
// is 1.5 new flaky tests/day, the re-measurement window is 7 days, and
// concurrency is review-bound and capped at 5, so drain is 0.10-0.37/day. When
// you can only fix a fraction of what arrives, WHAT you fix matters more than
// how fast. A test that fails on six PRs has cost six developers time; a worse
// test on master that nobody's PR runs has cost nobody anything yet.
package triage

import (
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// seedBlastRadius creates two tests with deliberately INVERTED rankings:
//
//	MM-T7801 — 3/20 on master (15%), failed on 6 distinct PRs   -> wide reach
//	MM-T7802 — 8/20 on master (40%), failed on 1 PR             -> worse, narrow
//
// Under the old master-failure-count ordering MM-T7802 wins. Under blast
// radius MM-T7801 does. Anything that silently reverts the ORDER BY fails here.
func seedBlastRadius(t *testing.T, env *testenv.Env) {
	t.Helper()
	_, err := env.Pool.Exec(t.Context(), `
		DO $$
		DECLARE gid uuid; rid uuid; sid uuid; i int; st text; p int;
		BEGIN
			-- MM-T7801 on master: 3 failures in 20 runs.
			FOR i IN 0..19 LOOP
				st := CASE WHEN i IN (3, 9, 15) THEN 'failed' ELSE 'passed' END;
				INSERT INTO report_groups (framework, name, status, repository, branch,
				                           commit_sha, gh_run_id, created_at)
				VALUES ('playwright', 'br-main', 'completed', 'mattermost/mattermost', 'main',
				        'br1sha'||i, 'br1-run-'||i, now() - make_interval(days => 25 - i))
				RETURNING id INTO gid;
				INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases, failed_cases)
				VALUES (gid, 'shard', 'complete', 1,
				        CASE WHEN st='passed' THEN 1 ELSE 0 END,
				        CASE WHEN st='passed' THEN 0 ELSE 1 END) RETURNING id INTO rid;
				INSERT INTO suites (report_id, title, total_count, ordinal)
				VALUES (rid, 's', 1, 0) RETURNING id INTO sid;
				INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
				VALUES (sid, 'c', 'c', st, 'MM-T7801', 0);
			END LOOP;

			-- MM-T7802 on master: 8 failures in 20 runs — strictly worse.
			FOR i IN 0..19 LOOP
				st := CASE WHEN i < 16 AND i % 2 = 0 THEN 'failed' ELSE 'passed' END;
				INSERT INTO report_groups (framework, name, status, repository, branch,
				                           commit_sha, gh_run_id, created_at)
				VALUES ('playwright', 'br-main', 'completed', 'mattermost/mattermost', 'main',
				        'br2sha'||i, 'br2-run-'||i, now() - make_interval(days => 25 - i))
				RETURNING id INTO gid;
				INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases, failed_cases)
				VALUES (gid, 'shard', 'complete', 1,
				        CASE WHEN st='passed' THEN 1 ELSE 0 END,
				        CASE WHEN st='passed' THEN 0 ELSE 1 END) RETURNING id INTO rid;
				INSERT INTO suites (report_id, title, total_count, ordinal)
				VALUES (rid, 's', 1, 0) RETURNING id INTO sid;
				INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
				VALUES (sid, 'c', 'c', st, 'MM-T7802', 0);
			END LOOP;

			-- MM-T7801 fails on SIX distinct PRs.
			FOR p IN 1..6 LOOP
				INSERT INTO report_groups (framework, name, status, repository, branch,
				                           commit_sha, gh_run_id, gh_pr_number, created_at)
				VALUES ('playwright', 'br-pr', 'completed', 'mattermost/mattermost', 'feat/br-'||p,
				        'br1pr'||p, 'br1-pr-'||p, 7800 + p, now() - make_interval(hours => 10 - p))
				RETURNING id INTO gid;
				INSERT INTO reports (report_group_id, name, status, total_cases, failed_cases)
				VALUES (gid, 'shard', 'complete', 1, 1) RETURNING id INTO rid;
				INSERT INTO suites (report_id, title, total_count, ordinal)
				VALUES (rid, 's', 1, 0) RETURNING id INTO sid;
				INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
				VALUES (sid, 'c', 'c', 'failed', 'MM-T7801', 0);
			END LOOP;

			-- MM-T7802 fails on ONE PR, but across THREE pushes to it. Distinct
			-- PR count must collapse those to 1 — one inconvenienced developer.
			FOR p IN 1..3 LOOP
				INSERT INTO report_groups (framework, name, status, repository, branch,
				                           commit_sha, gh_run_id, gh_pr_number, created_at)
				VALUES ('playwright', 'br-pr', 'completed', 'mattermost/mattermost', 'feat/br-single',
				        'br2pr'||p, 'br2-pr-'||p, 7899, now() - make_interval(hours => 10 - p))
				RETURNING id INTO gid;
				INSERT INTO reports (report_group_id, name, status, total_cases, failed_cases)
				VALUES (gid, 'shard', 'complete', 1, 1) RETURNING id INTO rid;
				INSERT INTO suites (report_id, title, total_count, ordinal)
				VALUES (rid, 's', 1, 0) RETURNING id INTO sid;
				INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
				VALUES (sid, 'c', 'c', 'failed', 'MM-T7802', 0);
			END LOOP;
		END $$;
	`)
	if err != nil {
		t.Fatalf("seed blast radius: %v", err)
	}
}

func TestStabilizationQueueRanksByBlastRadius(t *testing.T) {
	env := testenv.Start(t)
	seedBlastRadius(t, env)
	key := env.IssueAPIKey(t, "blast-radius")

	q := authedGet(t, env, key, "/api/v1/triage/stabilization/queue?repo=mattermost/mattermost&window=30d")
	ranked, ok := q["ranked"].([]any)
	if !ok || len(ranked) < 2 {
		t.Fatalf("expected at least 2 ranked entries, got %v", q["ranked"])
	}

	byID := map[string]map[string]any{}
	order := []string{}
	for _, r := range ranked {
		e := r.(map[string]any)
		id := e["test_id"].(string)
		byID[id] = e
		order = append(order, id)
	}

	wide, okWide := byID["MM-T7801"]
	narrow, okNarrow := byID["MM-T7802"]
	if !okWide || !okNarrow {
		t.Fatalf("both tests must be queued; got order %v", order)
	}

	// The counters the ranking is built from.
	if got := int(wide["affected_prs"].(float64)); got != 6 {
		t.Fatalf("MM-T7801 affected_prs = %d, want 6", got)
	}
	// Three pushes to ONE PR is one affected developer, not three.
	if got := int(narrow["affected_prs"].(float64)); got != 1 {
		t.Fatalf("MM-T7802 affected_prs = %d, want 1 — distinct PRs, not distinct runs", got)
	}
	if got := int(wide["failed"].(float64)); got != 3 {
		t.Fatalf("MM-T7801 master failed = %d, want 3", got)
	}
	if got := int(narrow["failed"].(float64)); got != 8 {
		t.Fatalf("MM-T7802 master failed = %d, want 8", got)
	}

	// The gate: wide reach outranks worse-on-master.
	posWide, posNarrow := -1, -1
	for i, id := range order {
		switch id {
		case "MM-T7801":
			posWide = i
		case "MM-T7802":
			posNarrow = i
		}
	}
	if posWide > posNarrow {
		t.Fatalf("blast-radius ranking broken: MM-T7801 (6 PRs, 3 master failures) ranked %d, "+
			"below MM-T7802 (1 PR, 8 master failures) at %d. Order: %v",
			posWide, posNarrow, order)
	}
}

// With no PR runs at all the ranking must degrade to the previous master-only
// order rather than returning nothing — a fresh install has no PR reports yet.
func TestStabilizationQueueFallsBackWithoutPRData(t *testing.T) {
	env := testenv.Start(t)
	_, err := env.Pool.Exec(t.Context(), `
		DO $$
		DECLARE gid uuid; rid uuid; sid uuid; i int; st text;
		BEGIN
			FOR i IN 0..9 LOOP
				st := CASE WHEN i < 4 THEN 'failed' ELSE 'passed' END;
				INSERT INTO report_groups (framework, name, status, repository, branch,
				                           commit_sha, gh_run_id, created_at)
				VALUES ('playwright', 'nb-main', 'completed', 'mattermost/mattermost', 'main',
				        'nbsha'||i, 'nb-run-'||i, now() - make_interval(days => 9 - i))
				RETURNING id INTO gid;
				INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases, failed_cases)
				VALUES (gid, 'shard', 'complete', 1,
				        CASE WHEN st='passed' THEN 1 ELSE 0 END,
				        CASE WHEN st='passed' THEN 0 ELSE 1 END) RETURNING id INTO rid;
				INSERT INTO suites (report_id, title, total_count, ordinal)
				VALUES (rid, 's', 1, 0) RETURNING id INTO sid;
				INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
				VALUES (sid, 'c', 'c', st, 'MM-T7810', 0);
			END LOOP;
		END $$;
	`)
	if err != nil {
		t.Fatalf("seed no-pr data: %v", err)
	}
	key := env.IssueAPIKey(t, "blast-radius-fallback")

	q := authedGet(t, env, key, "/api/v1/triage/stabilization/queue?repo=mattermost/mattermost&window=30d")
	ranked, ok := q["ranked"].([]any)
	if !ok || len(ranked) == 0 {
		t.Fatalf("queue must still rank master-only data with no PR runs, got %v", q["ranked"])
	}
	e := ranked[0].(map[string]any)
	if got := int(e["affected_prs"].(float64)); got != 0 {
		t.Fatalf("affected_prs = %d, want 0 with no PR runs", got)
	}
	if got := int(e["failed"].(float64)); got != 4 {
		t.Fatalf("master failed = %d, want 4 — master counters must still drive the fallback order", got)
	}
}
