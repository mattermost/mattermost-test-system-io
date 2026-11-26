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
		`94.2%`,
		`Lane pass (attempt 1)`,
		`Strict (0 retried units)`,
		`at least 18.5%`,
		`new-commit fixes excluded`,
		`mattermost: 5`,
		`Active: 1`,
		`Resurfaced: 1`,
		`Quiet: 1`,
		`Not enough runs: 1`,
		`14m 20s`,
		`28m 5s`,
		`75.0%`,
		`80.0%`,
		`50.0%`,
		`90.0%`,
		`flaky-infra: 4 (66.7%)`,
		`2026-11-16`,
		`Quiet (9d)`,
		`Resurfaced`,
		`20.0 / 1k`,
		`&#43;5.0 / 1k`,
		`/kpi/failures/019a1234-5678-7abc-8def-0123456789ab?`,
		`app=mattermost`,
		`window=14d`,
		`sort=rate_desc`,
		`page=3`,
		`html.dark .kpi-card`,
		`@media (max-width: 639px)`,
		`@media (min-width: 640px) and (max-width: 1023px)`,
		`@media (min-width: 1024px)`,
		`Upload-only lanes lack lease retry history.`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("RenderKPIOverview missing %q", want)
		}
	}

	for _, forbid := range []string{
		`id="connection-status"`,
		`[data-theme="dark"]`,
		`Fixed`,
		`error_context`,
	} {
		if strings.Contains(s, forbid) {
			t.Fatalf("RenderKPIOverview must not contain %q", forbid)
		}
	}
	if strings.Contains(strings.ToLower(s), "blocked") {
		t.Fatal("RenderKPIOverview must not contain 'blocked'")
	}

	// Verify /kpi/prs sub-tab link preserves filters without leaking incompatible /kpi sort=rate_desc or page=2.
	if !strings.Contains(s, `href="/kpi/prs?app=mattermost&amp;lane=e2e-pr&amp;platform=linux&amp;repo=mattermost%2Fmattermost&amp;stage=pr&amp;window=14d"`) {
		t.Fatalf("expected PR E2E sub-tab link to preserve filters without sort/page, got HTML snippet: %s", s)
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
		`N/A`,
		`No data`,
		`No triage or run completion data in this window`,
		`No weekly trend data in this window`,
		`No recurring failures`,
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
		`at least 22.5%`,
		`excludes red PR lanes fixed by pushing a new commit`,
		`Runs younger than 24 hours are unsettled`,
		`Went green on rerun`,
		`Also failing elsewhere`,
		`Other`,
		`12.5%`,
		`10.0%`,
		`77.5%`,
		`E2E red`,
		`#12345`,
		`Failing on main &#43; 4 other PRs`,
		`Unsettled (&lt;24h)`,
		`mattermost/mattermost:e2e-rf-pr`,
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
	if !strings.Contains(es, `N/A`) || !strings.Contains(es, `No settled red PR lanes to classify`) || !strings.Contains(es, `No E2E red PR lanes`) {
		t.Fatalf("empty PRs page missing expected empty states")
	}
}

func TestBuildKPIOverviewAndPRs_PRFalseRedZeroSettledAndUnsettledStates(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	ovParams, err := kpi.ParseFailuresParams(url.Values{}, now)
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
		`3 unsettled (&lt;24h)`,
		`No settled (&gt;24h) red lanes yet (3 unsettled); new-commit fixes excluded.`,
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
	if !strings.Contains(string(ovEmptyHTML), `No settled (&gt;24h) red PR lanes in window; new-commit fixes excluded.`) {
		t.Fatalf("zero-PR overview missing settled note")
	}

	// PR E2E page: nil FalseRedRate + unsettled "E2E red" row failing on 3 other PRs (without main) + 2 other PRs ("Seen on 2 other PR(s)").
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
		`No data — no settled (&gt;24h) red PR lanes in this window`,
		`E2E red`,
		`Failing on 3 other PRs`,
		`Seen on 2 other PR(s)`,
		`Also failing elsewhere`,
		`Other`,
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
		`flaky-infra`,
		`92%`,
		`Not enough runs`,
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
	checkStruct(t, reflect.TypeOf(KPIFailureDetailPage{}), "KPIFailureDetailPage")
}

func TestKPIHandlers_ValidationAndNilStore(t *testing.T) {
	// Nil KPI store -> 503 across all 3 handlers.
	hNil := &Handler{Logger: slog.New(slog.DiscardHandler)}
	for _, tc := range []struct {
		name string
		fn   http.HandlerFunc
		path string
	}{
		{"overview_nil", hNil.ServeKPIOverview, "/kpi"},
		{"prs_nil", hNil.ServeKPIPRs, "/kpi/prs"},
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

	// Verify chi router mounts /kpi, /kpi/prs, /kpi/failures/{id} when ServeKPI* wired.
	r := chi.NewRouter()
	h := &Handler{}
	r.Get("/kpi", h.ServeKPIOverview)
	r.Get("/kpi/prs", h.ServeKPIPRs)
	r.Get("/kpi/failures/{id}", h.ServeKPIFailureDetail)
	for _, path := range []string{"/kpi", "/kpi/prs", "/kpi/failures/not-a-uuid"} {
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
			`57 canonical signatures`,
			`Page 5 out of range`,
			`No failure signatures on page 5 (total 57 across 2 pages).`,
			`kpi-pagination`,
			`href="?app=mattermost&amp;page=2&amp;sort=rate_desc&amp;window=14d">Previous</a>`,
			`<span class="btn-page is-disabled" aria-disabled="true">Next</span>`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("out-of-range overview missing %q", want)
			}
		}
		if strings.Contains(s, "No recurring failures") {
			t.Fatal("out-of-range overview must NOT say 'No recurring failures'")
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
			`57 E2E red PR lane runs`,
			`Page 5 out of range`,
			`No red PR lanes on page 5 (total 57 across 2 pages).`,
			`kpi-pagination`,
			`href="?app=mattermost&amp;page=2&amp;sort=age_desc">Previous</a>`,
			`<span class="btn-page is-disabled" aria-disabled="true">Next</span>`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("out-of-range PRs missing %q", want)
			}
		}
		if strings.Contains(s, "No E2E red PR lanes") {
			t.Fatal("out-of-range PRs must NOT say 'No E2E red PR lanes'")
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
	const fallbackReportsURL = "/reports"
	if prPage.Lanes[0].RunURL != fallbackReportsURL || prPage.Lanes[1].RunURL != fallbackReportsURL {
		t.Fatalf("expected unsafe RunURLs to fall back to %s, got %q and %q", fallbackReportsURL, prPage.Lanes[0].RunURL, prPage.Lanes[1].RunURL)
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
			`<input type="hidden" name="limit" value="10">`,
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
			`<input type="hidden" name="limit" value="100">`,
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

		htmlBytes, err := RenderKPIPRs(page)
		if err != nil {
			t.Fatalf("RenderKPIPRs: %v", err)
		}
		if strings.Contains(string(htmlBytes), `name="limit"`) {
			t.Fatal("default limit=50 must not emit hidden limit input")
		}
	})
}
