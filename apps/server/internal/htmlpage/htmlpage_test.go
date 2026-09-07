package htmlpage

import (
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
)

func TestRenderHomeEmpty(t *testing.T) {
	html, err := RenderHome(HomePage{
		Title: "Test System IO",
		Site:  SiteInfo{ServerVersion: "0.1.0", Environment: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		`<!DOCTYPE html>`,
		`Mattermost Test System IO`,
		`Filter by repository`,
		`home-filters-row`,
		`home-filter-select`,
		`No reports yet`,
		`@media (max-width: 1023px)`,
		`@media (min-width: 1024px)`,
		`flex-direction: column`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestRenderHomeWithLiveAndStatic(t *testing.T) {
	html, err := RenderHome(HomePage{
		Title: "Test System IO",
		LiveRuns: []HomeRunRow{
			{
				RowNum: 1, GroupID: "live-1", Href: "/live", Name: "live-run", RepositoryName: "repo-name",
				Branch: "main", ShortSHA: "abc1234", GHRunID: "demo-1", GHRunAttempt: "1",
				CreatedAtRel: "5m ago", StatusIcon: "live", IsLive: true, Region: "home-live",
				SpecTotal: 10, SpecPass: 5, SpecFail: 1, SpecSkipped: 1, SpecPending: 2, SpecLeased: 1, SpecRetest: 2,
			},
		},
		StaticRuns: []HomeRunRow{
			{
				RowNum: 1, Href: "/reports/r/x", Name: "orchestration-demo-cypress",
				RepositoryName: "repo-name", Branch: "main", ShortSHA: "b111183",
				CreatedAt: "2026-01-01T00:00:00Z", CreatedAtRel: "10m ago", StatusIcon: "passed", HasStats: true,
				Passed: 19, Total: 20, PassRate: "100", PassRateClass: "rate-perfect",
				SpecTotal: 451, SpecPass: 449, SpecFail: 1, SpecSkipped: 1,
				Region: "home-static", IsNew: true,
			},
		},
		RowStart: 1,
		ShowEnd:  1,
		Total:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		`data-tsio-region="home-live"`,
		`data-tsio-mode="live"`,
		`live-run`,
		`orchestration-demo-cypress`,
		`2<span class="stat-word"> pending</span>`,
		`1<span class="stat-word"> running</span>`,
		`(2<span class="stat-word"> for retest</span>)`,
		`spec-blue">1<span class="stat-word"> running</span>`,
		`10 specs`,
		`run-card-live`,
		`run-card-static`,
		`live-status-icon`,
		`100%`,
		`datetime="2026-01-01T00:00:00Z"`,
		`<span class="run-specs run-stats-line"`,
		`<span class="run-stat-part">451 specs</span>`,
		`449<span class="stat-word"> passed</span>`,
		`1<span class="stat-word"> failed</span>`,
		`1<span class="stat-word"> skipped</span>`,
		`status-pill-pass`,
		`PASS`,
		`status-new`,
		`NEW`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if !strings.Contains(s, `data-group-id="live-1"`) {
		t.Fatal("missing group id on live row")
	}
}


func TestRenderHomeLiveRows(t *testing.T) {
	html, err := RenderHomeLiveRows([]HomeRunRow{{
		GroupID: "g-1", RowNum: 1, Href: "/live", Name: "live-run", RepositoryName: "repo",
		Branch: "main", CreatedAtRel: "1m ago", StatusIcon: "live", IsLive: true, Region: "home-live",
		SpecTotal: 5, SpecPending: 3,
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		`data-tsio-mode="live"`,
		`3 pending`,
		`5 specs`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestRenderHomePaginationPreviousFromPage2(t *testing.T) {
	html, err := RenderHome(HomePage{
		Title:      "Test System IO",
		Page:       2,
		TotalPages: 2,
		RowStart:   51,
		ShowEnd:    100,
		Total:      100,
		StaticRuns: []HomeRunRow{{RowNum: 1, Href: "/reports/r/x", Name: "run", Region: "home-static"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if strings.Contains(s, `href=""`) {
		t.Fatal("pagination must not use empty href")
	}
	if !strings.Contains(s, `href="?"`) && !strings.Contains(s, `href="?page=1"`) {
		t.Fatalf("expected previous link to page 1, got: %s", s[strings.Index(s, "pagination-actions"):strings.Index(s, "pagination-actions")+200])
	}
}

func TestRenderHomeWithStaticRunOnly(t *testing.T) {
	html, err := RenderHome(HomePage{
		Title: "Test System IO",
		StaticRuns: []HomeRunRow{{
			RowNum: 1, Href: "/reports/r/x", Name: "orchestration-demo-cypress",
			RepositoryName: "repo-name", Branch: "main", ShortSHA: "b111183",
			CreatedAtRel: "5m ago", StatusIcon: "passed", HasStats: true,
			Passed: 19, Total: 20, PassRate: "100", PassRateClass: "rate-perfect",
			Region: "home-static",
		}},
		RowStart: 1,
		ShowEnd:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if !strings.Contains(s, "orchestration-demo-cypress") {
		t.Fatal("missing run name")
	}
	if !strings.Contains(s, `href="/reports/r/x"`) {
		t.Fatal("missing run link")
	}
}

func TestStatusIconForRun(t *testing.T) {
	if statusIconForRun("completed", &reports.HomeOrchView{Status: "completed"}, 10, true, 0) != "failed" {
		t.Fatal("expected failed for completed orch with failures")
	}
	if statusIconForRun("incomplete", nil, 0, false, 0) != "failed" {
		t.Fatal("expected failed for incomplete group")
	}
	if statusIconForRun("completed", &reports.HomeOrchView{Status: "timed_out"}, 3, false, 0) != "failed" {
		t.Fatal("expected failed for timed_out orch")
	}
	if statusIconForRun("completed", &reports.HomeOrchView{Status: "completed"}, 10, false, 2) != "passed" {
		t.Fatal("expected passed (flaky counts as pass)")
	}
	if statusIconForRun("completed", &reports.HomeOrchView{Status: "in_progress"}, 0, false, 0) != "live" {
		t.Fatal("expected live indicator for in-progress orch")
	}
	// Completed orch, no failures → PASS.
	if statusIconForRun("incomplete", &reports.HomeOrchView{Status: "completed"}, 1319, false, 1) != "passed" {
		t.Fatal("expected passed for incomplete group with completed orch")
	}
	if statusIconForRun("incomplete", &reports.HomeOrchView{
		Status: "completed",
		Counts: reports.HomeOrchCountsView{Abandoned: 6},
	}, 3, false, 0) != "failed" {
		t.Fatal("expected failed for completed orch with abandoned units")
	}
}

func TestRelativeTimeISO(t *testing.T) {
	old := "2020-01-15T12:00:00Z"
	if relativeTimeISO(old) != "1/15/2020" {
		t.Fatalf("expected locale-style date for old timestamp, got %q", relativeTimeISO(old))
	}
	if relativeTimeISO("not-a-date") != "not-a-date" {
		t.Fatal("expected invalid input passthrough")
	}
}

func TestIsRecentISO(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	if !isRecentISO(now, time.Hour) {
		t.Fatal("expected recent timestamp within 1h")
	}
	old := "2020-01-15T12:00:00Z"
	if isRecentISO(old, time.Hour) {
		t.Fatal("expected old timestamp not recent")
	}
	if isRecentISO("bad", time.Hour) {
		t.Fatal("expected invalid timestamp not recent")
	}
}

func TestPassRateDisplay(t *testing.T) {
	if passRateDisplay(9, 1, 0) != "90" {
		t.Fatal("expected 90%")
	}
	if passRateDisplay(10, 0, 0) != "100" {
		t.Fatal("expected 100%")
	}
}
