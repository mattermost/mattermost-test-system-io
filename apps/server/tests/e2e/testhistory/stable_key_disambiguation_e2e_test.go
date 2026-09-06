//go:build e2e

// Cypress, Detox and Maestro build full_title from the describe/it chain
// only — never the spec file — unlike Playwright, whose root suite title is
// the file path itself. Two different spec files with an identically worded
// test therefore produce the same full_title, and without test_cases.file
// folded into stable_key's fallback (migration 28), they would collapse into
// one flakiness history: a real regression in one file would read as a
// flake because the sibling file's test kept passing.
//
// This ingests two real Cypress (mochawesome) reports — same fullTitle,
// different file — through the real upload path and proves both the key and
// the history it drives stay separate.
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

const collidingTitle = "renders the sidebar"

// mochawesomeReport is a minimal Cypress/mochawesome report: one root result
// with its tests inline, no nested suites — the shape that gives a plain
// fullTitle with no file or describe prefix at all.
func mochawesomeReport(file, status string) string {
	failed := `"fail": true, "err": {"message": "AssertionError: expected sidebar to be visible", "estack": "at Context.<anonymous> (` + file + `:12:3)"}`
	if status == "passed" {
		failed = `"pass": true`
	}
	return fmt.Sprintf(`{
  "stats": {"start": "2026-01-01T00:00:00.000Z"},
  "results": [
    {
      "title": "root",
      "file": %q,
      "fullFile": %q,
      "suites": [],
      "tests": [
        {"title": %q, "fullTitle": %q, "duration": 80, "state": %q, %s}
      ]
    }
  ]
}`, file, file, collidingTitle, collidingTitle, status, failed)
}

func ingestCypress(t *testing.T, env *testenv.Env, tok, commit, jobID, file, status string) {
	t.Helper()
	body := mochawesomeReport(file, status)

	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
		"repository":     ingestRepo,
		"framework":      "cypress",
		"name":           "cypress-" + jobID,
		"branch":         "master",
		"commit":         commit,
		"gh_run_id":      "run-" + jobID,
		"gh_run_attempt": "1",
		"gh_job_id":      jobID,
		"gh_job_name":    "cypress/" + jobID,
		"json_files":     []any{map[string]any{"path": "results.json", "size": len(body)}},
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

func stableKeyForFile(t *testing.T, env *testenv.Env, file string) string {
	t.Helper()
	var key string
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT stable_key FROM test_cases WHERE full_title = $1 AND file = $2 LIMIT 1`,
		collidingTitle, file).Scan(&key); err != nil {
		t.Fatalf("stable_key for %s: %v", file, err)
	}
	return key
}

func TestStableKey_DisambiguatesIdenticalTitlesAcrossFiles(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	const fileA = "cypress/e2e/sidebar_a.cy.js"
	const fileB = "cypress/e2e/sidebar_b.cy.js"

	// File A fails every run; file B passes every run. If the two collapsed
	// onto one stable_key, the merged series would show three fails and
	// three passes on a single key — a false flake — instead of two clean,
	// opposite series.
	for i := 0; i < 3; i++ {
		ingestCypress(t, env, tok, sha("e0", i), fmt.Sprintf("a-%d", i), fileA, "failed")
		ingestCypress(t, env, tok, sha("e1", i), fmt.Sprintf("b-%d", i), fileB, "passed")
	}

	keyA := stableKeyForFile(t, env, fileA)
	keyB := stableKeyForFile(t, env, fileB)

	if keyA == keyB {
		t.Fatalf("stable_key collided across files: both %q — %q and %q produced the same key from an identical fullTitle",
			keyA, fileA, fileB)
	}
	if keyA == collidingTitle || keyB == collidingTitle {
		t.Fatalf("stable_key was not disambiguated: keyA=%q keyB=%q, want each prefixed by its file", keyA, keyB)
	}

	q := func(key string) url.Values {
		v := url.Values{}
		v.Set("repo", "mattermost")
		v.Set("test_id", key)
		v.Set("branch", "master")
		return v
	}
	histA := getJSON(t, env, "/api/v1/tests/history?"+q(keyA).Encode())
	histB := getJSON(t, env, "/api/v1/tests/history?"+q(keyB).Encode())

	sa := histA["summary"].(map[string]any)
	sb := histB["summary"].(map[string]any)

	if sa["runs"].(float64) != 3 || sa["failed"].(float64) != 3 || sa["passed"].(float64) != 0 {
		t.Fatalf("file A history = %v, want 3 runs all failed — it must not have absorbed file B's passes", sa)
	}
	if sb["runs"].(float64) != 3 || sb["passed"].(float64) != 3 || sb["failed"].(float64) != 0 {
		t.Fatalf("file B history = %v, want 3 runs all passed — it must not have absorbed file A's failures", sb)
	}
}
