//go:build e2e

// The three answers a pull request can get, end to end through the real
// handler tree and a real Postgres.
//
// The unit tests in internal/api/triage cover the decision itself. What has to
// be proven here is the half that only a database can answer: that the baseline
// this endpoint reads is rolled up the way the rest of the system counts runs.
// Every historical bug in this area was a counting bug, not a logic bug —
// counting case rows instead of report groups turns a sharded suite's failure
// rate into a statement about how the suite is sharded.
package triage

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	attrRepo   = "mattermost/mattermost"
	attrBranch = "master"
)

// seedRun writes one master report group for a test id with the given outcome.
// Two shards per group on purpose: the rollup must count the group once, not
// once per shard.
func seedRun(t *testing.T, env *testenv.Env, testID, commit string, failed bool, seq int) {
	t.Helper()
	status := "passed"
	if failed {
		status = "failed"
	}
	var groupID string
	err := env.Pool.QueryRow(t.Context(), `
		INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, created_at)
		VALUES ('playwright', 'playwright-full-enterprise-master', 'completed', $1, $2, $3, $4,
		        now() - make_interval(mins => $5))
		RETURNING id::text
	`, attrRepo, attrBranch, commit, fmt.Sprintf("run-%s-%d", testID, seq), seq).Scan(&groupID)
	if err != nil {
		t.Fatalf("seed report_group: %v", err)
	}

	for shard := 0; shard < 2; shard++ {
		_, err = env.Pool.Exec(t.Context(), `
			WITH rep AS (
				INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases, failed_cases, skipped_cases, flaky_cases)
				VALUES ($1::uuid, $2, 'complete', 1, 0, 1, 0, 0)
				RETURNING id
			), s AS (
				INSERT INTO suites (report_id, title, total_count, failed_count, ordinal)
				SELECT id, 'suite', 1, 1, 0 FROM rep
				RETURNING id
			)
			INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
			SELECT id, $3, $3, $4, $5, 0 FROM s
		`, groupID, fmt.Sprintf("shard-%d", shard), testID+" renders", status, testID)
		if err != nil {
			t.Fatalf("seed cases: %v", err)
		}
	}
}

// sha builds a 40-char hex SHA whose LEADING characters vary, so the 8-char
// abbreviation in a reason string still distinguishes two commits.
func sha(prefix string, i int) string {
	return fmt.Sprintf("%s%06d", prefix, i) + "0123456789abcdef0123456789abcdef"
}

func attribution(t *testing.T, env *testenv.Env, testID string, attempts, failed int) map[string]any {
	t.Helper()
	q := url.Values{}
	q.Set("repo", "mattermost")
	q.Set("test_id", testID)
	q.Set("baseline_branch", attrBranch)
	q.Set("attempts", fmt.Sprint(attempts))
	q.Set("failed", fmt.Sprint(failed))
	return getJSON(t, env, "/api/v1/triage/attribution?"+q.Encode())
}

func TestAttribution_SpotlessBaselineMakesThePRTheSuspect(t *testing.T) {
	env := testenv.Start(t)
	for i := 0; i < 20; i++ {
		seedRun(t, env, "MM-T2002", sha("ca", i), false, i+1)
	}

	got := attribution(t, env, "MM-T2002", 3, 3)

	if got["outcome"] != "PR_SUSPECT" {
		t.Fatalf("outcome = %v, want PR_SUSPECT (reason: %v)", got["outcome"], got["reason"])
	}
	if got["can_green"].(bool) {
		t.Fatal("a test that has never failed on master greened the check")
	}
	baseline := got["baseline"].(map[string]any)
	// 20 groups of 2 shards each. Counting case rows would say 40.
	if baseline["runs"].(float64) != 20 {
		t.Fatalf("baseline runs = %v, want 20 — the rollup is counting shards, not runs", baseline["runs"])
	}
}

func TestAttribution_ChronicFlakeGreensTheBystanderButNotAThreeOfThree(t *testing.T) {
	env := testenv.Start(t)
	// 20 master runs, 8 failed: a 40% flake, the MM-T2001/MM-T5824 shape.
	//
	// seq is MINUTES AGO, so a higher i is an older run. The failures are the
	// older runs, which leaves the most recent run green — the flake path
	// rather than the bystander one, which the next test covers instead.
	for i := 0; i < 20; i++ {
		seedRun(t, env, "MM-T2001", sha("be", i), i >= 12, i+1)
	}

	one := attribution(t, env, "MM-T2001", 3, 1)
	if one["outcome"] != "KNOWN_FLAKE" {
		t.Fatalf("1-of-3: outcome = %v, want KNOWN_FLAKE (reason: %v)", one["outcome"], one["reason"])
	}
	if !one["can_green"].(bool) {
		t.Fatal("1-of-3 on a 40 percent flake did not green — this is the primary promise")
	}

	// Same test, same baseline, failed every attempt. p = 0.064.
	three := attribution(t, env, "MM-T2001", 3, 3)
	if three["can_green"].(bool) {
		t.Fatalf("3-of-3 greened: %v", three["reason"])
	}
	if three["outcome"] != "NEEDS_REPRODUCTION" || !three["needs_reproduction"].(bool) {
		t.Fatalf("3-of-3: outcome = %v, want NEEDS_REPRODUCTION", three["outcome"])
	}
	obs := three["observed"].(map[string]any)
	if obs["p_value"] == nil {
		t.Fatal("no p_value returned; the refusal is unexplainable")
	}
	if p := obs["p_value"].(float64); p >= 0.10 {
		t.Fatalf("p_value = %v, want below the 0.10 threshold", p)
	}
}

func TestAttribution_MasterCurrentlyBrokenGreensAndNamesTheCommitRange(t *testing.T) {
	env := testenv.Start(t)
	// The four most recent runs are red and everything before them passed —
	// the "it came from master" case. seq is minutes ago, so i < 4 is recent.
	for i := 0; i < 16; i++ {
		seedRun(t, env, "MM-T2004", sha("d0", i), i < 4, i+1)
	}

	got := attribution(t, env, "MM-T2004", 1, 1)

	if got["outcome"] != "MASTER_BROKEN" {
		t.Fatalf("outcome = %v, want MASTER_BROKEN (reason: %v)", got["outcome"], got["reason"])
	}
	if !got["can_green"].(bool) {
		t.Fatal("a bystander PR stayed red for a break that came from master")
	}
	baseline := got["baseline"].(map[string]any)
	if !baseline["failing_now"].(bool) {
		t.Fatal("failing_now is false while master is red")
	}
	// The commit range between last_pass and failing_since is what author
	// attribution consumes — a green with no owner is the old bucket list.
	if baseline["failing_since_commit"] == nil || baseline["last_pass_commit"] == nil {
		t.Fatalf("no commit range on a master break: %v", baseline)
	}
	if baseline["failing_since_commit"] == baseline["last_pass_commit"] {
		t.Fatal("failing_since and last_pass are the same commit")
	}
}

func TestAttribution_UnknownTestAsksForAReproduction(t *testing.T) {
	env := testenv.Start(t)

	got := attribution(t, env, "MM-T9999", 3, 3)

	if got["outcome"] != "NEEDS_REPRODUCTION" || got["can_green"].(bool) {
		t.Fatalf("outcome = %v can_green = %v, want NEEDS_REPRODUCTION and no green",
			got["outcome"], got["can_green"])
	}
}

func TestAttribution_RejectsIncoherentInput(t *testing.T) {
	env := testenv.Start(t)

	cases := []struct {
		name, query string
	}{
		{"no repo", "test_id=MM-T1"},
		{"no test id", "repo=mattermost"},
		{"more failures than attempts", "repo=mattermost&test_id=MM-T1&attempts=1&failed=3"},
		{"negative failures", "repo=mattermost&test_id=MM-T1&attempts=3&failed=-1"},
		{"zero attempts", "repo=mattermost&test_id=MM-T1&attempts=0&failed=0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Get(env.ServerURL + "/api/v1/triage/attribution?" + c.query)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestAttribution_IgnoresOtherBranchesWhenBuildingTheBaseline(t *testing.T) {
	env := testenv.Start(t)
	// Clean on master...
	for i := 0; i < 10; i++ {
		seedRun(t, env, "MM-T3003", sha("fe", i), false, i+1)
	}
	// ...but failing repeatedly on some other branch. That must not soften the
	// baseline, or a developer could make their own test look flaky by failing
	// it on a branch.
	var groupID string
	if err := env.Pool.QueryRow(t.Context(), `
		INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id)
		VALUES ('playwright', 'pw', 'completed', $1, 'pr-999', 'aaaa', 'noise-1')
		RETURNING id::text
	`, attrRepo).Scan(&groupID); err != nil {
		t.Fatalf("seed noise group: %v", err)
	}
	if _, err := env.Pool.Exec(t.Context(), `
		WITH rep AS (
			INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases, failed_cases, skipped_cases, flaky_cases)
			VALUES ($1::uuid, 'shard', 'complete', 1, 0, 1, 0, 0) RETURNING id
		), s AS (
			INSERT INTO suites (report_id, title, total_count, failed_count, ordinal)
			SELECT id, 'suite', 1, 1, 0 FROM rep RETURNING id
		)
		INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
		SELECT id, 'noise', 'noise', 'failed', 'MM-T3003', 0 FROM s
	`, groupID); err != nil {
		t.Fatalf("seed noise case: %v", err)
	}

	got := attribution(t, env, "MM-T3003", 1, 1)

	baseline := got["baseline"].(map[string]any)
	if baseline["failed"].(float64) != 0 {
		t.Fatalf("baseline failed = %v, want 0 — a non-baseline branch leaked in", baseline["failed"])
	}
	if got["outcome"] != "PR_SUSPECT" {
		t.Fatalf("outcome = %v, want PR_SUSPECT", got["outcome"])
	}
}
