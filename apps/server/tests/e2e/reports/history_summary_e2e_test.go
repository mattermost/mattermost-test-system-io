//go:build e2e
// +build e2e

package reportse2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	summaryRepo  = "mattermost/mattermost"
	summaryLane  = "playwright-full-enterprise-master"
	summaryFips  = "playwright-full-fips-master"
	summaryFile  = "specs/functional/channels/classification.spec.ts"
	summarySuite = "Channel Classification"
)

// summaryRun is one report group with one shard and one suite of tests.
type summaryRun struct {
	branch, name, sha, runID, groupStatus string
	at                                    time.Time
	tests                                 map[string][]string // test title → statuses of its tries
}

func sha(n int) string { return fmt.Sprintf("%07x%033d", n, 0) }

func seedRun(t *testing.T, env *testenv.Env, r summaryRun) {
	t.Helper()
	ctx := context.Background()
	if r.groupStatus == "" {
		r.groupStatus = "completed"
	}
	if r.branch == "" {
		r.branch = "master"
	}
	var gid, rid, sid string
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt, status, created_at)
		VALUES ('playwright', $1, $2, $3, $4, $5, '1', $6, $7) RETURNING id`,
		r.name, summaryRepo, r.branch, r.sha, r.runID, r.groupStatus, r.at).Scan(&gid); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `INSERT INTO reports (report_group_id, name, status) VALUES ($1, 'chrome-1', 'complete') RETURNING id`, gid).Scan(&rid); err != nil {
		t.Fatalf("insert report: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `INSERT INTO suites (report_id, title, file, ordinal) VALUES ($1, $2, $3, 0) RETURNING id`, rid, summarySuite, summaryFile).Scan(&sid); err != nil {
		t.Fatalf("insert suite: %v", err)
	}
	ordinal := 0
	for title, statuses := range r.tests {
		for retry, status := range statuses {
			if _, err := env.Pool.Exec(ctx, `
				INSERT INTO test_cases (suite_id, title, full_title, status, retry_count, ordinal) VALUES ($1, $2, $3, $4, $5, $6)`,
				sid, title, summarySuite+" > "+title, status, retry, ordinal); err != nil {
				t.Fatalf("insert case: %v", err)
			}
			ordinal++
		}
	}
}

type summaryResponse struct {
	Run struct {
		CommitSHA string `json:"commit_sha"`
	} `json:"run"`
	LaneRuns   int  `json:"lane_runs"`
	TotalTests int  `json:"total_tests"`
	Page       int  `json:"page"`
	HasMore    bool `json:"has_more"`
	Tests      []struct {
		File                       string `json:"file"`
		FullTitle                  string `json:"full_title"`
		StatusInRun                string `json:"status_in_run"`
		EarlierConsecutiveFailures int    `json:"earlier_consecutive_failures"`
		FirstFailedCommit          string `json:"first_failed_commit"`
		LastNotFailed              *struct {
			CommitSHA string `json:"commit_sha"`
			Status    string `json:"status"`
		} `json:"last_not_failed"`
		Window struct {
			Runs, Passed, Failed, Flaky, Skipped, Absent int
		} `json:"window"`
		SameCommit []struct {
			Name    string `json:"name"`
			GHRunID string `json:"gh_run_id"`
			Status  string `json:"status"`
		} `json:"same_commit"`
	} `json:"tests"`
}

func getSummary(t *testing.T, env *testenv.Env, key string, params url.Values) (int, summaryResponse, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.ServerURL+"/api/v1/reports/history/summary?"+params.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out summaryResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode, out, resp.Header
}

func summaryParams(sha, runID string) url.Values {
	return url.Values{
		"repository": {"mattermost"}, "branch": {"master"}, "name": {summaryLane},
		"commit": {sha[:7]}, "gh_run_id": {runID}, "gh_run_attempt": {"1"},
		"compare_names": {summaryFips},
	}
}

func TestHistorySummaryFactsAsOfTheRun(t *testing.T) {
	env := testenv.Start(t)
	viewer := env.IssueAPIKeyWithRole(t, "alpha-ai", policy.RoleViewer)
	now := time.Now().UTC().Truncate(time.Second)
	at := func(h int) time.Time { return now.Add(time.Duration(-h) * time.Hour) }
	const dropdown, banner, stable = "dropdown shows levels", "banner text", "creates a channel"

	// The lane, oldest first: c1 passed, c2 failed, c3 didn't run the test, c4 failed, then the run c5.
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(1), runID: "r1", at: at(50), tests: map[string][]string{dropdown: {"passed"}, stable: {"passed"}}})
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(2), runID: "r2", at: at(40), tests: map[string][]string{dropdown: {"failed", "failed"}, stable: {"passed"}}})
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(3), runID: "r3", at: at(30), tests: map[string][]string{stable: {"passed"}}})
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(4), runID: "r4", at: at(20), tests: map[string][]string{dropdown: {"failed", "failed"}, stable: {"passed"}}})
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(5), runID: "r5", at: at(10), tests: map[string][]string{
		dropdown: {"failed", "failed"}, banner: {"failed", "passed"}, stable: {"passed"},
	}})
	// The same commit: a re-run of the lane that passed, and the other edition that failed.
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(5), runID: "r6", at: at(9), tests: map[string][]string{dropdown: {"passed"}}})
	seedRun(t, env, summaryRun{name: summaryFips, sha: sha(5), runID: "r5", at: at(10), tests: map[string][]string{dropdown: {"failed", "failed"}}})
	// Noise that must not count: a later run, another branch, another lane, a group still uploading.
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(7), runID: "r7", at: at(1), tests: map[string][]string{dropdown: {"passed"}}})
	seedRun(t, env, summaryRun{branch: "release-12.0", name: summaryLane, sha: sha(8), runID: "r8", at: at(15), tests: map[string][]string{dropdown: {"passed"}}})
	seedRun(t, env, summaryRun{name: "cypress-full-enterprise-master", sha: sha(9), runID: "r9", at: at(15), tests: map[string][]string{dropdown: {"passed"}}})
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(10), runID: "r10", groupStatus: "in_progress", at: at(15), tests: map[string][]string{dropdown: {"passed"}}})

	code, out, header := getSummary(t, env, viewer, summaryParams(sha(5), "r5"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if header.Get("Cache-Control") != "private, max-age=60" {
		t.Errorf("Cache-Control = %q", header.Get("Cache-Control"))
	}
	if out.Run.CommitSHA != sha(5) || out.TotalTests != 2 || len(out.Tests) != 2 || out.LaneRuns != 4 {
		t.Fatalf("run %s, %d of %d tests, %d lane runs; want 2 failing tests and the 4 earlier lane runs", out.Run.CommitSHA, len(out.Tests), out.TotalTests, out.LaneRuns)
	}
	// Sorted by title: banner before dropdown.
	b, d := out.Tests[0], out.Tests[1]
	if b.FullTitle != summarySuite+" › "+banner || b.StatusInRun != "flaky" || b.File != summaryFile {
		t.Errorf("banner = %+v; want flaky in the run", b)
	}
	if d.StatusInRun != "failed" || d.EarlierConsecutiveFailures != 2 || d.FirstFailedCommit != sha(2) {
		t.Errorf("dropdown: %s, %d earlier failures from %s; want failed, 2 from c2", d.StatusInRun, d.EarlierConsecutiveFailures, d.FirstFailedCommit)
	}
	if d.LastNotFailed == nil || d.LastNotFailed.CommitSHA != sha(1) || d.LastNotFailed.Status != "passed" {
		t.Errorf("dropdown last not failed = %+v; want c1 passed", d.LastNotFailed)
	}
	if w := d.Window; w.Runs != 4 || w.Failed != 2 || w.Passed != 1 || w.Absent != 1 {
		t.Errorf("dropdown window = %+v; want 4 runs: 2 failed, 1 passed, 1 absent", w)
	}
	same := map[string]string{}
	for _, r := range d.SameCommit {
		same[r.Name+"/"+r.GHRunID] = r.Status
	}
	if len(same) != 2 || same[summaryLane+"/r6"] != "passed" || same[summaryFips+"/r5"] != "failed" {
		t.Errorf("dropdown same commit = %v; want the lane's re-run passed and fips failed", same)
	}
}

func TestHistorySummaryAccess(t *testing.T) {
	env := testenv.Start(t)
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(1), runID: "r1", at: time.Now(), tests: map[string][]string{"t": {"failed"}}})
	params := summaryParams(sha(1), "r1")

	if code, _, _ := getSummary(t, env, "", params); code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", code)
	}
	if code, _, _ := getSummary(t, env, env.IssueAPIKey(t, "ci"), params); code != http.StatusForbidden {
		t.Errorf("CI uploader key: status = %d, want 403", code)
	}
	for _, role := range []policy.Role{policy.RoleViewer, policy.RoleEditor, policy.RoleAdmin} {
		if code, _, _ := getSummary(t, env, env.IssueAPIKeyWithRole(t, string(role), role), params); code != http.StatusOK {
			t.Errorf("%s key: status = %d, want 200", role, code)
		}
	}
}

func TestHistorySummaryErrors(t *testing.T) {
	env := testenv.Start(t)
	viewer := env.IssueAPIKeyWithRole(t, "alpha-ai", policy.RoleViewer)

	if code, _, _ := getSummary(t, env, viewer, summaryParams(sha(42), "nope")); code != http.StatusNotFound {
		t.Errorf("unknown run: status = %d, want 404", code)
	}
	bad := summaryParams(sha(1), "r1")
	bad.Set("streak_runs", "500")
	if code, _, _ := getSummary(t, env, viewer, bad); code != http.StatusBadRequest {
		t.Errorf("streak_runs 500: status = %d, want 400", code)
	}
}

func TestHistorySummaryPagesMassFailures(t *testing.T) {
	env := testenv.Start(t)
	viewer := env.IssueAPIKeyWithRole(t, "alpha-ai", policy.RoleViewer)
	tests := map[string][]string{}
	for i := range 501 {
		tests[fmt.Sprintf("test %03d", i)] = []string{"failed"}
	}
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(1), runID: "r1", at: time.Now(), tests: tests})

	_, first, _ := getSummary(t, env, viewer, summaryParams(sha(1), "r1"))
	params := summaryParams(sha(1), "r1")
	params.Set("page", "2")
	_, second, _ := getSummary(t, env, viewer, params)
	if first.TotalTests != 501 || len(first.Tests) != 500 || !first.HasMore || len(second.Tests) != 1 || second.HasMore {
		t.Errorf("pages: %d (more %v), %d (more %v) of %d; want 500 then 1 of 501",
			len(first.Tests), first.HasMore, len(second.Tests), second.HasMore, first.TotalTests)
	}
	if len(second.Tests) == 1 && second.Tests[0].FullTitle != summarySuite+" › test 500" {
		t.Errorf("page 2 = %q; want the last test by title", second.Tests[0].FullTitle)
	}

	// Smaller pages (what the MCP tool asks for) still reach every test.
	params.Set("per_page", "100")
	params.Set("page", "6")
	_, sixth, _ := getSummary(t, env, viewer, params)
	if len(sixth.Tests) != 1 || sixth.HasMore || sixth.Tests[0].FullTitle != summarySuite+" › test 500" {
		t.Errorf("per_page 100, page 6: %d tests (more %v); want only test 500", len(sixth.Tests), sixth.HasMore)
	}
}

func TestHistorySummaryIsRateLimitedPerKey(t *testing.T) {
	env := testenv.Start(t)
	seedRun(t, env, summaryRun{name: summaryLane, sha: sha(1), runID: "r1", at: time.Now(), tests: map[string][]string{"t": {"failed"}}})
	viewer := env.IssueAPIKeyWithRole(t, "busy", policy.RoleViewer)
	params := summaryParams(sha(1), "r1")

	// 60 a minute, refilled continuously: a burst of 100 runs out well before the refill catches up.
	limited := false
	for range 100 {
		code, _, header := getSummary(t, env, viewer, params)
		if code == http.StatusTooManyRequests {
			limited = header.Get("Retry-After") != ""
			break
		}
	}
	if !limited {
		t.Error("100 requests in a burst were all served; want 429 with Retry-After")
	}
	if code, _, _ := getSummary(t, env, env.IssueAPIKeyWithRole(t, "other", policy.RoleViewer), params); code != http.StatusOK {
		t.Errorf("another key: status = %d, want 200", code)
	}
}
