package kpi

import (
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
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseSummaryParams(tc.q, now); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
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
