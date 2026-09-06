//go:build e2e

// Playwright stores every attempt of a retried test as 'flaky' — the failed
// attempt included — so a report group can be flaky with no 'failed' row at
// all. The history rollup used to count a 'flaky' row as a pass and only
// said "flaky" when a hard 'failed' row sat beside it, so a retry-survivor
// rolled up as a clean pass: evidence said flaky, history said passed, and
// an automation classifying from history never saw the flake.
package testhistory

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

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const flakyTitle = "MM-T7300 survives a retry"

// A Playwright report for a test that failed once and passed on retry, as
// the reporter emits it: test status "flaky", one result per attempt.
func flakyPlaywrightReport() string {
	return fmt.Sprintf(`{
  "config": {"projects": [{"name": "chrome"}]},
  "suites": [{
    "title": "channels/retry.spec.ts", "file": "channels/retry.spec.ts",
    "specs": [{"title": %q, "tests": [{"projectName": "chrome", "status": "flaky", "results": [
      {"status": "failed", "duration": 100, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z",
       "errors": [{"message": "Error: expect(locator).toBeVisible() failed", "stack": "at retry.spec.ts:9:5"}]},
      {"status": "passed", "duration": 100, "retry": 1, "startTime": "2026-01-01T00:00:01.000Z"}
    ]}]}]
  }]
}`, flakyTitle)
}

func ingestReport(t *testing.T, env *testenv.Env, tok, framework, name, commit, jobID, body string) {
	t.Helper()
	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
		"repository": ingestRepo, "framework": framework, "name": name, "branch": "master",
		"commit": commit, "gh_run_id": "run-" + jobID, "gh_run_attempt": "1",
		"gh_job_id": jobID, "gh_job_name": framework + "/" + jobID,
		"json_files": []any{map[string]any{"path": "results.json", "size": len(body)}},
	})
	if reg.StatusCode != http.StatusOK {
		t.Fatalf("register: status %d (body=%s)", reg.StatusCode, reg.Body)
	}
	var ids struct {
		ReportID string `json:"report_id"`
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(reg.Body, &ids); err != nil {
		t.Fatalf("register response: %v", err)
	}
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	part, err := w.CreateFormFile("files", "results.json")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := io.WriteString(part, body); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w.Close()
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/api/v1/reports/upload/%s/%s/json", env.ServerURL, ids.ReportID, ids.UploadID), buf)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: status %d", resp.StatusCode)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err := env.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM suites s JOIN reports r ON r.id = s.report_id WHERE r.id::text = $1`,
			ids.UploadID).Scan(&n); err != nil {
			t.Fatalf("count suites: %v", err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("extraction did not land within 5s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestHistory_ARetrySurvivorRollsUpAsFlakyNotPassed(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)
	ingestReport(t, env, tok, "playwright", ingestName, sha("f7", 0), "job-0", flakyPlaywrightReport())

	// What was stored: both attempts as 'flaky', no 'failed' row — the shape
	// that used to roll up as a pass.
	var statuses []string
	rows, err := env.Pool.Query(context.Background(),
		`SELECT status FROM test_cases WHERE stable_key = 'MM-T7300' ORDER BY ordinal`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		statuses = append(statuses, s)
	}
	rows.Close()
	if len(statuses) != 2 || statuses[0] != "flaky" || statuses[1] != "flaky" {
		t.Fatalf("stored statuses = %v, want [flaky flaky] (the fixture no longer reproduces the case)", statuses)
	}

	q := url.Values{}
	q.Set("repo", "mattermost")
	q.Set("test_id", "MM-T7300")
	q.Set("branch", "master")
	got := getJSON(t, env, "/api/v1/tests/history?"+q.Encode())
	entries := got["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if o := entries[0].(map[string]any)["outcome"]; o != "flaky" {
		t.Fatalf("outcome = %v, want flaky — a retry-survivor must not roll up as a clean pass", o)
	}
	s := got["summary"].(map[string]any)
	if s["flaky"].(float64) != 1 || s["passed"].(float64) != 0 || s["failure_rate"].(float64) != 1 {
		t.Fatalf("summary = %v, want flaky=1 passed=0 failure_rate=1", s)
	}
}
