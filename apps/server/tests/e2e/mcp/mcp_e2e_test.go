//go:build e2e
// +build e2e

// Package mcpe2e exercises /mcp end to end: who may connect, and every tool through a real MCP client
// over Streamable HTTP against seeded reports.
package mcpe2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	lane = "playwright-full-enterprise-master"
	file = "specs/functional/channels/classification.spec.ts"
	shot = "reports/r2/screenshots/iter-0/classification-chrome/test-failed-1.png"
)

var sha1, sha2 = "1111111" + strings.Repeat("a", 33), "2222222" + strings.Repeat("b", 33)

func seed(t *testing.T, env *testenv.Env, sha, runID string, at time.Time, status string) {
	t.Helper()
	ctx := context.Background()
	var gid, rid, sid, cid string
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt, status, created_at)
		VALUES ('playwright', $1, 'mattermost/mattermost', 'master', $2, $3, '1', 'completed', $4) RETURNING id`,
		lane, sha, runID, at).Scan(&gid); err != nil {
		t.Fatal(err)
	}
	if err := env.Pool.QueryRow(ctx, `INSERT INTO reports (report_group_id, name, status) VALUES ($1, 'chrome-1', 'complete') RETURNING id`, gid).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	if err := env.Pool.QueryRow(ctx, `INSERT INTO suites (report_id, title, file, ordinal) VALUES ($1, 'Classification', $2, 0) RETURNING id`, rid, file).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO test_cases (suite_id, title, full_title, status, retry_count, ordinal, error_message)
		VALUES ($1, 'dropdown', 'Classification > dropdown', $2, 0, 0, 'Error: checkbox did not change') RETURNING id`, sid, status).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	if status == "failed" {
		if _, err := env.Pool.Exec(ctx, `
			INSERT INTO report_screenshots (report_id, filename, s3_key, size_bytes, test_name, case_id)
			VALUES ($1, 'test-failed-1.png', $2, 72, 'dropdown', $3)`, rid, shot, cid); err != nil {
			t.Fatal(err)
		}
	}
}

func post(t *testing.T, url, key string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

type withKey struct{ key string }

func (k withKey) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-API-Key", k.key)
	return http.DefaultTransport.RoundTrip(r)
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool error %+v", name, res.Content)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestMCPAccess(t *testing.T) {
	env := testenv.Start(t)
	endpoint := env.ServerURL + "/mcp"
	if code := post(t, endpoint, ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", code)
	}
	if code := post(t, endpoint, env.IssueAPIKey(t, "ci")); code != http.StatusForbidden {
		t.Errorf("CI uploader key: status = %d, want 403", code)
	}
	if code := post(t, endpoint, env.IssueAPIKeyWithRole(t, "reader", policy.RoleViewer)); code != http.StatusOK {
		t.Errorf("viewer key: status = %d, want 200", code)
	}
}

func TestMCPToolsOverStreamableHTTP(t *testing.T) {
	env := testenv.Start(t)
	now := time.Now().UTC()
	seed(t, env, sha1, "r1", now.Add(-2*time.Hour), "passed")
	seed(t, env, sha2, "r2", now.Add(-time.Hour), "failed")
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	if err := env.Store.Put(context.Background(), shot, bytes.NewReader(png), "image/png", int64(len(png))); err != nil {
		t.Fatal(err)
	}

	key := env.IssueAPIKeyWithRole(t, "alpha-ai", policy.RoleViewer)
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   env.ServerURL + "/mcp",
		HTTPClient: &http.Client{Transport: withKey{key}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != 5 {
		t.Fatalf("tools = %v, %v; want 5", tools, err)
	}

	var runs struct {
		Runs []struct {
			CommitSHA string `json:"commit_sha"`
			GHRunID   string `json:"gh_run_id"`
		} `json:"runs"`
	}
	_ = json.Unmarshal([]byte(text(call(t, cs, "find_runs", map[string]any{"repository": "mattermost", "names": []string{lane}}))), &runs)
	if len(runs.Runs) != 2 || runs.Runs[0].CommitSHA != sha2 || runs.Runs[0].GHRunID != "r2" {
		t.Errorf("find_runs = %+v; want r2 then r1", runs.Runs)
	}

	run := map[string]any{"repository": "mattermost", "branch": "master", "name": lane, "commit": sha2[:7], "gh_run_id": "r2", "gh_run_attempt": "1"}
	if got := text(call(t, cs, "get_run_failures", run)); !strings.Contains(got, "checkbox did not change") || !strings.Contains(got, shot) {
		t.Errorf("get_run_failures = %s; want the error and the screenshot key", got)
	}
	if got := text(call(t, cs, "get_test_history", run)); !strings.Contains(got, `"last_not_failed":{"commit_sha":"`+sha1) {
		t.Errorf("get_test_history = %s; want the last run that didn't fail at %s", got, sha1[:7])
	}
	if got := text(call(t, cs, "get_test_runs", map[string]any{
		"repository": "mattermost/mattermost", "files": []string{file}, "names": []string{lane}, "include_screenshots": true,
	})); !strings.Contains(got, `"gh_run_id":"r1"`) || !strings.Contains(got, shot) {
		t.Errorf("get_test_runs = %s; want both runs and the screenshot key", got)
	}
	img, ok := call(t, cs, "get_screenshot", map[string]any{"key": shot}).Content[0].(*mcp.ImageContent)
	if !ok || img.MIMEType != "image/png" || !bytes.Equal(img.Data, png) {
		t.Errorf("get_screenshot did not return the PNG")
	}
}
