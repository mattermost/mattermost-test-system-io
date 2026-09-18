//go:build e2e
// +build e2e

package reportse2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// seedGroup inserts one completed report group with a single spec run; the
// history endpoint must see it through the same joins the dashboard uses.
func seedGroup(t *testing.T, env *testenv.Env, repo, branch string, pr *int, sha, name, file, title, status string, retry int, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	var gid string
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt, gh_pr_number, status, created_at)
		VALUES ('playwright', $1, $2, $3, $4, $5, '1', $6, 'completed', $7) RETURNING id`,
		name, repo, branch, sha, "run-"+sha+"-"+name, pr, createdAt).Scan(&gid); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	var rid string
	if err := env.Pool.QueryRow(ctx, `INSERT INTO reports (report_group_id, name, status) VALUES ($1, 'shard-1', 'complete') RETURNING id`, gid).Scan(&rid); err != nil {
		t.Fatalf("insert report: %v", err)
	}
	var sid string
	if err := env.Pool.QueryRow(ctx, `INSERT INTO suites (report_id, title, file, ordinal) VALUES ($1, $2, $2, 0) RETURNING id`, rid, file).Scan(&sid); err != nil {
		t.Fatalf("insert suite: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, `INSERT INTO test_cases (suite_id, title, full_title, status, retry_count, ordinal, error_message) VALUES ($1, $2, $3, $4, $5, 0, $6)`,
		sid, title, file+" > "+title, status, retry, "Error: expected visible\nat spec.ts:12"); err != nil {
		t.Fatalf("insert case: %v", err)
	}
}

type historyResponse struct {
	Page         int  `json:"page"`
	PerPage      int  `json:"per_page"`
	HasMore      bool `json:"has_more"`
	Observations []struct {
		File       string `json:"file"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		Branch     string `json:"branch"`
		GHPRNumber *int   `json:"gh_pr_number"`
		CommitSHA  string `json:"commit_sha"`
		Error      string `json:"error_excerpt"`
	} `json:"observations"`
}

func postHistory(t *testing.T, env *testenv.Env, body map[string]any) (int, historyResponse) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(env.ServerURL+"/api/v1/reports/history", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out historyResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode, out
}

func TestReportsHistoryReturnsPastExecutionsInNamedSpecFiles(t *testing.T) {
	env := testenv.Start(t)
	const repo = "mattermost/history-e2e"
	const file = "functional/channels/a.spec.ts"
	const other = "functional/channels/b.spec.ts"
	const title = "MM-T1 renders"
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(time.Hour).Format(time.RFC3339)
	pr7, pr9 := 7, 9

	seedGroup(t, env, repo, "master", nil, "aaaaaaa", "playwright-full-master", file, title, "passed", 0, now.Add(-72*time.Hour))
	seedGroup(t, env, repo, "pr-7", &pr7, "bbbbbbb", "playwright-full", file, title, "failed", 1, now.Add(-48*time.Hour))
	seedGroup(t, env, repo, "pr-9", &pr9, "ccccccc", "playwright-full", file, title, "flaky", 1, now.Add(-24*time.Hour))
	// Same spec file, a test the caller never named: file-keyed requests return
	// it, which is the whole point of keying on the file.
	seedGroup(t, env, repo, "pr-9", &pr9, "ddddddd", "playwright-full", file, "MM-T2 reworded later", "failed", 0, now.Add(-12*time.Hour))
	seedGroup(t, env, repo, "master", nil, "eeeeeee", "playwright-full-master", other, title, "failed", 0, now.Add(-6*time.Hour))  // another file
	seedGroup(t, env, repo, "master", nil, "fffffff", "playwright-full-master", file, title, "failed", 0, now.Add(-480*time.Hour)) // outside the window
	seedGroup(t, env, "mattermost/elsewhere", "master", nil, "9999999", "playwright-full-master", file, title, "failed", 0, now)   // other repo

	status, out := postHistory(t, env, map[string]any{"repository": repo, "until": until, "files": []string{file}})
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if out.Page != 1 || out.PerPage != 50 || out.HasMore {
		t.Fatalf("paging defaults = page %d, per_page %d, has_more %v; want 1/50/false", out.Page, out.PerPage, out.HasMore)
	}
	// Newest first, and every test in the file, not only the one named before.
	want := []string{"ddddddd:failed", "ccccccc:flaky", "bbbbbbb:failed", "aaaaaaa:passed"}
	if len(out.Observations) != len(want) {
		t.Fatalf("got %d observations, want %d: %+v", len(out.Observations), len(want), out.Observations)
	}
	for i, o := range out.Observations {
		if got := o.CommitSHA + ":" + o.Status; got != want[i] {
			t.Fatalf("observation %d = %s, want %s", i, got, want[i])
		}
		if o.File != file {
			t.Fatalf("observation %d is from %s, want %s", i, o.File, file)
		}
	}
	if out.Observations[0].Title != "MM-T2 reworded later" {
		t.Fatalf("the unnamed test in the file was not returned: %+v", out.Observations[0])
	}
	if out.Observations[3].GHPRNumber != nil || out.Observations[1].GHPRNumber == nil || *out.Observations[1].GHPRNumber != 9 {
		t.Fatalf("pr numbers not carried: %+v", out.Observations)
	}
	if out.Observations[2].Error == "" {
		t.Fatalf("error excerpt missing on the failed observation")
	}

	// Paging walks the same total order with no overlap and no gap.
	_, p1 := postHistory(t, env, map[string]any{"repository": repo, "until": until, "files": []string{file}, "per_page": 2})
	if !p1.HasMore || len(p1.Observations) != 2 || p1.Observations[0].CommitSHA != "ddddddd" {
		t.Fatalf("page 1 = %d observations, has_more %v: %+v", len(p1.Observations), p1.HasMore, p1.Observations)
	}
	_, p2 := postHistory(t, env, map[string]any{"repository": repo, "until": until, "files": []string{file}, "per_page": 2, "page": 2})
	if p2.HasMore || len(p2.Observations) != 2 || p2.Observations[0].CommitSHA != "bbbbbbb" || p2.Observations[1].CommitSHA != "aaaaaaa" {
		t.Fatalf("page 2 = %d observations, has_more %v: %+v", len(p2.Observations), p2.HasMore, p2.Observations)
	}
	_, p3 := postHistory(t, env, map[string]any{"repository": repo, "until": until, "files": []string{file}, "per_page": 2, "page": 3})
	if p3.HasMore || len(p3.Observations) != 0 {
		t.Fatalf("page past the end = %d observations, has_more %v", len(p3.Observations), p3.HasMore)
	}

	// branch narrows to one lane; repeated files are folded, not rejected.
	_, onMaster := postHistory(t, env, map[string]any{"repository": repo, "until": until, "files": []string{file, file}, "branch": "master"})
	if len(onMaster.Observations) != 1 || onMaster.Observations[0].CommitSHA != "aaaaaaa" {
		t.Fatalf("branch filter = %+v, want only the master run", onMaster.Observations)
	}

	// Several files in one request.
	_, both := postHistory(t, env, map[string]any{"repository": repo, "until": until, "files": []string{file, other}})
	if len(both.Observations) != 5 || both.Observations[0].File != other {
		t.Fatalf("two-file request = %d observations: %+v", len(both.Observations), both.Observations)
	}

	// The list filters find the run's own group without paging.
	listResp, err := http.Get(env.ServerURL + "/api/v1/reports?repository=" + repo + "&commit=ccccccc&name=playwright-full")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var list struct {
		Total   int              `json:"total"`
		Reports []map[string]any `json:"reports"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	listResp.Body.Close()
	if list.Total != 1 || len(list.Reports) != 1 || list.Reports[0]["name"] != "playwright-full" {
		t.Fatalf("filtered list = %d/%d %v, want exactly the pr-9 playwright-full group", list.Total, len(list.Reports), list.Reports)
	}

	for _, bad := range []struct {
		name string
		body map[string]any
	}{
		{"no files", map[string]any{"repository": repo}},
		{"empty path", map[string]any{"repository": repo, "files": []string{""}}},
		{"per_page over the cap", map[string]any{"repository": repo, "files": []string{file}, "per_page": 2001}},
		{"negative page", map[string]any{"repository": repo, "files": []string{file}, "page": -1}},
		{"40-day window", map[string]any{"repository": repo, "files": []string{file}, "since": now.Add(-960 * time.Hour).Format(time.RFC3339)}},
	} {
		if status, _ := postHistory(t, env, bad.body); status != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", bad.name, status)
		}
	}
}
