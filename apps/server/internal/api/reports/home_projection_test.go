package reports

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRunPageHref(t *testing.T) {
	got := runPageHref("/reports/repo-name/main/b111183/run", "demo-1", "1")
	want := "/reports/repo-name/main/b111183/run?gh_run_id=demo-1&gh_run_attempt=1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if runPageHref("/reports/x?gh_run_id=old", "new", "2") != "/reports/x?gh_run_id=old" {
		t.Fatal("should not append when gh_run_id present")
	}
}

func TestPassRatePercent(t *testing.T) {
	if passRatePercent(&testStats{Passed: 9, Flaky: 1, Total: 10}) != 100 {
		t.Fatal("expected 100%")
	}
}

func TestParseProjectedTestStats(t *testing.T) {
	raw, err := json.Marshal(groupStats{
		TotalCases: 10,
		Passed:     8,
		Failed:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	stats := parseProjectedTestStats(raw)
	if stats == nil || stats.Total != 10 || stats.Failed != 2 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestGroupProjectionRowIsLive(t *testing.T) {
	live := groupProjectionRow{Orch: &orchestrationSummary{Status: "in_progress"}}
	if !live.isLiveHomeRun() {
		t.Fatal("expected live")
	}
	done := groupProjectionRow{Orch: &orchestrationSummary{Status: "completed"}, Status: "complete"}
	if done.isLiveHomeRun() {
		t.Fatal("expected static")
	}
}

func TestMergeOrchestrationTestsIntoStats(t *testing.T) {
	stats := groupStats{TotalCases: 0}
	merged := mergeOrchestrationTestsIntoStats(stats, &orchestrationSummary{
		Tests: &orchestrationTestCounts{Total: 10, Passed: 9, Failed: 1},
	})
	if merged.TotalCases != 10 || merged.Failed != 1 {
		t.Fatalf("merged=%+v", merged)
	}
	keep := mergeOrchestrationTestsIntoStats(groupStats{TotalCases: 5, Passed: 5}, &orchestrationSummary{
		Tests: &orchestrationTestCounts{Total: 10},
	})
	if keep.TotalCases != 5 {
		t.Fatal("shard stats should win")
	}
}

func TestRefreshStaleGroupSummariesNoPool(t *testing.T) {
	if err := RefreshStaleGroupSummaries(context.Background(), nil, nil, 10); err != nil {
		t.Fatal(err)
	}
}

func TestBackfillSummariesDev(t *testing.T) {
	url := os.Getenv("TSIO_DATABASE_URL")
	if url == "" {
		url = "postgres://tsio:tsio@localhost:6432/tsio?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skip("database unavailable:", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skip("database unavailable:", err)
	}
	for i := 0; i < 20; i++ {
		if err := RefreshStaleGroupSummaries(ctx, pool, nil, 100); err != nil {
			t.Fatal(err)
		}
		var remaining int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM report_groups
			 WHERE last_summary_at IS NULL OR test_stats_json IS NULL
		`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining == 0 {
			return
		}
	}
	t.Fatal("stale summaries remain after backfill")
}

func TestToHomeStaticRun(t *testing.T) {
	rate := 80
	e := runEntry{
		ReportID:   "id",
		Repository: "org/repo-name",
		Name:       "run",
		Branch:     "main",
		ShortSHA:   "abc1234",
		Status:     "complete",
		CreatedAt:  "2026-01-01T00:00:00Z",
		URLPath:    "/reports/repo-name/main/abc1234/run",
		GHRunID:    "run-1",
		TestStats:  &testStats{Total: 5, Passed: 4, Failed: 1},
	}
	row := toHomeStaticRun(e)
	if row.RepositoryName != "repo-name" || !row.HasFailed {
		t.Fatalf("row=%+v", row)
	}
	if row.PassRate == nil || *row.PassRate != rate {
		t.Fatalf("pass rate=%v want %d", row.PassRate, rate)
	}
}
