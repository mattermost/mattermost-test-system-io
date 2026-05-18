//go:build e2e
// +build e2e

// Package orchestratione2e exercises the /api/v1/orchestration/* endpoints end
// to end against a real Postgres testcontainer and the in-process HTTP stack.
package orchestratione2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	defaultRepository    = "mattermost/orch-e2e"
	defaultRepoOwner     = "mattermost"
	defaultCommit        = "deadbeefcafebabe1234567890abcdefdeadbeef"
	defaultGHRunID       = "ci-run-1"
	defaultName          = "playwright-default"
	defaultRunAttempt    = "1"
	defaultBranch        = "main"
	defaultWorkflow      = "ci"
	defaultRef           = "refs/heads/main"
	defaultOIDCSubject   = "repo:mattermost/orch-e2e:ref:refs/heads/main"
	defaultPlaywrightPrj = "default"
)

// startEnv boots the test harness and installs an OIDC policy that grants the
// uploader role to anything from defaultRepoOwner. It returns the env plus a
// minted OIDC token usable as `Authorization: Bearer <tok>`.
func startEnv(t *testing.T) (*testenv.Env, string) {
	t.Helper()
	env := testenv.Start(t)
	env.InsertPolicy(t, "allow-orch-e2e", 1, string(policy.RoleUploader), map[string]string{
		"repository_owner": defaultRepoOwner,
	})
	tok := mintToken(t, env, defaultOIDCSubject)
	return env, tok
}

// mintToken issues an OIDC bearer token whose subject is `sub` (defaults to
// the package's defaultOIDCSubject) and whose claims match the policy seeded
// by startEnv.
func mintToken(t *testing.T, env *testenv.Env, sub string) string {
	t.Helper()
	if sub == "" {
		sub = defaultOIDCSubject
	}
	return env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         sub,
		Audience:        "tsio",
		Repository:      defaultRepository,
		RepositoryOwner: defaultRepoOwner,
		Workflow:        defaultWorkflow,
		Ref:             defaultRef,
	})
}

// identity returns the JSON-shape composite-identity fields used in begin /
// checkout / complete request bodies. Using a per-test override path makes it
// easy to isolate runs that share the same testcontainer.
func identity(extras map[string]any) map[string]any {
	out := map[string]any{
		"repository":     defaultRepository,
		"commit_sha":     defaultCommit,
		"gh_run_id":      defaultGHRunID,
		"name":           defaultName,
		"gh_run_attempt": defaultRunAttempt,
		"branch":         defaultBranch,
		"framework":      "playwright",
	}
	for k, v := range extras {
		out[k] = v
	}
	return out
}

// merge produces a new map that shallow-overlays src onto base. Helper for
// composing request bodies from identity() + per-test fields.
func merge(base map[string]any, src map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range src {
		out[k] = v
	}
	return out
}

// postJSON marshals body to JSON and POSTs it to env.ServerURL + path with the
// Bearer token attached. The returned response's body is closed by the test
// cleanup helper.
func postJSON(t *testing.T, env *testenv.Env, token, path string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, env.ServerURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// getJSON GETs env.ServerURL + path with the Bearer token attached.
func getJSON(t *testing.T, env *testenv.Env, token, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.ServerURL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// The default client does not auto-follow redirects we want to inspect.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// statusURL builds the GET /status query string from a composite-identity map.
func statusURL(id map[string]any) string {
	v := url.Values{}
	for _, k := range []string{"repository", "commit_sha", "gh_run_id", "name", "gh_run_attempt"} {
		if s, ok := id[k].(string); ok {
			v.Set(k, s)
		}
	}
	return "/api/v1/orchestration/status?" + v.Encode()
}

// decodeJSON decodes resp.Body into a map[string]any. Fatal on error.
func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var out map[string]any
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// readBodyString returns the response body as a string for error messages.
func readBodyString(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// jsonNumberInt converts a json.Number value to int. Fatal on type error.
func jsonNumberInt(t *testing.T, v any) int {
	t.Helper()
	n, ok := v.(json.Number)
	if !ok {
		t.Fatalf("expected json.Number, got %T (%v)", v, v)
	}
	i, err := n.Int64()
	if err != nil {
		t.Fatalf("int64: %v", err)
	}
	return int(i)
}

// expectStatus fatals when resp.StatusCode != want. Reads + truncates the body
// for the error message.
func expectStatus(t *testing.T, resp *http.Response, want int) map[string]any {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, want, readBodyString(resp))
	}
	if resp.ContentLength == 0 || resp.Header.Get("Content-Type") == "" {
		return map[string]any{}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return decodeJSON(t, resp)
	}
	return map[string]any{}
}

// counts extracts the run snapshot's counts object as a map[string]int.
func counts(t *testing.T, snapshot map[string]any) map[string]int {
	t.Helper()
	raw, ok := snapshot["counts"].(map[string]any)
	if !ok {
		t.Fatalf("counts missing from snapshot: %v", snapshot)
	}
	out := map[string]int{}
	for k, v := range raw {
		if v == nil {
			continue
		}
		out[k] = jsonNumberInt(t, v)
	}
	return out
}

// beginRunBody composes a begin body with the supplied spec_path entries.
// Each entry becomes its own dispatch unit; the orchestrator does not
// bundle specs.
func beginRunBody(specPaths []string, extras map[string]any) map[string]any {
	dispatch := make([]map[string]any, 0, len(specPaths))
	for _, sp := range specPaths {
		dispatch = append(dispatch, map[string]any{"spec_path": sp})
	}
	body := merge(identity(nil), map[string]any{
		"playwright_project":     defaultPlaywrightPrj,
		"total_reports_expected": 1,
		"dispatch_units":         dispatch,
	})
	for k, v := range extras {
		body[k] = v
	}
	return body
}

// checkoutBody composes a checkout body for the given worker.
func checkoutBody(jobName, jobID string, batch int) map[string]any {
	return merge(identity(nil), map[string]any{
		"gh_job_name": jobName,
		"gh_job_id":   jobID,
		"batch_size":  batch,
	})
}

// completeBody composes a complete body for the given worker with a list of
// (spec_path, status) results.
func completeBody(jobName, jobID string, results []map[string]any) map[string]any {
	return merge(identity(nil), map[string]any{
		"gh_job_name": jobName,
		"gh_job_id":   jobID,
		"results":     results,
	})
}

// passResults builds a results array marking every spec as passed.
func passResults(specs []string) []map[string]any {
	out := make([]map[string]any, 0, len(specs))
	for _, sp := range specs {
		out = append(out, map[string]any{
			"spec_path": sp,
			"status":    "passed",
		})
	}
	return out
}

// uploadScreenshot POSTs a multipart screenshot for the given worker. Returns
// the response so callers can assert status/body.
func uploadScreenshot(
	t *testing.T,
	env *testenv.Env,
	token string,
	jobName, jobID string,
	specPath, relativePath string,
	body []byte,
) *http.Response {
	t.Helper()

	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	idFields := identity(nil)
	for _, k := range []string{"repository", "commit_sha", "gh_run_id", "name", "gh_run_attempt", "branch", "framework"} {
		if v, ok := idFields[k].(string); ok && v != "" {
			if err := w.WriteField(k, v); err != nil {
				t.Fatalf("write field %s: %v", k, err)
			}
		}
	}
	if err := w.WriteField("gh_job_name", jobName); err != nil {
		t.Fatalf("write gh_job_name: %v", err)
	}
	if err := w.WriteField("gh_job_id", jobID); err != nil {
		t.Fatalf("write gh_job_id: %v", err)
	}
	if err := w.WriteField("spec_path", specPath); err != nil {
		t.Fatalf("write spec_path: %v", err)
	}
	if err := w.WriteField("relative_path", relativePath); err != nil {
		t.Fatalf("write relative_path: %v", err)
	}
	part, err := w.CreateFormFile("file", "screenshot.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/orchestration/screenshots", buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// pollUntil repeatedly invokes check until it returns true or timeout elapses.
// Used in lieu of fixed sleeps so tests are robust against varying CI latency.
func pollUntil(t *testing.T, timeout, every time.Duration, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s after %s", what, timeout)
		}
		time.Sleep(every)
	}
}

// dispatchSeqOf extracts the dispatch_seq integer from a units[] entry.
func dispatchSeqOf(t *testing.T, unit map[string]any) int {
	t.Helper()
	return jsonNumberInt(t, unit["dispatch_seq"])
}

// unitIDOf extracts the unit_id from a units[] entry, validating UUID shape.
func unitIDOf(t *testing.T, unit map[string]any) string {
	t.Helper()
	id, ok := unit["unit_id"].(string)
	if !ok {
		t.Fatalf("unit_id missing or not string: %v", unit)
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("unit_id not a valid uuid: %v", err)
	}
	return id
}

// runUUID looks up the orchestration run's internal PK by composite identity.
func runUUID(t *testing.T, env *testenv.Env, id map[string]any) uuid.UUID {
	t.Helper()
	var out uuid.UUID
	err := env.Pool.QueryRow(context.Background(), `
		SELECT id FROM orchestration_runs
		 WHERE repository = $1 AND commit_sha = $2 AND gh_run_id = $3 AND name = $4 AND gh_run_attempt = $5
	`, id["repository"], id["commit_sha"], id["gh_run_id"], id["name"], id["gh_run_attempt"]).Scan(&out)
	if err != nil {
		t.Fatalf("lookup run uuid: %v", err)
	}
	return out
}

// errorCode pulls the project's standard error-code field from an error
// response body. The wire schema is {"error": "<CODE>", "message": "..."}
// (see internal/api/errors.go), so the JSON key for the code is "error".
func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	body := decodeJSON(t, resp)
	code, _ := body["error"].(string)
	return code
}

// fmtCounts formats a counts map for assertion error messages.
func fmtCounts(c map[string]int) string {
	return fmt.Sprintf("pending=%d leased=%d pass=%d fail=%d skipped=%d abandoned=%d retest=%d",
		c["pending"], c["leased"], c["completed_pass"], c["completed_fail"],
		c["completed_skipped"], c["abandoned"], c["retest_eligible"])
}
