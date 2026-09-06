//go:build e2e

// Shared request helpers for the test-history e2e suite.
package testhistory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

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

// sha builds a 40-char hex SHA whose LEADING characters vary, so abbreviated
// forms still distinguish two commits.
func sha(prefix string, i int) string {
	return fmt.Sprintf("%s%06d", prefix, i) + "0123456789abcdef0123456789abcdef"
}
