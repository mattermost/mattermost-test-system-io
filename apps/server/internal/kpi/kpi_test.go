package kpi

import (
	"errors"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseSummaryParams_DefaultsAndValidation(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 30, 0, 0, time.UTC)

	params, err := ParseSummaryParams(url.Values{}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Window != Window30d || params.WindowDays != 30 {
		t.Fatalf("window = %q (%d), want 30d (30)", params.Window, params.WindowDays)
	}
	if params.Sort != SortLaneAsc {
		t.Fatalf("sort = %q, want %q", params.Sort, SortLaneAsc)
	}
	if params.Page != 0 || params.Limit != 0 {
		t.Fatalf("page=%d limit=%d, want 0/0 (summary has no pagination)", params.Page, params.Limit)
	}
	wantCurFrom := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	wantCurTo := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	wantPriorFrom := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	wantPriorTo := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	if !params.CurFromDate.Equal(wantCurFrom) || !params.CurToDate.Equal(wantCurTo) {
		t.Fatalf("cur bounds = [%s..%s], want [%s..%s]", params.CurFromDate, params.CurToDate, wantCurFrom, wantCurTo)
	}
	if !params.PriorFromDate.Equal(wantPriorFrom) || !params.PriorToDate.Equal(wantPriorTo) {
		t.Fatalf("prior bounds = [%s..%s], want [%s..%s]", params.PriorFromDate, params.PriorToDate, wantPriorFrom, wantPriorTo)
	}

	// Repo normalization + valid params (including multi-byte Unicode rune limits)
	q := url.Values{
		"app":      {" mobile "},
		"repo":     {" mattermost/mattermost-mobile.git "},
		"stage":    {"pr"},
		"lane":     {" detox-ios "},
		"platform": {" ios "},
		"window":   {"7d"},
		"sort":     {SortDurationDesc},
		"page":     {"0"},
		"limit":    {"999"},
	}
	params, err = ParseSummaryParams(q, now)
	if err != nil {
		t.Fatalf("unexpected error for valid params: %v", err)
	}
	if params.App != "mobile" || params.Repo != "mattermost/mattermost-mobile" || params.Stage != "pr" ||
		params.Lane != "detox-ios" || params.Platform != "ios" || params.WindowDays != 7 ||
		params.Sort != SortDurationDesc || params.Page != 0 || params.Limit != 0 {
		t.Fatalf("unexpected parsed params: %+v", params)
	}

	// Multi-byte UTF-8 rune count boundary: 64 runes (128 bytes) and 256 runes (512 bytes) must be accepted.
	okUnicode := url.Values{
		"app":      {strings.Repeat("é", 64)},
		"repo":     {strings.Repeat("é", 256)},
		"lane":     {strings.Repeat("é", 256)},
		"platform": {strings.Repeat("é", 64)},
	}
	if _, err := ParseSummaryParams(okUnicode, now); err != nil {
		t.Fatalf("expected 64/256 Unicode runes to pass, got %v", err)
	}

	badCases := []struct {
		name string
		q    url.Values
	}{
		{"bad_window", url.Values{"window": {"10d"}}},
		{"bad_stage", url.Values{"stage": {"staging"}}},
		{"bad_sort", url.Values{"sort": {"flaky_commits_desc"}}},
		{"long_app", url.Values{"app": {strings.Repeat("a", 65)}}},
		{"long_repo", url.Values{"repo": {strings.Repeat("r", 257)}}},
		{"long_lane", url.Values{"lane": {strings.Repeat("l", 257)}}},
		{"long_platform", url.Values{"platform": {strings.Repeat("p", 65)}}},
		{"long_unicode_app", url.Values{"app": {strings.Repeat("é", 65)}}},
		{"long_unicode_repo", url.Values{"repo": {strings.Repeat("é", 257)}}},
		{"long_unicode_lane", url.Values{"lane": {strings.Repeat("é", 257)}}},
		{"long_unicode_platform", url.Values{"platform": {strings.Repeat("é", 65)}}},
		{"nul_app", url.Values{"app": {"matter\x00most"}}},
		{"nul_repo", url.Values{"repo": {"mattermost/repo\x00"}}},
		{"nul_lane", url.Values{"lane": {"e2e\x00-pr"}}},
		{"nul_platform", url.Values{"platform": {"ios\x00"}}},
		{"invalid_utf8_app", url.Values{"app": {"app\xff"}}},
		{"invalid_utf8_repo", url.Values{"repo": {"mattermost/repo\xff"}}},
		{"invalid_utf8_lane", url.Values{"lane": {"lane\xff"}}},
		{"invalid_utf8_platform", url.Values{"platform": {"plat\xff"}}},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseSummaryParams(tc.q, now); !errors.Is(err, ErrValidation) {
				t.Fatalf("expected ErrValidation for %s, got %v", tc.name, err)
			}
			if tc.name != "bad_sort" {
				if _, err := ParseFlakyParams(tc.q, now); !errors.Is(err, ErrValidation) {
					t.Fatalf("expected ParseFlakyParams ErrValidation for %s, got %v", tc.name, err)
				}
			}
		})
	}
}

func TestParseFlakyParams_DefaultsAndSorts(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)

	params, err := ParseFlakyParams(url.Values{}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Window != Window14d || params.WindowDays != 14 {
		t.Fatalf("window = %q (%d), want 14d (14)", params.Window, params.WindowDays)
	}
	if params.Sort != SortFlakyCommitsDesc {
		t.Fatalf("sort = %q, want %q", params.Sort, SortFlakyCommitsDesc)
	}
	if params.Page != 1 || params.Limit != defaultLimit {
		t.Fatalf("page=%d limit=%d, want 1/%d", params.Page, params.Limit, defaultLimit)
	}

	pValid, err := ParseFlakyParams(url.Values{"page": {"2"}, "limit": {"25"}}, now)
	if err != nil || pValid.Page != 2 || pValid.Limit != 25 {
		t.Fatalf("flaky pagination valid failed: p=%+v err=%v", pValid, err)
	}

	for _, bad := range []url.Values{
		{"page": {"0"}},
		{"page": {"abc"}},
		{"limit": {"0"}},
		{"limit": {"101"}},
	} {
		if _, err := ParseFlakyParams(bad, now); err == nil {
			t.Fatalf("expected error for %v, got nil", bad)
		}
	}

	for _, s := range []string{SortFlakyCommitsDesc, SortFlakeRateDesc, SortTotalRunsDesc, SortLastFlakyDesc, SortNameAsc} {
		p, err := ParseFlakyParams(url.Values{"sort": {s}}, now)
		if err != nil || p.Sort != s {
			t.Fatalf("sort %q failed: p=%+v err=%v", s, p, err)
		}
	}
	if _, err := ParseFlakyParams(url.Values{"sort": {SortLaneAsc}}, now); err == nil {
		t.Fatal("expected summary sort to fail validation on /flaky")
	}
}

func TestParsePRsRedParams_DefaultsAndValidation(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 30, 0, 0, time.UTC)

	params, err := ParsePRsRedParams(url.Values{}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Window != Window30d || params.WindowDays != 30 {
		t.Fatalf("window = %q (%d), want 30d (30)", params.Window, params.WindowDays)
	}
	if params.Sort != SortCreatedDesc {
		t.Fatalf("sort = %q, want %q", params.Sort, SortCreatedDesc)
	}
	if params.Page != 1 || params.Limit != defaultLimit {
		t.Fatalf("page=%d limit=%d, want 1/%d", params.Page, params.Limit, defaultLimit)
	}

	for _, s := range []string{SortCreatedDesc, SortAgeDesc, SortPRDesc, SortLaneAsc, SortReasonAsc} {
		p, err := ParsePRsRedParams(url.Values{"sort": {s}}, now)
		if err != nil || p.Sort != s {
			t.Fatalf("sort %q failed: p=%+v err=%v", s, p, err)
		}
	}

	for _, bad := range []url.Values{
		{"sort": {SortFlakyCommitsDesc}},
		{"sort": {SortDurationDesc}},
		{"window": {"5d"}},
		{"stage": {"bogus"}},
		{"page": {"0"}},
		{"limit": {"101"}},
		{"app": {strings.Repeat("a", 65)}},
		{"repo": {strings.Repeat("r", 257)}},
		{"lane": {strings.Repeat("l", 257)}},
		{"platform": {strings.Repeat("p", 65)}},
	} {
		if _, err := ParsePRsRedParams(bad, now); err == nil {
			t.Fatalf("expected validation error for %v", bad)
		}
	}
}

func TestParseConfiguredE2ELanes(t *testing.T) {
	if got := parseConfiguredE2ELanes(nil); got != nil {
		t.Fatalf("expected nil for empty input, got %+v", got)
	}
	if got := parseConfiguredE2ELanes([]string{"  ", ""}); got != nil {
		t.Fatalf("expected nil for blank entries, got %+v", got)
	}
	got := parseConfiguredE2ELanes([]string{
		" mattermost/mattermost : playwright-full ",
		"Mattermost/Mattermost:playwright-full",
		"desktop-pr",
		"bad:",
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (%+v)", len(got), got)
	}
	if got[0].repo != "mattermost/mattermost" || got[0].lane != "playwright-full" {
		t.Fatalf("entry[0] = %+v", got[0])
	}
	if got[1].repo != "" || got[1].lane != "desktop-pr" {
		t.Fatalf("entry[1] = %+v", got[1])
	}

	// Overlapping repo-scoped + wildcard entries must prune the subsumed repo-scoped entries regardless of order.
	for _, tc := range []struct {
		name string
		raw  []string
	}{
		{
			name: "repo_before_wildcard",
			raw:  []string{"mattermost/mattermost:playwright-full", "playwright-full", "mattermost/desktop:desktop-pr"},
		},
		{
			name: "wildcard_before_repo",
			raw:  []string{"playwright-full", "mattermost/mattermost:playwright-full", "mattermost/desktop:desktop-pr"},
		},
		{
			name: "star_prefix_wildcard",
			raw:  []string{"mattermost/mattermost:playwright-full", "*:playwright-full", "mattermost/desktop:desktop-pr"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deduped := parseConfiguredE2ELanes(tc.raw)
			if len(deduped) != 2 {
				t.Fatalf("len = %d, want 2 (%+v)", len(deduped), deduped)
			}
			hasWildcardPW, hasDesktop := false, false
			for _, e := range deduped {
				if e.repo == "" && e.lane == "playwright-full" {
					hasWildcardPW = true
				}
				if e.repo == "mattermost/desktop" && e.lane == "desktop-pr" {
					hasDesktop = true
				}
			}
			if !hasWildcardPW || !hasDesktop {
				t.Fatalf("unexpected deduped entries: %+v", deduped)
			}
		})
	}
}

func TestResolveRepoApp(t *testing.T) {
	const mobileApp = "mobile"
	exact := map[string]string{"mattermost/mattermost-mobile": mobileApp, "mattermost/desktop": "desktop"}
	lower := map[string]string{"mattermost/mattermost-mobile": mobileApp, "mattermost/desktop": "desktop"}

	if got := resolveRepoApp("mattermost/mattermost-mobile", exact, lower); got != mobileApp {
		t.Fatalf("exact match = %q, want %s", got, mobileApp)
	}
	if got := resolveRepoApp("Mattermost/Mattermost-Mobile", exact, lower); got != mobileApp {
		t.Fatalf("case-insensitive match = %q, want %s", got, mobileApp)
	}
	if got := resolveRepoApp("mattermost/mattermost", exact, lower); got != "mattermost" {
		t.Fatalf("split_part fallback = %q, want mattermost", got)
	}
	if got := resolveRepoApp("custom-repo", exact, lower); got != "custom-repo" {
		t.Fatalf("bare repo fallback = %q, want custom-repo", got)
	}
}

func TestBuildRunURL(t *testing.T) {
	got := buildRunURL(
		"mattermost/mattermost",
		"refs/heads/feat/my-pr",
		"abcdef1234567890",
		"playwright-full",
		"",
		"987654",
		"2",
	)
	want := "/reports/mattermost/feat~my-pr/abcdef1/playwright-full?gh_run_id=987654&gh_run_attempt=2"
	if got != want {
		t.Fatalf("buildRunURL = %q, want %q", got, want)
	}
	if empty := buildRunURL("mattermost/mattermost", "", "abc", "lane", "", "1", "1"); empty != "/" {
		t.Fatalf("expected '/' fallback for empty branch, got %q", empty)
	}
}

func TestComputeGroupFlags_SinglePlatformStatsFallback(t *testing.T) {
	flags := computeGroupFlags(
		orchState{HasOrchestration: false},
		testStatsJSONPayload{Total: 10, Failed: 2},
		sliceCounts{},
		1,
		false, true, true,
	)
	if !flags.IsRed {
		t.Fatal("expected single-platform test_stats_json-only fallback to set IsRed=true")
	}
}

func TestParseFailuresParams_DefaultsAndSorts(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)

	params, err := ParseFailuresParams(url.Values{}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Window != Window30d || params.WindowDays != 30 {
		t.Fatalf("window = %q (%d), want 30d (30)", params.Window, params.WindowDays)
	}
	if params.Sort != SortOccurrencesDesc {
		t.Fatalf("sort = %q, want %q", params.Sort, SortOccurrencesDesc)
	}

	for _, s := range []string{SortOccurrencesDesc, SortCountDesc, SortRateDesc, SortTrendDesc, SortLastSeenDesc, SortNameAsc} {
		p, err := ParseFailuresParams(url.Values{"sort": {s}}, now)
		if err != nil || p.Sort != s {
			t.Fatalf("sort %q failed: p=%+v err=%v", s, p, err)
		}
	}
	if _, err := ParseFailuresParams(url.Values{"sort": {SortFlakyCommitsDesc}}, now); err == nil {
		t.Fatal("expected flaky sort to fail validation on /failures")
	}
	if _, err := ParseFailureDetailParams(url.Values{"window": {"99d"}}, now); err == nil {
		t.Fatal("expected invalid window to fail validation on /failures/{id}")
	}
}

func TestComputeFailureState_AllFourStatesAndBoundaries(t *testing.T) {
	today := utcDayNum(time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC))

	t.Run("Active_seen_in_7d_no_prior_quiet_gap", func(t *testing.T) {
		occ := map[int64]int{today - 2: 3}
		runs := map[int64]int{today - 2: 100, today - 1: 100}
		state, qd := computeFailureState(today, true, today-2, occ, runs)
		if state != FailureStateActive || qd != nil {
			t.Fatalf("got (%q, %v), want (%q, nil)", state, qd, FailureStateActive)
		}
	})

	t.Run("Active_brand_new_signature_with_prior_test_history", func(t *testing.T) {
		// Test ran 600 times before firstSeenDay, then signature appeared for the first time today.
		occ := map[int64]int{today: 1}
		runs := map[int64]int{today - 10: 600, today: 20}
		state, qd := computeFailureState(today, true, today, occ, runs)
		if state != FailureStateActive || qd != nil {
			t.Fatalf("got (%q, %v), want (%q, nil)", state, qd, FailureStateActive)
		}
	})

	t.Run("Active_prior_gap_under_500_runs_is_not_resurfaced", func(t *testing.T) {
		occ := map[int64]int{today - 15: 1, today - 2: 1}
		runs := map[int64]int{today - 12: 499, today - 2: 50}
		state, qd := computeFailureState(today, true, today-15, occ, runs)
		if state != FailureStateActive || qd != nil {
			t.Fatalf("got (%q, %v), want (%q, nil)", state, qd, FailureStateActive)
		}
	})

	t.Run("Quiet_exact_500_runs_in_7d", func(t *testing.T) {
		occ := map[int64]int{today - 10: 2}
		runs := map[int64]int{today - 6: 250, today - 1: 250}
		state, qd := computeFailureState(today, true, today-10, occ, runs)
		if state != FailureStateQuiet || qd == nil || *qd != 10 {
			t.Fatalf("got (%q, %v), want (%q, 10)", state, qd, FailureStateQuiet)
		}
	})

	t.Run("Not_enough_runs_499_runs_in_7d", func(t *testing.T) {
		occ := map[int64]int{today - 8: 1}
		runs := map[int64]int{today - 3: 499}
		state, qd := computeFailureState(today, true, today-8, occ, runs)
		if state != FailureStateNotEnoughRuns || qd == nil || *qd != 8 {
			t.Fatalf("got (%q, %v), want (%q, 8)", state, qd, FailureStateNotEnoughRuns)
		}
	})

	t.Run("Not_enough_runs_zero_occurrences_zero_runs", func(t *testing.T) {
		state, qd := computeFailureState(today, false, today, map[int64]int{}, map[int64]int{})
		if state != FailureStateNotEnoughRuns || qd != nil {
			t.Fatalf("got (%q, %v), want (%q, nil)", state, qd, FailureStateNotEnoughRuns)
		}
	})

	t.Run("Resurfaced_after_prior_7d_quiet_period", func(t *testing.T) {
		occ := map[int64]int{today - 20: 1, today - 3: 2}
		runs := map[int64]int{today - 18: 250, today - 15: 250, today - 3: 50}
		state, qd := computeFailureState(today, true, today-20, occ, runs)
		if state != FailureStateResurfaced || qd != nil {
			t.Fatalf("got (%q, %v), want (%q, nil)", state, qd, FailureStateResurfaced)
		}
	})

	t.Run("Resurfaced_when_quiet_window_overlaps_trailing_7d", func(t *testing.T) {
		// Occurred -10d, quiet -9d..-3d (7 days, 500 runs), recurred -2d.
		occ := map[int64]int{today - 10: 1, today - 2: 1}
		runs := map[int64]int{today - 7: 300, today - 4: 200, today - 2: 20}
		state, qd := computeFailureState(today, true, today-10, occ, runs)
		if state != FailureStateResurfaced || qd != nil {
			t.Fatalf("got (%q, %v), want (%q, nil)", state, qd, FailureStateResurfaced)
		}
	})
}

func TestRatePer1kZeroDenominatorGuards(t *testing.T) {
	if got := ratePer1kPtr(5, 0); got != nil {
		t.Fatalf("ratePer1kPtr(5, 0) = %v, want nil", *got)
	}
	if got := ratePer1kPtr(0, 500); got == nil || *got != 0.0 {
		t.Fatalf("ratePer1kPtr(0, 500) = %v, want 0.0", got)
	}
	if got := ratePer1kPtr(5, 250); got == nil || *got != 20.0 {
		t.Fatalf("ratePer1kPtr(5, 250) = %v, want 20.0", got)
	}
	ten := 10.0
	if got := diffFloatPtr(&ten, nil); got != nil {
		t.Fatalf("diffFloatPtr(10, nil) = %v, want nil", *got)
	}
	if got := diffFloatPtr(nil, &ten); got != nil {
		t.Fatalf("diffFloatPtr(nil, 10) = %v, want nil", *got)
	}
}

func TestAggregateGroupSlices_Attempt1AndPlatforms(t *testing.T) {
	t1 := uuid.New()
	t2 := uuid.New()
	t3 := uuid.New()
	sig1 := uuid.New()
	sig2 := uuid.New()

	results := []testResultRow{
		{TestID: t1, Platform: "ios", Verdict: "passed", FirstAttemptPassed: true},
		{TestID: t2, Platform: "ios", Verdict: "flaky", FirstAttemptPassed: false, SignatureIDs: []uuid.UUID{sig1}},
		{TestID: t2, Platform: "android", Verdict: "failed", FirstAttemptPassed: false, SignatureIDs: []uuid.UUID{sig2}},
		{TestID: t3, Platform: "android", Verdict: "skipped", FirstAttemptPassed: false},
	}
	verdicts := []verdictRow{
		{SignatureID: &sig2, Label: "infra", IsReused: true},
	}

	all, perPlat := aggregateGroupSlices(results, []string{"android", "ios"}, verdicts, "1")
	if all.TestsTotal != 4 || all.TestsPassed != 1 || all.TestsFlaky != 1 || all.TestsFailed != 1 || all.TestsSkipped != 1 {
		t.Fatalf("unexpected all counts: %+v", all)
	}
	if all.Attempt1TestsRun != 3 || all.Attempt1FirstAttempt != 1 {
		t.Fatalf("attempt1 counts = run:%d pass:%d, want 3/1", all.Attempt1TestsRun, all.Attempt1FirstAttempt)
	}
	if all.FailedTestOccurrences != 1 || all.TriagedTestOccurrences != 1 {
		t.Fatalf("triage occurrences = %d/%d, want 1/1", all.TriagedTestOccurrences, all.FailedTestOccurrences)
	}
	if perPlat["ios"].TestsPassed != 1 || perPlat["ios"].TestsFlaky != 1 || perPlat["ios"].TestsFailed != 0 {
		t.Fatalf("unexpected ios slice: %+v", perPlat["ios"])
	}
	if perPlat["android"].TestsFailed != 1 || perPlat["android"].TestsSkipped != 1 || perPlat["android"].Attempt1TestsRun != 1 {
		t.Fatalf("unexpected android slice: %+v", perPlat["android"])
	}

	// Attempt 2 must never contribute to attempt1 test counts.
	allAtt2, _ := aggregateGroupSlices(results, []string{"android", "ios"}, verdicts, "2")
	if allAtt2.Attempt1TestsRun != 0 || allAtt2.Attempt1FirstAttempt != 0 {
		t.Fatalf("expected 0 attempt1 counts for gh_run_attempt=2, got %+v", allAtt2)
	}
}

func TestTruncateToUTCWeekMonday(t *testing.T) {
	// 2026-11-26 is Thursday -> Monday is 2026-11-23.
	thursday := time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)
	got := truncateToUTCWeekMonday(thursday)
	want := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("truncateToUTCWeekMonday(%s) = %s, want %s", thursday, got, want)
	}

	// Sunday 2026-11-29 -> Monday 2026-11-23.
	sunday := time.Date(2026, 11, 29, 23, 59, 0, 0, time.UTC)
	got = truncateToUTCWeekMonday(sunday)
	if !got.Equal(want) {
		t.Fatalf("truncateToUTCWeekMonday(%s) = %s, want %s", sunday, got, want)
	}
}

func TestTestExecKeyNetZeroDeltaWithoutApp(t *testing.T) {
	day := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	tid := uuid.New()
	raw := []byte(`{"` + tid.String() + `":{"run":2,"pass":1,"fail":1,"flaky":0,"skip":0}}`)

	deltas := map[testExecKey]testExecDelta{}
	if err := addTestCountsJSON(deltas, day, "mattermost/mattermost", "", "main", "e2e", "", raw, -1); err != nil {
		t.Fatalf("addTestCountsJSON(-1): %v", err)
	}

	k := testExecKey{
		MetricDate: day,
		Repository: "mattermost/mattermost",
		Stage:      "main",
		Lane:       "e2e",
		Platform:   "",
		TestID:     tid,
	}
	cur := deltas[k]
	cur.App = "mattermost"
	cur.Run += 2
	cur.Pass++
	cur.Fail++
	deltas[k] = cur

	if len(deltas) != 1 {
		t.Fatalf("expected 1 coalesced delta key, got %d", len(deltas))
	}
	got := deltas[k]
	if got.Run != 0 || got.Pass != 0 || got.Fail != 0 || got.Flaky != 0 || got.Skip != 0 {
		t.Fatalf("expected net-zero delta, got %+v", got)
	}
}

func TestSafePageOffset(t *testing.T) {
	if off, ok := safePageOffset(1, 50); !ok || off != 0 {
		t.Fatalf("page=1 limit=50 got (%d, %v), want (0, true)", off, ok)
	}
	if off, ok := safePageOffset(3, 25); !ok || off != 50 {
		t.Fatalf("page=3 limit=25 got (%d, %v), want (50, true)", off, ok)
	}
	// Overflow boundary: (page-1)*2 would overflow math.MaxInt.
	overflowPage := math.MaxInt/2 + 2
	if off, ok := safePageOffset(overflowPage, 2); ok || off != 0 {
		t.Fatalf("overflow page=%d limit=2 got (%d, %v), want (0, false)", overflowPage, off, ok)
	}
	if _, ok := safePageOffset(0, 10); ok {
		t.Fatal("expected page=0 to fail")
	}
	if _, ok := safePageOffset(1, 0); ok {
		t.Fatal("expected limit=0 to fail")
	}
}

func TestHasScopeFilters(t *testing.T) {
	if hasScopeFilters(FilterParams{Window: Window30d, Sort: SortOccurrencesDesc, Page: 1, Limit: 50}) {
		t.Fatal("expected hasScopeFilters=false when only window/sort/pagination are set")
	}
	for _, tc := range []struct {
		name string
		f    FilterParams
	}{
		{"app", FilterParams{App: "mattermost"}},
		{"repo", FilterParams{Repo: "mattermost/mattermost"}},
		{"stage", FilterParams{Stage: "pr"}},
		{"lane", FilterParams{Lane: "e2e-pr"}},
		{"platform", FilterParams{Platform: "ios"}},
	} {
		if !hasScopeFilters(tc.f) {
			t.Fatalf("expected hasScopeFilters=true for %s", tc.name)
		}
	}
}

func TestComputeWindowRates_ScopedFirstSeenBoundPreventsCrossScopeBleed(t *testing.T) {
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	todayDay := utcDayNum(today)
	curFromDay := todayDay - 29

	// First occurrence in the scoped query was at today-1, even though the test had
	// 600 passing runs in that scope at today-20. With firstSeenBoundDay = today-1 (scoped),
	// state must be Active, not Resurfaced.
	acc := newSigAccum(nil)
	acc.addMetricRow(dailySigMetricRow{
		MetricDate:      today.AddDate(0, 0, -1),
		OccurrenceCount: 1,
		RawAttemptCount: 1,
		GroupCount:      1,
		LastSeenAt:      today.AddDate(0, 0, -1),
	}, curFromDay, todayDay)

	runsByDay := map[int64]int{
		todayDay - 20: 600,
		todayDay - 1:  10,
	}

	scopedBound := todayDay - 1
	scopedRates := computeWindowRates(acc, runsByDay, curFromDay, todayDay, &scopedBound)
	if scopedRates.state != FailureStateActive {
		t.Fatalf("scoped state = %q, want %q", scopedRates.state, FailureStateActive)
	}

	// Conversely, if the scoped occurrence history shows an occurrence >365d ago (before historyFrom),
	// and there were >=500 quiet runs in [-365d..-2d], state must be Resurfaced.
	oldBound := todayDay - 400
	resurfRates := computeWindowRates(acc, runsByDay, curFromDay, todayDay, &oldBound)
	if resurfRates.state != FailureStateResurfaced {
		t.Fatalf("pre-history scoped state = %q, want %q", resurfRates.state, FailureStateResurfaced)
	}
}

func TestPaginateRecurringFailures_OverflowAndBounds(t *testing.T) {
	rows := []RecurringFailureRow{
		{SignatureID: "s1"},
		{SignatureID: "s2"},
		{SignatureID: "s3"},
	}

	if got := paginateRecurringFailures(rows, 1, 2); len(got) != 2 || got[0].SignatureID != "s1" || got[1].SignatureID != "s2" {
		t.Fatalf("page=1 limit=2 got %+v", got)
	}
	if got := paginateRecurringFailures(rows, 2, 2); len(got) != 1 || got[0].SignatureID != "s3" {
		t.Fatalf("page=2 limit=2 got %+v", got)
	}
	if got := paginateRecurringFailures(rows, 3, 2); got == nil || len(got) != 0 {
		t.Fatalf("page=3 limit=2 got %+v, want non-nil empty slice", got)
	}
	// Overflow boundary: (page-1)*limit would wrap negative in signed arithmetic without safePageOffset.
	if got := paginateRecurringFailures(rows, math.MaxInt, 50); got == nil || len(got) != 0 {
		t.Fatalf("overflow page=MaxInt limit=50 got %+v, want non-nil empty slice", got)
	}
}

func TestParseTestStateParams_Validation(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 30, 0, 0, time.UTC)
	p, err := ParseTestStateParams(url.Values{"app": {"webapp"}, "window": {"30d"}, "stage": {"pr"}}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.App != "webapp" || p.Window != Window30d || p.Stage != "pr" {
		t.Fatalf("unexpected parsed params: %+v", p)
	}
	if _, err := ParseTestStateParams(url.Values{"window": {"5d"}}, now); err == nil {
		t.Fatal("expected error for invalid window")
	}
	if _, err := ParseTestStateParams(url.Values{"stage": {"bogus"}}, now); err == nil {
		t.Fatal("expected error for invalid stage")
	}
}

func TestBuildTriageSummary_MetricsAndZeroDenominators(t *testing.T) {
	t.Run("zero_denominators_return_nil_and_empty_label_mix", func(t *testing.T) {
		got := buildTriageSummary(dailyTriageAgg{}, timedOutSplit{}, map[string]int{})
		if got.RunCoverageRate != nil || got.TestCoverageRate != nil || got.VerdictReuseRate != nil || got.RunCompletion.CompletionRate != nil {
			t.Fatalf("expected all nil rates for zero denominators, got %+v", got)
		}
		if got.LabelMix == nil || len(got.LabelMix) != 0 {
			t.Fatalf("expected non-nil empty LabelMix, got %#v", got.LabelMix)
		}
	})

	t.Run("timed_out_partition_avoids_double_count_and_drop", func(t *testing.T) {
		// Suppose 5 total runs:
		// - 2 completed (1 orch=completed, 1 orch=timed_out)
		// - 1 incomplete (orch=null)
		// - 2 in_progress (1 orch=in_progress, 1 orch=timed_out)
		// So dl totals: total=5, completed=2, incomplete=1, in_progress=2
		// timedOutSplit: total=2, completed=1, incomplete=0, in_progress=1
		// Disjoint: completed=1, incomplete=1, in_progress=1, timed_out=2 (sum = 5!)
		// completion_rate = 1 / (5 - 1) * 100 = 25.0%
		agg := dailyTriageAgg{
			totalRuns:              5,
			completedRuns:          2,
			incompleteRuns:         1,
			inProgressRuns:         2,
			failedRunsCount:        4,
			triagedRunsCount:       3,
			failedTestOccurrences:  10,
			triagedTestOccurrences: 5,
			verdictsTotal:          4,
			verdictsReused:         1,
		}
		to := timedOutSplit{
			total:      2,
			completed:  1,
			incomplete: 0,
			inProgress: 1,
		}
		labels := map[string]int{
			"product-bug": 1,
			"infra":       2,
			"flaky-infra": 1,
		}
		got := buildTriageSummary(agg, to, labels)
		if got.RunCoverageRate == nil || *got.RunCoverageRate != 75.0 {
			t.Fatalf("RunCoverageRate = %v, want 75.0", got.RunCoverageRate)
		}
		if got.TestCoverageRate == nil || *got.TestCoverageRate != 50.0 {
			t.Fatalf("TestCoverageRate = %v, want 50.0", got.TestCoverageRate)
		}
		if got.VerdictReuseRate == nil || *got.VerdictReuseRate != 25.0 {
			t.Fatalf("VerdictReuseRate = %v, want 25.0", got.VerdictReuseRate)
		}
		rc := got.RunCompletion
		if rc.TotalRuns != 5 || rc.CompletedRuns != 1 || rc.IncompleteRuns != 1 || rc.InProgressRuns != 1 || rc.TimedOutRuns != 2 {
			t.Fatalf("unexpected RunCompletion counts: %+v", rc)
		}
		if rc.CompletionRate == nil || *rc.CompletionRate != 25.0 {
			t.Fatalf("CompletionRate = %v, want 25.0", rc.CompletionRate)
		}
		if len(got.LabelMix) != 3 {
			t.Fatalf("LabelMix len = %d, want 3", len(got.LabelMix))
		}
		// Sorted by count DESC, label ASC: infra (2, 50%), flaky-infra (1, 25%), product-bug (1, 25%)
		if got.LabelMix[0].Label != "infra" || got.LabelMix[0].Count != 2 || got.LabelMix[0].Share != 50.0 {
			t.Fatalf("LabelMix[0] = %+v, want infra/2/50", got.LabelMix[0])
		}
		if got.LabelMix[1].Label != "flaky-infra" || got.LabelMix[2].Label != "product-bug" {
			t.Fatalf("LabelMix tie-break order = %+v", got.LabelMix)
		}
	})
}

func TestClassifyTestState(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	curFrom14d, _, _, _ := WindowBounds(now, TrendWindowDays) // 2026-11-13 00:00:00 UTC

	// Flaky takes precedence even if newly seen.
	state, flaky := classifyTestState(2, 2, now.Add(-24*time.Hour), curFrom14d)
	if state != TestStateFlaky || !flaky {
		t.Fatalf("got (%q, %v), want (flaky, true)", state, flaky)
	}

	// New: within trailing 14d (exact boundary curFrom14d inclusive) even with >= 2 commits.
	state, flaky = classifyTestState(1, 3, curFrom14d, curFrom14d)
	if state != TestStateNew || flaky {
		t.Fatalf("boundary inclusive got (%q, %v), want (new, false)", state, flaky)
	}

	// New: older than 14d, but <= 1 commit.
	state, flaky = classifyTestState(0, 1, curFrom14d.Add(-24*time.Hour), curFrom14d)
	if state != TestStateNew || flaky {
		t.Fatalf("single commit got (%q, %v), want (new, false)", state, flaky)
	}

	// Known: older than 14d AND >= 2 commits AND < 2 flaky commits.
	state, flaky = classifyTestState(1, 2, curFrom14d.Add(-time.Second), curFrom14d)
	if state != TestStateKnown || flaky {
		t.Fatalf("known got (%q, %v), want (known, false)", state, flaky)
	}
}
