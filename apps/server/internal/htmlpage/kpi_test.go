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
		PRFalseRed: map[string]any{
			"is_lower_bound":          true,
			"rate_qualifier":          "at least",
			"settled_after_hours":     24,
			"false_red_rate":          rate,
			"prior_false_red_rate":    priorRate,
			"delta_pp":                deltaPP,
			"settled_red_lanes":       200,
			"settled_false_red_lanes": 37,
			"unsettled_red_lanes":     5,
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
	page := BuildKPIOverviewPage(SiteInfo{}, kpi.FilterOptions{}, kpi.SummaryResponse{Filters: params}, kpi.FailuresResponse{Filters: params}, now)
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
	params, err := ParseKPIPRsParams(url.Values{
		"app":    {"mattermost"},
		"window": {"7d"},
		"sort":   {kpiPRSortAgeDesc},
		"page":   {"1"},
	}, now)
	if err != nil {
		t.Fatalf("ParseKPIPRsParams: %v", err)
	}

	input := KPIPRsInput{
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
		ReasonMix: KPIPRReasonMixInput{
			WentGreenOnRerun: 5,
			FailingElsewhere: 4,
			Other:            31,
			Total:            40,
		},
		ConfiguredE2ELanes: []KPIConfiguredLane{{Repository: "mattermost/mattermost", Lane: "e2e-rf-pr"}},
		LaneCoverage:       kpi.LaneCoverage{TotalLanes: 1, OrchestratedLanes: 1, TotalRuns: 43},
		Pagination:         kpi.Pagination{Page: 1, Limit: 50, Total: 2, TotalPages: 1},
		Lanes: []KPIRedLaneInput{
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
				Reason:               kpiReasonFailingElsewhere,
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
				Reason:               kpiReasonWentGreenOnRerun,
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
	emptyPage := BuildKPIPRsPage(SiteInfo{}, kpi.FilterOptions{}, KPIPRsInput{Filters: params, SettledAfterHours: 24}, now)
	emptyHTML, err := RenderKPIPRs(emptyPage)
	if err != nil {
		t.Fatal(err)
	}
	es := string(emptyHTML)
	if !strings.Contains(es, `N/A`) || !strings.Contains(es, `No settled red PR lanes to classify`) || !strings.Contains(es, `No E2E red PR lanes`) {
		t.Fatalf("empty PRs page missing expected empty states")
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

	// Non-nil KPI store with nil pool still returns 503; let's test ParseKPIPRsParams validation directly.
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	for _, bad := range []url.Values{
		{"window": {"5d"}},
		{"stage": {"unknown"}},
		{"sort": {"bad_sort"}},
		{"page": {"0"}},
		{"page": {"abc"}},
	} {
		if _, err := ParseKPIPRsParams(bad, now); err == nil {
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
