package reports

import "testing"

const (
	runNameMobilePR   = "mobile-pr"
	runNameMobileMain = "mobile-main"
)

func TestExpandedGroupNames(t *testing.T) {
	t.Parallel()
	got := expandedGroupNames(runNameMobilePR)
	want := []string{runNameMobilePR, "mobile-detox-pr", "mobile-maestro-pr"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if names := expandedGroupNames("mobile-detox-pr"); len(names) != 3 || names[0] != runNameMobilePR {
		t.Fatalf("member should expand to canon + family, got %v", names)
	}
	if names := expandedGroupNames("desktop-pr"); len(names) != 1 || names[0] != "desktop-pr" {
		t.Fatalf("desktop-pr should not expand, got %v", names)
	}
	if names := expandedGroupNames(runNameMobileMain); names[0] != runNameMobileMain {
		t.Fatalf("mobile-main should be canonical, got %v", names)
	}
	if names := expandedGroupNames("mobile-master"); names[0] != runNameMobileMain {
		t.Fatalf("legacy mobile-master should expand under mobile-main, got %v", names)
	}
}

func TestCanonicalRunName(t *testing.T) {
	t.Parallel()
	if got := canonicalRunName("mobile-maestro-pr"); got != runNameMobilePR {
		t.Fatalf("got %q", got)
	}
	if got := canonicalRunName("mobile-master"); got != runNameMobileMain {
		t.Fatalf("legacy mobile-master should canonicalize to mobile-main, got %q", got)
	}
	if got := canonicalRunName("mobile-detox-main"); got != runNameMobileMain {
		t.Fatalf("got %q", got)
	}
	if got := canonicalRunName("cmt-desktop"); got != "cmt-desktop" {
		t.Fatalf("got %q", got)
	}
}

func TestParsePRBranch(t *testing.T) {
	t.Parallel()
	if n, ok := parsePRBranch("pr-3891"); !ok || n != 3891 {
		t.Fatalf("got %d %v", n, ok)
	}
	if _, ok := parsePRBranch("tsio-spike"); ok {
		t.Fatal("expected false for feature branch")
	}
	if _, ok := parsePRBranch("pr-0"); ok {
		t.Fatal("expected false for pr-0")
	}
}

func TestMergeGroupedRunEntries(t *testing.T) {
	t.Parallel()
	runs := []runEntry{
		{
			ReportID: "d1", Name: "mobile-detox-pr", GHRunID: "100", GHRunAttempt: "1",
			Branch: "main", Commit: "abc", URLPath: "/reports/mobile/main/abc/mobile-detox-pr",
			TestStats: &testStats{Total: 10, Passed: 9, Failed: 1}, ReportsCount: 3, TotalReportsExpected: 3,
			CreatedAt: "2026-01-01T00:00:00Z",
		},
		{
			ReportID: "m1", Name: "mobile-maestro-pr", GHRunID: "100", GHRunAttempt: "1",
			Branch: "main", Commit: "abc", URLPath: "/reports/mobile/main/abc/mobile-maestro-pr",
			TestStats: &testStats{Total: 8, Passed: 7, Failed: 1}, ReportsCount: 2, TotalReportsExpected: 2,
			CreatedAt: "2026-01-01T00:01:00Z",
		},
		{
			ReportID: "x1", Name: "desktop-pr", GHRunID: "200", GHRunAttempt: "1",
			Branch: "main", Commit: "def", URLPath: "/reports/desktop/main/def/desktop-pr",
			TestStats: &testStats{Total: 5, Passed: 5}, ReportsCount: 1,
		},
	}
	merged := mergeGroupedRunEntries(runs)
	if len(merged) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(merged))
	}
	var mobile *runEntry
	for i := range merged {
		if merged[i].Name == runNameMobilePR {
			mobile = &merged[i]
		}
	}
	if mobile == nil {
		t.Fatal("missing merged mobile-pr")
	}
	if mobile.TestStats.Total != 18 || mobile.ReportsCount != 5 || mobile.TotalReportsExpected != 5 {
		t.Fatalf("bad merge: %+v", mobile)
	}
	if mobile.URLPath != "/reports/mobile/main/abc/"+runNameMobilePR {
		t.Fatalf("url path %q", mobile.URLPath)
	}
}

func TestConsolidatedRunURLPathDesktopPR(t *testing.T) {
	t.Parallel()
	g := groupDTO{
		Repository:   "mattermost/desktop",
		Branch:       "tsio-spike",
		CommitSHA:    "cbe461edcda38b98726f2abeafc4682bf945f440",
		Name:         "desktop-pr",
		GHRunID:      "837585694163",
		GHRunAttempt: "1",
	}
	pr := 3891
	g.GHPRNumber = &pr
	got := consolidatedRunURLPath(g)
	want := "/reports/desktop/tsio-spike/cbe461e/desktop-pr"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestConsolidatedRunURLPathMobile(t *testing.T) {
	t.Parallel()
	g := groupDTO{
		Repository:   "mattermost/mattermost-mobile",
		Branch:       "feat/tsio-mobile-reporting",
		CommitSHA:    "abc1234deadbeef0123456789abcdef012345678",
		Name:         runNameMobilePR,
		GHRunID:      "12345678901",
		GHRunAttempt: "1",
	}
	pr := 8421
	g.GHPRNumber = &pr
	got := consolidatedRunURLPath(g)
	want := "/reports/mobile/feat~tsio-mobile-reporting/abc1234/" + runNameMobilePR
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestConsolidatedRunURLPathDesktopMaster(t *testing.T) {
	t.Parallel()
	g := groupDTO{
		Repository:   "mattermost/desktop",
		Branch:       "master",
		CommitSHA:    "29b47e7dcda38b98726f2abeafc4682bf945f440",
		Name:         "desktop-master",
		GHRunID:      "837585694163",
		GHRunAttempt: "1",
	}
	got := consolidatedRunURLPath(g)
	want := "/reports/desktop/master/29b47e7/desktop-master"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewriteGroupedRunURLPathFinalSegment(t *testing.T) {
	t.Parallel()
	// Repo slug contains the old name; only the trailing segment should change.
	path := "/reports/mobile-detox-pr-repo/main/abc/mobile-detox-pr?gh_run_id=99&gh_run_attempt=1"
	got := rewriteGroupedRunURLPath(path, "mobile-detox-pr", runNameMobilePR)
	want := "/reports/mobile-detox-pr-repo/main/abc/" + runNameMobilePR + "?gh_run_id=99&gh_run_attempt=1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMergeRunEntryStatsNilTestStats(t *testing.T) {
	t.Parallel()
	withStats := runEntry{TestStats: &testStats{Total: 5, Passed: 4, Failed: 1}}
	nilStats := runEntry{TestStats: nil}
	mergeRunEntryStats(&withStats, &nilStats)
	if withStats.TestStats.Total != 5 {
		t.Fatalf("nil rhs should not change lhs: %+v", withStats.TestStats)
	}

	mergeRunEntryStats(&nilStats, &withStats)
	if nilStats.TestStats == nil || nilStats.TestStats.Total != 5 {
		t.Fatalf("nil lhs should adopt rhs stats: %+v", nilStats.TestStats)
	}
}
