package htmlpage

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

// KPIRepoScorecardRow summarizes one repository's compatible health metrics on All Repos view.
type KPIRepoScorecardRow struct {
	Repository      string
	DisplayName     string
	Href            string
	RunnersSummary  string
	IsOrchestrated  bool
	MainRunPassText string
	MainRunClass    string
	MainTestSub     string
	ZeroRetryText   string
	PRFalseRedText  string
	PRFalseRedSub   string
	FlakyText       string
	PRDurationText  string
	PRDurationSub   string
	TimeoutsText    string
	TimeoutsClass   string
}

// KPILaneBreakdownRow combines main pass, PR false-red, and PR duration per lane inside a repo tab.
type KPILaneBreakdownRow struct {
	Lane           string
	Href           string
	Stage          string
	RunsCount      int
	RunPassText    string
	RunPassClass   string
	TestPassText   string
	ZeroRetryText  string
	PRDurationText string
	PRFalseRedText string
}

// KPIMainPassCard holds KPI 1 card data (single-repo view).
type KPIMainPassCard struct {
	HasData            bool
	HealthLabel        string
	HealthClass        string
	PrimaryRate        string
	PrimaryDelta       string
	DeltaClass         string
	PrimaryMeta        string
	FirstAttemptPassed int
	TotalTests         int
	LaneRate           string
	LanePassed         int
	LaneTotal          int
	HasStrict          bool
	StrictRate         string
	StrictPassed       int
	StrictTotal        int
}

// KPIPRFalseRedCard holds KPI 2 card data (single-repo view).
type KPIPRFalseRedCard struct {
	HasData              bool
	HealthLabel          string
	HealthClass          string
	HeadlineRate         string
	Delta                string
	DeltaClass           string
	SettledFalseRedLanes int
	SettledRedLanes      int
	UnsettledRedLanes    int
	SettledNote          string
	PRsHref              string
}

// KPIFlakyCard holds KPI 3 card data (single-repo view).
type KPIFlakyCard struct {
	HasData     bool
	HealthLabel string
	HealthClass string
	Count       int
	Delta       string
	DeltaClass  string
	RuleNote    string
}

// KPIDurationCard holds KPI 5 + run completion card data (orchestrated repos).
type KPIDurationCard struct {
	ShowCard       bool
	HasData        bool
	P50            string
	P50Delta       string
	P50DeltaClass  string
	P90            string
	SampleCount    int
	SampleText     string
	TimedOutRuns   int
	CompletedRuns  int
	CompletionRate string
}

// KPIRecurringFailureRow holds one row in the recurring failures table.
type KPIRecurringFailureRow struct {
	SignatureID        string
	Href               string
	Title              string
	NormalizedMessage  string
	TopFrame           string
	StateLabel         string
	StateClass         string
	OccurrenceCount    int
	GroupCount         int
	AffectedTestCount  int
	MetaSummary        string
	DeltaRate14d       string
	DeltaClass         string
	LastSeenISO        string
	LastSeenRel        string
	ReposSummary       string
	LatestVerdictLabel string
}

// KPIOverviewPage is the V2 view model for GET /kpi.
// Author names and verdicts.error_context are intentionally omitted.
type KPIOverviewPage struct {
	Title             string
	Site              SiteInfo
	Filters           KPIFilterBar
	OverviewHref      string
	PRsHref           string
	IsAllRepos        bool
	ActiveRepoLabel   string
	PulseSummary      string
	HasScorecard      bool
	RepoScorecard     []KPIRepoScorecardRow
	HasLaneRows       bool
	LaneRows          []KPILaneBreakdownRow
	MainPass          KPIMainPassCard
	PRFalseRed        KPIPRFalseRedCard
	Flaky             KPIFlakyCard
	E2EDuration       KPIDurationCard
	HasWeekly         bool
	WeeklyBars        []KPIChartBar
	WeeklyChartW      int
	ShowAll           bool
	ActiveOnlyHref    string
	ShowAllHref       string
	ActiveTotalCount  int
	AllTotalCount     int
	DormantCount      int
	HasFailures       bool
	Failures          []KPIRecurringFailureRow
	Total             int
	TotalPatternsText string
	Page              int
	TotalPages        int
	RowStart          int
	ShowEnd           int
	ShowPagination    bool
	HasPrev           bool
	HasNext           bool
	PrevHref          string
	NextHref          string
}

// BuildKPIOverviewPage maps KPI summary and failures into KPIOverviewPage defaulting to Active-only.
func BuildKPIOverviewPage(
	site SiteInfo,
	opts kpi.FilterOptions,
	summary kpi.SummaryResponse,
	failures kpi.FailuresResponse,
	now time.Time,
) KPIOverviewPage {
	return BuildKPIOverviewPageWithShow(site, opts, summary, failures, now, false)
}

// BuildKPIOverviewPageWithShow maps KPI summary and failures into KPIOverviewPage with explicit ShowAll toggle.
func BuildKPIOverviewPageWithShow(
	site SiteInfo,
	opts kpi.FilterOptions,
	summary kpi.SummaryResponse,
	failures kpi.FailuresResponse,
	now time.Time,
	showAll bool,
) KPIOverviewPage {
	filterParams := failures.Filters
	if filterParams.Window == "" {
		filterParams = summary.Filters
	}
	filterBar := buildKPIFilterBar(filterParams, opts, "/kpi", kpi.SortOccurrencesDesc, kpiOverviewSortOptions)
	filterBar.State.ShowAll = showAll

	overviewHref := kpiHref("/kpi", filterBar.State, 1, "", false)
	prsHref := kpiHref("/kpi/prs", filterBar.State, 1, "", false)
	activeOnlyHref := kpiOverviewHref(filterBar.State, false)
	showAllHref := kpiOverviewHref(filterBar.State, true)

	isAllRepos := !filterBar.IsSingleRepo && filterBar.State.Lane == ""
	activeRepoLabel := ""
	if filterBar.State.Repo != "" {
		activeRepoLabel = friendlyRepoName(filterBar.State.Repo)
	} else if filterBar.State.App != "" {
		activeRepoLabel = filterBar.State.App
	}

	scorecard := buildRepoScorecard(opts, summary, filterBar.State)
	laneRows := buildLaneBreakdown(summary, filterBar.State)

	// Single-repo cards.
	tl := summary.MainPassRate.TestLevel
	ll := summary.MainPassRate.LaneLevel
	mainRate := formatPct(ll.PassRate)
	mainDelta := formatDeltaPP(ll.DeltaPP)
	mainDeltaClass := deltaClassHigherGood(ll.DeltaPP)
	mainHealthLabel, mainHealthClass := healthBadgeHigherGood(ll.PassRate, 90, 75)
	if ll.TotalLanes == 0 && tl.TotalTests > 0 {
		mainRate = formatPct(tl.PassRate)
		mainDelta = formatDeltaPP(tl.DeltaPP)
		mainDeltaClass = deltaClassHigherGood(tl.DeltaPP)
		mainHealthLabel, mainHealthClass = healthBadgeHigherGood(tl.PassRate, 95, 85)
	}
	mainMeta := fmt.Sprintf(
		"%d of %s green · %s first-try tests (%d/%d)",
		ll.PassedLanes,
		pluralize(ll.TotalLanes, "run", "runs"),
		formatPct(tl.PassRate),
		tl.FirstAttemptPassed,
		tl.TotalTests,
	)
	mainCard := KPIMainPassCard{
		HasData:            tl.TotalTests > 0 || ll.TotalLanes > 0,
		HealthLabel:        mainHealthLabel,
		HealthClass:        mainHealthClass,
		PrimaryRate:        mainRate,
		PrimaryDelta:       mainDelta,
		DeltaClass:         mainDeltaClass,
		PrimaryMeta:        mainMeta,
		FirstAttemptPassed: tl.FirstAttemptPassed,
		TotalTests:         tl.TotalTests,
		LaneRate:           formatPct(ll.PassRate),
		LanePassed:         ll.PassedLanes,
		LaneTotal:          ll.TotalLanes,
		HasStrict:          ll.StrictTotalLanes > 0,
		StrictRate:         formatPct(ll.StrictPassRate),
		StrictPassed:       ll.StrictPassedLanes,
		StrictTotal:        ll.StrictTotalLanes,
	}

	prCard := buildPRFalseRedCard(summary.PRFalseRed, prsHref)

	flakyHasData := summary.LaneCoverage.TotalRuns > 0 || summary.FlakyTests.Count > 0
	var flakyHealthLabel, flakyHealthClass string
	if flakyHasData {
		cnt := float64(summary.FlakyTests.Count)
		flakyHealthLabel, flakyHealthClass = healthBadgeLowerGood(&cnt, 0, 5)
	}
	flakyCard := KPIFlakyCard{
		HasData:     flakyHasData,
		HealthLabel: flakyHealthLabel,
		HealthClass: flakyHealthClass,
		Count:       summary.FlakyTests.Count,
		Delta:       formatSignedIntDelta(summary.FlakyTests.Delta),
		DeltaClass:  deltaIntLowerGoodClass(summary.FlakyTests.Delta),
		RuleNote:    "Trailing 14d · passed & failed on same commit ≥2 times",
	}

	dur := summary.E2EDuration
	durCard := KPIDurationCard{
		ShowCard:      dur.SampleCount > 0 || summary.LaneCoverage.OrchestratedRuns > 0,
		HasData:       dur.SampleCount > 0 && dur.P50Ms != nil,
		P50:           formatDurationMs(dur.P50Ms),
		P50Delta:      formatDeltaDurationMs(dur.DeltaP50Ms),
		P50DeltaClass: deltaClassLowerGood(dur.DeltaP50Ms),
		P90:           formatDurationMs(dur.P90Ms),
		SampleCount:   dur.SampleCount,
		SampleText:    pluralize(dur.SampleCount, "orchestrated PR run", "orchestrated PR runs"),
	}
	if summary.Triage != nil && summary.Triage.RunCompletion.TotalRuns > 0 {
		rc := summary.Triage.RunCompletion
		durCard.TimedOutRuns = rc.TimedOutRuns
		durCard.CompletedRuns = rc.CompletedRuns
		durCard.CompletionRate = formatPct(rc.CompletionRate)
	}

	recSum := failures.Summary
	if summary.RecurringFailures != nil {
		recSum = *summary.RecurringFailures
	}
	activeTotal := recSum.ActiveCount + recSum.ResurfacedCount
	dormantTotal := recSum.QuietCount + recSum.NotEnoughRunsCount

	weeklyBars, chartW := buildWeeklyChartBars(summary.WeeklySeries)

	failureRows := buildFailureTableRows(failures.Failures, filterBar.State, now)
	pag := buildKPIPagination(filterBar.State, failures.Pagination.Page, failures.Pagination.Limit, failures.Pagination.Total, failures.Pagination.TotalPages)

	pulse := buildOverviewPulse(isAllRepos, activeRepoLabel, filterBar.State.Window, scorecard, mainCard, flakyCard, activeTotal)

	return KPIOverviewPage{
		Title:             "KPIs · Test System IO",
		Site:              site,
		Filters:           filterBar,
		OverviewHref:      overviewHref,
		PRsHref:           prsHref,
		IsAllRepos:        isAllRepos,
		ActiveRepoLabel:   activeRepoLabel,
		PulseSummary:      pulse,
		HasScorecard:      len(scorecard) > 0,
		RepoScorecard:     scorecard,
		HasLaneRows:       len(laneRows) > 0,
		LaneRows:          laneRows,
		MainPass:          mainCard,
		PRFalseRed:        prCard,
		Flaky:             flakyCard,
		E2EDuration:       durCard,
		HasWeekly:         !isAllRepos && len(weeklyBars) > 0,
		WeeklyBars:        weeklyBars,
		WeeklyChartW:      chartW,
		ShowAll:           showAll,
		ActiveOnlyHref:    activeOnlyHref,
		ShowAllHref:       showAllHref,
		ActiveTotalCount:  activeTotal,
		AllTotalCount:     recSum.TotalSignatures,
		DormantCount:      dormantTotal,
		HasFailures:       len(failureRows) > 0,
		Failures:          failureRows,
		Total:             failures.Pagination.Total,
		TotalPatternsText: pluralize(failures.Pagination.Total, "failure pattern", "failure patterns"),
		Page:              pag.Page,
		TotalPages:        pag.TotalPages,
		RowStart:          pag.RowStart,
		ShowEnd:           pag.ShowEnd,
		ShowPagination:    pag.ShowPagination,
		HasPrev:           pag.HasPrev,
		HasNext:           pag.HasNext,
		PrevHref:          pag.PrevHref,
		NextHref:          pag.NextHref,
	}
}

func buildOverviewPulse(
	isAllRepos bool,
	repoLabel, window string,
	scorecard []KPIRepoScorecardRow,
	mainCard KPIMainPassCard,
	flakyCard KPIFlakyCard,
	activePatterns int,
) string {
	if isAllRepos {
		if len(scorecard) == 0 {
			return fmt.Sprintf("Org Pulse (%s): No test runs in this window.", window)
		}
		parts := make([]string, 0, len(scorecard))
		for _, r := range scorecard {
			parts = append(parts, fmt.Sprintf("%s %s", r.DisplayName, r.MainRunPassText))
		}
		return fmt.Sprintf("Org Pulse (%s) · Main run pass: %s · %s.",
			window, strings.Join(parts, " · "), pluralize(activePatterns, "active failure pattern", "active failure patterns"))
	}
	if !mainCard.HasData {
		return fmt.Sprintf("%s (%s): %s · %s.",
			repoLabel, window, pluralize(flakyCard.Count, "flaky test", "flaky tests"), pluralize(activePatterns, "active failure pattern", "active failure patterns"))
	}
	return fmt.Sprintf("%s (%s): Main run pass %s · %s · %s.",
		repoLabel, window, mainCard.PrimaryRate, pluralize(flakyCard.Count, "flaky test", "flaky tests"), pluralize(activePatterns, "active failure pattern", "active failure patterns"))
}

type repoAccum struct {
	repo                                string
	app                                 string
	firstPass, testsTotal               int
	lanesPassed, lanesTotal             int
	strictPassed, strictTotal           int
	orchRuns, uploadRuns                int
	settledRed, settledFalse, unsettled int
	wentGreen, failingElsewhere, other  int
	prOrch, prUpload                    int
	durSamples                          int
	weightedP50                         float64
	maxP90                              float64
	exactP50, exactP90                  *float64
	durLaneCount                        int
}

func buildRepoScorecard(opts kpi.FilterOptions, summary kpi.SummaryResponse, base KPIFilterState) []KPIRepoScorecardRow {
	byRepo := map[string]*repoAccum{}
	ensure := func(repo, app string) *repoAccum {
		a, ok := byRepo[repo]
		if !ok {
			a = &repoAccum{repo: repo, app: app}
			byRepo[repo] = a
		} else if a.app == "" && app != "" {
			a.app = app
		}
		return a
	}

	for _, r := range opts.Repos {
		if r != "" {
			ensure(r, "")
		}
	}
	for _, m := range summary.MainPassRate.ByLane {
		a := ensure(m.Repository, m.App)
		a.firstPass += m.FirstAttemptPassed
		a.testsTotal += m.TestsTotal
		a.lanesPassed += m.LanesPassed
		a.lanesTotal += m.LanesTotal
		a.strictPassed += m.StrictLanesPassed
		a.strictTotal += m.StrictLanesTotal
		a.orchRuns += m.OrchestratedRuns
		a.uploadRuns += m.UploadOnlyRuns
	}
	if summary.PRFalseRed != nil {
		for _, p := range summary.PRFalseRed.ByLane {
			a := ensure(p.Repository, p.App)
			a.settledRed += p.SettledRedLanes
			a.settledFalse += p.SettledFalseRedLanes
			a.unsettled += p.UnsettledRedLanes
			a.wentGreen += p.WentGreenOnRerun
			a.failingElsewhere += p.FailingElsewhere
			a.other += p.Other
			a.prOrch += p.OrchestratedRuns
			a.prUpload += p.UploadOnlyRuns
		}
	}
	durReposWithSamples := 0
	for _, d := range summary.E2EDuration.ByLane {
		a := ensure(d.Repository, d.App)
		a.prOrch += d.OrchestratedRuns
		a.prUpload += d.UploadOnlyRuns
		if d.SampleCount > 0 && d.P50Ms != nil {
			if a.durSamples == 0 {
				durReposWithSamples++
			}
			a.durSamples += d.SampleCount
			a.durLaneCount++
			a.weightedP50 += *d.P50Ms * float64(d.SampleCount)
			a.exactP50 = d.P50Ms
			if d.P90Ms != nil {
				a.exactP90 = d.P90Ms
				if *d.P90Ms > a.maxP90 {
					a.maxP90 = *d.P90Ms
				}
			}
		}
	}

	flakyByApp := map[string]kpi.AppFlakyCount{}
	for _, fa := range summary.FlakyTests.ByApp {
		flakyByApp[fa.App] = fa
	}

	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	out := make([]KPIRepoScorecardRow, 0, len(repos))
	for _, repo := range repos {
		a := byRepo[repo]
		if a.lanesTotal == 0 && a.testsTotal == 0 && a.settledRed == 0 && a.unsettled == 0 && a.durSamples == 0 && a.prOrch == 0 && a.prUpload == 0 {
			continue
		}
		out = append(out, formatRepoScorecardRow(a, durReposWithSamples, summary, flakyByApp, base))
	}
	return out
}

func formatRepoScorecardRow(
	a *repoAccum,
	durReposWithSamples int,
	summary kpi.SummaryResponse,
	flakyByApp map[string]kpi.AppFlakyCount,
	base KPIFilterState,
) KPIRepoScorecardRow {
	rState := KPIFilterState{Repo: a.repo, Window: base.Window, Limit: base.Limit, ActionPath: "/kpi"}
	isOrch := a.orchRuns+a.prOrch > 0
	mode := "Upload-only"
	if isOrch {
		mode = "Orchestrated"
	}

	mainRunText, mainRunClass, mainTestSub := kpiNotAvailable, "kpi-badge kpi-badge-muted", ""
	if a.lanesTotal > 0 {
		pct := float64(a.lanesPassed) * 100.0 / float64(a.lanesTotal)
		mainRunText = fmt.Sprintf("%.1f%% (%d/%d)", pct, a.lanesPassed, a.lanesTotal)
		_, mainRunClass = healthBadgeHigherGood(&pct, 90, 75)
	}
	if a.testsTotal > 0 {
		testPct := float64(a.firstPass) * 100.0 / float64(a.testsTotal)
		mainTestSub = fmt.Sprintf("%.1f%% tests (%d/%d)", testPct, a.firstPass, a.testsTotal)
	}

	zeroRetryText := kpiDash
	if a.strictTotal > 0 {
		strictPct := float64(a.strictPassed) * 100.0 / float64(a.strictTotal)
		zeroRetryText = fmt.Sprintf("%.0f%% (%d/%d)", strictPct, a.strictPassed, a.strictTotal)
	}

	prText, prSub := formatRepoPRFalseRedCell(a)

	flakyText := "0"
	if appName := resolveRepoApp(a.repo, a.app); appName != "" {
		if fa, ok := flakyByApp[appName]; ok {
			flakyText = strconv.Itoa(fa.Count)
			if fa.Delta != nil {
				flakyText += " (" + formatSignedIntDelta(fa.Delta) + ")"
			}
		}
	}

	durText, durSub := formatRepoDurationCell(a, durReposWithSamples, summary.E2EDuration)

	timeoutsText, timeoutsClass := kpiDash, "kpi-muted-text"
	if isOrch && summary.Triage != nil && summary.Triage.RunCompletion.TotalRuns > 0 {
		to := summary.Triage.RunCompletion.TimedOutRuns
		timeoutsText = pluralize(to, "timeout", "timeouts")
		if to > 0 {
			timeoutsClass = "kpi-badge kpi-reason-elsewhere"
		} else {
			timeoutsClass = "kpi-badge kpi-state-quiet"
		}
	}

	return KPIRepoScorecardRow{
		Repository:      a.repo,
		DisplayName:     friendlyRepoName(a.repo),
		Href:            kpiHref("/kpi", rState, 1, "", false),
		RunnersSummary:  mode,
		IsOrchestrated:  isOrch,
		MainRunPassText: mainRunText,
		MainRunClass:    mainRunClass,
		MainTestSub:     mainTestSub,
		ZeroRetryText:   zeroRetryText,
		PRFalseRedText:  prText,
		PRFalseRedSub:   prSub,
		FlakyText:       flakyText,
		PRDurationText:  durText,
		PRDurationSub:   durSub,
		TimeoutsText:    timeoutsText,
		TimeoutsClass:   timeoutsClass,
	}
}

func formatRepoPRFalseRedCell(a *repoAccum) (text, sub string) {
	if a.settledRed > 0 {
		if a.wentGreen == 0 && a.failingElsewhere == 0 {
			text = fmt.Sprintf("0/%d settled", a.settledRed)
			sub = "unclassified"
		} else {
			pct := float64(a.settledFalse) * 100.0 / float64(a.settledRed)
			text = fmt.Sprintf("at least %.1f%% (%d/%d)", pct, a.settledFalse, a.settledRed)
		}
	} else {
		text = "0 settled"
	}
	if a.unsettled > 0 {
		if sub != "" {
			sub += " · "
		}
		sub += fmt.Sprintf("%d <24h", a.unsettled)
	}
	return text, sub
}

func formatRepoDurationCell(a *repoAccum, durReposWithSamples int, globalDur kpi.E2EDurationSummary) (text, sub string) {
	if a.durSamples == 0 {
		return kpiDash, ""
	}
	p50 := a.weightedP50 / float64(a.durSamples)
	p90 := a.maxP90
	if durReposWithSamples == 1 && globalDur.P50Ms != nil {
		p50 = *globalDur.P50Ms
		if globalDur.P90Ms != nil {
			p90 = *globalDur.P90Ms
		}
	} else if a.durLaneCount == 1 && a.exactP50 != nil {
		p50 = *a.exactP50
		if a.exactP90 != nil {
			p90 = *a.exactP90
		}
	}
	text = formatDurationMs(&p50)
	sub = fmt.Sprintf("p90: %s (%d)", formatDurationMs(&p90), a.durSamples)
	return text, sub
}

func resolveRepoApp(repo, app string) string {
	if app != "" {
		return app
	}
	switch repo {
	case repoMattermostWeb:
		return "mattermost"
	case repoMattermostMobile:
		return "mobile"
	case repoMattermostDesktop:
		return "desktop"
	default:
		return ""
	}
}

func buildLaneBreakdown(summary kpi.SummaryResponse, state KPIFilterState) []KPILaneBreakdownRow {
	type laneKey struct{ repo, lane string }
	type laneAcc struct {
		repo, lane string
		hasMain    bool
		hasPR      bool
		runsCount  int
		mainRow    *kpi.MainLaneRow
		durRow     *kpi.DurationLaneRow
		prRow      *kpi.PRFalseRedLaneRow
	}
	byKey := map[laneKey]*laneAcc{}
	getAcc := func(repo, lane string) *laneAcc {
		k := laneKey{repo: repo, lane: lane}
		a, ok := byKey[k]
		if !ok {
			a = &laneAcc{repo: repo, lane: lane}
			byKey[k] = a
		}
		return a
	}

	for i := range summary.MainPassRate.ByLane {
		m := &summary.MainPassRate.ByLane[i]
		a := getAcc(m.Repository, m.Lane)
		a.hasMain = true
		a.mainRow = m
		a.runsCount += m.LanesTotal
	}
	for i := range summary.E2EDuration.ByLane {
		d := &summary.E2EDuration.ByLane[i]
		a := getAcc(d.Repository, d.Lane)
		a.hasPR = true
		a.durRow = d
		a.runsCount += d.OrchestratedRuns + d.UploadOnlyRuns
	}
	if summary.PRFalseRed != nil {
		for i := range summary.PRFalseRed.ByLane {
			p := &summary.PRFalseRed.ByLane[i]
			a := getAcc(p.Repository, p.Lane)
			a.hasPR = true
			a.prRow = p
		}
	}

	keys := make([]laneKey, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].lane != keys[j].lane {
			return keys[i].lane < keys[j].lane
		}
		return keys[i].repo < keys[j].repo
	})

	out := make([]KPILaneBreakdownRow, 0, len(keys))
	for _, k := range keys {
		a := byKey[k]
		stage := "main"
		if a.hasMain && a.hasPR {
			stage = "main + pr"
		} else if a.hasPR {
			stage = "pr"
		}
		runPassText, runPassClass, testPassText, zeroRetryText := kpiDash, "", kpiDash, kpiDash
		if a.mainRow != nil && a.mainRow.LanesTotal > 0 {
			pct := float64(a.mainRow.LanesPassed) * 100.0 / float64(a.mainRow.LanesTotal)
			runPassText = fmt.Sprintf("%.1f%% (%d/%d)", pct, a.mainRow.LanesPassed, a.mainRow.LanesTotal)
			_, runPassClass = healthBadgeHigherGood(&pct, 90, 75)
			if a.mainRow.TestsTotal > 0 {
				testPassText = fmt.Sprintf("%s (%d/%d)", formatPct(a.mainRow.TestPassRate), a.mainRow.FirstAttemptPassed, a.mainRow.TestsTotal)
			}
			if a.mainRow.StrictLanesTotal > 0 {
				zeroRetryText = fmt.Sprintf("%s (%d/%d)", formatPct(a.mainRow.StrictPassRate), a.mainRow.StrictLanesPassed, a.mainRow.StrictLanesTotal)
			}
		}
		prDurText := kpiDash
		if a.durRow != nil && a.durRow.SampleCount > 0 && a.durRow.P50Ms != nil {
			prDurText = fmt.Sprintf("%s (p90: %s · %d)", formatDurationMs(a.durRow.P50Ms), formatDurationMs(a.durRow.P90Ms), a.durRow.SampleCount)
		}
		prRedText := kpiDash
		if a.prRow != nil && (a.prRow.SettledRedLanes > 0 || a.prRow.UnsettledRedLanes > 0) {
			if a.prRow.SettledRedLanes > 0 {
				prRedText = fmt.Sprintf("%d/%d settled red", a.prRow.SettledFalseRedLanes, a.prRow.SettledRedLanes)
			} else {
				prRedText = fmt.Sprintf("%d <24h", a.prRow.UnsettledRedLanes)
			}
		}

		lState := state
		lState.Lane = a.lane
		out = append(out, KPILaneBreakdownRow{
			Lane:           a.lane,
			Href:           kpiHref("/kpi", lState, 1, "", false),
			Stage:          stage,
			RunsCount:      a.runsCount,
			RunPassText:    runPassText,
			RunPassClass:   runPassClass,
			TestPassText:   testPassText,
			ZeroRetryText:  zeroRetryText,
			PRDurationText: prDurText,
			PRFalseRedText: prRedText,
		})
	}
	return out
}

func buildFailureTableRows(rows []kpi.RecurringFailureRow, state KPIFilterState, now time.Time) []KPIRecurringFailureRow {
	out := make([]KPIRecurringFailureRow, 0, len(rows))
	for _, r := range rows {
		stateLabel, stateClass := mapFailureStateBadge(r.State, r.QuietDays)
		lastISO, lastRel := formatKPITimestamp(r.LastSeenAt, now)
		topFrame := ""
		if r.TopFrame != nil {
			topFrame = *r.TopFrame
		}
		verdictLabel := ""
		if r.LatestVerdictLabel != nil {
			verdictLabel = humanizeVerdictLabel(*r.LatestVerdictLabel)
		}
		reposSummary := ""
		if len(r.Repositories) == 1 {
			reposSummary = friendlyRepoName(r.Repositories[0])
		} else if len(r.Repositories) > 1 {
			reposSummary = fmt.Sprintf("%s (+%d)", friendlyRepoName(r.Repositories[0]), len(r.Repositories)-1)
		}
		title := shortSignatureLabel(r.ErrorClass, r.NormalizedMessage)
		normMsg := r.NormalizedMessage
		// Avoid showing "Error" title right above "Error: exact same sentence".
		if r.ErrorClass != nil && strings.HasPrefix(strings.TrimSpace(normMsg), strings.TrimSpace(*r.ErrorClass)+":") {
			title = normMsg
			normMsg = ""
		} else if strings.EqualFold(strings.TrimSpace(title), strings.TrimSpace(normMsg)) {
			normMsg = ""
		}

		deltaText, deltaClass := formatFailureDelta14d(r.DeltaRatePer1k, r.Occurrences14d, r.AffectedRuns14d, r.PriorOccurrences14d, r.PriorAffectedRuns14d)
		out = append(out, KPIRecurringFailureRow{
			SignatureID:        r.SignatureID,
			Href:               kpiHref("/kpi/failures/"+url.PathEscape(r.SignatureID), state, 1, "", false),
			Title:              title,
			NormalizedMessage:  normMsg,
			TopFrame:           topFrame,
			StateLabel:         stateLabel,
			StateClass:         stateClass,
			OccurrenceCount:    r.OccurrenceCount,
			GroupCount:         r.GroupCount,
			AffectedTestCount:  r.AffectedTestCount,
			MetaSummary:        pluralize(r.AffectedTestCount, "test", "tests") + " · " + pluralize(r.GroupCount, "CI run", "CI runs"),
			DeltaRate14d:       deltaText,
			DeltaClass:         deltaClass,
			LastSeenISO:        lastISO,
			LastSeenRel:        lastRel,
			ReposSummary:       reposSummary,
			LatestVerdictLabel: verdictLabel,
		})
	}
	return out
}

func buildPRFalseRedCard(pr *kpi.PRFalseRedSummary, prsHref string) KPIPRFalseRedCard {
	card := KPIPRFalseRedCard{
		HeadlineRate: kpiNotAvailable,
		DeltaClass:   kpiDeltaNeutral,
		SettledNote:  "No data — settled (>24h) red PR lanes; pushed fixes excluded.",
		PRsHref:      prsHref,
	}
	if pr == nil {
		return card
	}
	settledHours := pr.SettledAfterHours
	if settledHours <= 0 {
		settledHours = 24
	}
	qualifier := pr.RateQualifier
	if qualifier == "" {
		qualifier = "at least"
	}
	card.SettledFalseRedLanes = pr.SettledFalseRedLanes
	card.SettledRedLanes = pr.SettledRedLanes
	card.UnsettledRedLanes = pr.UnsettledRedLanes
	card.Delta, card.DeltaClass = formatPRFalseRedDelta(pr.DeltaPP, pr.SettledRedLanes, pr.PriorSettledRedLanes)

	switch {
	case pr.FalseRedRate != nil:
		card.HasData = true
		card.HeadlineRate = fmt.Sprintf("%s %.1f%%", qualifier, *pr.FalseRedRate)
		if pr.ReasonMix.Total > 0 && pr.ReasonMix.Other == pr.ReasonMix.Total {
			card.HealthLabel = "Unclassified"
			card.HealthClass = kpiBadgeNeutral
		} else {
			card.HealthLabel, card.HealthClass = healthBadgeLowerGood(pr.FalseRedRate, 5, 15)
		}
		card.SettledNote = fmt.Sprintf("Settled >%dh runs; new-commit fixes excluded.", settledHours)
	case pr.UnsettledRedLanes > 0:
		card.SettledNote = fmt.Sprintf("%d recent red runs (<%dh unsettled); new-commit fixes excluded.", pr.UnsettledRedLanes, settledHours)
	default:
		card.SettledNote = fmt.Sprintf("No settled (>%dh) red PR runs in window.", settledHours)
	}
	return card
}

func buildWeeklyChartBars(series []kpi.WeeklyPoint) (bars []KPIChartBar, chartWidth int) {
	hasActivity := false
	for _, pt := range series {
		if pt.MainTestPassRate != nil || pt.MainLanePassRate != nil || pt.MainTestsTotal > 0 || pt.MainLanesTotal > 0 {
			hasActivity = true
			break
		}
	}
	if !hasActivity {
		return nil, 0
	}
	const (
		barW   = 28
		gap    = 12
		padX   = 36
		padR   = 12
		maxH   = 76
		baseY  = 86
		minBar = 4
	)
	bars = make([]KPIChartBar, 0, len(series))
	for i, pt := range series {
		h := minBar
		colorClass := "kpi-bar-empty"
		valText := kpiNotAvailable
		pctPtrVal := pt.MainLanePassRate
		if pctPtrVal == nil {
			pctPtrVal = pt.MainTestPassRate
		}
		if pctPtrVal != nil {
			pct := *pctPtrVal
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			h = int(pct/100.0*float64(maxH) + 0.5)
			if h < minBar {
				h = minBar
			}
			valText = fmt.Sprintf("%.1f%%", *pctPtrVal)
			switch {
			case pct >= 90:
				colorClass = "kpi-bar-good"
			case pct >= 75:
				colorClass = "kpi-bar-warn"
			default:
				colorClass = "kpi-bar-bad"
			}
		}
		x := padX + i*(barW+gap)
		y := baseY - h
		label := pt.WeekStart
		if len(label) >= 10 {
			label = label[5:10]
		}
		bars = append(bars, KPIChartBar{
			Label:      label,
			ValueText:  valText,
			Tooltip:    fmt.Sprintf("Week of %s: Main pass %s (%s tests)", pt.WeekStart, valText, formatPct(pt.MainTestPassRate)),
			X:          x,
			Y:          y,
			Width:      barW,
			Height:     h,
			ColorClass: colorClass,
		})
	}
	chartWidth = padX + padR + len(series)*barW + (len(series)-1)*gap
	if chartWidth < 240 {
		chartWidth = 240
	}
	return bars, chartWidth
}
