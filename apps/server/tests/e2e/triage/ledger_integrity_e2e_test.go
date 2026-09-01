//go:build e2e

// W1 + W2 e2e gates.
//
// W2 — waivers never edit history: a waived verdict changes the check status
// and nothing else. The failure must still be present in /tests/history, still
// counted by /tests/flakiness, and still a raw failure in /triage/pass-rates.
//
// W1 — end to end through the real handler tree: one waived and one real
// failure in the window produce raw_failures=2, effective_failures=1.
package triage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	waivedRepo   = "mattermost/mattermost"
	waivedBranch = "main"
	waivedCommit = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	waivedRunID  = "99001"
)

func seedFailedCase(t *testing.T, env *testenv.Env, suffix string) {
	t.Helper()
	var groupID string
	err := env.Pool.QueryRow(t.Context(), `
		INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id)
		VALUES ('playwright', $1, 'completed', $2, $3, $4, $5)
		RETURNING id::text
	`, "e2e-"+suffix, waivedRepo, waivedBranch, waivedCommit, waivedRunID+suffix).Scan(&groupID)
	if err != nil {
		t.Fatalf("seed report_group: %v", err)
	}

	// A report (shard) + suite + two failed cases: MM-T9901 (to be waived) and
	// MM-T9902 (to stay a real failure).
	_, err = env.Pool.Exec(t.Context(), `
		WITH rep AS (
			INSERT INTO reports (report_group_id, name, status, total_cases, passed_cases, failed_cases, skipped_cases, flaky_cases)
			VALUES ($1::uuid, 'e2e-shard', 'complete', 2, 0, 2, 0, 0)
			RETURNING id
		), s AS (
			INSERT INTO suites (report_id, title, total_count, failed_count, ordinal)
			SELECT id, 'e2e suite', 2, 2, 0 FROM rep
			RETURNING id
		)
		INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
		SELECT id, 'waived case', 'waived case', 'failed', 'MM-T9901', 0 FROM s
		UNION ALL
		SELECT id, 'real case', 'real case', 'failed', 'MM-T9902', 1 FROM s
	`, groupID)
	if err != nil {
		t.Fatalf("seed report/suite/cases: %v", err)
	}
}

func TestWaiverNeverEditsHistoryOrRates(t *testing.T) {
	env := testenv.Start(t)
	seedFailedCase(t, env, "w2")

	// Before the waiver: history shows the failure.
	assertHistoryOutcome(t, env, "MM-T9901", "failed")

	// Post a waived verdict for MM-T9901 only, as the triage action would.
	key := env.IssueAPIKey(t, "w2-triage")
	postVerdict(t, env, key, "MM-T9901")

	// W2 gate: the failure is still in history with outcome failed.
	assertHistoryOutcome(t, env, "MM-T9901", "failed")

	// W2 gate: the leaderboard still counts it — flaky_runs/failures unchanged
	// by the ledger write. min_runs=1 so the fresh series qualifies.
	flaky := getJSON(t, env, "/api/v1/tests/flakiness?repo=mattermost&window=30d&min_runs=1")
	entries := flaky["tests"].([]any)
	found9901, found9902 := false, false
	for _, e := range entries {
		m := e.(map[string]any)
		if m["test_id"] == "MM-T9901" {
			found9901 = true
		}
		if m["test_id"] == "MM-T9902" {
			found9902 = true
		}
	}
	if !found9901 || !found9902 {
		t.Fatalf("flakiness lost a test after waiver: 9901=%v 9902=%v", found9901, found9902)
	}

	// W1 gate end to end: one waived + one real failure in the window.
	rates := getJSON(t, env, "/api/v1/triage/pass-rates?repo=mattermost&branch=main&window=30d")
	if rates["raw_failures"].(float64) != 2 {
		t.Fatalf("raw_failures = %v, want 2 (waived and real both counted)", rates["raw_failures"])
	}
	if rates["waived_failures"].(float64) != 1 {
		t.Fatalf("waived_failures = %v, want 1", rates["waived_failures"])
	}
	if rates["effective_failures"].(float64) != 1 {
		t.Fatalf("effective_failures = %v, want 1 (only the real failure)", rates["effective_failures"])
	}
	if rates["raw_pass_rate"].(float64) != 0 {
		t.Fatalf("raw_pass_rate = %v, want 0", rates["raw_pass_rate"])
	}
	if rates["effective_pass_rate"].(float64) != 50 {
		t.Fatalf("effective_pass_rate = %v, want 50", rates["effective_pass_rate"])
	}
}

func assertHistoryOutcome(t *testing.T, env *testenv.Env, testID, want string) {
	t.Helper()
	body := getJSON(t, env, "/api/v1/tests/history?test_id="+testID+"&repo=mattermost&window=30d")
	entries, ok := body["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("history for %s returned no entries after waiver — waiver edited history", testID)
	}
	got := entries[0].(map[string]any)["outcome"]
	if got != want {
		t.Fatalf("history outcome for %s = %v, want %q — waiver mutated the outcome", testID, got, want)
	}
}

func postVerdict(t *testing.T, env *testenv.Env, key, testID string) {
	t.Helper()
	batch := map[string]any{
		"repository": waivedRepo,
		"branch":     waivedBranch,
		"commit_sha": waivedCommit,
		"gh_run_id":  waivedRunID + "w2",
		"verdicts": []map[string]any{{
			"external_test_id": testID,
			"verdict":          "FLAKY_TEST",
			"confidence":       0.9,
			"check_state":      "success",
			"waived":           true,
			"member_count":     1,
		}},
	}
	raw, _ := json.Marshal(batch)
	req, err := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/triage/verdicts", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post verdicts: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("post verdicts: status %d", resp.StatusCode)
	}
}

func getJSON(t *testing.T, env *testenv.Env, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(env.ServerURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET %s: decode: %v", path, err)
	}
	return body
}