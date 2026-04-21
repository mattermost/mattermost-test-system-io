//go:build e2e

// Package web_parity exercises the /api/v1 surface the React web client depends
// on (see specs/007-web-api-parity/). Each test asserts a single route the web
// hits on page load, and the asserted behavior is the contract — regressions
// here mean the web breaks.
package web_parity

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestConfigEndpoint_anonymousReturnsShape(t *testing.T) {
	e := testenv.Start(t)

	resp := doGET(t, e.ServerURL+"/api/v1/config")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		UploadTimeoutMs    int  `json:"upload_timeout_ms"`
		HTMLViewEnabled    bool `json:"html_view_enabled"`
		SearchMinLength    int  `json:"search_min_length"`
		GitHubOAuthEnabled bool `json:"github_oauth_enabled"`
	}
	mustDecode(t, resp, &body)
	if body.SearchMinLength <= 0 {
		t.Errorf("search_min_length = %d, want > 0", body.SearchMinLength)
	}
	if body.UploadTimeoutMs <= 0 {
		t.Errorf("upload_timeout_ms = %d, want > 0", body.UploadTimeoutMs)
	}
}

func TestInfoEndpoint_anonymousReturnsShape(t *testing.T) {
	e := testenv.Start(t)

	resp := doGET(t, e.ServerURL+"/api/v1/info")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		ServerVersion string `json:"server_version"`
		Environment   string `json:"environment"`
		RepoURL       string `json:"repo_url"`
		CommitSHA     string `json:"commit_sha"`
		BuildTime     string `json:"build_time"`
	}
	mustDecode(t, resp, &body)
	if body.ServerVersion == "" {
		t.Errorf("server_version empty")
	}
}

func TestAuthMe_anonymousReturnsNullUser(t *testing.T) {
	e := testenv.Start(t)

	resp := doGET(t, e.ServerURL+"/api/v1/auth/me")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		User any `json:"user"`
	}
	mustDecode(t, resp, &body)
	if body.User != nil {
		t.Errorf("user = %v, want nil", body.User)
	}
}

func TestReportsGrouped_anonymousReturnsEmptyGroups(t *testing.T) {
	e := testenv.Start(t)

	resp := doGET(t, e.ServerURL+"/api/v1/reports/grouped")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		Groups []any `json:"groups"`
	}
	mustDecode(t, resp, &body)
	if body.Groups == nil {
		t.Errorf("groups key missing; want at least empty array")
	}
}

func TestReportsIndividual_anonymousReturnsOffsetShape(t *testing.T) {
	e := testenv.Start(t)

	resp := doGET(t, e.ServerURL+"/api/v1/reports/individual?limit=50&offset=0")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		Reports []any `json:"reports"`
		Total   int   `json:"total"`
		Limit   int   `json:"limit"`
		Offset  int   `json:"offset"`
	}
	mustDecode(t, resp, &body)
	if body.Limit != 50 {
		t.Errorf("limit = %d, want 50", body.Limit)
	}
}

func TestReports_anonymousReadsAllowed(t *testing.T) {
	e := testenv.Start(t)

	resp := doGET(t, e.ServerURL+"/api/v1/reports?limit=10&offset=0")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		Reports []any `json:"reports"`
		Total   int   `json:"total"`
		Limit   int   `json:"limit"`
		Offset  int   `json:"offset"`
	}
	mustDecode(t, resp, &body)
}

func TestUpload_requiresAuth(t *testing.T) {
	e := testenv.Start(t)

	resp, err := http.Post(e.ServerURL+"/api/v1/reports", "application/octet-stream", strings.NewReader("noop"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", resp.StatusCode, bodyString(resp))
	}
}

// ---- helpers ----

func doGET(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustDecode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func bodyString(resp *http.Response) string {
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
