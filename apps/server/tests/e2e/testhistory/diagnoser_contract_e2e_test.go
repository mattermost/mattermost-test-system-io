//go:build e2e

// The contract the PR Diagnoser reads.
//
// The diagnoser is a Cursor automation, not code in this repository, so nothing
// here can be caught by a compiler or by its own tests. It reads exactly two
// endpoints and a fixed set of fields from each, and does its own
// classification from them. This file pins that field set, so a rename or a
// dropped key breaks a test here rather than silently breaking an automation
// nobody can grep.
//
// Every field asserted below is one the prompt names. When the prompt changes,
// change this file with it.
package testhistory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// The diagnoser's step 1 and step 2 reads, verbatim from the prompt:
//
//	GET /tests/evidence?repository=&commit_sha=&gh_run_id=&name=&gh_run_attempt=
//	GET /tests/history?repo=mattermost&test_id=<stable_key>&window=30d&limit=100
const (
	diagRepo  = "mattermost/mattermost"
	diagTitle = "MM-T7400 opens the channel switcher"
	diagKey   = "chrome :: MM-T7400"
)

func diagReport(status string) string {
	result := `{"status": "passed", "duration": 90, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z"}`
	outcome := "expected"
	if status != "passed" {
		result = `{"status": "failed", "duration": 900, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z",
		   "errors": [{"message": "Error: expect(locator).toBeVisible() failed", "stack": "at switcher.spec.ts:14:5"}]}`
		outcome = "unexpected"
	}
	return fmt.Sprintf(`{
  "config": {"projects": [{"name": "chrome"}]},
  "suites": [{
    "title": "channels/switcher.spec.ts", "file": "channels/switcher.spec.ts",
    "specs": [{"title": %q, "tests": [{"projectName": "chrome", "status": %q, "results": [%s]}]}]
  }]
}`, diagTitle, outcome, result)
}

// ingestDiag uploads one run, optionally as a pull request.
func ingestDiag(t *testing.T, env *testenv.Env, tok, branch, commit, runID, status string, pr *int) {
	t.Helper()
	body := diagReport(status)
	identity := map[string]any{
		"repository": diagRepo, "framework": "playwright", "name": diagJobName,
		"branch": branch, "commit": commit, "gh_run_id": runID, "gh_run_attempt": "1",
		"gh_job_id": runID, "gh_job_name": "playwright/" + runID,
		"total_reports_expected": 1,
		"json_files":             []any{map[string]any{"path": "results.json", "size": len(body)}},
	}
	if pr != nil {
		identity["gh_pr_number"] = *pr
	}
	uploadDiagReport(t, env, tok, identity, body)
}

const diagJobName = "playwright-full-enterprise-master"

// TestDiagnoserContract_EvidenceCarriesEveryFieldStep1Reads pins the evidence
// response against the diagnoser's step 1.
func TestDiagnoserContract_EvidenceCarriesEveryFieldStep1Reads(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	commit := sha("dg", 0)
	pr := 38356
	ingestDiag(t, env, tok, "ci/some-branch", commit, "diag-run-1", "failed", &pr)

	q := url.Values{}
	q.Set("repository", diagRepo)
	q.Set("commit_sha", commit)
	q.Set("gh_run_id", "diag-run-1")
	q.Set("name", diagJobName)
	q.Set("gh_run_attempt", "1")
	got := getJSON(t, env, "/api/v1/tests/evidence?"+q.Encode())

	// Step 1: "Read failure_count, cluster_count, truncated, and clusters[]."
	for _, field := range []string{"failure_count", "cluster_count", "truncated", "clusters", "group"} {
		if _, ok := got[field]; !ok {
			t.Fatalf("evidence response has no %q — the diagnoser reads it in step 1", field)
		}
	}
	if got["failure_count"].(float64) != 1 {
		t.Errorf("failure_count = %v, want 1", got["failure_count"])
	}
	// "If truncated is true the pack is incomplete." It must be a real boolean,
	// not absent: the diagnoser refuses to label on a truncated pack, and a
	// missing field would read as false.
	if _, ok := got["truncated"].(bool); !ok {
		t.Fatalf("truncated = %v, want a boolean — the diagnoser must not label on a partial list",
			got["truncated"])
	}

	clusters := got["clusters"].([]any)
	if len(clusters) != 1 {
		t.Fatalf("clusters = %d, want 1", len(clusters))
	}
	c := clusters[0].(map[string]any)

	// "Its representative carries error_message, error_stack, screenshots[].url
	// and stable_key; every member carries its own stable_key."
	rep, ok := c["representative"].(map[string]any)
	if !ok {
		t.Fatalf("cluster has no representative: %v", c)
	}
	for _, field := range []string{"error_message", "error_stack", "stable_key", "screenshots"} {
		if _, ok := rep[field]; !ok {
			t.Errorf("representative has no %q — the diagnoser reads it in step 1", field)
		}
	}
	if rep["stable_key"] != diagKey {
		t.Errorf("representative stable_key = %v, want %q", rep["stable_key"], diagKey)
	}
	members, ok := c["members"].([]any)
	if !ok || len(members) == 0 {
		t.Fatalf("cluster has no members: %v", c["members"])
	}
	for i, m := range members {
		if k, _ := m.(map[string]any)["stable_key"].(string); k == "" {
			t.Errorf("member %d has no stable_key", i)
		}
	}
}

// TestDiagnoserContract_EnvironmentMetadataIsReadableForReproduction pins
// step 3's input. The diagnoser reads group.environment_metadata to configure a
// server matching the run — "a reproduction under a different configuration
// than the failure ran under proves nothing".
func TestDiagnoserContract_EnvironmentMetadataIsReadableForReproduction(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	commit := sha("dg", 1)
	body := diagReport("failed")
	// The shape the mattermost templates now emit, built with jq so an empty
	// input cannot take the document down.
	metadata := map[string]any{
		"server_edition":          "enterprise",
		"server_image":            "mattermostdevelopment/mattermost-enterprise-edition:master",
		"server_image_digest":     "sha256:0123456789abcdef",
		"server_image_repo":       "mattermostdevelopment",
		"server_image_tag":        "master",
		"license_secret_present":  true,
		"playwright_retries":      1,
		"testcontainers":          true,
		"testcontainers_services": "openldap,keycloak",
	}
	identity := map[string]any{
		"repository": diagRepo, "framework": "playwright", "name": diagJobName,
		"branch": "master", "commit": commit, "gh_run_id": "diag-run-2", "gh_run_attempt": "1",
		"gh_job_id": "diag-run-2", "gh_job_name": "playwright/diag-run-2",
		"environment_metadata": metadata,
		"json_files":           []any{map[string]any{"path": "results.json", "size": len(body)}},
	}
	uploadDiagReport(t, env, tok, identity, body)

	q := url.Values{}
	q.Set("repository", diagRepo)
	q.Set("commit_sha", commit)
	q.Set("gh_run_id", "diag-run-2")
	q.Set("name", diagJobName)
	got := getJSON(t, env, "/api/v1/tests/evidence?"+q.Encode())

	group := got["group"].(map[string]any)
	raw, ok := group["environment_metadata"]
	if !ok {
		t.Fatal("group has no environment_metadata — step 3 cannot match the run's configuration, " +
			"and a reproduction under a different configuration proves nothing")
	}
	meta, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("environment_metadata is %T, want an object the diagnoser can read fields from", raw)
	}
	// The fields step 3 names, so the reproduction runs against the same binary.
	for _, field := range []string{"server_image_repo", "server_image_tag", "server_edition"} {
		if _, ok := meta[field]; !ok {
			t.Errorf("environment_metadata has no %q — step 3 derives the image from it", field)
		}
	}
	// The digest is what makes "verified against a real server" sound:
	// server_image_tag is very often the mutable "master".
	if meta["server_image_digest"] == nil || meta["server_image_digest"] == "" {
		t.Error("no server_image_digest — a reproduction can then run against a different " +
			"binary than the one that failed")
	}
}

// TestDiagnoserContract_AMissingReportIs404 — step 1: "A 404 means the report
// never landed: comment that and stop." It must not be a 200 with an empty
// pack, which the diagnoser would read as "nothing failed".
func TestDiagnoserContract_AMissingReportIs404(t *testing.T) {
	env := testenv.Start(t)
	q := url.Values{}
	q.Set("repository", diagRepo)
	q.Set("commit_sha", sha("zz", 9))
	q.Set("gh_run_id", "never-happened")
	q.Set("name", diagJobName)
	resp, err := http.Get(env.ServerURL + "/api/v1/tests/evidence?" + q.Encode())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — a 200 with an empty pack reads as 'nothing failed'",
			resp.StatusCode)
	}
}

// TestDiagnoserContract_HistorySummaryCarriesEveryFieldStep2Reads pins the
// history response against step 2's classification rules.
func TestDiagnoserContract_HistorySummaryCarriesEveryFieldStep2Reads(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	// Six master runs, mixed, then two other PRs failing it.
	for i, s := range []string{"passed", "failed", "passed", "failed", "passed", "failed"} {
		ingestDiag(t, env, tok, "master", sha("dh", i), fmt.Sprintf("diag-master-%d", i), s, nil)
	}
	for i, prNum := range []int{9001, 9002} {
		pr := prNum
		ingestDiag(t, env, tok, fmt.Sprintf("pr-%d", prNum), sha("dq", i),
			fmt.Sprintf("diag-otherpr-%d", i), "failed", &pr)
	}

	q := url.Values{}
	q.Set("repo", "mattermost")
	q.Set("test_id", diagKey)
	q.Set("window", "30d")
	q.Set("limit", "100")
	got := getJSON(t, env, "/api/v1/tests/history?"+q.Encode())

	summary, ok := got["summary"].(map[string]any)
	if !ok {
		t.Fatal("history response has no summary")
	}
	// Step 2 names every one of these.
	for _, field := range []string{"runs", "passed", "failed", "flaky", "flips", "failure_rate"} {
		if _, ok := summary[field]; !ok {
			t.Errorf("summary has no %q — step 2's classification reads it", field)
		}
	}
	// last_pass_commit and failing_since_commit are omitempty, so assert them
	// where they must be present rather than merely present-always.
	if _, ok := summary["last_pass_commit"]; !ok {
		t.Error("summary has no last_pass_commit despite passing runs in the window")
	}

	// Entries carry branch and gh_pr_number: step 2 counts failures on OTHER
	// PRs from these, for its KNOWN_FLAKE rule.
	entries := got["entries"].([]any)
	if len(entries) != 8 {
		t.Fatalf("entries = %d, want 8 (6 master + 2 other PRs)", len(entries))
	}
	otherPRs := map[float64]bool{}
	sawBranch := false
	for _, e := range entries {
		m := e.(map[string]any)
		if _, ok := m["branch"]; ok {
			sawBranch = true
		}
		if n, ok := m["gh_pr_number"].(float64); ok {
			otherPRs[n] = true
		}
	}
	if !sawBranch {
		t.Error("entries carry no branch — step 2 needs it to separate master from PRs")
	}
	if len(otherPRs) != 2 {
		t.Errorf("distinct gh_pr_number values = %d, want 2 — step 2's KNOWN_FLAKE rule counts "+
			"failures on other PRs from this", len(otherPRs))
	}
}

// TestDiagnoserContract_BranchMasterNarrowsToTheBaseline — step 2 uses
// "&branch=master to see master alone" for both MASTER_BROKEN and KNOWN_FLAKE.
// If the filter leaked PR runs in, a test failing on many PRs would look
// broken on master and every PR meeting it would be waved through.
func TestDiagnoserContract_BranchMasterNarrowsToTheBaseline(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	for i := 0; i < 5; i++ {
		ingestDiag(t, env, tok, "master", sha("db", i), fmt.Sprintf("diag-m-%d", i), "passed", nil)
	}
	for i, prNum := range []int{9101, 9102, 9103} {
		pr := prNum
		ingestDiag(t, env, tok, fmt.Sprintf("pr-%d", prNum), sha("dp", i),
			fmt.Sprintf("diag-p-%d", i), "failed", &pr)
	}
	// A fork can legitimately open its master branch as a pull request.
	// Its failures must not become evidence about the product's master.
	forkPR := 9104
	ingestDiag(t, env, tok, "master", sha("dp", 4), "diag-fork-master", "failed", &forkPR)

	base := url.Values{}
	base.Set("repo", "mattermost")
	base.Set("test_id", diagKey)
	base.Set("branch", "master")
	got := getJSON(t, env, "/api/v1/tests/history?"+base.Encode())

	s := got["summary"].(map[string]any)
	if s["runs"].(float64) != 5 || s["failed"].(float64) != 0 || s["flaky"].(float64) != 0 {
		t.Fatalf("master-only summary = %v, want 5 runs all clean — the PR failures must not "+
			"leak into the baseline", s)
	}
	// This is the shape step 2 calls NEW_TO_THIS_PR: clean on master, failing
	// on the PR. The diagnoser must not label.
	if _, ok := s["failing_since_commit"]; ok {
		t.Error("failing_since_commit is set on a clean master history — step 2 would read " +
			"that as MASTER_BROKEN and wave the PR through")
	}
}

// TestDiagnoserContract_FailingSinceCommitIsAFloorNotAnAnswer is the trap in
// step 2's MASTER_BROKEN rule and step 6's comment template, which prints
// failing_since_commit "so the author sees it is upstream".
//
// When the failing streak reaches the oldest run in the window,
// failing_since_commit names the oldest commit the window HAS, not the commit
// that broke it. last_pass_commit being absent is the only signal that the two
// are different, and an automation that prints the first without checking the
// second sends the author to the wrong commit.
func TestDiagnoserContract_FailingSinceCommitIsAFloorNotAnAnswer(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	// Every master run in the window failed: the streak runs off the edge.
	for i := 0; i < 6; i++ {
		ingestDiag(t, env, tok, "master", sha("df", i), fmt.Sprintf("diag-f-%d", i), "failed", nil)
	}
	q := url.Values{}
	q.Set("repo", "mattermost")
	q.Set("test_id", diagKey)
	q.Set("branch", "master")
	got := getJSON(t, env, "/api/v1/tests/history?"+q.Encode())
	s := got["summary"].(map[string]any)

	if _, ok := s["failing_since_commit"]; !ok {
		t.Fatal("failing_since_commit absent on an unbroken failing streak")
	}
	if _, ok := s["last_pass_commit"]; ok {
		t.Fatalf("last_pass_commit = %v, want it ABSENT — its absence is the only signal that "+
			"failing_since_commit is a floor rather than the breaking commit",
			s["last_pass_commit"])
	}
}

// uploadDiagReport is the upload half, taking a full identity so the
// environment-metadata case can supply one.
func uploadDiagReport(t *testing.T, env *testenv.Env, tok string, identity map[string]any, body string) {
	t.Helper()
	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, identity)
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
	postMultipartJSON(t, env, tok, ids.ReportID, ids.UploadID, body)

	// Extraction is asynchronous: the upload returns 200 before the rows land.
	// Without this wait the reads below race it, and the test fails claiming
	// the endpoint returned nothing when it simply had nothing yet.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err := env.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM test_cases tc
			 JOIN suites s ON s.id = tc.suite_id
			 JOIN reports r ON r.id = s.report_id
			 WHERE r.id::text = $1`, ids.UploadID).Scan(&n); err != nil {
			t.Fatalf("count test_cases: %v", err)
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

// TestDiagnoserContract_AnUnknownAPIPathIs404JSONNotTheDashboard is the bug the
// Cursor automation hit in production on mattermost#38356:
//
//	"The prescribed evidence endpoint returned the Test System IO dashboard
//	 HTML (200 text/html) instead of JSON for the uploaded report identity."
//
// The endpoint was not deployed on that server, and chi served the SPA for it.
// chi's Mux.NotFound propagates the handler into every sub-router whose own
// notFoundHandler is nil (mux.go: `if subMux.notFoundHandler == nil`), so
// registering the web UI as the root 404 handler silently captured the whole
// /api/v1 subtree — including paths that simply do not exist on this build.
//
// A client cannot tell "this route is not deployed" from "here is your answer"
// when both are 200 with a body. The automation's step 1 says a 404 means the
// report never landed; it has no branch for HTML, and could not have.
//
// Any unmatched /api/v1 path must be a JSON 404.
func TestDiagnoserContract_AnUnknownAPIPathIs404JSONNotTheDashboard(t *testing.T) {
	env := testenv.Start(t)

	for _, path := range []string{
		"/api/v1/tests/does-not-exist",
		"/api/v1/triage/does-not-exist",
		"/api/v1/nope",
	} {
		resp, err := http.Get(env.ServerURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		ct := resp.Header.Get("Content-Type")
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404 — a client cannot distinguish "+
				"'not deployed' from an answer when both are 200", path, resp.StatusCode)
		}
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s: Content-Type %q, want application/json (body began %q)",
				path, ct, string(body[:min(60, len(body))]))
		}
		var envelope map[string]any
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Errorf("GET %s: body is not JSON: %v", path, err)
			continue
		}
		if envelope["error"] == nil {
			t.Errorf("GET %s: JSON carries no error code: %v", path, envelope)
		}
	}
}

// TestDiagnoserContract_NonAPIPathsStillServeTheDashboard guards the other
// direction: the fix above must not break client-side routing, which depends on
// unknown non-API paths falling through to index.html.
func TestDiagnoserContract_NonAPIPathsStillServeTheDashboard(t *testing.T) {
	env := testenv.Start(t)
	resp, err := http.Get(env.ServerURL + "/reports/some/deep/link")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Asserting 200 text/html here would only hold where the web bundle is
	// actually built; a development checkout embeds an empty dist and the
	// handler answers 500. The invariant that matters in both is narrower and
	// is the one the fix could break: a non-API path must NOT be claimed by
	// the API subtree's JSON 404, because client-side routing depends on
	// reaching the web handler at all.
	ct := resp.Header.Get("Content-Type")
	body, _ := io.ReadAll(resp.Body)
	if strings.HasPrefix(ct, "application/json") {
		var envelope map[string]any
		if json.Unmarshal(body, &envelope) == nil && envelope["error"] == "NOT_FOUND" {
			t.Fatalf("a non-API deep link got the API's JSON 404 (%s) — the /api/v1 "+
				"NotFound handler has escaped its subtree and client-side routing is broken",
				string(body))
		}
	}
	t.Logf("non-API deep link reached the web handler: status=%d content-type=%q",
		resp.StatusCode, ct)
}
