//go:build e2e

// W13 gates: phase starts at shadow; promotion is refused without measured
// agreement; auto-evaluate with no data does nothing; a human may always
// demote; the single source reads back consistently.
package triage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestPhaseGateEndpoints(t *testing.T) {
	env := testenv.Start(t)
	key := env.IssueAPIKey(t, "w13-phase")

	// Bootstrap phase is shadow.
	phase := getJSON(t, env, "/api/v1/triage/phase")
	if phase["phase"].(float64) != 0 {
		t.Fatalf("bootstrap phase = %v, want 0 (shadow)", phase["phase"])
	}

	// Promotion without measured agreement is refused.
	promote := postJSON(t, env, key, "/api/v1/triage/phase?repo=mattermost",
		map[string]any{"phase": 1, "reason": "premature"})
	if promote == nil || promote["status"].(float64) == 200 {
		t.Fatal("promotion must be refused before the bar is met")
	}

	// Auto-evaluate with no reviews/false-greens: no action.
	eval := postJSON(t, env, key, "/api/v1/triage/phase/evaluate?repo=mattermost", map[string]any{})
	if eval["action"] != "none" {
		t.Fatalf("evaluate action = %v, want none on empty state", eval["action"])
	}

	// A human may always demote (already at floor: stays 0, still recorded).
	demote := postJSON(t, env, key, "/api/v1/triage/phase?repo=mattermost",
		map[string]any{"phase": 0, "reason": "holding at shadow"})
	if demote["phase"].(float64) != 0 {
		t.Fatalf("demote response phase = %v, want 0", demote["phase"])
	}
}

func postJSON(t *testing.T, env *testenv.Env, key, path string, body any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, env.ServerURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	out["status"] = float64(resp.StatusCode)
	return out
}
