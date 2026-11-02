//go:build e2e
// +build e2e

package reportse2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const pagingRepo = "paging-owner/paging-repo"

// insertProjectedGroup inserts a finished group with a precomputed projection.
func insertProjectedGroup(t *testing.T, env *testenv.Env, name, runGroup, ghRunID, sha string, createdAt time.Time, passed, failed int) {
	t.Helper()
	stats := fmt.Sprintf(`{"TotalSuites":1,"TotalCases":%d,"Passed":%d,"Failed":%d,"Skipped":0,"Flaky":0,"ReportsCount":1}`,
		passed+failed, passed, failed)
	var rg any
	if runGroup != "" {
		rg = runGroup
	}
	if _, err := env.Pool.Exec(context.Background(), `
		INSERT INTO report_groups
		       (framework, name, run_group, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
		        created_at, updated_at, last_upload_at, test_stats_json, reports_count, last_summary_at)
		VALUES ('cypress', $1, $2, 'completed', $3, 'master', $4, $5, '1',
		        $6, $6, $6, $7::jsonb, 1, now() + interval '1 hour')`,
		name, rg, pagingRepo, sha, ghRunID, createdAt, stats); err != nil {
		t.Fatalf("insert group %s: %v", name, err)
	}
}

// A run whose two groups sit on rows 50 and 51 must show once, with combined totals.
func TestHomePagingKeepsRunGroupsTogether(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	base := time.Now().UTC().Add(-time.Hour)
	at := func(i int) time.Time { return base.Add(-time.Duration(i) * time.Minute) }

	// 49 newer single-group runs, the split run at rows 50-51, then 5 older runs.
	for i := 1; i <= 49; i++ {
		insertProjectedGroup(t, env, fmt.Sprintf("single-%02d", i), "", fmt.Sprintf("gh_run_%d", i), fmt.Sprintf("%040x", i), at(i), 1, 0)
	}
	const splitSHA = "abcdef0123456789abcdef0123456789abcdef01"
	insertProjectedGroup(t, env, "cypress-shard-a", "cypress-full", "gh_run_split", splitSHA, at(50), 3, 1)
	insertProjectedGroup(t, env, "cypress-shard-b", "cypress-full", "gh_run_split", splitSHA, at(51), 4, 0)
	for i := 52; i <= 56; i++ {
		insertProjectedGroup(t, env, fmt.Sprintf("single-%02d", i), "", fmt.Sprintf("gh_run_%d", i), fmt.Sprintf("%040x", i), at(i), 1, 0)
	}

	ctx := context.Background()
	page1, err := reports.LoadHomePageData(ctx, env.Pool, 50, 0, pagingRepo, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	page2, err := reports.LoadHomePageData(ctx, env.Pool, 50, 50, pagingRepo, "")
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	// 49 + 1 merged + 5 runs (56 groups).
	if page1.Total != 55 {
		t.Fatalf("total = %d, want 55 runs", page1.Total)
	}
	if len(page1.Static) != 50 || len(page2.Static) != 5 {
		t.Fatalf("page sizes = %d/%d, want 50/5", len(page1.Static), len(page2.Static))
	}

	seen := 0
	for pi, page := range [][]reports.HomeStaticRun{page1.Static, page2.Static} {
		for _, r := range page {
			if r.GHRunID != "gh_run_split" {
				continue
			}
			seen++
			if pi != 0 {
				t.Errorf("split run on page %d, want page 1", pi+1)
			}
			if r.Name != "cypress-full" {
				t.Errorf("merged name = %q, want cypress-full", r.Name)
			}
			if r.Passed != 7 || r.Failed != 1 || r.Total != 8 {
				t.Errorf("merged totals = passed %d failed %d total %d, want 7/1/8", r.Passed, r.Failed, r.Total)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("split run appeared %d times across pages, want 1", seen)
	}
}
