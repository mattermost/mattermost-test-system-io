//go:build e2e

// Shared request helpers for the triage e2e suite.
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

// decodeJSON reads a response body as a JSON object.
func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// getJSON performs an unauthenticated GET and fails the test on any non-200.
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
	return decodeJSON(t, resp)
}

// getJSONAuthed performs a GET with an API key attached.
func getJSONAuthed(t *testing.T, env *testenv.Env, key, path string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.ServerURL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	return decodeJSON(t, resp)
}
