package htmlpage

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

// KPIMainPassCard holds KPI 1 card data.
type KPIMainPassCard struct {
	HasData              bool
	HealthLabel          string
	HealthClass          string
	PrimaryRate          string
	PrimaryDelta         string
	DeltaClass           string
	FirstAttemptPassed   int
	TotalTests           int
	LaneRate             string
	LanePassed           int
	LaneTotal            int
	StrictRate           string
	StrictPassed         int
	StrictTotal          int
	UploadOnlyLanesCount int
	UploadOnlyNote       string
	CoverageSummary      string
	CoverageNotes        []string
}

// KPIPRFalseRedCard holds KPI 2 card data.
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

// KPIAppFlakyItem holds per-app flaky test counts.
type KPIAppFlakyItem struct {
	App   string
	Count int
	Delta string
}

// KPIFlakyCard holds KPI 3 card data.
type KPIFlakyCard struct {
	HasData     bool
	HealthLabel string
	HealthClass string
	Count       int
	Delta       string
	DeltaClass  string
	RuleNote    string
	ByApp       []KPIAppFlakyItem
}

// KPIRecurringCard holds KPI 4 summary card data.
type KPIRecurringCard struct {
	HasData            bool
	TotalSignatures    int
	TotalOccurrences   int
	OccurrencesText    string
	TriagedCount       int
	ActiveCount        int
	ResurfacedCount    int
	QuietCount         int
	NotEnoughRunsCount int
}

// KPIDurationCard holds KPI 5 card data.
type KPIDurationCard struct {
	HasData         bool
	P50             string
	P50Delta        string
	P50DeltaClass   string
	P90             string
	SampleCount     int
	SampleCountText string
	CoverageSummary string
	CoverageNotes   []string
}

// KPILabelMixItem holds one verdict label count and share.
type KPILabelMixItem struct {
	Label    string
	Count    int
	SharePct string
	WidthPct int
}

// KPITriageSection holds SEC-11520 triage coverage, reuse, label mix, and run completion.
type KPITriageSection struct {
	HasData                bool
	RunCoverage            string
	FailedRunsCount        int
	TriagedRunsCount       int
	TestCoverage           string
	FailedTestOccurrences  int
	TriagedTestOccurrences int
	VerdictReuseRate       string
	VerdictsTotal          int
	VerdictsReused         int
	CompletionRate         string
	TotalRuns              int
	CompletedRuns          int
	IncompleteRuns         int
	TimedOutRuns           int
	InProgressRuns         int
	LabelMix               []KPILabelMixItem
}

// KPIWeeklyRow holds one row in the weekly trend table.
type KPIWeeklyRow struct {
	WeekStart         string
	MainTestPassRate  string
	MainLanePassRate  string
	PRFalseRedRate    string
	FlakyTestCount    int
	RecurringFailures string
	E2EP50            string
	TriageCoverage    string
}

// KPIRecurringFailureRow holds one row in the paginated recurring failures list.
type KPIRecurringFailureRow struct {
	SignatureID        string
	Href               string
	Title              string
	NormalizedMessage  string
	TopFrame           string
	StateLabel         string
	StateClass         string
	OccurrenceCount    int
	RawAttemptCount    int
	GroupCount         int
	AffectedTestCount  int
	AffectedTestRuns   int
	RatePer1k          string
	RateText           string
	IsLowSample        bool
	MetaSummary        string
	DeltaRate14d       string
	DeltaClass         string
	LastSeenISO        string
	LastSeenRel        string
	ReposSummary       string
	LatestVerdictLabel string
}

// KPIOverviewPage is the view model for GET /kpi.
// Author names and verdicts.error_context are intentionally omitted.
type KPIOverviewPage struct {
	Title             string
	Site              SiteInfo
	Filters           KPIFilterBar
	OverviewHref      string
	PRsHref           string
	CoverageSummary   string
	CoverageNotes     []string
	MainPass          KPIMainPassCard
	PRFalseRed        KPIPRFalseRedCard
	Flaky             KPIFlakyCard
	Recurring         KPIRecurringCard
	E2EDuration       KPIDurationCard
	Triage            KPITriageSection
	HasWeekly         bool
	WeeklyBars        []KPIChartBar
	WeeklyChartW      int
	WeeklyRows        []KPIWeeklyRow
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

// BuildKPIOverviewPage maps KPI summary and recurring failures responses into KPIOverviewPage.
func BuildKPIOverviewPage(
	site SiteInfo,
	opts kpi.FilterOptions,
	summary kpi.SummaryResponse,
	failures kpi.FailuresResponse,
	now time.Time,
) KPIOverviewPage {
	filterParams := failures.Filters
	if filterParams.Window == "" {
		filterParams = summary.Filters
	}
	filterBar := buildKPIFilterBar(filterParams, opts, "/kpi", kpi.SortOccurrencesDesc, kpiOverviewSortOptions)
	overviewHref := kpiHref("/kpi", filterBar.State, 1, "", false)
	prsHref := kpiHref("/kpi/prs", filterBar.State, 1, "", false)

	// KPI 1: Main first-attempt pass rate.
	tl := summary.MainPassRate.TestLevel
	ll := summary.MainPassRate.LaneLevel
	mainHealthLabel, mainHealthClass := healthBadgeHigherGood(tl.PassRate, 95, 85)
	uploadOnlyNote := ""
	if ll.UploadOnlyLanesCount > 0 {
		uploadOnlyNote = pluralize(ll.UploadOnlyLanesCount, "upload-only lane", "upload-only lanes") + " excluded from zero-retry rate"
	}
	mainCard := KPIMainPassCard{
		HasData:              tl.TotalTests > 0 || ll.TotalLanes > 0,
		HealthLabel:          mainHealthLabel,
		HealthClass:          mainHealthClass,
		PrimaryRate:          formatPct(tl.PassRate),
		PrimaryDelta:         formatDeltaPP(tl.DeltaPP),
		DeltaClass:           deltaClassHigherGood(tl.DeltaPP),
		FirstAttemptPassed:   tl.FirstAttemptPassed,
		TotalTests:           tl.TotalTests,
		LaneRate:             formatPct(ll.PassRate),
		LanePassed:           ll.PassedLanes,
		LaneTotal:            ll.TotalLanes,
		StrictRate:           formatPct(ll.StrictPassRate),
		StrictPassed:         ll.StrictPassedLanes,
		StrictTotal:          ll.StrictTotalLanes,
		UploadOnlyLanesCount: ll.UploadOnlyLanesCount,
		UploadOnlyNote:       uploadOnlyNote,
		CoverageSummary:      formatLaneCoverageSummary(summary.MainPassRate.LaneCoverage),
		CoverageNotes:        humanizeCoverageNotes(summary.MainPassRate.LaneCoverage.Notes),
	}

	// KPI 2: PR false-red rate (lower bound).
	prCard := buildPRFalseRedCard(summary.PRFalseRed, prsHref)

	// KPI 3: Flaky test count.
	appItems := make([]KPIAppFlakyItem, 0, len(summary.FlakyTests.ByApp))
	for _, a := range summary.FlakyTests.ByApp {
		appItems = append(appItems, KPIAppFlakyItem{
			App:   a.App,
			Count: a.Count,
			Delta: formatSignedIntDelta(a.Delta),
		})
	}
	flakyHasData := summary.LaneCoverage.TotalRuns > 0 || summary.FlakyTests.Count > 0 || len(appItems) > 0
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
		RuleNote:    fmt.Sprintf("Trailing %dd · >=%d flaky commits", summary.FlakyTests.WindowDays, summary.FlakyTests.MinFlakyCommits),
		ByApp:       appItems,
	}
	if summary.FlakyTests.WindowDays == 0 {
		flakyCard.RuleNote = "Trailing 14d · >=2 flaky commits"
	}

	// KPI 4: Recurring failures summary.
	recSum := failures.Summary
	if summary.RecurringFailures != nil {
		recSum = *summary.RecurringFailures
	}
	recCard := KPIRecurringCard{
		HasData:            recSum.TotalSignatures > 0 || recSum.TotalOccurrences > 0,
		TotalSignatures:    recSum.TotalSignatures,
		TotalOccurrences:   recSum.TotalOccurrences,
		OccurrencesText:    pluralize(recSum.TotalOccurrences, "occurrence", "occurrences") + " in window",
		TriagedCount:       recSum.TriagedCount,
		ActiveCount:        recSum.ActiveCount,
		ResurfacedCount:    recSum.ResurfacedCount,
		QuietCount:         recSum.QuietCount,
		NotEnoughRunsCount: recSum.NotEnoughRunsCount,
	}

	// KPI 5: E2E PR run time.
	dur := summary.E2EDuration
	durCard := KPIDurationCard{
		HasData:         dur.SampleCount > 0 && dur.P50Ms != nil,
		P50:             formatDurationMs(dur.P50Ms),
		P50Delta:        formatDeltaDurationMs(dur.DeltaP50Ms),
		P50DeltaClass:   deltaClassLowerGood(dur.DeltaP50Ms),
		P90:             formatDurationMs(dur.P90Ms),
		SampleCount:     dur.SampleCount,
		SampleCountText: pluralize(dur.SampleCount, "orchestrated PR run", "orchestrated PR runs"),
		CoverageSummary: formatLaneCoverageSummary(dur.LaneCoverage),
		CoverageNotes:   humanizeCoverageNotes(dur.LaneCoverage.Notes),
	}

	// Triage & Run completion (SEC-11520).
	triageSec := buildTriageSection(summary.Triage)

	// Weekly trends.
	weeklyBars, chartW, weeklyRows := buildWeeklyTrendData(summary.WeeklySeries)

	// Paginated recurring failures table.
	failureRows := make([]KPIRecurringFailureRow, 0, len(failures.Failures))
	for _, r := range failures.Failures {
		stateLabel, stateClass := mapFailureStateBadge(r.State, r.QuietDays)
		lastISO, lastRel := formatKPITimestamp(r.LastSeenAt, now)
		topFrame := ""
		if r.TopFrame != nil {
			topFrame = *r.TopFrame
		}
		verdictLabel := ""
		if r.LatestVerdictLabel != nil {
			verdictLabel = *r.LatestVerdictLabel
		}
		reposSummary := ""
		if len(r.Repositories) == 1 {
			reposSummary = r.Repositories[0]
		} else if len(r.Repositories) > 1 {
			reposSummary = fmt.Sprintf("%s (+%d)", r.Repositories[0], len(r.Repositories)-1)
		}
		rateText, isLowSample := formatFailureRate(r.RatePer1k, r.OccurrenceCount, r.AffectedTestRuns)
		deltaText, deltaClass := formatFailureDelta14d(r.DeltaRatePer1k, r.Occurrences14d, r.AffectedRuns14d, r.PriorOccurrences14d, r.PriorAffectedRuns14d)
		failureRows = append(failureRows, KPIRecurringFailureRow{
			SignatureID:        r.SignatureID,
			Href:               kpiHref("/kpi/failures/"+url.PathEscape(r.SignatureID), filterBar.State, 1, "", false),
			Title:              shortSignatureLabel(r.ErrorClass, r.NormalizedMessage),
			NormalizedMessage:  r.NormalizedMessage,
			TopFrame:           topFrame,
			StateLabel:         stateLabel,
			StateClass:         stateClass,
			OccurrenceCount:    r.OccurrenceCount,
			RawAttemptCount:    r.RawAttemptCount,
			GroupCount:         r.GroupCount,
			AffectedTestCount:  r.AffectedTestCount,
			AffectedTestRuns:   r.AffectedTestRuns,
			RatePer1k:          formatRatePer1k(r.RatePer1k),
			RateText:           rateText,
			IsLowSample:        isLowSample,
			MetaSummary:        pluralize(r.AffectedTestCount, "test", "tests") + " · " + pluralize(r.GroupCount, "run", "runs"),
			DeltaRate14d:       deltaText,
			DeltaClass:         deltaClass,
			LastSeenISO:        lastISO,
			LastSeenRel:        lastRel,
			ReposSummary:       reposSummary,
			LatestVerdictLabel: verdictLabel,
		})
	}

	pag := buildKPIPagination(filterBar.State, failures.Pagination.Page, failures.Pagination.Limit, failures.Pagination.Total, failures.Pagination.TotalPages)

	return KPIOverviewPage{
		Title:             "KPIs · Test System IO",
		Site:              site,
		Filters:           filterBar,
		OverviewHref:      overviewHref,
		PRsHref:           prsHref,
		CoverageSummary:   formatLaneCoverageSummary(summary.LaneCoverage),
		CoverageNotes:     humanizeCoverageNotes(summary.LaneCoverage.Notes),
		MainPass:          mainCard,
		PRFalseRed:        prCard,
		Flaky:             flakyCard,
		Recurring:         recCard,
		E2EDuration:       durCard,
		Triage:            triageSec,
		HasWeekly:         len(weeklyRows) > 0,
		WeeklyBars:        weeklyBars,
		WeeklyChartW:      chartW,
		WeeklyRows:        weeklyRows,
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

func buildPRFalseRedCard(pr *kpi.PRFalseRedSummary, prsHref string) KPIPRFalseRedCard {
	card := KPIPRFalseRedCard{
		HeadlineRate: kpiNotAvailable,
		Delta:        kpiNotAvailable,
		DeltaClass:   kpiDeltaNeutral,
		SettledNote:  "No data — settled (>24h) red PR lanes; new-commit fixes excluded.",
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
	card.Delta = formatDeltaPP(pr.DeltaPP)
	card.DeltaClass = deltaClassLowerGood(pr.DeltaPP)

	switch {
	case pr.FalseRedRate != nil:
		card.HasData = true
		card.HealthLabel, card.HealthClass = healthBadgeLowerGood(pr.FalseRedRate, 5, 15)
		card.HeadlineRate = fmt.Sprintf("%s %.1f%%", qualifier, *pr.FalseRedRate)
		card.SettledNote = fmt.Sprintf("Lower bound (>%dh settled); new-commit fixes excluded.", settledHours)
	case pr.UnsettledRedLanes > 0:
		card.SettledNote = fmt.Sprintf("No settled (>%dh) red lanes yet (%d unsettled); new-commit fixes excluded.", settledHours, pr.UnsettledRedLanes)
	default:
		card.SettledNote = fmt.Sprintf("No settled (>%dh) red PR lanes in window; new-commit fixes excluded.", settledHours)
	}
	return card
}

func buildTriageSection(ts *kpi.TriageSummary) KPITriageSection {
	if ts == nil {
		return KPITriageSection{}
	}
	hasData := ts.FailedRunsCount > 0 || ts.FailedTestOccurrences > 0 || ts.VerdictsTotal > 0 || ts.RunCompletion.TotalRuns > 0
	mix := make([]KPILabelMixItem, 0, len(ts.LabelMix))
	for _, m := range ts.LabelMix {
		w := int(m.Share + 0.5)
		if m.Count > 0 && w < 4 {
			w = 4
		}
		mix = append(mix, KPILabelMixItem{
			Label:    m.Label,
			Count:    m.Count,
			SharePct: fmt.Sprintf("%.1f%%", m.Share),
			WidthPct: w,
		})
	}
	return KPITriageSection{
		HasData:                hasData,
		RunCoverage:            formatPct(ts.RunCoverageRate),
		FailedRunsCount:        ts.FailedRunsCount,
		TriagedRunsCount:       ts.TriagedRunsCount,
		TestCoverage:           formatPct(ts.TestCoverageRate),
		FailedTestOccurrences:  ts.FailedTestOccurrences,
		TriagedTestOccurrences: ts.TriagedTestOccurrences,
		VerdictReuseRate:       formatPct(ts.VerdictReuseRate),
		VerdictsTotal:          ts.VerdictsTotal,
		VerdictsReused:         ts.VerdictsReused,
		CompletionRate:         formatPct(ts.RunCompletion.CompletionRate),
		TotalRuns:              ts.RunCompletion.TotalRuns,
		CompletedRuns:          ts.RunCompletion.CompletedRuns,
		IncompleteRuns:         ts.RunCompletion.IncompleteRuns,
		TimedOutRuns:           ts.RunCompletion.TimedOutRuns,
		InProgressRuns:         ts.RunCompletion.InProgressRuns,
		LabelMix:               mix,
	}
}

func buildWeeklyTrendData(series []kpi.WeeklyPoint) (bars []KPIChartBar, chartWidth int, rows []KPIWeeklyRow) {
	hasActivity := false
	for _, pt := range series {
		if pt.MainTestPassRate != nil || pt.MainLanePassRate != nil || pt.PRFalseRedRate != nil ||
			pt.MainTestsTotal > 0 || pt.MainLanesTotal > 0 || pt.FlakyTestCount > 0 ||
			pt.E2ESampleCount > 0 || pt.E2EP50Ms != nil || pt.TriageCoverageRate != nil ||
			(pt.RecurringFailuresCount != nil && *pt.RecurringFailuresCount > 0) {
			hasActivity = true
			break
		}
	}
	if !hasActivity {
		return nil, 0, nil
	}
	const (
		barW   = 28
		gap    = 12
		padX   = 12
		maxH   = 80
		baseY  = 88
		minBar = 4
	)
	bars = make([]KPIChartBar, 0, len(series))
	rows = make([]KPIWeeklyRow, 0, len(series))

	for i, pt := range series {
		recText := kpiNotAvailable
		if pt.RecurringFailuresCount != nil {
			recText = strconv.Itoa(*pt.RecurringFailuresCount)
		}
		rows = append(rows, KPIWeeklyRow{
			WeekStart:         pt.WeekStart,
			MainTestPassRate:  formatPct(pt.MainTestPassRate),
			MainLanePassRate:  formatPct(pt.MainLanePassRate),
			PRFalseRedRate:    formatPct(pt.PRFalseRedRate),
			FlakyTestCount:    pt.FlakyTestCount,
			RecurringFailures: recText,
			E2EP50:            formatDurationMs(pt.E2EP50Ms),
			TriageCoverage:    formatPct(pt.TriageCoverageRate),
		})

		h := minBar
		colorClass := "kpi-bar-empty"
		valText := kpiNotAvailable
		if pt.MainTestPassRate != nil {
			pct := *pt.MainTestPassRate
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
			valText = fmt.Sprintf("%.1f%%", *pt.MainTestPassRate)
			switch {
			case pct >= 95:
				colorClass = "kpi-bar-good"
			case pct >= 80:
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
			Tooltip:    fmt.Sprintf("%s: Main pass %s (%d tests)", pt.WeekStart, valText, pt.MainTestsTotal),
			X:          x,
			Y:          y,
			Width:      barW,
			Height:     h,
			ColorClass: colorClass,
		})
	}
	chartWidth = padX*2 + len(series)*barW + (len(series)-1)*gap
	if chartWidth < 200 {
		chartWidth = 200
	}
	return bars, chartWidth, rows
}
