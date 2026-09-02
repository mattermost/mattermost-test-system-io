//go:build e2e

// Round-3 major 4 gate: the dry GET (/alerts/evaluation) and the real POST
// (/alerts/evaluate) must agree on what ?repo=X means for a short-form X. The
// bug: AlertEvaluate normalized the repo to owner/name before the data query,
// so a short-form repo read zero rows while the GET endpoint returned data.
// This seeds a streak stored in SHORT form and asserts both endpoints see it.
package triage

import (
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// seedShortFormStreak stores the same 12-day streak as seedTwelveDayStreak but
// with repository = 'mattermost' (short form) instead of 'mattermost/mattermost'.
func seedShortFormStreak(t *testing.T, env *testenv.Env) {
	t.Helper()
	_, err := env.Pool.Exec(t.Context(), `
		DO $$
		DECLARE g int; gid uuid; rid uuid; sid uuid;
		BEGIN
			INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, created_at)
			VALUES ('playwright', 'short-pass', 'completed', 'mattermost', 'main',
			        'shortpass', 'short-run-pass', now() - interval '12 days')
			RETURNING id INTO gid;
			INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases)
			VALUES (gid, 'shard', 'complete', 1, 1) RETURNING id INTO rid;
			INSERT INTO suites (report_id, title, total_count, passed_count, ordinal)
			VALUES (rid, 's', 1, 1, 0) RETURNING id INTO sid;
			INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
			VALUES (sid, 'c', 'c', 'passed', 'MM-T7002', 0);

			FOR g IN 0..11 LOOP
				INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, created_at)
				VALUES ('playwright', 'short-' || g, 'completed', 'mattermost', 'main',
				        'shortsha' || g, 'short-run-' || g, now() - (g || ' days')::interval)
				RETURNING id INTO gid;
				INSERT INTO reports (report_group_id, name, status, total_cases, failed_cases)
				VALUES (gid, 'shard', 'complete', 1, 1)
				RETURNING id INTO rid;
				INSERT INTO suites (report_id, title, total_count, failed_count, ordinal)
				VALUES (rid, 's', 1, 1, 0)
				RETURNING id INTO sid;
				INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
				VALUES (sid, 'c', 'c', 'failed', 'MM-T7002', 0);
			END LOOP;
		END $$;
	`)
	if err != nil {
		t.Fatalf("seed short-form streak: %v", err)
	}
}

func TestAlertEvaluateAndEvaluationAgreeOnShortRepo(t *testing.T) {
	env := testenv.Start(t)
	seedShortFormStreak(t, env)
	key := env.IssueAPIKey(t, "w7-short-repo")

	// Dry GET: the streak rule must fire for the short-form repo.
	dry := getJSON(t, env, "/api/v1/triage/alerts/evaluation?repo=mattermost")
	dryAlerts, ok := dry["alerts"].([]any)
	if !ok || !alertListHasRule(dryAlerts, "new_failing_streak") {
		t.Fatalf("dry evaluation alerts = %+v, want new_failing_streak", dry["alerts"])
	}

	// Real POST: must see the SAME streak (round-3 major 4 — before the fix it
	// normalized to mattermost/mattermost and read zero rows).
	real := postJSON(t, env, key, "/api/v1/triage/alerts/evaluate?repo=mattermost", map[string]any{})
	if real["status"].(float64) != 200 {
		t.Fatalf("evaluate status = %v, want 200", real["status"])
	}
	realAlerts, ok := real["alerts"].([]any)
	if !ok || !alertListHasRule(realAlerts, "new_failing_streak") {
		t.Fatalf("evaluate alerts = %+v, want new_failing_streak (short-form repo must not read zero data)", real["alerts"])
	}
}
