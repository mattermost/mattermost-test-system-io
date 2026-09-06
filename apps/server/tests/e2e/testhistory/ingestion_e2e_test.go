//go:build e2e

// These tests go through REAL ingestion: a Playwright report is uploaded the
// way CI does it, and the two read endpoints are then asked about it.
//
// Seeding test_cases rows directly would bypass consolidate.go, and that is
// precisely how a whole class of bug stays invisible — the branch once shipped
// without writing external_test_id at ingest at all (only migration 27's
// one-time backfill set it), so every history series would have gone empty
// within a month of deploy while every seeded test stayed green.
//
// Two tests are in the report on purpose: one with an MM-T id, as
// mattermost/mattermost writes them, and one with a plain title, as
// mattermost/desktop and mattermost-mobile write all of theirs. Both must end
// up with a stable_key, and both must have history.
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
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	ingestRepo    = "mattermost/mattermost"
	ingestOwner   = "mattermost"
	ingestSubject = "repo:mattermost/mattermost:ref:refs/heads/master"
	ingestName    = "playwright-full-enterprise-master"
	ingestError   = "Error: expect(locator).toBeVisible() failed — Join Call"
	mmTitle       = "MM-T9801 channel switcher opens"
	plainTitle    = "sidebar collapses on narrow window"
)

// A minimal Playwright JSON report with one MM-T-titled test and one plain
// title, both with the same error. Shape mirrors what the reporter emits.
func playwrightReport(status string) string {
	result := fmt.Sprintf(`{"status": %q, "duration": 100, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z",
	   "errors": [{"message": %q}]}`, status, ingestError)
	if status == "passed" {
		result = fmt.Sprintf(`{"status": %q, "duration": 100, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z"}`, status)
	}
	return fmt.Sprintf(`{
  "config": {"projects": [{"name": "chrome"}]},
  "suites": [
    {
      "title": "channels/switcher.spec.ts",
      "file": "channels/switcher.spec.ts",
      "specs": [
        {"title": %q, "tests": [{"projectName": "chrome", "status": "unexpected", "results": [%s]}]},
        {"title": %q, "tests": [{"projectName": "chrome", "status": "unexpected", "results": [%s]}]}
      ]
    }
  ]
}`, mmTitle, result, plainTitle, result)
}

// ingest uploads one report as CI would and waits for extraction to land.
func ingest(t *testing.T, env *testenv.Env, tok, branch, commit, jobID, status string) {
	t.Helper()
	body := playwrightReport(status)

	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
		"repository":     ingestRepo,
		"framework":      "playwright",
		"name":           ingestName,
		"branch":         branch,
		"commit":         commit,
		"gh_run_id":      "run-" + jobID,
		"gh_run_attempt": "1",
		"gh_job_id":      jobID,
		"gh_job_name":    "playwright/" + jobID,
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

	// Extraction is asynchronous; wait for the suite rows to land.
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

func uploaderToken(t *testing.T, env *testenv.Env) string {
	t.Helper()
	env.InsertPolicy(t, "allow-history-ingest", 1, string(policy.RoleUploader), map[string]string{
		"repository_owner": ingestOwner,
	})
	return env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         ingestSubject,
		Audience:        "tsio",
		Repository:      ingestRepo,
		RepositoryOwner: ingestOwner,
		Workflow:        "e2e",
		Ref:             "refs/heads/master",
	})
}

func history(t *testing.T, env *testenv.Env, key, branch string) map[string]any {
	t.Helper()
	q := url.Values{}
	q.Set("repo", "mattermost")
	q.Set("test_id", key)
	if branch != "" {
		q.Set("branch", branch)
	}
	return getJSON(t, env, "/api/v1/tests/history?"+q.Encode())
}

func TestIngestion_WritesTheIdentityHistoryIsKeyedOn(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	ingest(t, env, tok, "master", sha("f0", 1), "job-1", "failed")

	// Both rows must carry a stable_key. The MM-T one must ALSO carry the
	// external_test_id, written at ingest — not by a migration that only runs
	// once.
	rows, err := env.Pool.Query(context.Background(), `
		SELECT title, external_test_id, stable_key FROM test_cases ORDER BY title`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	got := map[string][2]string{}
	for rows.Next() {
		var title, key string
		var ext *string
		if err := rows.Scan(&title, &ext, &key); err != nil {
			t.Fatalf("scan: %v", err)
		}
		e := ""
		if ext != nil {
			e = *ext
		}
		got[title] = [2]string{e, key}
	}
	if len(got) != 2 {
		t.Fatalf("ingested %d test cases, want 2: %v", len(got), got)
	}

	mm := got[mmTitle]
	if mm[0] != "MM-T9801" {
		t.Fatalf("MM-T test: external_test_id = %q, want MM-T9801 — the ingest write is missing again", mm[0])
	}
	if mm[1] != "MM-T9801" {
		t.Fatalf("MM-T test: stable_key = %q, want the MM-T id to win", mm[1])
	}

	plain := got[plainTitle]
	if plain[0] != "" {
		t.Fatalf("plain test: external_test_id = %q, want none", plain[0])
	}
	if plain[1] == "" {
		t.Fatal("plain test has no stable_key — desktop and mobile would have no history")
	}
}

func TestHistory_SpansBranchesAndPRsAndSummarisesTheStreak(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	// Master: pass, pass, then three failures. A PR branch: one failure.
	ingest(t, env, tok, "master", sha("a0", 0), "job-0", "passed")
	ingest(t, env, tok, "master", sha("a0", 1), "job-1", "passed")
	for i := 2; i < 5; i++ {
		ingest(t, env, tok, "master", sha("a0", i), fmt.Sprintf("job-%d", i), "failed")
	}
	ingest(t, env, tok, "pr-4242", sha("b0", 0), "job-pr", "failed")

	// Across branches: six entries, PR included. This is the read the PR
	// automation makes to see whether master is already red.
	all := history(t, env, "MM-T9801", "")
	if n := len(all["entries"].([]any)); n != 6 {
		t.Fatalf("entries across branches = %d, want 6", n)
	}

	// Master only: the summary names the streak boundaries the master
	// automation uses as its git-log range.
	m := history(t, env, "MM-T9801", "master")
	entries := m["entries"].([]any)
	if len(entries) != 5 {
		t.Fatalf("master entries = %d, want 5", len(entries))
	}
	if b := entries[0].(map[string]any)["branch"]; b != "master" {
		t.Fatalf("newest entry branch = %v, want master", b)
	}
	s := m["summary"].(map[string]any)
	if s["runs"].(float64) != 5 || s["failed"].(float64) != 3 || s["passed"].(float64) != 2 {
		t.Fatalf("summary = %v", s)
	}
	if s["failing_since_commit"] != sha("a0", 2) {
		t.Fatalf("failing_since_commit = %v, want %s (oldest commit of the current streak)", s["failing_since_commit"], sha("a0", 2))
	}
	if s["last_pass_commit"] != sha("a0", 1) {
		t.Fatalf("last_pass_commit = %v, want %s", s["last_pass_commit"], sha("a0", 1))
	}

	// A plain-titled test has the same history under its stable_key — which
	// for Playwright is the file-prefixed full title, not the bare title, so
	// two files with the same test name keep separate series. Read it back
	// the way the automation does: from the stored row, never reconstructed.
	var plainKey string
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT stable_key FROM test_cases WHERE title = $1 LIMIT 1`, plainTitle).Scan(&plainKey); err != nil {
		t.Fatalf("plain stable_key: %v", err)
	}
	if plainKey == plainTitle || !strings.Contains(plainKey, plainTitle) {
		t.Fatalf("plain stable_key = %q, want the file-prefixed full title", plainKey)
	}
	p := history(t, env, plainKey, "master")
	if ps := p["summary"].(map[string]any); ps["runs"].(float64) != 5 || ps["failed"].(float64) != 3 {
		t.Fatalf("plain-title summary = %v — desktop and mobile would have no history", ps)
	}
}

func TestEvidence_ReturnsErrorAndStableKeyPerFailureGroupedByCause(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)
	ingest(t, env, tok, "master", sha("c0", 0), "job-0", "failed")

	q := url.Values{}
	q.Set("repository", "mattermost")
	q.Set("commit_sha", sha("c0", 0))
	q.Set("gh_run_id", "run-job-0")
	q.Set("name", ingestName)
	got := getJSON(t, env, "/api/v1/tests/evidence?"+q.Encode())

	if got["failure_count"].(float64) != 2 || got["cluster_count"].(float64) != 1 || got["truncated"] != false {
		t.Fatalf("counts = failure_count %v cluster_count %v truncated %v", got["failure_count"], got["cluster_count"], got["truncated"])
	}
	c := got["clusters"].([]any)[0].(map[string]any)
	if c["member_count"].(float64) != 2 {
		t.Fatalf("member_count = %v, want 2 (same error, one cause)", c["member_count"])
	}
	rep := c["representative"].(map[string]any)
	msg, _ := rep["error_message"].(string)
	if !strings.Contains(msg, "toBeVisible") {
		t.Fatalf("representative error_message = %q, want the Playwright error", msg)
	}
	if rep["stable_key"] == "" || rep["stable_key"] == nil {
		t.Fatal("representative has no stable_key")
	}
	keys := map[string]bool{}
	for _, m := range c["members"].([]any) {
		k, _ := m.(map[string]any)["stable_key"].(string)
		if k == "" {
			t.Fatalf("member without stable_key: %v", m)
		}
		keys[k] = true
	}
	if !keys["MM-T9801"] {
		t.Fatalf("members = %v, want the MM-T id as one key", keys)
	}
	delete(keys, "MM-T9801")
	for k := range keys {
		if !strings.Contains(k, plainTitle) {
			t.Fatalf("plain member key = %q, want it to carry the title", k)
		}
	}
}

func TestEvidence_UnknownRunIs404(t *testing.T) {
	env := testenv.Start(t)
	resp, err := http.Get(env.ServerURL + "/api/v1/tests/evidence?repository=mattermost&commit_sha=deadbeef&gh_run_id=1&name=x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
