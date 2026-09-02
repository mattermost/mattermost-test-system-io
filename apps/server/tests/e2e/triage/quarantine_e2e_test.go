//go:build e2e

// R7-L3 gates: quarantine's three invariants, against a real database.
//
//  1. Master is untouched — quarantining a test does not move the raw
//     pass-rate and does not remove it from the stabilization ranking.
//  2. Expiry is self-enforcing — active is computed at read time, so a lapsed
//     quarantine stops applying with no cron and no sweeper, and the test can
//     be re-quarantined afterwards.
//  3. Nothing is silent — owner, reason, deadline and creator are all
//     mandatory, and a release is itself a recorded decision.
package triage

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// seedQuarantineTarget makes MM-T7901 a chronic flake on master (8/20) with one
// failing PR run, so the rates endpoint and the queue both have it.
func seedQuarantineTarget(t *testing.T, env *testenv.Env) {
	t.Helper()
	_, err := env.Pool.Exec(t.Context(), `
		DO $$
		DECLARE gid uuid; rid uuid; sid uuid; i int; st text;
		BEGIN
			FOR i IN 0..19 LOOP
				st := CASE WHEN i < 16 AND i % 2 = 0 THEN 'failed' ELSE 'passed' END;
				INSERT INTO report_groups (framework, name, status, repository, branch,
				                           commit_sha, gh_run_id, created_at)
				VALUES ('playwright', 'q-main', 'completed', 'mattermost/mattermost', 'main',
				        'qsha'||i, 'q-run-'||i, now() - make_interval(days => 25 - i))
				RETURNING id INTO gid;
				INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases, failed_cases)
				VALUES (gid, 'shard', 'complete', 1,
				        CASE WHEN st='passed' THEN 1 ELSE 0 END,
				        CASE WHEN st='passed' THEN 0 ELSE 1 END) RETURNING id INTO rid;
				INSERT INTO suites (report_id, title, total_count, ordinal)
				VALUES (rid, 's', 1, 0) RETURNING id INTO sid;
				INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
				VALUES (sid, 'c', 'c', st, 'MM-T7901', 0);
			END LOOP;

			-- One failing PR run, so /triage/evidence has a pack to serve.
			INSERT INTO report_groups (framework, name, status, repository, branch,
			                           commit_sha, gh_run_id, gh_pr_number, created_at)
			VALUES ('playwright', 'q-pr', 'completed', 'mattermost/mattermost', 'feat/q',
			        'qprsha', 'q-run-pr', 7901, now())
			RETURNING id INTO gid;
			INSERT INTO reports (report_group_id, name, status, total_cases, failed_cases)
			VALUES (gid, 'shard', 'complete', 1, 1) RETURNING id INTO rid;
			INSERT INTO suites (report_id, title, total_count, ordinal)
			VALUES (rid, 's', 1, 0) RETURNING id INTO sid;
			INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, error_message, ordinal)
			VALUES (sid, 'c', 'c', 'failed', 'MM-T7901', 'locator.click: Timeout 30000ms exceeded', 0);
		END $$;
	`)
	if err != nil {
		t.Fatalf("seed quarantine target: %v", err)
	}
}

func qPost(t *testing.T, env *testenv.Env, key, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, env.ServerURL+path, strings.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		// A 204 or an empty error body is legitimate; the status is what matters.
		return resp.StatusCode, map[string]any{}
	}
	return resp.StatusCode, out
}

func TestQuarantineRequiresOwnerReasonAndDeadline(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "quarantine-validation")

	cases := []struct {
		name string
		body string
		want string
	}{
		{"no owner", `{"test_id":"MM-T7901","reason":"chronic","days":7}`, "owner is required"},
		{"no reason", `{"test_id":"MM-T7901","owner":"@ti","days":7}`, "reason is required"},
		{"no deadline", `{"test_id":"MM-T7901","owner":"@ti","reason":"chronic"}`, "expires_at or days is required"},
		{"no test", `{"owner":"@ti","reason":"chronic","days":7}`, "test_id is required"},
		{
			"deadline too far",
			`{"test_id":"MM-T7901","owner":"@ti","reason":"chronic","days":365}`,
			"may not exceed",
		},
		{
			"both forms",
			`{"test_id":"MM-T7901","owner":"@ti","reason":"c","days":7,"expires_at":"2026-09-20T00:00:00Z"}`,
			"not both",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := qPost(t, env, key, "/api/v1/triage/quarantine?repo=mattermost/mattermost", tc.body)
			if status < 400 {
				t.Fatalf("expected a 4xx refusal, got %d %v", status, body)
			}
			msg, _ := body["message"].(string)
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("message %q does not mention %q", msg, tc.want)
			}
		})
	}
}

func TestQuarantineWritesAreAuthenticated(t *testing.T) {
	env := testenv.Start(t)
	req, _ := http.NewRequest(http.MethodPost,
		env.ServerURL+"/api/v1/triage/quarantine?repo=mattermost/mattermost",
		strings.NewReader(`{"test_id":"MM-T7901","owner":"@ti","reason":"chronic","days":7}`))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unauthenticated POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("quarantine write without a credential returned %d, want 401/403", resp.StatusCode)
	}
}

// INVARIANT 1: quarantining changes nothing about master.
func TestQuarantineDoesNotImproveRawPassRateOrLeaveTheQueue(t *testing.T) {
	env := testenv.Start(t)
	seedQuarantineTarget(t, env)
	key := env.IssueAPIKey(t, "quarantine-master")

	before := getJSON(t, env, "/api/v1/triage/pass-rates?repo=mattermost/mattermost&branch=main&window=30d")
	rawBefore := before["raw_pass_rate"].(float64)
	failBefore := before["raw_failures"].(float64)

	status, created := qPost(t, env, key,
		"/api/v1/triage/quarantine?repo=mattermost/mattermost",
		`{"test_id":"MM-T7901","owner":"@test-infra","reason":"chronic flake, queued for a fix","days":14}`)
	if status != http.StatusCreated {
		t.Fatalf("quarantine create returned %d: %v", status, created)
	}
	if created["active"] != true {
		t.Fatalf("a fresh quarantine must be active, got %v", created["active"])
	}
	if created["created_by"] == nil || created["created_by"] == "" {
		t.Fatal("created_by must come from the authenticated subject")
	}

	after := getJSON(t, env, "/api/v1/triage/pass-rates?repo=mattermost/mattermost&branch=main&window=30d")
	if got := after["raw_pass_rate"].(float64); got != rawBefore {
		t.Fatalf("raw_pass_rate moved from %v to %v — quarantine must never improve the number "+
			"the team is judged by", rawBefore, got)
	}
	if got := after["raw_failures"].(float64); got != failBefore {
		t.Fatalf("raw_failures moved from %v to %v — master keeps counting a quarantined test",
			failBefore, got)
	}

	// And it stays in the fix ranking: quarantine buys time, not forgiveness.
	q := authedGet(t, env, key, "/api/v1/triage/stabilization/queue?repo=mattermost/mattermost&window=30d")
	found := false
	for _, r := range q["ranked"].([]any) {
		if r.(map[string]any)["test_id"] == "MM-T7901" {
			found = true
		}
	}
	if !found {
		t.Fatal("a quarantined test must stay in the stabilization ranking — otherwise it is the bucket list")
	}
}

// INVARIANT 2: expiry needs no sweeper, and a lapsed row does not block renewal.
func TestQuarantineExpiryIsSelfEnforcing(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "quarantine-expiry")

	status, created := qPost(t, env, key,
		"/api/v1/triage/quarantine?repo=mattermost/mattermost",
		`{"test_id":"MM-T7902","owner":"@test-infra","reason":"chronic","days":7}`)
	if status != http.StatusCreated {
		t.Fatalf("create returned %d: %v", status, created)
	}

	// A second live quarantine for the same test is refused.
	dupStatus, dup := qPost(t, env, key,
		"/api/v1/triage/quarantine?repo=mattermost/mattermost",
		`{"test_id":"MM-T7902","owner":"@test-infra","reason":"again","days":7}`)
	if dupStatus < 400 {
		t.Fatalf("a duplicate live quarantine must be refused, got %d %v", dupStatus, dup)
	}

	// Age the row into the past: created 8 days ago with a 7-day window, so it
	// lapsed yesterday. Both timestamps move together — the expires_at >
	// created_at constraint correctly forbids a deadline before creation, so
	// the only honest way to simulate a lapse is to age the whole record.
	// No job runs; the next read must simply stop seeing it as active.
	if _, err := env.Pool.Exec(t.Context(), `
		UPDATE triage_quarantine
		SET created_at = now() - interval '8 days',
		    expires_at = now() - interval '1 day'
		WHERE external_test_id = 'MM-T7902'
	`); err != nil {
		t.Fatalf("age the row: %v", err)
	}

	live := getJSON(t, env, "/api/v1/triage/quarantine?repo=mattermost/mattermost&test_id=MM-T7902")
	if got := live["active_count"].(float64); got != 0 {
		t.Fatalf("active_count = %v after the deadline passed, want 0 — expiry must need no sweeper", got)
	}

	all := getJSON(t, env, "/api/v1/triage/quarantine?repo=mattermost/mattermost&test_id=MM-T7902&all=true")
	if got := all["count"].(float64); got != 1 {
		t.Fatalf("count = %v with all=true, want 1 — the lapsed row is the audit trail", got)
	}

	// Re-quarantining must now succeed: the lapsed row is stamped as expired
	// rather than blocking the live-unique index forever.
	renewStatus, renewed := qPost(t, env, key,
		"/api/v1/triage/quarantine?repo=mattermost/mattermost",
		`{"test_id":"MM-T7902","owner":"@test-infra","reason":"renewed after review","days":7}`)
	if renewStatus != http.StatusCreated {
		t.Fatalf("renewal after expiry returned %d: %v", renewStatus, renewed)
	}

	var releasedBy string
	if err := env.Pool.QueryRow(t.Context(), `
		SELECT released_by FROM triage_quarantine
		WHERE external_test_id = 'MM-T7902' AND released_by IS NOT NULL
	`).Scan(&releasedBy); err != nil {
		t.Fatalf("lapsed row must be stamped: %v", err)
	}
	if releasedBy != "system:expiry" {
		t.Fatalf("released_by = %q, want system:expiry — the trail must show it lapsed, "+
			"not that a person cancelled it", releasedBy)
	}
}

// INVARIANT 3: a release is a recorded decision, with a reason.
func TestQuarantineReleaseIsARecordedDecision(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "quarantine-release")

	_, created := qPost(t, env, key,
		"/api/v1/triage/quarantine?repo=mattermost/mattermost",
		`{"test_id":"MM-T7903","owner":"@test-infra","reason":"chronic","days":14}`)
	id := created["id"].(string)

	noReason, body := qPost(t, env, key, "/api/v1/triage/quarantine/"+id+"/release", `{}`)
	if noReason < 400 {
		t.Fatalf("a release without a reason must be refused, got %d %v", noReason, body)
	}

	okStatus, released := qPost(t, env, key,
		"/api/v1/triage/quarantine/"+id+"/release", `{"reason":"fixed in #123"}`)
	if okStatus != http.StatusOK {
		t.Fatalf("release returned %d: %v", okStatus, released)
	}
	if released["active"] != false {
		t.Fatalf("a released quarantine must not be active, got %v", released["active"])
	}
	if released["released_by"] == nil || released["released_by"] == "" {
		t.Fatal("released_by must come from the authenticated subject")
	}
	if released["release_reason"] != "fixed in #123" {
		t.Fatalf("release_reason = %v, want the caller's reason", released["release_reason"])
	}

	// Releasing twice is not found — the first release closed it.
	again, _ := qPost(t, env, key,
		"/api/v1/triage/quarantine/"+id+"/release", `{"reason":"again"}`)
	if again != http.StatusNotFound {
		t.Fatalf("second release returned %d, want 404", again)
	}
}

// The evidence pack must carry the live quarantine on a PR run so the action
// gets it in one payload — and must NOT carry it on a master run, where a
// consumer might use it to green master.
func TestEvidencePackCarriesQuarantineOnPRRunsOnly(t *testing.T) {
	env := testenv.Start(t)
	seedQuarantineTarget(t, env)
	key := env.IssueAPIKey(t, "quarantine-evidence")

	if status, body := qPost(t, env, key,
		"/api/v1/triage/quarantine?repo=mattermost/mattermost",
		`{"test_id":"MM-T7901","owner":"@test-infra","reason":"chronic","days":14}`); status != http.StatusCreated {
		t.Fatalf("create returned %d: %v", status, body)
	}

	pr := getJSON(t, env,
		"/api/v1/triage/evidence?repository=mattermost&commit_sha=qprsha&gh_run_id=q-run-pr&name=q-pr&baseline_branch=main")
	clusters := pr["clusters"].([]any)
	if len(clusters) == 0 {
		t.Fatal("expected a failing cluster in the PR pack")
	}
	rep := clusters[0].(map[string]any)["representative"].(map[string]any)
	q, ok := rep["quarantine"].(map[string]any)
	if !ok {
		t.Fatalf("PR evidence pack must carry the live quarantine, got %v", rep["quarantine"])
	}
	if q["active"] != true {
		t.Fatalf("quarantine in the pack must be active, got %v", q["active"])
	}
	if q["owner"] != "@test-infra" {
		t.Fatalf("owner = %v, want @test-infra", q["owner"])
	}

	// The last master run for the same test: no quarantine in the pack.
	master := getJSON(t, env,
		"/api/v1/triage/evidence?repository=mattermost&commit_sha=qsha19&gh_run_id=q-run-19&name=q-main&baseline_branch=main")
	mc := master["clusters"].([]any)
	for _, c := range mc {
		r := c.(map[string]any)["representative"].(map[string]any)
		if r["quarantine"] != nil {
			t.Fatalf("a MAIN evidence pack must never carry a quarantine — master is not gated by it, got %v",
				r["quarantine"])
		}
	}
}

// A quarantine may not outlast the cap, and the cap is reported so a caller
// knows the bound without reading the source.
func TestQuarantineReportsItsCap(t *testing.T) {
	env := testenv.Start(t)
	list := getJSON(t, env, "/api/v1/triage/quarantine?repo=mattermost/mattermost")
	if got := list["max_days"].(float64); got != 30 {
		t.Fatalf("max_days = %v, want 30", got)
	}
}
