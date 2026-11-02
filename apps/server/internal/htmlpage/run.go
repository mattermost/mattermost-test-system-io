package htmlpage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
)

// RunPageOpts holds run page query options.
type RunPageOpts struct {
	GHRunID      string
	GHRunAttempt string
	Page         int // 1-based unit list page
}

// Crumb is one breadcrumb entry.
type Crumb struct {
	Label string
	Href  string // empty = current
}

// RunIdentity is the resolved composite identity for templates + WS.
type RunIdentity struct {
	Repository   string
	CommitSHA    string
	ShortSHA     string
	GHRunID      string
	Name         string
	GHRunAttempt string
	Branch       string
	Framework    string
	GHPRNumber   int // 0 = none
}

// WorkerSlot is a worker slot badge on a unit row.
type WorkerSlot struct {
	Label string
	Class string // slot-inflight | slot-pass | slot-fail | slot-neutral
	Title string
}

// RunUnitRow is one dispatch unit row (SpecList-style).
type RunUnitRow struct {
	RowNum             int
	SpecPath           string
	SuiteTitle         string
	Status             string // display label
	StatusIcon         string // pass|fail|flaky|leased|retest|pending|skipped|abandoned
	StatusClass        string
	WorkerSlots        []WorkerSlot
	DurationText       string
	RetestDurationText string
	DurationClass      string // duration-ok | duration-slow | duration-long
	TestPassed         int
	TestFailed         int
	TestFlaky          int
	TestSkipped        int
	TestTotal          int
	LeaseElapsedText   string // set when leased with IssuedAt
	CanExpand          bool
	// Space-separated filter tokens for client-side chips (spec_passed, test_failed, …).
	FilterAttrs string
	SearchText  string // lowercased path + titles
	Tests       []RunTestRow
}

// RunTestRow is an aggregated test under a spec (expandable).
type RunTestRow struct {
	SubIndex     int
	DisplayNum   string // e.g. "108.2" — mirrors SPA SpecList numbering
	Title        string
	FullTitle    string
	StatusIcon   string
	StatusClass  string
	Status       string // passed|failed|flaky|skipped
	DurationText string
	AttemptCount int
	CanExpand    bool
	Attempts     []RunTestAttempt
}

// RunTestAttempt is one chronological attempt/retry for a test.
type RunTestAttempt struct {
	Label        string
	JobName      string
	IsRetest     bool
	StatusIcon   string
	StatusClass  string
	DurationText string
	ErrorText    string
	Screenshots  []RunScreenshot
}

// RunScreenshot is a screenshot thumb / lightbox entry.
type RunScreenshot struct {
	Key  string
	Path string
}

// TimelineSeg is a timeline segment for the summary row.
type TimelineSeg struct {
	Label string
	Text  string
}

// RunPage is the run page view model.
type RunPage struct {
	Site        SiteInfo
	Title       string
	Breadcrumbs []Crumb
	Identity    RunIdentity
	Mode        string // dispatch | reports | empty
	IsLive      bool

	// Summary (ReportSummary-shaped).
	HasTestStatus   bool
	TestStatus      string // passed | failed | timed_out
	TestStatusClass string
	ProgressStatus  string // in_progress | completed | timed_out
	ProgressClass   string
	ProgressLabel   string

	PassRate      string
	PassRateClass string
	HasTests      bool
	TestPassed    int
	TestFailed    int
	TestFlaky     int
	TestSkipped   int
	TestTotal     int

	SetupDurationText  string
	DurationText       string
	RetestDurationText string

	TimelineHeadline string
	TimelineSegs     []TimelineSeg
	HasTimeline      bool

	CreatedAtText string
	SpecsDone     int
	TotalSpecs    int
	SpecsMismatch bool

	// Spec list header chips (all units, not just current page).
	UnitTotalAll         int
	UnitPass             int
	UnitFail             int
	UnitSkipped          int
	SpecInProgress       int
	SpecRetestInProgress int

	Units         []RunUnitRow
	UnitPage      int
	UnitPages     int
	UnitShowStart int
	UnitShowEnd   int

	ReportsHint string
}

// RunDispatchLive is the live fragment body (header chips + rows).
type RunDispatchLive struct {
	UnitTotalAll         int
	UnitPass             int
	UnitFail             int
	UnitSkipped          int
	SpecInProgress       int
	SpecRetestInProgress int
	HasTests             bool
	TestPassed           int
	TestFailed           int
	TestFlaky            int
	TestSkipped          int
	TestTotal            int
	Units                []RunUnitRow
}

// BuildRunPage loads orchestration (preferred) or falls back to empty/reports hint.
// Returns (page, redirectQuery, err). If redirectQuery is non-empty, caller should 303.
func BuildRunPage(
	ctx context.Context,
	pool *pgxpool.Pool,
	orch *orchestration.Store,
	site SiteInfo,
	repoSlug, branchSeg, sha, name string,
	opts RunPageOpts,
) (RunPage, url.Values, error) {
	if pool == nil {
		return RunPage{}, nil, errors.New("htmlpage: nil pool")
	}
	branch := decodeBranchPathSegment(branchSeg)
	attempt := strings.TrimSpace(opts.GHRunAttempt)
	if attempt == "" {
		attempt = "1"
	}
	pageNum := opts.Page
	if pageNum < 1 {
		pageNum = 1
	}

	base := RunPage{
		Site:  site,
		Title: name + " · Test System IO",
		Breadcrumbs: []Crumb{
			{Label: "Reports", Href: "/reports"},
			{Label: repoSlug, Href: "/reports/" + url.PathEscape(repoSlug)},
			{Label: branch, Href: "/reports/" + url.PathEscape(repoSlug) + "/" + url.PathEscape(encodeBranchPathSegment(branch))},
			{Label: shortSHA(sha), Href: "/reports/" + url.PathEscape(repoSlug) + "/" + url.PathEscape(encodeBranchPathSegment(branch)) + "/" + url.PathEscape(shortSHA(sha))},
			{Label: name},
		},
		Identity: RunIdentity{
			Repository:   repoSlug,
			CommitSHA:    sha,
			ShortSHA:     shortSHA(sha),
			Name:         name,
			GHRunAttempt: attempt,
			Branch:       branch,
		},
		UnitPage: pageNum,
		Mode:     modeEmpty,
	}

	ghRunID := strings.TrimSpace(opts.GHRunID)
	if orch == nil {
		base.Mode = modeEmpty
		base.ReportsHint = "Orchestration store unavailable."
		return base, nil, nil
	}

	if ghRunID == "" {
		candidates, err := orch.ListRunsByDisplayIdentity(ctx, repoSlug, sha, name, branch)
		if err != nil {
			return RunPage{}, nil, err
		}
		if len(candidates) == 0 {
			candidates, err = orch.ListRunsByDisplayIdentity(ctx, repoSlug, sha, name, "")
			if err != nil {
				return RunPage{}, nil, err
			}
		}
		picked := pickRunForRedirect(candidates)
		if picked == nil {
			base.Mode = modeEmpty
			base.ReportsHint = "No orchestration run or reports found for this path."
			return base, nil, nil
		}
		q := url.Values{}
		q.Set("gh_run_id", picked.Identity.GHRunID)
		q.Set("gh_run_attempt", picked.Identity.GHRunAttempt)
		if pageNum > 1 {
			q.Set("page", strconv.Itoa(pageNum))
		}
		return RunPage{}, q, nil
	}

	ci := orchestration.CompositeIdentity{
		Repository:   repoSlug,
		CommitSHA:    sha,
		GHRunID:      ghRunID,
		Name:         name,
		GHRunAttempt: attempt,
	}
	snap, err := orch.GetRunWithUnits(ctx, ci)
	if err != nil {
		if errors.Is(err, orchestration.ErrNotFound) {
			base.Mode = modeEmpty
			base.Identity.GHRunID = ghRunID
			base.ReportsHint = "No orchestration run found for this identity."
			return base, nil, nil
		}
		return RunPage{}, nil, err
	}

	fillRunPageFromSnap(&base, snap, repoSlug, branch, pageNum)
	return base, nil, nil
}

func fillRunPageFromSnap(base *RunPage, snap *orchestration.RunWithUnits, repoSlug, branch string, pageNum int) {
	run := snap.Run
	id := run.Identity
	pr := 0
	if id.GHPRNumber != nil {
		pr = *id.GHPRNumber
	}
	base.Identity = RunIdentity{
		Repository:   id.Repository,
		CommitSHA:    id.CommitSHA,
		ShortSHA:     shortSHA(id.CommitSHA),
		GHRunID:      id.GHRunID,
		Name:         id.Name,
		GHRunAttempt: id.GHRunAttempt,
		Branch:       firstNonEmpty(stripRefPrefix(id.Branch), branch),
		Framework:    capitalizeFramework(id.Framework),
		GHPRNumber:   pr,
	}
	if len(base.Breadcrumbs) >= 4 {
		base.Breadcrumbs[3].Label = base.Identity.ShortSHA
		base.Breadcrumbs[3].Href = "/reports/" + url.PathEscape(repoSlug) + "/" +
			url.PathEscape(encodeBranchPathSegment(base.Identity.Branch)) + "/" +
			url.PathEscape(base.Identity.ShortSHA)
	}

	base.Mode = "dispatch"
	base.IsLive = run.Status == orchestration.RunStatusInProgress

	applyRunSummary(base, snap)
	applyUnitPagination(base, snap.Units, pageNum)
}

func applyRunSummary(base *RunPage, snap *orchestration.RunWithUnits) {
	run := snap.Run
	allJobNames := collectJobNames(snap.Units)
	built := buildSpecRows(snap.Units, allJobNames)

	tcPassed, tcFailed, tcFlaky, tcSkipped := 0, 0, 0, 0
	for _, r := range built {
		tcPassed += r.TestPassed
		tcFailed += r.TestFailed
		tcFlaky += r.TestFlaky
		tcSkipped += r.TestSkipped
	}
	tcTotal := tcPassed + tcFailed + tcFlaky + tcSkipped
	base.HasTests = tcTotal > 0
	base.TestPassed = tcPassed
	base.TestFailed = tcFailed
	base.TestFlaky = tcFlaky
	base.TestSkipped = tcSkipped
	base.TestTotal = tcTotal
	if rate := passRateDisplay(tcPassed, tcFailed, tcFlaky); rate != "" {
		base.PassRate = rate
		base.PassRateClass = passRateColorClass(rate)
	}

	switch run.Status {
	case orchestration.RunStatusInProgress:
		base.HasTestStatus = false
		base.ProgressStatus = statusInProgress
		base.ProgressClass = "progress-live"
		base.ProgressLabel = "In Progress"
	case orchestration.RunStatusTimedOut:
		base.HasTestStatus = true
		base.TestStatus = statusTimedOut
		base.TestStatusClass = "test-status-timeout"
		base.ProgressStatus = statusTimedOut
		base.ProgressClass = "progress-timeout"
		base.ProgressLabel = "Timed Out"
	default:
		base.HasTestStatus = true
		if tcFailed > 0 {
			base.TestStatus = statusFailed
			base.TestStatusClass = "test-status-fail"
		} else {
			base.TestStatus = statusPassed
			base.TestStatusClass = "test-status-pass"
		}
		base.ProgressStatus = "completed"
		base.ProgressClass = "progress-done"
		base.ProgressLabel = "Completed"
	}

	c := run.Counts
	base.TotalSpecs = c.Total
	base.SpecsDone = c.CompletedPass + c.CompletedFail + c.CompletedSkipped + c.Abandoned
	base.SpecsMismatch = base.SpecsDone < base.TotalSpecs

	base.UnitTotalAll = len(built)
	for _, r := range built {
		switch r.StatusIcon {
		case iconPass, statusFlaky:
			base.UnitPass++
		case iconFail:
			base.UnitFail++
		case statusSkipped:
			base.UnitSkipped++
		case iconLeased:
			base.SpecInProgress++
		case iconRetest:
			base.SpecRetestInProgress++
		}
	}

	durs := computeTestDurations(snap.Units, run.StartedAt)
	// Match SPA: only show setup when > 0; format sub-second as ms so we
	// never render a misleading "(0s setup)".
	if durs.setupMs > 0 {
		base.SetupDurationText = formatDurationPrecise(durs.setupMs)
	}
	if durs.firstPassMs > 0 {
		base.DurationText = formatDurationPrecise(durs.firstPassMs)
	}
	if durs.retestMs > 0 {
		base.RetestDurationText = formatDurationPrecise(durs.retestMs)
	}

	tl := formatRunTimeline(run.StartedAt, durs.firstTestAt, durs.firstRetestAt, durs.lastTestAt)
	base.HasTimeline = tl.hasContent
	base.TimelineHeadline = tl.headline
	base.TimelineSegs = tl.segments

	base.CreatedAtText = formatRunDate(run.StartedAt)
}

func applyUnitPagination(base *RunPage, units []orchestration.UnitView, pageNum int) {
	allJobNames := collectJobNames(units)
	built := buildSpecRows(units, allJobNames)
	// SPA SpecList keeps the full unit set in memory and paginates
	// client-side after filtering. SSR mirrors that: ship every row so
	// filter chips / search work across the run, not just one server page.
	_ = pageNum
	base.UnitPages = 1
	base.UnitPage = 1
	base.UnitShowStart = 0
	base.UnitShowEnd = 0
	if len(built) > 0 {
		base.UnitShowStart = 1
		base.UnitShowEnd = len(built)
	}
	base.Units = built
}

// BuildRunDispatchLive rebuilds header chips + current page of unit rows for the live fragment.
func BuildRunDispatchLive(
	ctx context.Context,
	orch *orchestration.Store,
	repo, commit, ghRunID, name, attempt string,
	page int,
) (live RunDispatchLive, total int, isLive bool, err error) {
	if orch == nil {
		return RunDispatchLive{}, 0, false, errors.New("htmlpage: nil orch store")
	}
	if attempt == "" {
		attempt = "1"
	}
	if page < 1 {
		page = 1
	}
	ci := orchestration.CompositeIdentity{
		Repository:   repo,
		CommitSHA:    commit,
		GHRunID:      ghRunID,
		Name:         name,
		GHRunAttempt: attempt,
	}
	snap, err := orch.GetRunWithUnits(ctx, ci)
	if err != nil {
		return RunDispatchLive{}, 0, false, err
	}
	tmp := RunPage{}
	applyRunSummary(&tmp, snap)
	applyUnitPagination(&tmp, snap.Units, page)
	return RunDispatchLive{
		UnitTotalAll:         tmp.UnitTotalAll,
		UnitPass:             tmp.UnitPass,
		UnitFail:             tmp.UnitFail,
		UnitSkipped:          tmp.UnitSkipped,
		SpecInProgress:       tmp.SpecInProgress,
		SpecRetestInProgress: tmp.SpecRetestInProgress,
		HasTests:             tmp.HasTests,
		TestPassed:           tmp.TestPassed,
		TestFailed:           tmp.TestFailed,
		TestFlaky:            tmp.TestFlaky,
		TestSkipped:          tmp.TestSkipped,
		TestTotal:            tmp.TestTotal,
		Units:                tmp.Units,
	}, tmp.UnitTotalAll, snap.Run.Status == orchestration.RunStatusInProgress, nil
}

func pickRunForRedirect(runs []*orchestration.Run) *orchestration.Run {
	if len(runs) == 0 {
		return nil
	}
	var bestInProg *orchestration.Run
	for _, r := range runs {
		if r.Status == orchestration.RunStatusInProgress {
			if bestInProg == nil || r.StartedAt.After(bestInProg.StartedAt) {
				bestInProg = r
			}
		}
	}
	if bestInProg != nil {
		return bestInProg
	}
	return runs[0]
}

type orchTestCase struct {
	FullTitle    string           `json:"full_title"`
	Title        string           `json:"title"`
	Status       string           `json:"status"`
	DurationMs   *int64           `json:"duration_ms"`
	ErrorMessage *string          `json:"error_message"`
	ErrorStack   *string          `json:"error_stack"`
	Attachments  *orchAttachments `json:"attachments"`
}

type orchAttachments struct {
	Screenshots []orchScreenshot `json:"screenshots"`
}

type orchScreenshot struct {
	Key          string  `json:"key"`
	RelativePath *string `json:"relative_path"`
}

func collectJobNames(units []orchestration.UnitView) []string {
	var out []string
	for _, u := range units {
		for _, a := range u.Attempts {
			if a.GHJobName != "" {
				out = append(out, a.GHJobName)
			}
		}
	}
	return out
}

func parseAttemptTests(raw json.RawMessage) []orchTestCase {
	if len(raw) == 0 {
		return nil
	}
	var cases []orchTestCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		return nil
	}
	return cases
}

func aggregateUnitTests(u orchestration.UnitView) map[string]string {
	byTitle := map[string][]orchTestCase{}
	order := []string{}
	for _, a := range u.Attempts {
		for _, tc := range parseAttemptTests(a.TestCases) {
			if tc.FullTitle == "" {
				continue
			}
			if _, ok := byTitle[tc.FullTitle]; !ok {
				order = append(order, tc.FullTitle)
			}
			byTitle[tc.FullTitle] = append(byTitle[tc.FullTitle], tc)
		}
	}
	out := make(map[string]string, len(byTitle))
	for _, title := range order {
		entries := byTitle[title]
		everPassed, everFailed, everSkipped := false, false, false
		for _, e := range entries {
			switch e.Status {
			case statusPassed, statusFlaky:
				everPassed = true
			case statusFailed, statusTimedOutPW, statusInterrupted:
				everFailed = true
			case statusSkipped:
				everSkipped = true
			}
		}
		switch {
		case everPassed && everFailed:
			out[title] = statusFlaky
		case everPassed:
			out[title] = statusPassed
		case everFailed:
			out[title] = statusFailed
		case everSkipped:
			out[title] = statusSkipped
		default:
			out[title] = statusFailed
		}
	}
	return out
}

func buildSpecRows(units []orchestration.UnitView, allJobNames []string) []RunUnitRow {
	out := make([]RunUnitRow, 0, len(units))
	now := time.Now()
	for i, u := range units {
		agg := aggregateUnitTests(u)
		passed, failed, flaky, skipped := 0, 0, 0, 0
		for _, s := range agg {
			switch s {
			case statusPassed:
				passed++
			case statusFailed:
				failed++
			case statusFlaky:
				flaky++
			case statusSkipped:
				skipped++
			}
		}
		total := passed + failed + flaky + skipped
		icon := ""
		status := ""
		statusClass := ""
		switch {
		case u.State == orchestration.UnitStatePending:
			icon, status, statusClass = statusPending, statusPending, clsUnitPending
		case u.State == orchestration.UnitStateLeased:
			if u.LeaseCount > 1 {
				icon, status, statusClass = iconRetest, statusRunning, "unit-retest"
			} else {
				icon, status, statusClass = iconLeased, statusRunning, "unit-leased"
			}
		case u.State == orchestration.UnitStateAbandoned:
			icon, status, statusClass = statusAbandoned, statusAbandoned, "unit-abandoned"
		case total == 0:
			status, statusClass = unitStatusDisplay(u.State)
			icon = statusIconFromState(u.State, false)
		case failed > 0:
			icon, status, statusClass = iconFail, statusFailed, clsUnitFail
		case flaky > 0:
			icon, status, statusClass = statusFlaky, statusPassed, "unit-flaky"
		case passed > 0:
			icon, status, statusClass = iconPass, statusPassed, clsUnitPass
		default:
			icon, status, statusClass = statusSkipped, statusSkipped, clsUnitSkipped
		}

		tests := buildUnitTestRows(u, i+1)
		row := RunUnitRow{
			RowNum:      i + 1,
			SpecPath:    u.SpecPath,
			SuiteTitle:  deriveSuiteTitle(u),
			Status:      status,
			StatusIcon:  icon,
			StatusClass: statusClass,
			TestPassed:  passed,
			TestFailed:  failed,
			TestFlaky:   flaky,
			TestSkipped: skipped,
			TestTotal:   total,
			WorkerSlots: buildWorkerSlots(u, allJobNames),
			CanExpand:   total > 0,
			Tests:       tests,
			FilterAttrs: unitFilterAttrs(icon, status, passed, failed, flaky, skipped),
			SearchText:  unitSearchText(u.SpecPath, tests),
		}

		if u.State == orchestration.UnitStateLeased && u.CurrentLease != nil {
			elapsed := now.Sub(u.CurrentLease.IssuedAt)
			if elapsed < 0 {
				elapsed = 0
			}
			row.LeaseElapsedText = formatDuration(elapsed.Milliseconds())
			row.DurationClass = "duration-live"
		} else if len(u.Attempts) > 0 {
			var firstMs *int64
			var retestMs int64
			if u.Attempts[0].ActualDurationMs != nil {
				firstMs = u.Attempts[0].ActualDurationMs
			}
			for j := 1; j < len(u.Attempts); j++ {
				if u.Attempts[j].ActualDurationMs != nil {
					retestMs += *u.Attempts[j].ActualDurationMs
				}
			}
			totalMs := int64(0)
			if firstMs != nil {
				row.DurationText = formatDuration(*firstMs)
				totalMs += *firstMs
			}
			if retestMs > 0 {
				row.RetestDurationText = formatDuration(retestMs)
				totalMs += retestMs
			}
			row.DurationClass = durationColorClass(totalMs)
		}
		out = append(out, row)
	}
	return out
}

func unitFilterAttrs(icon, _ string, passed, failed, flaky, skipped int) string {
	var parts []string
	switch icon {
	case iconPass, statusFlaky:
		parts = append(parts, "spec_passed")
	case iconFail:
		parts = append(parts, "spec_failed")
	case iconLeased:
		parts = append(parts, "spec_in_progress")
	case iconRetest:
		parts = append(parts, "spec_retest_in_progress")
	case statusSkipped:
		parts = append(parts, "spec_skipped")
	}
	if passed > 0 {
		parts = append(parts, "test_passed")
	}
	if failed > 0 {
		parts = append(parts, "test_failed")
	}
	if flaky > 0 {
		parts = append(parts, "test_flaky")
	}
	if skipped > 0 {
		parts = append(parts, "test_skipped")
	}
	return strings.Join(parts, " ")
}

func unitSearchText(path string, tests []RunTestRow) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(path))
	for _, t := range tests {
		b.WriteByte(' ')
		b.WriteString(strings.ToLower(t.Title))
		b.WriteByte(' ')
		b.WriteString(strings.ToLower(t.FullTitle))
	}
	return b.String()
}

type testCaseEntry struct {
	tc         orchTestCase
	ghJobName  string
	attemptIdx int
}

func buildUnitTestRows(u orchestration.UnitView, parentNum int) []RunTestRow {
	byTitle := map[string][]testCaseEntry{}
	order := []string{}
	for attemptIdx, a := range u.Attempts {
		for _, tc := range parseAttemptTests(a.TestCases) {
			if tc.FullTitle == "" {
				continue
			}
			if _, ok := byTitle[tc.FullTitle]; !ok {
				order = append(order, tc.FullTitle)
			}
			byTitle[tc.FullTitle] = append(byTitle[tc.FullTitle], testCaseEntry{
				tc: tc, ghJobName: a.GHJobName, attemptIdx: attemptIdx,
			})
		}
	}
	out := make([]RunTestRow, 0, len(order))
	for i, title := range order {
		entries := byTitle[title]
		agg := aggregateTestEntries(entries)
		final := entries[len(entries)-1].tc
		sub := i + 1
		row := RunTestRow{
			SubIndex:     sub,
			DisplayNum:   fmt.Sprintf("%d.%d", parentNum, sub),
			Title:        final.Title,
			FullTitle:    title,
			Status:       agg,
			StatusIcon:   testStatusIcon(agg),
			StatusClass:  testStatusClass(agg),
			AttemptCount: len(entries),
		}
		if final.DurationMs != nil {
			row.DurationText = formatDurationPrecise(*final.DurationMs)
		}
		hasErr := false
		hasShot := false
		for _, e := range entries {
			att := buildTestAttempt(e, len(row.Attempts)+1)
			if att.ErrorText != "" {
				hasErr = true
			}
			if len(att.Screenshots) > 0 {
				hasShot = true
			}
			row.Attempts = append(row.Attempts, att)
		}
		row.CanExpand = len(entries) > 1 || hasErr || hasShot
		out = append(out, row)
	}
	return out
}

func aggregateTestEntries(entries []testCaseEntry) string {
	everPassed, everFailed, everSkipped := false, false, false
	for _, e := range entries {
		switch e.tc.Status {
		case statusPassed, statusFlaky:
			everPassed = true
		case statusFailed, statusTimedOutPW, statusInterrupted:
			everFailed = true
		case statusSkipped:
			everSkipped = true
		}
	}
	switch {
	case everPassed && everFailed:
		return statusFlaky
	case everPassed:
		return statusPassed
	case everFailed:
		return statusFailed
	case everSkipped:
		return statusSkipped
	default:
		return statusFailed
	}
}

func testStatusIcon(status string) string {
	switch status {
	case statusPassed:
		return iconPass
	case statusFailed:
		return iconFail
	case statusFlaky:
		return statusFlaky
	case statusSkipped:
		return statusSkipped
	default:
		return iconFail
	}
}

func testStatusClass(status string) string {
	switch status {
	case statusPassed:
		return clsUnitPass
	case statusFailed:
		return clsUnitFail
	case statusFlaky:
		return "unit-flaky"
	case statusSkipped:
		return clsUnitSkipped
	default:
		return clsUnitPending
	}
}

func buildTestAttempt(e testCaseEntry, n int) RunTestAttempt {
	tc := e.tc
	isFail := tc.Status == statusFailed || tc.Status == statusTimedOutPW || tc.Status == statusInterrupted
	isSkip := tc.Status == statusSkipped
	isPass := tc.Status == statusPassed || tc.Status == statusFlaky
	icon, cls := iconFail, clsUnitFail
	switch {
	case isSkip:
		icon, cls = statusSkipped, clsUnitSkipped
	case isPass:
		icon, cls = iconPass, clsUnitPass
	case isFail:
		icon, cls = iconFail, clsUnitFail
	}
	att := RunTestAttempt{
		Label:       fmt.Sprintf("Attempt %d", n),
		JobName:     e.ghJobName,
		IsRetest:    e.attemptIdx > 0,
		StatusIcon:  icon,
		StatusClass: cls,
	}
	if tc.DurationMs != nil {
		att.DurationText = formatDurationPrecise(*tc.DurationMs)
	}
	if isFail {
		if tc.ErrorStack != nil && *tc.ErrorStack != "" {
			att.ErrorText = *tc.ErrorStack
		} else if tc.ErrorMessage != nil {
			att.ErrorText = *tc.ErrorMessage
		}
	}
	if tc.Attachments != nil {
		for _, s := range tc.Attachments.Screenshots {
			if s.Key == "" {
				continue
			}
			path := s.Key
			if s.RelativePath != nil && *s.RelativePath != "" {
				path = *s.RelativePath
			}
			att.Screenshots = append(att.Screenshots, RunScreenshot{Key: s.Key, Path: path})
		}
	}
	return att
}

func statusIconFromState(state string, flaky bool) string {
	if flaky {
		return statusFlaky
	}
	switch state {
	case orchestration.UnitStatePending:
		return statusPending
	case orchestration.UnitStateLeased:
		return iconLeased
	case orchestration.UnitStateCompletedPass:
		return iconPass
	case orchestration.UnitStateCompletedFail:
		return iconFail
	case orchestration.UnitStateCompletedSkipped:
		return statusSkipped
	case orchestration.UnitStateAbandoned:
		return statusAbandoned
	default:
		return statusPending
	}
}

func durationColorClass(totalMs int64) string {
	switch {
	case totalMs >= 180_000:
		return "duration-long"
	case totalMs >= 120_000:
		return "duration-slow"
	default:
		return "duration-ok"
	}
}

func buildWorkerSlots(u orchestration.UnitView, allJobNames []string) []WorkerSlot {
	if len(u.Attempts) == 0 {
		return nil
	}
	out := make([]WorkerSlot, 0, len(u.Attempts))
	for i, a := range u.Attempts {
		slot := workerSlotLabel(a.GHJobName, i+1, allJobNames)
		inFlight := a.Status == nil && !a.Expired
		passed := a.Status != nil && (*a.Status == statusPassed || *a.Status == statusFlaky)
		failed := a.Expired || (a.Status != nil && (*a.Status == statusFailed || *a.Status == statusTimedOutPW || *a.Status == statusInterrupted))
		cls := "slot-neutral"
		switch {
		case inFlight:
			cls = "slot-inflight"
		case passed:
			cls = "slot-pass"
		case failed:
			cls = "slot-fail"
		}
		title := a.GHJobName
		if title == "" {
			title = fmt.Sprintf("Attempt %d", i+1)
		}
		out = append(out, WorkerSlot{Label: strconv.Itoa(slot), Class: cls, Title: title})
	}
	return out
}

var (
	trailingNumberRE = regexp.MustCompile(`^(.*)-(\d+)$`)
	specFilePartRE   = regexp.MustCompile(`\.spec\.[tj]sx?$`)
)

func capitalizeFramework(fw string) string {
	fw = strings.TrimSpace(fw)
	if fw == "" {
		return ""
	}
	return strings.ToUpper(fw[:1]) + fw[1:]
}

func workerSlotLabel(name string, fallback int, siblings []string) int {
	if name == "" {
		return fallback
	}
	m := trailingNumberRE.FindStringSubmatch(name)
	if m == nil {
		return fallback
	}
	base := m[1]
	hasShared := false
	for _, other := range siblings {
		if other == "" || other == name {
			continue
		}
		om := trailingNumberRE.FindStringSubmatch(other)
		if om != nil && om[1] == base {
			hasShared = true
			break
		}
	}
	if !hasShared {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(m[2], "%d", &n); err != nil {
		return fallback
	}
	return n
}

func deriveSuiteTitle(u orchestration.UnitView) string {
	var tc *orchTestCase
	for i := len(u.Attempts) - 1; i >= 0; i-- {
		cases := parseAttemptTests(u.Attempts[i].TestCases)
		if len(cases) > 0 {
			tc = &cases[0]
			break
		}
	}
	if tc == nil || tc.FullTitle == "" {
		return ""
	}
	suite := tc.FullTitle
	if tc.Title != "" {
		pw := " > " + tc.Title
		switch {
		case strings.HasSuffix(suite, pw):
			suite = suite[:len(suite)-len(pw)]
		case suite == tc.Title:
			return ""
		case strings.HasSuffix(suite, " "+tc.Title):
			suite = suite[:len(suite)-len(tc.Title)-1]
		}
	}
	parts := strings.Split(suite, " > ")
	fileIdx := -1
	for i, p := range parts {
		if specFilePartRE.MatchString(p) {
			fileIdx = i
			break
		}
	}
	if fileIdx >= 0 && fileIdx+1 < len(parts) {
		return strings.Join(parts[fileIdx+1:], " > ")
	}
	return suite
}

type testDurations struct {
	setupMs       int64
	firstPassMs   int64
	retestMs      int64
	firstTestAt   *time.Time
	firstRetestAt *time.Time
	lastTestAt    *time.Time
}

func computeTestDurations(units []orchestration.UnitView, runStartedAt time.Time) testDurations {
	firstStart := int64(math.MaxInt64)
	firstEnd := int64(0)
	retestStart := int64(math.MaxInt64)
	retestEnd := int64(0)
	var firstTestAt, firstRetestAt, lastTestAt *time.Time

	for _, u := range units {
		for i, a := range u.Attempts {
			startMs := a.CreatedAt.UnixMilli()
			isRetest := i > 0
			if isRetest {
				if startMs < retestStart {
					retestStart = startMs
					t := a.CreatedAt
					firstRetestAt = &t
				}
			} else {
				if startMs < firstStart {
					firstStart = startMs
					t := a.CreatedAt
					firstTestAt = &t
				}
			}
			if a.ReportedAt != nil {
				endMs := a.ReportedAt.UnixMilli()
				if isRetest {
					if endMs > retestEnd {
						retestEnd = endMs
					}
				} else if endMs > firstEnd {
					firstEnd = endMs
				}
				if lastTestAt == nil || a.ReportedAt.After(*lastTestAt) {
					t := *a.ReportedAt
					lastTestAt = &t
				}
			}
		}
	}

	var d testDurations
	d.firstTestAt = firstTestAt
	d.firstRetestAt = firstRetestAt
	d.lastTestAt = lastTestAt
	runStartMs := runStartedAt.UnixMilli()
	if firstStart < math.MaxInt64 && runStartMs > 0 {
		setup := firstStart - runStartMs
		if setup > 0 {
			d.setupMs = setup
		}
	}
	if firstStart < math.MaxInt64 && firstEnd > firstStart {
		d.firstPassMs = firstEnd - firstStart
	}
	if retestStart < math.MaxInt64 && retestEnd > retestStart {
		d.retestMs = retestEnd - retestStart
	}
	return d
}

type runTimeline struct {
	headline   string
	segments   []TimelineSeg
	hasContent bool
}

func formatRunTimeline(begin time.Time, first, retest, last *time.Time) runTimeline {
	type raw struct {
		label string
		t     time.Time
	}
	var raws []raw
	if !begin.IsZero() {
		raws = append(raws, raw{"Begin", begin})
	}
	if first != nil {
		raws = append(raws, raw{"First test", *first})
	}
	if retest != nil {
		raws = append(raws, raw{"First retest", *retest})
	}
	if last != nil {
		raws = append(raws, raw{"Last test", *last})
	}
	if len(raws) == 0 {
		return runTimeline{}
	}
	sameDay := true
	y0, m0, d0 := raws[0].t.Local().Date()
	for _, r := range raws[1:] {
		y, m, d := r.t.Local().Date()
		if y != y0 || m != m0 || d != d0 {
			sameDay = false
			break
		}
	}
	out := runTimeline{hasContent: true}
	if sameDay {
		out.headline = raws[0].t.Local().Format("Jan 2, 2006")
		for _, r := range raws {
			out.segments = append(out.segments, TimelineSeg{
				Label: r.label,
				Text:  r.t.Local().Format("3:04 PM"),
			})
		}
	} else {
		for _, r := range raws {
			out.segments = append(out.segments, TimelineSeg{
				Label: r.label,
				Text:  r.t.Local().Format("Jan 2, 3:04 PM"),
			})
		}
	}
	return out
}

func formatDurationPrecise(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return formatDuration(ms)
}

func formatRunDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("Jan 2, 2006, 3:04 PM")
}

func unitStatusDisplay(state string) (label, class string) {
	switch state {
	case orchestration.UnitStatePending:
		return statusPending, clsUnitPending
	case orchestration.UnitStateLeased:
		return statusRunning, "unit-leased"
	case orchestration.UnitStateCompletedPass:
		return statusPassed, clsUnitPass
	case orchestration.UnitStateCompletedFail:
		return statusFailed, clsUnitFail
	case orchestration.UnitStateCompletedSkipped:
		return statusSkipped, clsUnitSkipped
	case orchestration.UnitStateAbandoned:
		return statusAbandoned, "unit-abandoned"
	default:
		return state, clsUnitPending
	}
}

func decodeBranchPathSegment(seg string) string {
	s := strings.TrimSpace(seg)
	if u, err := url.PathUnescape(s); err == nil {
		s = u
	}
	return strings.ReplaceAll(s, "~", "/")
}

func encodeBranchPathSegment(branch string) string {
	return strings.ReplaceAll(stripRefPrefix(branch), "/", "~")
}

func stripRefPrefix(branch string) string {
	b := strings.TrimSpace(branch)
	b = strings.TrimPrefix(b, "refs/heads/")
	b = strings.TrimPrefix(b, "refs/tags/")
	return b
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
