//go:build e2e

// The replay path's two server-side gates.
//
// The replay job is what makes the collection window (spec §7 step 1) produce
// an accuracy number while the calling repository's workflows are unmerged. It
// only works if two things hold at the API, and both are easy to break by
// accident:
//
//  1. The worklist must drain. A run that has been adjudicated must stop being
//     returned, or the job re-spends model calls on the same runs forever and
//     never reaches the rest of the backlog.
//
//  2. Replay verdicts must not contaminate the live accuracy figure. A replay
//     verdict is decided with later runs of the same test already in the
//     database; averaging it with live verdicts would overstate what CI does.
package triage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	replayRepo   = "mattermost/mattermost"
	replayBranch = "main"
	replayCommit = "cafebabecafebabecafebabecafebabecafebabe"
	replayRunID  = "77010"
)

func TestReplayCandidatesDrainAndStaySeparateFromLive(t *testing.T) {
	env := testenv.Start(t)
	seedFailedCase(t, env, "rp")

	// 1. An ingested failing run with no ledger row is on the worklist.
	before := getJSON(t, env, "/api/v1/triage/replay/candidates?repo=mattermost&days=30&limit=50")
	if before["count"].(float64) == 0 {
		t.Fatal("a freshly ingested failing run is not on the replay worklist")
	}
	target := ""
	for _, c := range before["candidates"].([]any) {
		m := c.(map[string]any)
		if m["gh_run_id"] == waivedRunID+"rp" {
			target = m["group_id"].(string)
			if m["failed"].(float64) != 2 {
				t.Fatalf("failed = %v, want 2 (both seeded cases)", m["failed"])
			}
		}
	}
	if target == "" {
		t.Fatalf("seeded run %srp is missing from the worklist", waivedRunID)
	}

	// 2. Adjudicating it takes it off the worklist. Without this the job
	//    re-spends model calls on the same runs and never drains the backlog.
	key := env.IssueAPIKey(t, "replay-job")
	postReplayVerdict(t, env, key, waivedRunID+"rp", "MM-T9901")

	after := getJSON(t, env, "/api/v1/triage/replay/candidates?repo=mattermost&days=30&limit=50")
	for _, c := range after["candidates"].([]any) {
		if c.(map[string]any)["group_id"] == target {
			t.Fatal("an adjudicated run is still on the worklist — replay would loop on it forever")
		}
	}

	// 3. The verdict exists, but only under source=replay. The live figure —
	//    the one the rollout is gated on — must not have moved.
	live := getJSON(t, env, "/api/v1/triage/accuracy?repo=mattermost&window=30d")
	if live["source"] != "live" {
		t.Fatalf("default source = %v, want live", live["source"])
	}
	if live["total_verdicts"].(float64) != 0 {
		t.Fatalf("live total_verdicts = %v, want 0 — a replay verdict leaked into the live figure",
			live["total_verdicts"])
	}

	replayed := getJSON(t, env, "/api/v1/triage/accuracy?repo=mattermost&window=30d&source=replay")
	if replayed["total_verdicts"].(float64) != 1 {
		t.Fatalf("replay total_verdicts = %v, want 1", replayed["total_verdicts"])
	}
	if replayed["waived"].(float64) != 1 {
		t.Fatalf("replay waived = %v, want 1 — replay must record what the gate WOULD have done",
			replayed["waived"])
	}

	// 4. An unknown source is rejected rather than silently treated as live:
	//    a typo that quietly returned live numbers under a replay label is the
	//    worst outcome for a measurement.
	resp, err := http.Get(env.ServerURL + "/api/v1/triage/accuracy?repo=mattermost&source=shadow")
	if err != nil {
		t.Fatalf("GET accuracy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("source=shadow: status %d, want 400", resp.StatusCode)
	}
}

func postReplayVerdict(t *testing.T, env *testenv.Env, key, runID, testID string) {
	t.Helper()
	batch := map[string]any{
		"repository": replayRepo,
		"branch":     replayBranch,
		"commit_sha": waivedCommit,
		"gh_run_id":  runID,
		"replay":     true,
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
		t.Fatalf("post replay verdicts: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("post replay verdicts: status %d", resp.StatusCode)
	}
}
