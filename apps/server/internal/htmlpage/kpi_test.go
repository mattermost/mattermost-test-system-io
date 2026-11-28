package htmlpage

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }
func strPtr(v string) *string     { return &v }

func TestRenderKPIOverview_PopulatedAndGuardrails(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	rate := 18.5
	priorRate := 21.0
	deltaPP := -2.5
	testRate := 94.2
	testDelta := 1.4
	laneRate := 88.0
	strictRate := 82.0
	p50 := 860000.0
	p90 := 1685000.0
	p50Delta := -45000.0
	flakyDelta := -2
	runCov := 75.0
	testCov := 80.0
	reuseRate := 50.0
	compRate := 90.0
	rate1k := 20.0
	delta1k := 5.0
	recCount := 3

	params, err := kpi.ParseFailuresParams(url.Values{
		"app":      {"mattermost"},
		"repo":     {"mattermost/mattermost"},
		"stage":    {"pr"},
		"lane":     {"e2e-pr"},
		"platform": {"linux"},
		"window":   {"14d"},
		"sort":     {kpi.SortRateDesc},
		"page":     {"2"},
	}, now)
	if err != nil {
		t.Fatalf("ParseFailuresParams: %v", err)
	}

	opts := kpi.FilterOptions{
		Apps:      []string{"mattermost", "mobile"},
		Repos:     []string{"mattermost/mattermost"},
		Stages:    []string{"pr", "main"},
		Lanes:     []string{"e2e-pr"},
		Platforms: []string{"linux"},
	}

	summary := kpi.SummaryResponse{
		Filters: params,
		LaneCoverage: kpi.LaneCoverage{
			TotalLanes:        2,
			OrchestratedLanes: 1,
			UploadOnlyLanes:   1,
			TotalRuns:         42,
			Notes:             []string{"Upload-only lanes lack lease retry history."},
		},
		MainPassRate: kpi.MainPassRate{
			TestLevel: kpi.TestLevelPassRate{
				PassRate:           &testRate,
				DeltaPP:            &testDelta,
				FirstAttemptPassed: 942,
				TotalTests:         1000,
			},
			LaneLevel: kpi.LaneLevelPassRate{
				PassRate:             &laneRate,
				PassedLanes:          22,
				TotalLanes:           25,
				StrictPassRate:       &strictRate,
				StrictPassedLanes:    20,
				StrictTotalLanes:     25,
				UploadOnlyLanesCount: 1,
			},
			LaneCoverage: kpi.LaneCoverage{TotalLanes: 2, OrchestratedLanes: 1, UploadOnlyLanes: 1, TotalRuns: 25},
		},
		PRFalseRed: &kpi.PRFalseRedSummary{
			IsLowerBound:         true,
			RateQualifier:        "at least",
			SettledAfterHours:    24,
			FalseRedRate:         &rate,
			PriorFalseRedRate:    &priorRate,
			DeltaPP:              &deltaPP,
			SettledRedLanes:      200,
			SettledFalseRedLanes: 37,
			PriorSettledRedLanes: 150,
			UnsettledRedLanes:    5,
		},
		FlakyTests: kpi.FlakySummary{
			WindowDays:      14,
			MinFlakyCommits: 2,
			Count:           7,
			Delta:           &flakyDelta,
			ByApp:           []kpi.AppFlakyCount{{App: "mattermost", Count: 5, Delta: intPtr(-1)}, {App: "mobile", Count: 2, Delta: intPtr(-1)}},
		},
		E2EDuration: kpi.E2EDurationSummary{
			P50Ms:        &p50,
			DeltaP50Ms:   &p50Delta,
			P90Ms:        &p90,
			SampleCount:  18,
			LaneCoverage: kpi.LaneCoverage{TotalLanes: 1, OrchestratedLanes: 1, TotalRuns: 18},
		},
		RecurringFailures: &kpi.RecurringFailuresSummary{
			TotalSignatures:    4,
			ActiveCount:        1,
			ResurfacedCount:    1,
			QuietCount:         1,
			NotEnoughRunsCount: 1,
			TotalOccurrences:   29,
			TriagedCount:       2,
		},
		Triage: &kpi.TriageSummary{
			RunCoverageRate:        &runCov,
			FailedRunsCount:        8,
			TriagedRunsCount:       6,
			TestCoverageRate:       &testCov,
			FailedTestOccurrences:  10,
			TriagedTestOccurrences: 8,
			VerdictReuseRate:       &reuseRate,
			VerdictsTotal:          6,
			VerdictsReused:         3,
			LabelMix:               []kpi.TriageLabelMix{{Label: "flaky-infra", Count: 4, Share: 66.7}, {Label: "product-bug", Count: 2, Share: 33.3}},
			RunCompletion: kpi.RunCompletionSummary{
				TotalRuns:      10,
				CompletedRuns:  8,
				IncompleteRuns: 1,
				TimedOutRuns:   1,
				CompletionRate: &compRate,
			},
			PRRunCompletion: &kpi.RunCompletionSummary{
				TotalRuns:      10,
				CompletedRuns:  8,
				IncompleteRuns: 1,
				TimedOutRuns:   1,
				CompletionRate: &compRate,
			},
		},
		WeeklySeries: []kpi.WeeklyPoint{
			{
				WeekStart:              "2026-11-16",
				MainTestPassRate:       &testRate,
				MainLanePassRate:       &laneRate,
				PRFalseRedRate:         &rate,
				FlakyTestCount:         7,
				RecurringFailuresCount: &recCount,
				E2EP50Ms:               &p50,
				TriageCoverageRate:     &runCov,
				MainTestsTotal:         500,
			},
		},
	}

	failures := kpi.FailuresResponse{
		Filters: params,
		Summary: *summary.RecurringFailures,
		Pagination: kpi.Pagination{
			Page:       2,
			Limit:      50,
			Total:      120,
			TotalPages: 3,
		},
		Failures: []kpi.RecurringFailureRow{
			{
				SignatureID:        "019a1234-5678-7abc-8def-0123456789ab",
				ErrorClass:         strPtr("TimeoutError"),
				NormalizedMessage:  "waiting for selector #btn",
				TopFrame:           strPtr("e2e/login.spec.ts:42"),
				State:              kpi.FailureStateQuiet,
				QuietDays:          intPtr(9),
				OccurrenceCount:    12,
				GroupCount:         5,
				AffectedTestCount:  2,
				AffectedTestRuns:   600,
				RatePer1k:          &rate1k,
				DeltaRatePer1k:     &delta1k,
				LastSeenAt:         now.Add(-9 * 24 * time.Hour),
				Repositories:       []string{"mattermost/mattermost"},
				LatestVerdictLabel: strPtr("flaky-infra"),
			},
			{
				SignatureID:       "019a1234-5678-7abc-8def-0123456789ac",
				NormalizedMessage: "network connection reset",
				State:             kpi.FailureStateResurfaced,
				OccurrenceCount:   4,
				GroupCount:        2,
				AffectedTestCount: 1,
				LastSeenAt:        now.Add(-2 * time.Hour),
			},
		},
	}

	page := BuildKPIOverviewPage(SiteInfo{ServerVersion: "0.16.0"}, opts, summary, failures, now)
	htmlBytes, err := RenderKPIOverview(page)
	if err != nil {
		t.Fatalf("RenderKPIOverview: %v", err)
	}
	s := string(htmlBytes)

	for _, want := range []string{
		`href="/reports"`,
		`href="/kpi`,
		`class="site-nav-link is-active">KPIs</a>`,
		`88.0%`,
		`94.2%`,
		`No CI retests`,
		`at least 18.5%`,
		`Down 2.5 pts`,
		`excludes failures resolved by pushing a new commit`,
		`Last 14d`,
		`Active (2)`,
		`All (4)`,
		`show=all`,
		`14m 20s`,
		`28m 5s`,
		`90.0%`,
		`1 timed out`,
		`Quiet (9d)`,
		`Resurfaced`,
		`/kpi/failures/019a1234-5678-7abc-8def-0123456789ab?`,
		`app=mattermost`,
		`window=14d`,
		`sort=rate_desc`,
		`page=3`,
		`html.dark .kpi-card`,
		`@media (max-width: 639px)`,
		`@media (min-width: 640px) and (max-width: 1023px)`,
		`@media (min-width: 1024px)`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("RenderKPIOverview missing %q", want)
		}
	}
	if !strings.Contains(page.ShowAllHref, `show=all`) {
		t.Fatalf("ShowAllHref = %q, want show=all included", page.ShowAllHref)
	}

	for _, forbid := range []string{
		`id="connection-status"`,
		`[data-theme="dark"]`,
		`Fixed`,
		`error_context`,
		`No blended cross-repo averages`,
	} {
		if strings.Contains(s, forbid) {
			t.Fatalf("RenderKPIOverview must not contain %q", forbid)
		}
	}
	if strings.Contains(strings.ToLower(s), "blocked") {
		t.Fatal("RenderKPIOverview must not contain 'blocked'")
	}

	// Verify /kpi/prs and /kpi/flaky sub-tab links preserve filters cleanly.
	if !strings.Contains(s, `href="/kpi/prs?app=mattermost&amp;lane=e2e-pr&amp;platform=linux&amp;repo=mattermost%2Fmattermost&amp;stage=pr&amp;window=14d"`) {
		t.Fatalf("expected PR E2E sub-tab link to preserve filters without sort/page, got HTML snippet: %s", s)
	}
	if !strings.Contains(s, `href="/kpi/flaky?app=mattermost&amp;lane=e2e-pr&amp;platform=linux&amp;repo=mattermost%2Fmattermost&amp;stage=pr"`) {
		t.Fatalf("expected Flaky sub-tab link to preserve filters with 14d default omitted, got HTML snippet: %s", s)
	}
}

func TestRenderKPIOverview_EmptyAndZeroDenominators(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	params, err := kpi.ParseFailuresParams(url.Values{}, now)
	if err != nil {
		t.Fatal(err)
	}
	page := BuildKPIOverviewPage(SiteInfo{}, kpi.FilterOptions{}, kpi.SummaryResponse{
		Filters: params,
		WeeklySeries: []kpi.WeeklyPoint{
			{WeekStart: "2026-11-16", RecurringFailuresCount: intPtr(0)},
		},
	}, kpi.FailuresResponse{Filters: params}, now)
	htmlBytes, err := RenderKPIOverview(page)
	if err != nil {
		t.Fatal(err)
	}
	s := string(htmlBytes)
	for _, want := range []string{
		`Repository Health Scorecard`,
		`No data`,
		`No test runs recorded in this time window.`,
		`No active failure patterns`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("empty KPI overview missing %q", want)
		}
	}
}

func TestRenderKPIPRs_PopulatedAndEmpty(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	params, err := kpi.ParsePRsRedParams(url.Values{
		"app":    {"mattermost"},
		"window": {"7d"},
		"sort":   {kpi.SortAgeDesc},
		"page":   {"1"},
	}, now)
	if err != nil {
		t.Fatalf("ParsePRsRedParams: %v", err)
	}

	input := kpi.PRRedResponse{
		Filters:              params,
		WindowDays:           7,
		SettledAfterHours:    24,
		IsLowerBound:         true,
		RateQualifier:        "at least",
		FalseRedRate:         floatPtr(22.5),
		DeltaPP:              floatPtr(3.1),
		SettledRedLanes:      40,
		SettledFalseRedLanes: 9,
		UnsettledRedLanes:    3,
		StillRedLanes:        4,
		ReasonMix: kpi.PRFalseRedReasonMix{
			WentGreenOnRerun: 5,
			FailingElsewhere: 4,
			Other:            31,
			Total:            40,
		},
		ConfiguredE2ELanes: []kpi.ConfiguredE2ELane{{Repository: "mattermost/mattermost", Lane: "e2e-rf-pr"}},
		LaneCoverage:       kpi.LaneCoverage{TotalLanes: 1, OrchestratedLanes: 1, TotalRuns: 43},
		Pagination:         kpi.Pagination{Page: 1, Limit: 50, Total: 2, TotalPages: 1},
		Lanes: []kpi.RedPRLaneRow{
			{
				ReportGroupID:        "rg-1",
				Repository:           "mattermost/mattermost",
				App:                  "mattermost",
				Lane:                 "e2e-rf-pr",
				GHPRNumber:           12345,
				CommitSHA:            "abcdef1234567",
				CreatedAt:            now.Add(-3 * time.Hour),
				AgeSeconds:           10800,
				Settled:              false,
				StillRed:             true,
				FailingOnMain:        true,
				OtherPRsCount:        4,
				FailedSignatureCount: 2,
				TestsFailed:          3,
				Reason:               kpi.ReasonFailingElsewhere,
				RunURL:               "/reports/mattermost/pr~12345/abcdef1/e2e-rf-pr",
			},
			{
				ReportGroupID:        "rg-2",
				Repository:           "mattermost/mattermost",
				App:                  "mattermost",
				Lane:                 "e2e-rf-pr",
				GHPRNumber:           12346,
				CommitSHA:            "fedcba9876543",
				CreatedAt:            now.Add(-30 * time.Hour),
				AgeSeconds:           108000,
				Settled:              true,
				StillRed:             false,
				WentGreenOnRerun:     true,
				FailedSignatureCount: 1,
				TestsFailed:          1,
				Reason:               kpi.ReasonWentGreenOnRerun,
				RunURL:               "/reports/mattermost/pr~12346/fedcba9/e2e-rf-pr",
			},
		},
	}

	page := BuildKPIPRsPage(SiteInfo{}, kpi.FilterOptions{Apps: []string{"mattermost"}}, input, now)
	htmlBytes, err := RenderKPIPRs(page)
	if err != nil {
		t.Fatalf("RenderKPIPRs: %v", err)
	}
	s := string(htmlBytes)

	for _, want := range []string{
		`PR E2E Reliability`,
		`at least 22.5%`,
		`excludes failures resolved by pushing a new commit`,
		`Passed on rerun`,
		`Failing on main or ≥3 PRs`,
		`Only this PR (or &lt;3 PRs)`,
		`12.5%`,
		`10.0%`,
		`77.5%`,
		`E2E red`,
		`https://github.com/mattermost/mattermost/pull/12345`,
		`https://github.com/mattermost/mattermost/commit/abcdef1234567`,
		`/kpi?lane=e2e-rf-pr&amp;repo=mattermost%2Fmattermost&amp;window=7d`,
		`#12345`,
		`Failing on main &#43; 4 other PRs`,
		`3 tests · 2 errors`,
		`40 (&gt;24h) · 3 (&lt;24h) · 2 red PR runs total`,
		`2 red PR runs total · 4 still red`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("RenderKPIPRs missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(s), "blocked") {
		t.Fatal("RenderKPIPRs must NEVER contain 'blocked'")
	}
	if strings.Contains(s, "Fixed") {
		t.Fatal("RenderKPIPRs must NEVER contain 'Fixed'")
	}

	// Empty state
	emptyPage := BuildKPIPRsPage(SiteInfo{}, kpi.FilterOptions{}, kpi.PRRedResponse{Filters: params, SettledAfterHours: 24}, now)
	emptyHTML, err := RenderKPIPRs(emptyPage)
	if err != nil {
		t.Fatal(err)
	}
	es := string(emptyHTML)
	if !strings.Contains(es, `N/A`) || !strings.Contains(es, `No red PR runs (&gt;24h) to classify`) || !strings.Contains(es, `No E2E red PR runs`) {
		t.Fatalf("empty PRs page missing expected empty states")
	}
}

func TestBuildKPIOverviewAndPRs_PRFalseRedZeroSettledAndUnsettledStates(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	ovParams, err := kpi.ParseFailuresParams(url.Values{"repo": {"mattermost/mattermost"}}, now)
	if err != nil {
		t.Fatal(err)
	}

	// Overview: zero settled red lanes + 3 unsettled (<24h) lanes -> "N/A" / "No data" + unsettled count.
	sumWithUnsettled := kpi.SummaryResponse{
		Filters: ovParams,
		PRFalseRed: &kpi.PRFalseRedSummary{
			IsLowerBound:      true,
			RateQualifier:     "at least",
			SettledAfterHours: 24,
			FalseRedRate:      nil,
			UnsettledRedLanes: 3,
		},
	}
	ovPage := BuildKPIOverviewPage(SiteInfo{}, kpi.FilterOptions{}, sumWithUnsettled, kpi.FailuresResponse{Filters: ovParams}, now)
	ovHTML, err := RenderKPIOverview(ovPage)
	if err != nil {
		t.Fatalf("RenderKPIOverview unsettled: %v", err)
	}
	ovStr := string(ovHTML)
	for _, want := range []string{
		`N/A`,
		`No data`,
		`3 recent (&lt;24h)`,
		`3 recent red runs (&lt;24h old); excludes failures resolved by pushing a new commit.`,
	} {
		if !strings.Contains(ovStr, want) {
			t.Fatalf("unsettled overview missing %q", want)
		}
	}

	// Overview: zero settled and zero unsettled -> explicit no-settled message.
	sumEmptyPR := kpi.SummaryResponse{
		Filters: ovParams,
		PRFalseRed: &kpi.PRFalseRedSummary{
			IsLowerBound:      true,
			RateQualifier:     "at least",
			SettledAfterHours: 24,
			FalseRedRate:      nil,
		},
	}
	ovEmptyHTML, err := RenderKPIOverview(BuildKPIOverviewPage(SiteInfo{}, kpi.FilterOptions{}, sumEmptyPR, kpi.FailuresResponse{Filters: ovParams}, now))
	if err != nil {
		t.Fatalf("RenderKPIOverview zero PR: %v", err)
	}
	if !strings.Contains(string(ovEmptyHTML), `No red PR runs &gt;24h old in window.`) {
		t.Fatalf("zero-PR overview missing settled note")
	}

	// PR E2E page: nil FalseRedRate + unsettled "E2E red" row failing on 3 other PRs (without main) + 2 other PRs.
	prParams, err := kpi.ParsePRsRedParams(url.Values{"repo": {"mattermost/mattermost"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	prResp := kpi.PRRedResponse{
		Filters:           prParams,
		WindowDays:        30,
		SettledAfterHours: 24,
		IsLowerBound:      true,
		RateQualifier:     "at least",
		FalseRedRate:      nil,
		UnsettledRedLanes: 2,
		StillRedLanes:     2,
		Pagination:        kpi.Pagination{Page: 1, Limit: 50, Total: 2, TotalPages: 1},
		Lanes: []kpi.RedPRLaneRow{
			{
				ReportGroupID:        "rg-3prs",
				Repository:           "mattermost/mattermost",
				App:                  "mattermost",
				Lane:                 "playwright-full",
				GHPRNumber:           701,
				CommitSHA:            "1234567890ab",
				CreatedAt:            now.Add(-2 * time.Hour),
				AgeSeconds:           7200,
				Settled:              false,
				StillRed:             true,
				FailingOnMain:        false,
				OtherPRsCount:        3,
				FailedSignatureCount: 1,
				TestsFailed:          2,
				Reason:               kpi.ReasonFailingElsewhere,
				RunURL:               "/reports/mattermost/pr-701/1234567/playwright-full",
			},
			{
				ReportGroupID:        "rg-2prs",
				Repository:           "mattermost/mattermost",
				App:                  "mattermost",
				Lane:                 "playwright-full",
				GHPRNumber:           702,
				CommitSHA:            "abcdef012345",
				CreatedAt:            now.Add(-4 * time.Hour),
				AgeSeconds:           14400,
				Settled:              false,
				StillRed:             true,
				FailingOnMain:        false,
				OtherPRsCount:        2,
				FailedSignatureCount: 1,
				TestsFailed:          1,
				Reason:               kpi.ReasonOther,
				RunURL:               "/reports/mattermost/pr-702/abcdef0/playwright-full",
			},
		},
	}
	prHTML, err := RenderKPIPRs(BuildKPIPRsPage(SiteInfo{}, kpi.FilterOptions{}, prResp, now))
	if err != nil {
		t.Fatalf("RenderKPIPRs zero-settled: %v", err)
	}
	prStr := string(prHTML)
	for _, want := range []string{
		`N/A`,
		`No red PR runs (&gt;24h) in this window`,
		`E2E red`,
		`Failing on 3 other PRs`,
		`Recent (&lt;24h) · seen on 2 other PRs`,
		`Only this PR (or &lt;3 PRs)`,
	} {
		if !strings.Contains(prStr, want) {
			t.Fatalf("PRs zero-settled missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(prStr), "blocked") {
		t.Fatal("PRs page must NEVER contain 'blocked'")
	}
}

func TestRenderKPIFailureDetail_XSSRedactionAndGuardrails(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	detail := kpi.FailureDetailResponse{
		Filters:              kpi.FilterParams{Window: kpi.Window30d},
		RequestedSignatureID: "019a1234-5678-7abc-8def-0123456789ab",
		CanonicalSignatureID: "019a1234-5678-7abc-8def-0123456789ab",
		Fingerprint:          "fp-abc123",
		AlgoVersion:          2,
		ErrorClass:           strPtr(`<script>alert("err")</script>`),
		NormalizedMessage:    `Timeout on <script>alert(1)</script>`,
		TopFrame:             strPtr(`spec.ts:10 <img src=x onerror=alert(1)>`),
		SampleMessage:        strPtr(`raw <script>alert(2)</script>`),
		State:                "Fixed", // Tampered input must NEVER render as "Fixed"
		OccurrenceCount:      9,
		RawAttemptCount:      11,
		GroupCount:           4,
		AffectedTestCount:    1,
		AffectedTestRuns:     300,
		RatePer1k:            floatPtr(30.0),
		Occurrences14d:       6,
		AffectedRuns14d:      150,
		RatePer1k14d:         floatPtr(40.0),
		PriorOccurrences14d:  3,
		PriorAffectedRuns14d: 300,
		PriorRatePer1k14d:    floatPtr(10.0),
		DeltaRatePer1k:       floatPtr(30.0),
		Occurrences7d:        4,
		AffectedRuns7d:       100,
		LastSeenAt:           &now,
		FirstSeen: kpi.FailureFirstSeen{
			FirstSeenAt: now.Add(-10 * 24 * time.Hour),
			Repository:  strPtr("mattermost/mattermost"),
			Lane:        strPtr("e2e-pr"),
			Branch:      strPtr("pr-999"),
			CommitSHA:   strPtr("deadbeef1234"),
			GHPRNumber:  intPtr(999),
		},
		LatestVerdict: &kpi.FailureVerdict{
			ID:         "v-1",
			Label:      "flaky-infra",
			DecidedBy:  strPtr("llm"),
			Rule:       strPtr("sig-match"),
			Confidence: floatPtr(0.92),
			Summary:    strPtr(`Intermittent <script>alert("xss")</script> timeout`),
			Model:      strPtr("claude-sonnet"),
			TriageURL:  strPtr(`javascript:alert(1)`), // Unsafe scheme must be stripped
			CreatedAt:  now.Add(-1 * time.Hour),
		},
		DailySeries: []kpi.FailureDailyPoint{
			{Date: "2026-11-25", OccurrenceCount: 5, RawAttemptCount: 6, GroupCount: 2, AffectedTestRuns: 100, RatePer1k: floatPtr(50.0)},
		},
		AffectedTests: []kpi.FailureAffectedTest{
			{TestID: "t-1", FullTitle: `Login <script>alert(3)</script>`, File: "e2e/login.ts", Repository: "mattermost/mattermost", Framework: "playwright", FailCount: 9, RunCount: 300, GroupCount: 4, LastSeenAt: now},
		},
		AffectedRepos: []kpi.FailureAffectedRepo{
			{Repository: "mattermost/mattermost", App: "mattermost", OccurrenceCount: 9, GroupCount: 4, AffectedRuns: 300, RatePer1k: floatPtr(30.0), Lanes: []string{"e2e-pr"}},
		},
		PRNumbers: []int{999, 1001},
	}

	page := BuildKPIFailureDetailPage(SiteInfo{}, detail, now)
	htmlBytes, err := RenderKPIFailureDetail(page)
	if err != nil {
		t.Fatalf("RenderKPIFailureDetail: %v", err)
	}
	s := string(htmlBytes)

	for _, forbid := range []string{
		`<script>alert`,
		`<img src=x`,
		`javascript:alert`,
		`Fixed`,
		`error_context`,
	} {
		if strings.Contains(s, forbid) {
			t.Fatalf("RenderKPIFailureDetail must not contain %q", forbid)
		}
	}
	if strings.Contains(strings.ToLower(s), "blocked") {
		t.Fatal("RenderKPIFailureDetail must not contain 'blocked'")
	}

	for _, want := range []string{
		`&lt;script&gt;alert(1)&lt;/script&gt;`,
		`40.0 / 1k`,
		`10.0 / 1k`,
		`&#43;30.0 / 1k`,
		`PR #999`,
		`deadbee`,
		`Run verdict: flaky-infra`,
		`92%`,
		`Dormant`,
		`e2e/login.ts`,
		`#1001`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("RenderKPIFailureDetail missing %q", want)
		}
	}
}

func TestKPIViewModelsHaveNoAuthorOrErrorContextFields(t *testing.T) {
	forbidden := []string{"author", "committer", "errorcontext", "error_context"}
	var checkStruct func(t *testing.T, typ reflect.Type, path string)
	visited := map[reflect.Type]bool{}
	checkStruct = func(t *testing.T, typ reflect.Type, path string) {
		t.Helper()
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || visited[typ] {
			return
		}
		visited[typ] = true
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			lower := strings.ToLower(f.Name)
			for _, bad := range forbidden {
				if strings.Contains(lower, bad) {
					t.Fatalf("forbidden field %s.%s matching %q", path, f.Name, bad)
				}
			}
			checkStruct(t, f.Type, path+"."+f.Name)
		}
	}
	checkStruct(t, reflect.TypeOf(KPIOverviewPage{}), "KPIOverviewPage")
	checkStruct(t, reflect.TypeOf(KPIPRsPage{}), "KPIPRsPage")
	checkStruct(t, reflect.TypeOf(KPIFlakyPage{}), "KPIFlakyPage")
	checkStruct(t, reflect.TypeOf(KPIFailureDetailPage{}), "KPIFailureDetailPage")
}

func TestKPIHandlers_ValidationAndNilStore(t *testing.T) {
	// Nil KPI store -> 503 across all 4 KPI handlers.
	hNil := &Handler{Logger: slog.New(slog.DiscardHandler)}
	for _, tc := range []struct {
		name string
		fn   http.HandlerFunc
		path string
	}{
		{"overview_nil", hNil.ServeKPIOverview, "/kpi"},
		{"prs_nil", hNil.ServeKPIPRs, "/kpi/prs"},
		{"flaky_nil", hNil.ServeKPIFlaky, "/kpi/flaky"},
		{"failure_nil", hNil.ServeKPIFailureDetail, "/kpi/failures/019a1234-5678-7abc-8def-0123456789ab"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		tc.fn(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503", tc.name, rec.Code)
		}
	}

	// Non-nil KPI store with nil pool still returns 503; let's test ParsePRsRedParams validation directly.
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	for _, bad := range []url.Values{
		{"window": {"5d"}},
		{"stage": {"unknown"}},
		{"sort": {"bad_sort"}},
		{"page": {"0"}},
		{"page": {"abc"}},
	} {
		if _, err := kpi.ParsePRsRedParams(bad, now); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}

	// Verify chi router mounts /kpi, /kpi/prs, /kpi/flaky, /kpi/failures/{id} when ServeKPI* wired.
	r := chi.NewRouter()
	h := &Handler{}
	r.Get("/kpi", h.ServeKPIOverview)
	r.Get("/kpi/prs", h.ServeKPIPRs)
	r.Get("/kpi/flaky", h.ServeKPIFlaky)
	r.Get("/kpi/failures/{id}", h.ServeKPIFailureDetail)
	for _, path := range []string{"/kpi", "/kpi/prs", "/kpi/flaky", "/kpi/failures/not-a-uuid"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET %s with nil store = %d, want 503", path, rec.Code)
		}
	}
}

func TestKPIPaginationOutOfRangeRecovery(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)

	t.Run("overview_page_gt_total_pages_with_multiple_pages", func(t *testing.T) {
		params, err := kpi.ParseFailuresParams(url.Values{
			"app":    {"mattermost"},
			"window": {"14d"},
			"sort":   {kpi.SortRateDesc},
			"page":   {"5"},
		}, now)
		if err != nil {
			t.Fatalf("ParseFailuresParams: %v", err)
		}

		page := BuildKPIOverviewPage(
			SiteInfo{},
			kpi.FilterOptions{},
			kpi.SummaryResponse{Filters: params},
			kpi.FailuresResponse{
				Filters: params,
				Pagination: kpi.Pagination{
					Page:       5,
					Limit:      50,
					Total:      57,
					TotalPages: 2,
				},
				Failures: []kpi.RecurringFailureRow{},
			},
			now,
		)
		if !page.ShowPagination || !page.HasPrev || page.HasNext {
			t.Fatalf("unexpected pagination flags: Show=%v HasPrev=%v HasNext=%v", page.ShowPagination, page.HasPrev, page.HasNext)
		}
		if !strings.Contains(page.PrevHref, "page=2") || !strings.Contains(page.PrevHref, "app=mattermost") || !strings.Contains(page.PrevHref, "sort=rate_desc") {
			t.Fatalf("expected PrevHref to jump directly to page=2 preserving filters, got %q", page.PrevHref)
		}

		htmlBytes, err := RenderKPIOverview(page)
		if err != nil {
			t.Fatalf("RenderKPIOverview: %v", err)
		}
		s := string(htmlBytes)
		for _, want := range []string{
			`57 failure patterns`,
			`Page 5 out of range`,
			`No failure patterns on page 5 (total 57 across 2 pages).`,
			`kpi-pagination`,
			`href="?app=mattermost&amp;page=2&amp;sort=rate_desc&amp;window=14d">Previous</a>`,
			`<span class="btn-page is-disabled" aria-disabled="true">Next</span>`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("out-of-range overview missing %q", want)
			}
		}
		if strings.Contains(s, "No active failure patterns") {
			t.Fatal("out-of-range overview must NOT say 'No active failure patterns'")
		}
	})

	t.Run("prs_page_gt_total_pages_with_multiple_pages", func(t *testing.T) {
		params, err := kpi.ParsePRsRedParams(url.Values{
			"app":  {"mattermost"},
			"sort": {kpi.SortAgeDesc},
			"page": {"5"},
		}, now)
		if err != nil {
			t.Fatalf("ParsePRsRedParams: %v", err)
		}

		page := BuildKPIPRsPage(
			SiteInfo{},
			kpi.FilterOptions{},
			kpi.PRRedResponse{
				Filters: params,
				Pagination: kpi.Pagination{
					Page:       5,
					Limit:      50,
					Total:      57,
					TotalPages: 2,
				},
				Lanes: []kpi.RedPRLaneRow{},
			},
			now,
		)
		if !page.ShowPagination || !page.HasPrev || page.HasNext {
			t.Fatalf("unexpected PRs pagination flags: Show=%v HasPrev=%v HasNext=%v", page.ShowPagination, page.HasPrev, page.HasNext)
		}
		if !strings.Contains(page.PrevHref, "page=2") {
			t.Fatalf("expected PrevHref to jump to page=2, got %q", page.PrevHref)
		}

		htmlBytes, err := RenderKPIPRs(page)
		if err != nil {
			t.Fatalf("RenderKPIPRs: %v", err)
		}
		s := string(htmlBytes)
		for _, want := range []string{
			`57 red PR runs total · 0 still red`,
			`Page 5 out of range`,
			`No red PR runs on page 5 (total 57 across 2 pages).`,
			`kpi-pagination`,
			`href="?app=mattermost&amp;page=2&amp;sort=age_desc">Previous</a>`,
			`<span class="btn-page is-disabled" aria-disabled="true">Next</span>`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("out-of-range PRs missing %q", want)
			}
		}
		if strings.Contains(s, "No E2E red PR runs") {
			t.Fatal("out-of-range PRs must NOT say 'No E2E red PR runs'")
		}
	})

	t.Run("page_gt_total_pages_when_single_page_exists", func(t *testing.T) {
		params, err := kpi.ParseFailuresParams(url.Values{"page": {"4"}}, now)
		if err != nil {
			t.Fatal(err)
		}
		page := BuildKPIOverviewPage(
			SiteInfo{},
			kpi.FilterOptions{},
			kpi.SummaryResponse{Filters: params},
			kpi.FailuresResponse{
				Filters: params,
				Pagination: kpi.Pagination{
					Page:       4,
					Limit:      50,
					Total:      10,
					TotalPages: 1,
				},
			},
			now,
		)
		if !page.ShowPagination || !page.HasPrev || page.HasNext || page.PrevHref != "?" {
			t.Fatalf("unexpected single-page recovery pagination: %+v", page)
		}
	})
}

func TestKPIHardening_URLSanitizationPathEscapeUTF8AndMobileCSS(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)

	// 1. Protocol-relative RunURL rejected in BuildKPIPRsPage.
	prParams, err := kpi.ParsePRsRedParams(url.Values{}, now)
	if err != nil {
		t.Fatal(err)
	}
	prPage := BuildKPIPRsPage(SiteInfo{}, kpi.FilterOptions{}, kpi.PRRedResponse{
		Filters:    prParams,
		Pagination: kpi.Pagination{Page: 1, Limit: 50, Total: 2, TotalPages: 1},
		Lanes: []kpi.RedPRLaneRow{
			{ReportGroupID: "rg-proto", GHPRNumber: 101, RunURL: "//evil.example/phish", CreatedAt: now},
			{ReportGroupID: "rg-bslash", GHPRNumber: 102, RunURL: `/\evil.example/phish`, CreatedAt: now},
		},
	}, now)
	if prPage.Lanes[0].RunURL != "" || prPage.Lanes[1].RunURL != "" {
		t.Fatalf("expected unsafe RunURLs to neutralize to empty string, got %q and %q", prPage.Lanes[0].RunURL, prPage.Lanes[1].RunURL)
	}

	// 2. SignatureID path-escaped in BuildKPIOverviewPage.
	ovParams, err := kpi.ParseFailuresParams(url.Values{}, now)
	if err != nil {
		t.Fatal(err)
	}
	ovPage := BuildKPIOverviewPage(SiteInfo{}, kpi.FilterOptions{}, kpi.SummaryResponse{Filters: ovParams}, kpi.FailuresResponse{
		Filters:    ovParams,
		Pagination: kpi.Pagination{Page: 1, Limit: 50, Total: 1, TotalPages: 1},
		Failures: []kpi.RecurringFailureRow{
			{
				SignatureID:       "../admin?x=1#frag",
				NormalizedMessage: "boom",
				LastSeenAt:        now,
			},
		},
	}, now)
	wantHref := "/kpi/failures/" + url.PathEscape("../admin?x=1#frag")
	if ovPage.Failures[0].Href != wantHref {
		t.Fatalf("Signature Href = %q, want %q", ovPage.Failures[0].Href, wantHref)
	}

	// 3. Multi-byte UTF-8 rune truncation in shortSignatureLabel.
	multiByte := strings.Repeat("日本語テスト", 12) // 72 runes, 216 bytes
	gotLabel := shortSignatureLabel(nil, multiByte)
	if !utf8.ValidString(gotLabel) {
		t.Fatalf("shortSignatureLabel produced invalid UTF-8: %q", gotLabel)
	}
	if runeLen := utf8.RuneCountInString(gotLabel); runeLen != 64 {
		t.Fatalf("shortSignatureLabel rune count = %d, want 64 (%q)", runeLen, gotLabel)
	}
	if !strings.HasSuffix(gotLabel, "...") {
		t.Fatalf("shortSignatureLabel missing ellipsis: %q", gotLabel)
	}

	// 4. Verify 480px wrapping CSS rules exist in embedded styleCSS.
	if !strings.Contains(styleCSS, ".site-header-bar {\n  display: flex;\n  flex-wrap: wrap;") {
		t.Fatal("expected .site-header-bar to include flex-wrap: wrap")
	}
	if !strings.Contains(styleCSS, ".kpi-card-substats > div {\n  display: flex;\n  flex-wrap: wrap;") {
		t.Fatal("expected .kpi-card-substats > div to include flex-wrap: wrap")
	}
}

func TestKPICustomLimitPreservation(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)

	t.Run("prs_custom_limit_preserved_in_links_and_filter_form", func(t *testing.T) {
		params, err := kpi.ParsePRsRedParams(url.Values{
			"app":   {"mattermost"},
			"sort":  {kpi.SortAgeDesc},
			"limit": {"10"},
			"page":  {"2"},
		}, now)
		if err != nil {
			t.Fatalf("ParsePRsRedParams: %v", err)
		}

		page := BuildKPIPRsPage(SiteInfo{}, kpi.FilterOptions{Apps: []string{"mattermost"}}, kpi.PRRedResponse{
			Filters: params,
			Pagination: kpi.Pagination{
				Page:       2,
				Limit:      10,
				Total:      25,
				TotalPages: 3,
			},
			Lanes: []kpi.RedPRLaneRow{
				{ReportGroupID: "rg-1", GHPRNumber: 101, Repository: "mattermost/mattermost", Lane: "e2e", CreatedAt: now},
			},
		}, now)

		if page.Filters.State.Limit != 10 || !page.Filters.State.HasCustomLimit() || !page.Filters.HasActiveFilters {
			t.Fatalf("expected custom limit=10 and HasActiveFilters=true, got %+v", page.Filters.State)
		}
		if page.PrevHref != "?app=mattermost&limit=10&sort=age_desc" {
			t.Fatalf("PrevHref = %q, want %q", page.PrevHref, "?app=mattermost&limit=10&sort=age_desc")
		}
		if page.NextHref != "?app=mattermost&limit=10&page=3&sort=age_desc" {
			t.Fatalf("NextHref = %q, want %q", page.NextHref, "?app=mattermost&limit=10&page=3&sort=age_desc")
		}
		if page.OverviewHref != "/kpi?app=mattermost&limit=10" {
			t.Fatalf("OverviewHref = %q, want %q", page.OverviewHref, "/kpi?app=mattermost&limit=10")
		}
		if page.PRsHref != "/kpi/prs?app=mattermost&limit=10" {
			t.Fatalf("PRsHref = %q, want %q", page.PRsHref, "/kpi/prs?app=mattermost&limit=10")
		}

		htmlBytes, err := RenderKPIPRs(page)
		if err != nil {
			t.Fatalf("RenderKPIPRs: %v", err)
		}
		s := string(htmlBytes)
		for _, want := range []string{
			`limit=10`,
			`href="?app=mattermost&amp;limit=10&amp;sort=age_desc">Previous</a>`,
			`href="?app=mattermost&amp;limit=10&amp;page=3&amp;sort=age_desc">Next</a>`,
			`href="/kpi/prs" class="kpi-btn kpi-btn-ghost">Reset</a>`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("rendered /kpi/prs missing %q", want)
			}
		}
	})

	t.Run("overview_custom_limit_100_and_only_limit_active", func(t *testing.T) {
		params, err := kpi.ParseFailuresParams(url.Values{"limit": {"100"}}, now)
		if err != nil {
			t.Fatalf("ParseFailuresParams: %v", err)
		}

		page := BuildKPIOverviewPage(
			SiteInfo{},
			kpi.FilterOptions{},
			kpi.SummaryResponse{Filters: params},
			kpi.FailuresResponse{
				Filters:    params,
				Pagination: kpi.Pagination{Page: 1, Limit: 100, Total: 150, TotalPages: 2},
				Failures: []kpi.RecurringFailureRow{
					{SignatureID: "sig-1", NormalizedMessage: "boom", LastSeenAt: now},
				},
			},
			now,
		)

		if !page.Filters.HasActiveFilters {
			t.Fatal("expected HasActiveFilters=true when custom limit=100 is active")
		}
		if page.NextHref != "?limit=100&page=2" {
			t.Fatalf("NextHref = %q, want %q", page.NextHref, "?limit=100&page=2")
		}
		if page.PRsHref != "/kpi/prs?limit=100" {
			t.Fatalf("PRsHref = %q, want %q", page.PRsHref, "/kpi/prs?limit=100")
		}
		if page.Failures[0].Href != "/kpi/failures/sig-1?limit=100" {
			t.Fatalf("Failure Href = %q, want %q", page.Failures[0].Href, "/kpi/failures/sig-1?limit=100")
		}

		htmlBytes, err := RenderKPIOverview(page)
		if err != nil {
			t.Fatalf("RenderKPIOverview: %v", err)
		}
		s := string(htmlBytes)
		for _, want := range []string{
			`href="/kpi?limit=100"`,
			`href="?limit=100&amp;page=2">Next</a>`,
			`href="/kpi" class="kpi-btn kpi-btn-ghost">Reset</a>`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("rendered /kpi missing %q", want)
			}
		}
	})

	t.Run("default_limit_omits_limit_param_and_hidden_input", func(t *testing.T) {
		params, err := kpi.ParsePRsRedParams(url.Values{"limit": {"50"}}, now)
		if err != nil {
			t.Fatalf("ParsePRsRedParams: %v", err)
		}
		page := BuildKPIPRsPage(SiteInfo{}, kpi.FilterOptions{}, kpi.PRRedResponse{
			Filters:    params,
			Pagination: kpi.Pagination{Page: 1, Limit: 50, Total: 80, TotalPages: 2},
			Lanes:      []kpi.RedPRLaneRow{{ReportGroupID: "rg-1", GHPRNumber: 1, CreatedAt: now}},
		}, now)

		if page.Filters.State.HasCustomLimit() || page.Filters.HasActiveFilters {
			t.Fatalf("expected default limit=50 not to mark custom/active filters, got %+v", page.Filters)
		}
		if page.NextHref != "?page=2" {
			t.Fatalf("NextHref = %q, want %q", page.NextHref, "?page=2")
		}
		if got := kpiQueryString(KPIFilterState{}, 1, "", false); got != "" {
			t.Fatalf("zero-value KPIFilterState kpiQueryString = %q, want empty", got)
		}
	})
}

func TestRenderKPI_EngProdUXClarityAndSmallSamples(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	// 1. Small-sample failure detail: 3 failures in 3 test runs across 1 CI run, duplicate SampleMessage, 30 days (no daily table, no SHA-256 box).
	dailySeries := make([]kpi.FailureDailyPoint, 0, 30)
	for d := 0; d < 30; d++ {
		dt := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Format("2006-01-02")
		if dt == "2026-11-21" {
			dailySeries = append(dailySeries, kpi.FailureDailyPoint{
				Date:             dt,
				OccurrenceCount:  3,
				RawAttemptCount:  3,
				GroupCount:       1,
				AffectedTestRuns: 3,
				RatePer1k:        floatPtr(1000.0),
			})
		} else {
			dailySeries = append(dailySeries, kpi.FailureDailyPoint{Date: dt})
		}
	}

	msg := "Assertion is false: id: mute-unmute is visible"
	detail := kpi.FailureDetailResponse{
		Filters:              kpi.FilterParams{Window: kpi.Window30d, WindowDays: 30},
		RequestedSignatureID: "019a1234-5678-7abc-8def-0123456789ab",
		CanonicalSignatureID: "019a1234-5678-7abc-8def-0123456789ab",
		Fingerprint:          "f273fbcfb0ce0bbea78611817c57dcc214f532910bcfd29133b5e791ca1e949a",
		AlgoVersion:          2,
		NormalizedMessage:    msg,
		SampleMessage:        strPtr(msg), // Exact duplicate must be suppressed.
		State:                kpi.FailureStateNotEnoughRuns,
		OccurrenceCount:      3,
		RawAttemptCount:      3,
		GroupCount:           1,
		AffectedTestCount:    3,
		AffectedTestRuns:     3,
		RatePer1k:            floatPtr(1000.0),
		Occurrences14d:       3,
		AffectedRuns14d:      3,
		RatePer1k14d:         floatPtr(1000.0),
		PriorOccurrences14d:  0,
		PriorAffectedRuns14d: 0,
		DeltaRatePer1k:       floatPtr(1000.0),
		LastSeenAt:           floatTimePtr(now.Add(-7 * 24 * time.Hour)),
		FirstSeen: kpi.FailureFirstSeen{
			FirstSeenAt: now.Add(-7 * 24 * time.Hour),
			Repository:  strPtr("mattermost/mattermost-mobile"),
			Lane:        strPtr("mobile-main-maestro-android-e2e"),
			Branch:      strPtr("main"),
			CommitSHA:   strPtr("731b710abcdef"),
		},
		DailySeries: dailySeries,
		AffectedTests: []kpi.FailureAffectedTest{
			{
				TestID:     "t-1",
				FullTitle:  "Maestro E2E > mute_unmute",
				File:       "e2e/mute_unmute.yaml",
				Repository: "mattermost/mattermost-mobile",
				Framework:  "maestro",
				FailCount:  1,
				RunCount:   1,
				GroupCount: 1,
				LastSeenAt: now.Add(-7 * 24 * time.Hour),
			},
		},
		AffectedRepos: []kpi.FailureAffectedRepo{
			{
				Repository:      "mattermost/mattermost-mobile",
				App:             "mobile",
				OccurrenceCount: 3,
				GroupCount:      1,
				AffectedRuns:    3,
				RatePer1k:       floatPtr(1000.0),
				Lanes:           []string{"mobile-main-maestro-android-e2e"},
			},
		},
	}

	detailHTMLBytes, err := RenderKPIFailureDetail(BuildKPIFailureDetailPage(SiteInfo{}, detail, now))
	if err != nil {
		t.Fatalf("RenderKPIFailureDetail: %v", err)
	}
	ds := string(detailHTMLBytes)

	for _, want := range []string{
		`Dormant`,
		`Last 30 days`,
		`100% (3 of 3)`,
		`Low sample`,
		`New in 14d`,
		`3 failures`,
		`1 CI run`,
		`1 active day`,
		`2026-11-21: 3 failures (100% (3 of 3), 3 test runs)`,
		`Repository:`,
		`mobile-main-maestro-android-e2e`,
	} {
		if !strings.Contains(ds, want) {
			t.Fatalf("small-sample failure detail missing %q", want)
		}
	}
	for _, forbid := range []string{
		`1000.0 / 1k`,
		`class="kpi-sample-msg"`,
		`Technical details`,
		`Not enough runs`,
		`1 runs`,
		`1 groups`,
		`<td>2026-10-30</td>`,
	} {
		if strings.Contains(ds, forbid) {
			t.Fatalf("small-sample failure detail must not contain %q", forbid)
		}
	}
	idxTests := strings.Index(ds, `Affected Tests`)
	idxDaily := strings.Index(ds, `Daily Failures`)
	if idxTests == -1 || idxDaily == -1 || idxTests > idxDaily {
		t.Fatalf("expected Affected Tests (%d) to render before Daily Failures (%d)", idxTests, idxDaily)
	}

	// 2. Per-repo Scorecard on All Repos avoids blended fake averages & leaks zero raw column names.
	ovParams, err := kpi.ParseFailuresParams(url.Values{}, now)
	if err != nil {
		t.Fatal(err)
	}
	p50Web := 744097.5
	p90Web := 1252457.0
	ovHTMLBytes, err := RenderKPIOverview(BuildKPIOverviewPage(
		SiteInfo{},
		kpi.FilterOptions{Repos: []string{"mattermost/mattermost", "mattermost/mattermost-mobile", "mattermost/desktop"}},
		kpi.SummaryResponse{
			Filters: ovParams,
			MainPassRate: kpi.MainPassRate{
				ByLane: []kpi.MainLaneRow{
					{App: "mattermost", Repository: "mattermost/mattermost", Lane: "pw-main", LanesPassed: 11, LanesTotal: 11, StrictLanesPassed: 11, StrictLanesTotal: 11, FirstAttemptPassed: 1015, TestsTotal: 1026, OrchestratedRuns: 11},
					{App: "mobile", Repository: "mattermost/mattermost-mobile", Lane: "mob-main", LanesPassed: 2, LanesTotal: 7, FirstAttemptPassed: 322, TestsTotal: 348, UploadOnlyRuns: 7},
				},
			},
			E2EDuration: kpi.E2EDurationSummary{
				P50Ms: &p50Web, P90Ms: &p90Web, SampleCount: 46,
				ByLane: []kpi.DurationLaneRow{
					{App: "mattermost", Repository: "mattermost/mattermost", Lane: "pw-pr", P50Ms: &p50Web, P90Ms: &p90Web, SampleCount: 46, OrchestratedRuns: 46},
				},
			},
			FlakyTests: kpi.FlakySummary{
				Count: 20,
				ByApp: []kpi.AppFlakyCount{{App: "mattermost", Count: 3, Delta: intPtr(3)}, {App: "mobile", Count: 17}},
			},
			PRFalseRed: &kpi.PRFalseRedSummary{
				IsLowerBound: true, RateQualifier: "at least", SettledAfterHours: 24,
				SettledRedLanes: 23, PriorSettledRedLanes: 1, FalseRedRate: floatPtr(0), PriorFalseRedRate: floatPtr(100), DeltaPP: floatPtr(-100),
				ReasonMix: kpi.PRFalseRedReasonMix{Other: 23, Total: 23},
			},
		},
		kpi.FailuresResponse{
			Filters: ovParams,
			Summary: kpi.RecurringFailuresSummary{TotalSignatures: 349, ActiveCount: 167, NotEnoughRunsCount: 182},
		},
		now,
	))
	if err != nil {
		t.Fatalf("RenderKPIOverview scorecard: %v", err)
	}
	ovs := string(ovHTMLBytes)
	for _, want := range []string{
		`Repository Health Scorecard`,
		`Web &amp; Server`,
		`100.0% (11/11)`,
		`28.6% (2/7)`,
		`12m 24s`,
		`Active (167)`,
		`All (349)`,
	} {
		if !strings.Contains(ovs, want) {
			t.Fatalf("All Repos scorecard missing %q", want)
		}
	}
	for _, forbid := range []string{`total_duration_ms`, `test_stats_json`, `Not enough runs: 182`, `-100.0 pp`} {
		if strings.Contains(ovs, forbid) {
			t.Fatalf("All Repos scorecard must not contain %q", forbid)
		}
	}

	// 3. Single-repo PR E2E suppresses noisy -100.0 pp when prior_settled_red_lanes < 10 and labels 100% Other as Unclassified instead of Healthy.
	prParams, err := kpi.ParsePRsRedParams(url.Values{"repo": {"mattermost/desktop"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	prHTMLBytes, err := RenderKPIPRs(BuildKPIPRsPage(
		SiteInfo{},
		kpi.FilterOptions{Repos: []string{"mattermost/mattermost", "mattermost/desktop"}},
		kpi.PRRedResponse{
			Filters:              prParams,
			SettledAfterHours:    24,
			IsLowerBound:         true,
			RateQualifier:        "at least",
			FalseRedRate:         floatPtr(0),
			PriorFalseRedRate:    floatPtr(100),
			DeltaPP:              floatPtr(-100),
			SettledRedLanes:      8,
			PriorSettledRedLanes: 1,
			ReasonMix:            kpi.PRFalseRedReasonMix{Other: 8, Total: 8},
		},
		now,
	))
	if err != nil {
		t.Fatalf("RenderKPIPRs desktop noise suppression: %v", err)
	}
	prs := string(prHTMLBytes)
	if !strings.Contains(prs, `0% passed rerun (&gt;24h)`) {
		t.Fatal("expected '0% passed rerun (>24h)' badge when ReasonMix is 100% Other")
	}
	for _, forbid := range []string{`-100.0 pp`, `Healthy`, `Configured E2E lanes:`, `test_stats_json`, `total_duration_ms`} {
		if strings.Contains(prs, forbid) {
			t.Fatalf("PRs page must not contain %q", forbid)
		}
	}
}

func floatTimePtr(t time.Time) *time.Time { return &t }

func TestKPIURLAndPaginationHardening(t *testing.T) {
	// safeExternalURL rejects control characters and non-HTTP(S) schemes.
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"https://github.com/mattermost/mattermost/issues/1", "https://github.com/mattermost/mattermost/issues/1"},
		{"https://github.com/a\nb", ""},
		{"http://example.com/\tfoo", ""},
		{"javascript:alert(1)", ""},
		{"https://", ""},
	} {
		raw := tc.raw
		if got := safeExternalURL(&raw); got != tc.want {
			t.Errorf("safeExternalURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	// isSafeLocalReportHref rejects external hosts and control chars.
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"/", true},
		{"/reports/mattermost/main/abcdef1/e2e", true},
		{"https://evil.example/reports/1", false},
		{"//evil.example/reports/1", false},
		{"/\t/reports/1", false},
		{"/other/path", false},
	} {
		if got := isSafeLocalReportHref(tc.raw); got != tc.want {
			t.Errorf("isSafeLocalReportHref(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}

	// BuildKPIPRsPage neutralizes unsafe or root RunURL to empty (never falls back to /reports or /).
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	prParams, _ := kpi.ParsePRsRedParams(url.Values{}, now)
	prsPage := BuildKPIPRsPage(SiteInfo{}, kpi.FilterOptions{}, kpi.PRRedResponse{
		Filters: prParams,
		Lanes: []kpi.RedPRLaneRow{
			{ReportGroupID: "rg-ext", GHPRNumber: 9, Repository: "mattermost/mattermost", Lane: "e2e", RunURL: "https://phish.example/evil", CreatedAt: now},
			{ReportGroupID: "rg-root", GHPRNumber: 10, Repository: "mattermost/mattermost", Lane: "e2e", RunURL: "/", CreatedAt: now},
		},
	}, now)
	if len(prsPage.Lanes) != 2 || prsPage.Lanes[0].RunURL != "" || prsPage.Lanes[1].RunURL != "" {
		t.Fatalf("unsafe/root RunURL not neutralized to empty: %+v", prsPage.Lanes)
	}

	// ServeHome, ServeRepo, and ServeKPIOverview reject overflowing pages & malformed queries with 400.
	h := &Handler{
		Pool: new(pgxpool.Pool),
		KPI:  &kpi.Store{Pool: new(pgxpool.Pool)},
	}
	r := chi.NewRouter()
	r.Get("/", h.ServeHome)
	r.Get("/reports/{repo}", h.ServeRepo)
	r.Get("/kpi", h.ServeKPIOverview)

	for _, path := range []string{
		"/?page=1001",
		"/reports/mattermost?page=1001",
		"/kpi?window=%ZZ",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400", path, rec.Code)
		}
	}

	// Single-lane views hide irrelevant empty Main/PR cards and always render pagination when rows exist.
	mainLaneParams, _ := kpi.ParseFailuresParams(url.Values{"repo": {"mattermost/mattermost-mobile"}, "lane": {"mobile-main-detox-android"}}, now)
	mainLanePage := BuildKPIOverviewPageWithShow(SiteInfo{}, kpi.FilterOptions{
		Repos: []string{"mattermost/mattermost-mobile"},
		Lanes: []string{"mobile-main-detox-android"},
	}, kpi.SummaryResponse{
		Filters: mainLaneParams,
		MainPassRate: kpi.MainPassRate{
			TestLevel: kpi.TestLevelPassRate{FirstAttemptPassed: 94, TotalTests: 95, PassRate: floatPtr(98.95)},
			LaneLevel: kpi.LaneLevelPassRate{TotalLanes: 1, UploadOnlyLanesCount: 1},
		},
	}, kpi.FailuresResponse{
		Filters:    mainLaneParams,
		Pagination: kpi.Pagination{Page: 1, Limit: 50, Total: 1, TotalPages: 1},
		Failures:   []kpi.RecurringFailureRow{{SignatureID: "sig-1", NormalizedMessage: "detox err", State: kpi.FailureStateNotEnoughRuns, OccurrenceCount: 1, LastSeenAt: now}},
	}, now, true)
	if !mainLanePage.MainPass.ShowCard || mainLanePage.PRFalseRed.ShowCard {
		t.Fatalf("main-only lane card visibility = (main=%v, pr=%v), want (true, false)", mainLanePage.MainPass.ShowCard, mainLanePage.PRFalseRed.ShowCard)
	}
	if !mainLanePage.ShowPagination {
		t.Fatal("expected ShowPagination=true when Total=1")
	}
	mainLaneHTML, err := RenderKPIOverview(mainLanePage)
	if err != nil {
		t.Fatalf("RenderKPIOverview main lane: %v", err)
	}
	if !strings.Contains(string(mainLaneHTML), "Page 1 of 1") {
		t.Fatal("expected single-page pagination summary 'Page 1 of 1'")
	}
	if strings.Contains(string(mainLaneHTML), "PR false-red rate") {
		t.Fatal("main-only single-lane page must hide empty PR false-red rate card")
	}
	if !strings.Contains(string(mainLaneHTML), "Low sample") {
		t.Fatal("single-lane page with n=1 run must display Low sample badge instead of red Action needed")
	}
	if strings.Contains(string(mainLaneHTML), "Lanes in Mobile") {
		t.Fatal("single-lane page must hide redundant 1-row Lanes table")
	}

	// RenderKPIFlaky, GitHub URL validation, and per-repo scorecard Timeouts isolation.
	flakyParams, _ := kpi.ParseFlakyParams(url.Values{"repo": {"mattermost/desktop"}}, now)
	flakyPage := BuildKPIFlakyPage(SiteInfo{}, kpi.FilterOptions{Repos: []string{"mattermost/desktop"}}, kpi.FlakyResponse{
		Filters:         flakyParams,
		WindowDays:      14,
		MinFlakyCommits: 2,
		Pagination:      kpi.Pagination{Page: 1, Limit: 50, Total: 1, TotalPages: 1},
		Tests: []kpi.FlakyTestRow{{
			TestID: "t-flaky-1", ZephyrKey: strPtr("MM-T4055"), File: "e2e/links.spec.ts",
			FullTitle: "Links > opens external URL", Repository: "mattermost/desktop",
			Framework: "playwright", FlakyCommitCount: 3, TotalCommitCount: 4, TotalRuns: 5,
			FlakeRate: 75.0, LastFlakyAt: now.Add(-2 * time.Hour), LastCommitSHA: "abcdef1234567",
			Lanes: []string{"desktop-pr"},
		}},
	}, now)
	flakyHTML, err := RenderKPIFlaky(flakyPage)
	if err != nil {
		t.Fatalf("RenderKPIFlaky: %v", err)
	}
	fs := string(flakyHTML)
	for _, want := range []string{`Flaky Tests`, `MM-T4055`, `3 / 4`, `75.0%`, `desktop-pr`, `https://github.com/mattermost/desktop/commit/abcdef1234567`, `Page 1 of 1`} {
		if !strings.Contains(fs, want) {
			t.Fatalf("RenderKPIFlaky missing %q", want)
		}
	}
	if githubPRURL("../evil", 10) != "" || githubCommitURL("mattermost/desktop", "not-a-sha!") != "" {
		t.Fatal("GitHub URL helpers must reject unsafe repo slugs or non-hex SHAs")
	}
	ovAllParams, _ := kpi.ParseFailuresParams(url.Values{}, now)
	ovTimeoutPage := BuildKPIOverviewPage(SiteInfo{}, kpi.FilterOptions{Repos: []string{"mattermost/mattermost"}}, kpi.SummaryResponse{
		Filters: ovAllParams,
		MainPassRate: kpi.MainPassRate{ByLane: []kpi.MainLaneRow{
			{App: "mattermost", Repository: "mattermost/mattermost", Lane: "main", LanesPassed: 5, LanesTotal: 5, OrchestratedRuns: 5},
		}},
		Triage: &kpi.TriageSummary{RunCompletion: kpi.RunCompletionSummary{
			TotalRuns: 10, TimedOutRuns: 9, TimedOutByRepo: map[string]int{"mattermost/mattermost": 2},
		}},
	}, kpi.FailuresResponse{Filters: ovAllParams}, now)
	if len(ovTimeoutPage.RepoScorecard) != 1 || ovTimeoutPage.RepoScorecard[0].TimeoutsText != "2" {
		t.Fatalf("expected per-repo timeouts '2' (not global 9), got %+v", ovTimeoutPage.RepoScorecard)
	}
}

func TestKPIHardeningPass_P0AndP1(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	// P0-1, P0-2, P0-4, P1-2, P1-6 on Overview:
	// - Single-lane pulse names repo+lane and marks low sample
	// - Window != 14d qualifies flaky count with (14d) and kpiFlakyHref stays on 14d
	// - PR run time completion uses PRRunCompletion (stage='pr'), never all-stage RunCompletion
	// - ShowAll preserved on RepoTabs / WindowPills / failure drilldown links
	// - Lane filter without Repo still enables ShowLaneSelect and keeps All Repos inactive
	ovParams, err := kpi.ParseFailuresParams(url.Values{
		"repo":   {"mattermost/mattermost-mobile"},
		"lane":   {"mobile-main-detox-android"},
		"window": {"7d"},
	}, now)
	if err != nil {
		t.Fatalf("ParseFailuresParams: %v", err)
	}
	prComp := 90.0
	allComp := 50.0
	ovPage := BuildKPIOverviewPageWithShow(
		SiteInfo{},
		kpi.FilterOptions{Repos: []string{"mattermost/mattermost", "mattermost/mattermost-mobile"}},
		kpi.SummaryResponse{
			Filters: ovParams,
			MainPassRate: kpi.MainPassRate{
				LaneLevel: kpi.LaneLevelPassRate{PassedLanes: 0, TotalLanes: 1, PassRate: floatPtr(0)},
			},
			FlakyTests:  kpi.FlakySummary{Count: 3},
			E2EDuration: kpi.E2EDurationSummary{SampleCount: 10, P50Ms: floatPtr(60000), P90Ms: floatPtr(90000)},
			Triage: &kpi.TriageSummary{
				RunCompletion: kpi.RunCompletionSummary{
					TotalRuns: 100, CompletedRuns: 50, TimedOutRuns: 10, CompletionRate: &allComp,
				},
				PRRunCompletion: &kpi.RunCompletionSummary{
					TotalRuns: 10, CompletedRuns: 9, TimedOutRuns: 1, CompletionRate: &prComp,
				},
			},
		},
		kpi.FailuresResponse{
			Filters: ovParams,
			Failures: []kpi.RecurringFailureRow{
				{SignatureID: "sig-show", NormalizedMessage: "detox crash", State: kpi.FailureStateActive, OccurrenceCount: 2, LastSeenAt: now},
			},
		},
		now,
		true,
	)
	if !strings.Contains(ovPage.PulseSummary, "Mobile · mobile-main-detox-android (7d): Main pass rate 0.0% (low sample) · 3 flaky tests (14d)") {
		t.Fatalf("unexpected PulseSummary: %q", ovPage.PulseSummary)
	}
	if strings.Contains(ovPage.FlakyHref, "window=7d") {
		t.Fatalf("FlakyHref must pin to default 14d (no window=7d), got %q", ovPage.FlakyHref)
	}
	if ovPage.E2EDuration.CompletedRuns != 9 || ovPage.E2EDuration.TimedOutRuns != 1 || ovPage.E2EDuration.CompletionRate != "90.0%" {
		t.Fatalf("PR run time card must use PRRunCompletion (9/1/90%%), got %+v", ovPage.E2EDuration)
	}
	if !strings.Contains(ovPage.Failures[0].Href, "show=all") {
		t.Fatalf("failure link must preserve show=all, got %q", ovPage.Failures[0].Href)
	}
	if len(ovPage.Filters.WindowPills) == 0 || !strings.Contains(ovPage.Filters.WindowPills[0].Href, "show=all") {
		t.Fatalf("window pills must preserve show=all, got %+v", ovPage.Filters.WindowPills)
	}

	// Lane-only URL without repo= must not select All Repos and must keep lane dropdown visible.
	laneOnlyParams, _ := kpi.ParseFailuresParams(url.Values{"lane": {"mobile-main-detox-android"}}, now)
	laneOnlyPage := BuildKPIOverviewPage(SiteInfo{}, kpi.FilterOptions{
		Repos: []string{"mattermost/mattermost-mobile"},
		Lanes: []string{"mobile-main-detox-android"},
	}, kpi.SummaryResponse{Filters: laneOnlyParams}, kpi.FailuresResponse{Filters: laneOnlyParams}, now)
	if !laneOnlyPage.Filters.ShowLaneSelect || laneOnlyPage.Filters.RepoTabs[0].Active {
		t.Fatalf("lane-only view: ShowLaneSelect=%v AllReposActive=%v", laneOnlyPage.Filters.ShowLaneSelect, laneOnlyPage.Filters.RepoTabs[0].Active)
	}

	// P0-3 on /kpi/prs: resolved via newer commit -> Newer commit green (never "Fixed"); partial main -> "Some errors on main".
	prParams, _ := kpi.ParsePRsRedParams(url.Values{}, now)
	prsPage := BuildKPIPRsPage(SiteInfo{}, kpi.FilterOptions{}, kpi.PRRedResponse{
		Filters: prParams,
		Lanes: []kpi.RedPRLaneRow{
			{
				ReportGroupID: "rg-1", Repository: "mattermost/mattermost", Lane: "e2e-pr", GHPRNumber: 42,
				CommitSHA: "abcdef1234567", Settled: true, StillRed: false, WentGreenOnRerun: false,
				FailingOnMain: true, AllSignaturesFailingElsewhere: false, Reason: kpi.ReasonOther,
				CreatedAt: now.Add(-30 * time.Hour),
			},
		},
	}, now)
	if prsPage.Lanes[0].StatusLabel != "Newer commit green" {
		t.Fatalf("resolved non-rerun row StatusLabel = %q, want 'Newer commit green'", prsPage.Lanes[0].StatusLabel)
	}
	if prsPage.Lanes[0].CauseLabel != "Some errors on main" {
		t.Fatalf("partial main failure CauseLabel = %q, want 'Some errors on main'", prsPage.Lanes[0].CauseLabel)
	}

	// P0-4 & P0-6 & P1-4 on /kpi/flaky: default 14d does not leak ?window=14d back to Overview/PRs/RepoHref; renders LastRunURL and CI runs header.
	flakyParams, _ := kpi.ParseFlakyParams(url.Values{"repo": {"mattermost/mattermost"}}, now)
	flakyPage := BuildKPIFlakyPage(SiteInfo{}, kpi.FilterOptions{}, kpi.FlakyResponse{
		Filters:    flakyParams,
		WindowDays: 14,
		Tests: []kpi.FlakyTestRow{{
			TestID: "t-1", FullTitle: "Test > flaky", Repository: "mattermost/mattermost",
			LastFlakyAt: now, LastCommitSHA: "abcdef1234567", LastRunURL: "/reports/mattermost/main/abcdef1/e2e",
			Lanes: []string{"e2e-pw"},
		}},
	}, now)
	if strings.Contains(flakyPage.OverviewHref, "window=14d") || strings.Contains(flakyPage.PRsHref, "window=14d") || strings.Contains(flakyPage.Tests[0].RepoHref, "window=14d") {
		t.Fatalf("flaky page leaked window=14d into links: ov=%q prs=%q repo=%q", flakyPage.OverviewHref, flakyPage.PRsHref, flakyPage.Tests[0].RepoHref)
	}
	flakyHTML, err := RenderKPIFlaky(flakyPage)
	if err != nil {
		t.Fatalf("RenderKPIFlaky: %v", err)
	}
	for _, want := range []string{`>CI runs</th>`, `href="/reports/mattermost/main/abcdef1/e2e"`, `Run &rarr;`} {
		if !strings.Contains(string(flakyHTML), want) {
			t.Fatalf("RenderKPIFlaky missing %q", want)
		}
	}

	// P0-6, P1-2, P1-3, P1-5, P1-7 on Failure Detail:
	// - Root breadcrumb links to /kpi (no repo/lane) with show=all
	// - Multi-repo PR chips leave Href empty while FirstSeen retains explicit PRURL
	// - LatestRunURL renders View latest run
	// - Low-sample 14d rate does not repeat occurrences/runs twice
	failParams, _ := kpi.ParseFailureDetailParams(url.Values{"repo": {"mattermost/mattermost"}, "lane": {"e2e-pw"}}, now)
	rate1k := 1000.0
	prNum := 888
	detailPage := BuildKPIFailureDetailPageWithShow(SiteInfo{}, kpi.FailureDetailResponse{
		Filters:           failParams,
		ErrorClass:        strPtr("TimeoutError"),
		NormalizedMessage: "timeout waiting for selector",
		OccurrenceCount:   33,
		AffectedTestRuns:  33,
		AffectedTestCount: 3,
		RatePer1k:         &rate1k,
		Occurrences14d:    33,
		AffectedRuns14d:   33,
		RatePer1k14d:      &rate1k,
		FirstSeen: kpi.FailureFirstSeen{
			FirstSeenAt: now.Add(-7 * 24 * time.Hour),
			Repository:  strPtr("mattermost/mattermost"),
			GHPRNumber:  &prNum,
			RunURL:      "/reports/mattermost/main/1111111/e2e-pw",
		},
		LatestRunURL: "/reports/mattermost/main/2222222/e2e-pw",
		AffectedRepos: []kpi.FailureAffectedRepo{
			{Repository: "mattermost/mattermost", App: "mattermost"},
			{Repository: "mattermost/desktop", App: "desktop"},
		},
		PRNumbers: []int{888, 101},
		LatestVerdict: &kpi.FailureVerdict{
			Label:     "cleared",
			CreatedAt: now.Add(-time.Hour),
		},
	}, now, true)
	if detailPage.Breadcrumbs[0].Href != "/kpi?show=all" || detailPage.Breadcrumbs[1].Href != "/kpi?repo=mattermost%2Fmattermost&show=all" {
		t.Fatalf("unexpected breadcrumbs: %+v", detailPage.Breadcrumbs)
	}
	if detailPage.PRLinks[0].Href != "" || !strings.Contains(detailPage.FirstSeen.PRURL, "/mattermost/mattermost/pull/888") {
		t.Fatalf("multi-repo PR links must stay unlinked while FirstSeen keeps PRURL: links=%+v fs=%+v", detailPage.PRLinks, detailPage.FirstSeen)
	}
	if detailPage.Current14dText != "100% (33 of 33 affected test runs)" {
		t.Fatalf("Current14dText = %q, want compact non-redundant format", detailPage.Current14dText)
	}
	detailHTML, err := RenderKPIFailureDetail(detailPage)
	if err != nil {
		t.Fatalf("RenderKPIFailureDetail: %v", err)
	}
	ds := string(detailHTML)
	for _, want := range []string{`Impact (Last 30d) &amp; 14d trend`, `View latest run &rarr;`, `/reports/mattermost/main/2222222/e2e-pw`} {
		if !strings.Contains(ds, want) {
			t.Fatalf("RenderKPIFailureDetail missing %q", want)
		}
	}
	if strings.Contains(ds, "· <time") && strings.Contains(ds, ">\n      · <time") {
		t.Fatal("empty DecidedBy must not produce leading middot")
	}
}
