package reports

import "testing"

func TestExpandedGroupNames(t *testing.T) {
	t.Parallel()
	got := expandedGroupNames("mobile-pr")
	want := []string{"mobile-detox-pr", "mobile-maestro-pr"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if names := expandedGroupNames("mobile-detox-pr"); len(names) != 2 {
		t.Fatalf("member should expand family, got %v", names)
	}
	if names := expandedGroupNames("desktop-pr"); len(names) != 1 || names[0] != "desktop-pr" {
		t.Fatalf("desktop-pr should not expand, got %v", names)
	}
}

func TestCanonicalRunName(t *testing.T) {
	t.Parallel()
	if got := canonicalRunName("mobile-maestro-pr"); got != "mobile-pr" {
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
			Branch: "main", Commit: "abc", URLPath: "/reports/mattermost-mobile/main/abc/mobile-detox-pr",
			TestStats: &testStats{Total: 10, Passed: 9, Failed: 1}, ReportsCount: 3, TotalReportsExpected: 3,
			CreatedAt: "2026-01-01T00:00:00Z",
		},
		{
			ReportID: "m1", Name: "mobile-maestro-pr", GHRunID: "100", GHRunAttempt: "1",
			Branch: "main", Commit: "abc", URLPath: "/reports/mattermost-mobile/main/abc/mobile-maestro-pr",
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
		if merged[i].Name == "mobile-pr" {
			mobile = &merged[i]
		}
	}
	if mobile == nil {
		t.Fatal("missing merged mobile-pr")
	}
	if mobile.TestStats.Total != 18 || mobile.ReportsCount != 5 || mobile.TotalReportsExpected != 5 {
		t.Fatalf("bad merge: %+v", mobile)
	}
	if mobile.URLPath != "/reports/mattermost-mobile/main/abc/mobile-pr" {
		t.Fatalf("url path %q", mobile.URLPath)
	}
}

func TestRewriteGroupedRunURLPathFinalSegment(t *testing.T) {
	t.Parallel()
	// Repo slug contains the old name; only the trailing segment should change.
	path := "/reports/mobile-detox-pr-repo/main/abc/mobile-detox-pr"
	got := rewriteGroupedRunURLPath(path, "mobile-detox-pr", "mobile-pr")
	want := "/reports/mobile-detox-pr-repo/main/abc/mobile-pr"
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
