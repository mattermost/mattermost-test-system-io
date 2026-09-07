//go:build e2e

// A retry-survivor — one failed attempt, one passing retry — must roll up as
// flaky, and must do so identically whichever framework reported it. Both CI
// configs retry once (playwright.config.ts `retries: isCI ? 1 : 0`,
// cypress.config.ts `retries.runMode: 1`), so this is the shape of every
// retried test in production.
//
// The two frameworks used to disagree about what was stored, which is what
// these tests pin:
//
//	Playwright  wrote one row per attempt but stamped every one of them with
//	            the test's rolled-up status, so the stored shape was
//	            [flaky, flaky] with no 'failed' row anywhere. The rollup
//	            reached "flaky" through a bool_or(status = 'flaky') branch
//	            that existed only to read that shape. Rows now carry each
//	            attempt's own status, [failed, passed], and reach the same
//	            outcome through ever_passed AND ever_failed. Same verdict,
//	            recoverable evidence.
//
//	Cypress     wrote a single final-state row with retry_count hardcoded to
//	            0, so the same run stored one 'passed' row and rolled up as a
//	            CLEAN PASS. That is the before/after difference: identical
//	            runs got opposite outcomes depending on which framework ran
//	            them, and any rate computed across both was meaningless.
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

// The stored key: the MM-T id under the Playwright project prefix that keeps
// the browsers' histories apart.
const flakyKey = "chrome :: MM-T7300"

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

	// What is stored: one row per attempt, each with its OWN status. The
	// failing attempt is recoverable, which it was not when every attempt was
	// stamped with the test's rolled-up 'flaky'.
	var statuses []string
	rows, err := env.Pool.Query(context.Background(),
		`SELECT status FROM test_cases WHERE stable_key = $1 ORDER BY ordinal`, flakyKey)
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
	if len(statuses) != 2 || statuses[0] != "failed" || statuses[1] != "passed" {
		t.Fatalf("stored statuses = %v, want [failed passed] — one row per attempt, "+
			"each carrying its own result", statuses)
	}

	// The run-level rollup, repeated across both attempt rows so a consumer
	// reads run-level truth off either one without re-grouping.
	var attempts, attemptsFailed int
	var runFailed bool
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT attempts, attempts_failed, run_failed FROM test_cases
		 WHERE stable_key = $1 ORDER BY ordinal LIMIT 1`, flakyKey).
		Scan(&attempts, &attemptsFailed, &runFailed); err != nil {
		t.Fatalf("rollup columns: %v", err)
	}
	if attempts != 2 || attemptsFailed != 1 || runFailed {
		t.Fatalf("rollup = (attempts=%d, attempts_failed=%d, run_failed=%v), want (2, 1, false) — "+
			"a run with a surviving attempt is a flake, not a failure",
			attempts, attemptsFailed, runFailed)
	}

	q := url.Values{}
	q.Set("repo", "mattermost")
	q.Set("test_id", flakyKey)
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
