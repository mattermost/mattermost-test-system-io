//go:build e2e

// W9 gate: a failure whose ONLY difference from the last passing run is a
// captured config value is pre-tagged FLAKY_INFRA by the deterministic layer
// — NeedsAI false means zero model calls, asserted on the payload.
package triage

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func seedConfigDeltaGroups(t *testing.T, env *testenv.Env) {
	t.Helper()
	_, err := env.Pool.Exec(t.Context(), `
		DO $$
		DECLARE gid uuid; rid uuid; sid uuid;
		BEGIN
			-- The last PASSING run, two days ago, under E2E_FLAG_X=true.
			INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, created_at, environment_metadata)
			VALUES ('playwright', 'w9-pass', 'completed', 'mattermost/mattermost', 'main',
			        'w9passsha', 'w9-run-pass', now() - interval '2 days', '{"E2E_FLAG_X": "true"}'::jsonb)
			RETURNING id INTO gid;
			INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases) VALUES (gid, 'shard', 'complete', 1, 1) RETURNING id INTO rid;
			INSERT INTO suites (report_id, title, total_count, passed_count, ordinal) VALUES (rid, 's', 1, 1, 0) RETURNING id INTO sid;
			INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
			VALUES (sid, 'c', 'c', 'passed', 'MM-T9501', 0);

			-- The failing run, now, under E2E_FLAG_X=false — the sole difference.
			INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, created_at, environment_metadata)
			VALUES ('playwright', 'w9-fail', 'completed', 'mattermost/mattermost', 'main',
			        'w9failsha', 'w9-run-fail', now(), '{"E2E_FLAG_X": "false"}'::jsonb)
			RETURNING id INTO gid;
			INSERT INTO reports (report_group_id, name, status, total_cases, failed_cases) VALUES (gid, 'shard', 'complete', 1, 1) RETURNING id INTO rid;
			INSERT INTO suites (report_id, title, total_count, failed_count, ordinal) VALUES (rid, 's', 1, 1, 0) RETURNING id INTO sid;
			INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, error_message, ordinal)
			VALUES (sid, 'c', 'c', 'failed', 'MM-T9501', 'element not visible', 0);
		END $$;
	`)
	if err != nil {
		t.Fatalf("seed config-delta groups: %v", err)
	}
}

func TestConfigDeltaPreTagDeterministic(t *testing.T) {
	env := testenv.Start(t)
	seedConfigDeltaGroups(t, env)

	ev := getJSON(t, env, "/api/v1/triage/evidence?repository=mattermost&commit_sha=w9failsha&gh_run_id=w9-run-fail&name=w9-fail&baseline_branch=main")

	group := ev["group"].(map[string]any)
	if group["environment_metadata"] == nil {
		t.Fatal("failing group must carry its captured environment_metadata")
	}

	clusters := ev["clusters"].([]any)
	if len(clusters) == 0 {
		t.Fatal("no failure clusters in evidence")
	}
	c := clusters[0].(map[string]any)
	suggested := c["suggested"].(map[string]any)

	if suggested["verdict"] != "FLAKY_INFRA" {
		t.Fatalf("verdict = %v, want FLAKY_INFRA (config-delta pre-tag)", suggested["verdict"])
	}
	if suggested["needs_ai"] != false {
		t.Fatalf("needs_ai = %v, want false — the pre-tag exists to make model calls zero", suggested["needs_ai"])
	}
	cites, _ := suggested["citations"].([]any)
	found := false
	for _, c2 := range cites {
		if c2 == "config_delta_only" {
			found = true
		}
	}
	if !found {
		t.Fatalf("citations = %v, want config_delta_only", cites)
	}

	// Same config on both sides → no delta → no pre-tag (fail closed).
	_, err := env.Pool.Exec(t.Context(), `
		UPDATE report_groups SET environment_metadata = '{"E2E_FLAG_X": "true"}'::jsonb
		WHERE commit_sha = 'w9failsha'
	`)
	if err != nil {
		t.Fatalf("reset env: %v", err)
	}
	ev2 := getJSON(t, env, "/api/v1/triage/evidence?repository=mattermost&commit_sha=w9failsha&gh_run_id=w9-run-fail&name=w9-fail&baseline_branch=main")
	c2 := ev2["clusters"].([]any)[0].(map[string]any)
	s2 := c2["suggested"].(map[string]any)
	if s2["verdict"] == "FLAKY_INFRA" {
		t.Fatal("identical configs must not pre-tag — the delta is the signal, not the failure")
	}
	_ = time.Now
}
