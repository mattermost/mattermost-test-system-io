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

// seedGroup inserts one completed report group with a single spec run and
// returns nothing; the history endpoint must see it through the same joins
// the dashboard uses.
func seedGroup(t *testing.T, env *testenv.Env, repo, branch string, pr *int, sha, name, file, title, status string, retry int, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	var gid string
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt, gh_pr_number, status, created_at)
		VALUES ('playwright', $1, $2, $3, $4, $5, '1', $6, 'completed', $7) RETURNING id`,
		name, repo, branch, sha, "run-"+sha, pr, createdAt).Scan(&gid); err != nil {
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

func TestReportsHistoryReturnsPastExecutionsOfNamedTests(t *testing.T) {
	env := testenv.Start(t)
	const repo = "mattermost/history-e2e"
	const file = "functional/channels/a.spec.ts"
	const title = "MM-T1 renders"
	now := time.Now().UTC().Truncate(time.Second)
	pr7, pr9 := 7, 9
	seedGroup(t, env, repo, "master", nil, "aaaaaaa", "playwright-full-master", file, title, "passed", 0, now.Add(-3*24*time.Hour))
	seedGroup(t, env, repo, "pr-7", &pr7, "bbbbbbb", "playwright-full", file, title, "failed", 1, now.Add(-2*24*time.Hour))
	seedGroup(t, env, repo, "pr-9", &pr9, "ccccccc", "playwright-full", file, title, "flaky", 1, now.Add(-1*24*time.Hour))
	seedGroup(t, env, repo, "pr-9", &pr9, "ccccccc", "playwright-full-b", file, "other test", "failed", 0, now.Add(-1*24*time.Hour))
	seedGroup(t, env, repo, "master", nil, "ddddddd", "playwright-full-master", file, title, "failed", 0, now.Add(-20*24*time.Hour)) // outside the window
	seedGroup(t, env, "mattermost/elsewhere", "master", nil, "eeeeeee", "playwright-full-master", file, title, "failed", 0, now)     // other repo

	body, _ := json.Marshal(map[string]any{
		"repository": repo,
		"until":      now.Add(time.Hour).Format(time.RFC3339),
		"tests":      []map[string]string{{"file": file, "title": title}},
	})
	resp, err := http.Post(env.ServerURL+"/api/v1/reports/history", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Truncated    bool `json:"truncated"`
		Observations []struct {
			Title      string `json:"title"`
			Status     string `json:"status"`
			Branch     string `json:"branch"`
			GHPRNumber *int   `json:"gh_pr_number"`
			CommitSHA  string `json:"commit_sha"`
			Error      string `json:"error_excerpt"`
		} `json:"observations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Truncated || len(out.Observations) != 3 {
		t.Fatalf("got %d observations (truncated=%v), want 3: %+v", len(out.Observations), out.Truncated, out.Observations)
	}
	// Newest first: pr-9 flaky, pr-7 failed, master passed.
	want := []string{"ccccccc:flaky", "bbbbbbb:failed", "aaaaaaa:passed"}
	for i, o := range out.Observations {
		if got := o.CommitSHA + ":" + o.Status; got != want[i] || o.Title != title {
			t.Fatalf("observation %d = %s (%s), want %s", i, got, o.Title, want[i])
		}
	}
	if out.Observations[2].GHPRNumber != nil || out.Observations[0].GHPRNumber == nil || *out.Observations[0].GHPRNumber != 9 {
		t.Fatalf("pr numbers not carried: %+v", out.Observations)
	}
	if out.Observations[1].Error == "" {
		t.Fatalf("error excerpt missing on the failed observation")
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

	dup, _ := json.Marshal(map[string]any{"repository": repo, "tests": []map[string]string{{"file": file, "title": title}, {"file": file, "title": title}}})
	respDup, err := http.Post(env.ServerURL+"/api/v1/reports/history", "application/json", bytes.NewReader(dup))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	respDup.Body.Close()
	if respDup.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate test pair: status = %d, want 400", respDup.StatusCode)
	}

	bad, _ := json.Marshal(map[string]any{"repository": repo, "since": now.Add(-40 * 24 * time.Hour).Format(time.RFC3339), "tests": []map[string]string{{"file": file, "title": title}}})
	resp2, err := http.Post(env.ServerURL+"/api/v1/reports/history", "application/json", bytes.NewReader(bad))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("40-day window: status = %d, want 400", resp2.StatusCode)
	}
}
