//go:build e2e

// Shared request helpers for the triage e2e suite.
//
// These lived in phase_e2e_test.go until the rollout-phase ladder was removed,
// which took the helper with it and broke the build for every other file in
// the package. They belong in a file that is about the suite rather than about
// one feature.
package triage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// postJSON posts an authenticated JSON body and returns the decoded response
// with the HTTP status folded in under "status". Callers assert on the status
// as often as on the body — several of these endpoints answer 409 or 401 as a
// normal, expected outcome rather than a failure.
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
	if out == nil {
		out = map[string]any{}
	}
	out["status"] = float64(resp.StatusCode)
	return out
}
