//go:build e2e

// W7/W8 gates: a test failing 12 consecutive master days fires the
// new-failing-streak rule; the same-day second evaluate is suppressed (one
// channel post per 24h); fire counts still record the truth; replay walks the
// window and reports posts-per-week.
package triage

import (
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func seedTwelveDayStreak(t *testing.T, env *testenv.Env) {
	t.Helper()
	_, err := env.Pool.Exec(t.Context(), `
		DO $$
		DECLARE g int; gid uuid; rid uuid; sid uuid;
		BEGIN
			-- A PASS 12 days back: the streak is NEWLY entered (M5 — an
			-- all-fail series predates the window and must not fire).
			INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, created_at)
			VALUES ('playwright', 'w7-pass', 'completed', 'mattermost/mattermost', 'main',
			        'w7pass', 'w7-run-pass', now() - interval '12 days')
			RETURNING id INTO gid;
			INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases)
			VALUES (gid, 'shard', 'complete', 1, 1) RETURNING id INTO rid;
			INSERT INTO suites (report_id, title, total_count, passed_count, ordinal)
			VALUES (rid, 's', 1, 1, 0) RETURNING id INTO sid;
			INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
			VALUES (sid, 'c', 'c', 'passed', 'MM-T7001', 0);

			FOR g IN 0..11 LOOP
				INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, created_at)
				VALUES ('playwright', 'w7-' || g, 'completed', 'mattermost/mattermost', 'main',
				        'w7sha' || g, 'w7-run-' || g, now() - (g || ' days')::interval)
				RETURNING id INTO gid;
				INSERT INTO reports (report_group_id, name, status, total_cases, failed_cases)
				VALUES (gid, 'shard', 'complete', 1, 1)
				RETURNING id INTO rid;
				INSERT INTO suites (report_id, title, total_count, failed_count, ordinal)
				VALUES (rid, 's', 1, 1, 0)
				RETURNING id INTO sid;
				INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
				VALUES (sid, 'c', 'c', 'failed', 'MM-T7001', 0);
			END LOOP;
		END $$;
	`)
	if err != nil {
		t.Fatalf("seed streak: %v", err)
	}
}

func TestMasterAlertingFiresAndDedups(t *testing.T) {
	env := testenv.Start(t)
	seedTwelveDayStreak(t, env)
	key := env.IssueAPIKey(t, "w7-alerts")

	// First evaluate: the streak rule fires, one post goes out, no issue yet
	// (persistence needs 48h).
	first := postJSON(t, env, key, "/api/v1/triage/alerts/evaluate?repo=mattermost", map[string]any{})
	if first["status"].(float64) != 200 {
		t.Fatalf("evaluate #1: status %v", first["status"])
	}
	alerts := first["alerts"].([]any)
	if !alertListHasRule(alerts, "new_failing_streak") {
		t.Fatalf("evaluate #1 alerts = %+v, want new_failing_streak", alerts)
	}
	if first["posted"].(float64) != 1 {
		t.Fatalf("posted = %v, want 1", first["posted"])
	}
	if first["issues_opened"].(float64) != 0 {
		t.Fatalf("issues_opened = %v, want 0 (first firing, <48h)", first["issues_opened"])
	}

	// Second evaluate, same day: suppressed — one channel post per 24h. The
	// firing itself still records (fire_count 2).
	second := postJSON(t, env, key, "/api/v1/triage/alerts/evaluate?repo=mattermost", map[string]any{})
	if second["posted"].(float64) != 0 {
		t.Fatalf("second evaluate posted = %v, want 0 (24h cooldown)", second["posted"])
	}
	if second["suppressed"].(float64) != 1 {
		t.Fatalf("second evaluate suppressed = %v, want 1", second["suppressed"])
	}

	var fireCount, channelPosts int
	if err := env.Pool.QueryRow(t.Context(), `
		SELECT fire_count, channel_posts FROM alert_firings
		WHERE rule = 'new_failing_streak' AND subject = 'MM-T7001'
		  AND resolved_at IS NULL
	`).Scan(&fireCount, &channelPosts); err != nil {
		t.Fatalf("read firing record: %v", err)
	}
	if fireCount != 2 {
		t.Fatalf("fire_count = %d, want 2 (truth records every firing)", fireCount)
	}
	if channelPosts != 1 {
		t.Fatalf("channel_posts = %d, want 1 (deduped)", channelPosts)
	}

	// Replay: walks the window, reports structure + posts-per-week under the
	// fatigue bar even for this pathological all-failing fixture.
	replay := getJSON(t, env, "/api/v1/triage/alerts/replay?repo=mattermost&days=12")
	if replay["days_with_data"].(float64) < 10 {
		t.Fatalf("replay days_with_data = %v, want >= 10", replay["days_with_data"])
	}
	if replay["posts_per_week"].(float64) >= 10 {
		t.Fatalf("replay posts_per_week = %v, want < 10 (alert-fatigue bar)", replay["posts_per_week"])
	}
	if _, ok := replay["gate_note"].(string); !ok {
		t.Fatal("replay must carry the gate note (real 30-day gate needs production data + reviewer)")
	}
}

func alertListHasRule(alerts []any, rule string) bool {
	for _, a := range alerts {
		if m, ok := a.(map[string]any); ok && m["rule"] == rule {
			return true
		}
	}
	return false
}
