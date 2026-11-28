package htmlpage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

// KPIFailureVerdictView holds non-sensitive latest triage metadata.
// verdicts.error_context is deliberately omitted and never rendered.
type KPIFailureVerdictView struct {
	Label      string
	DecidedBy  string
	Rule       string
	Confidence string
	Model      string
	Summary    string
	TriageURL  string
	CreatedISO string
	CreatedRel string
}

// KPIFailureFirstSeenView describes where a failure signature was first observed (no author names).
type KPIFailureFirstSeenView struct {
	HasData      bool
	TimestampISO string
	TimestampRel string
	Repository   string
	Lane         string
	Branch       string
	ShortSHA     string
	CommitSHA    string
	PRNumber     int
	HasPR        bool
}

// KPIFailureDailyRow holds one day in the failure daily table/series.
type KPIFailureDailyRow struct {
	Date             string
	OccurrenceCount  int
	RawAttemptCount  int
	GroupCount       int
	AffectedTestRuns int
	RatePer1k        string
}

// KPIFailureTestRow summarizes one affected test.
type KPIFailureTestRow struct {
	TestID      string
	Title       string
	File        string
	ZephyrKey   string
	Repository  string
	Framework   string
	FailCount   int
	RunCount    int
	GroupCount  int
	RunsText    string
	LastSeenISO string
	LastSeenRel string
}

// KPIFailureRepoRow summarizes one affected repository.
type KPIFailureRepoRow struct {
	Repository      string
	App             string
	OccurrenceCount int
	GroupCount      int
	OccurrencesText string
	AffectedRuns    int
	RatePer1k       string
	LanesSummary    string
}

// KPIFailureDetailPage is the view model for GET /kpi/failures/{id}.
type KPIFailureDetailPage struct {
	Title                  string
	Site                   SiteInfo
	Filters                KPIFilterState
	OverviewHref           string
	PRsHref                string
	RequestedSignatureID   string
	CanonicalSignatureID   string
	IsMerged               bool
	MergedCount            int
	Fingerprint            string
	ShortFingerprint       string
	AlgoVersion            int
	ErrorClass             string
	NormalizedMessage      string
	TopFrame               string
	SampleMessage          string
	StateLabel             string
	StateClass             string
	OccurrenceCount        int
	RawAttemptCount        int
	GroupCount             int
	GroupCountText         string
	AffectedTestCount      int
	AffectedTestRuns       int
	RatePer1k              string
	IsLowSample            bool
	WindowPrimaryValue     string
	WindowMeta             string
	Rate14d                string
	Occ14d                 int
	Runs14d                int
	Current14dText         string
	PriorRate14d           string
	PriorOcc14d            int
	PriorRuns14d           int
	Prior14dText           string
	Delta14d               string
	Delta14dClass          string
	Occ7d                  int
	Runs7d                 int
	Trailing7dText         string
	LastSeenISO            string
	LastSeenRel            string
	FirstSeen              KPIFailureFirstSeenView
	LatestVerdict          *KPIFailureVerdictView
	HasDaily               bool
	DailyBars              []KPIChartBar
	DailyChartW            int
	DailyRows              []KPIFailureDailyRow
	ActiveDailyRows        []KPIFailureDailyRow
	InactiveDaysCount      int
	ActiveDaysText         string
	AffectedTests          []KPIFailureTestRow
	AffectedTestsCountText string
	AffectedRepos          []KPIFailureRepoRow
	AffectedReposCountText string
	PRNumbers              []int
}

// BuildKPIFailureDetailPage maps kpi.FailureDetailResponse into KPIFailureDetailPage.
func BuildKPIFailureDetailPage(site SiteInfo, d kpi.FailureDetailResponse, now time.Time) KPIFailureDetailPage {
	filterState := kpiFilterStateFromParams(d.Filters, "/kpi", kpi.SortOccurrencesDesc)
	overviewHref := kpiHref("/kpi", filterState, 1, "", false)
	prsHref := kpiHref("/kpi/prs", filterState, 1, "", false)

	stateLabel, stateClass := mapFailureStateBadge(d.State, d.QuietDays)
	errClass := ""
	if d.ErrorClass != nil {
		errClass = *d.ErrorClass
	}
	topFrame := ""
	if d.TopFrame != nil {
		topFrame = *d.TopFrame
	}
	sampleMsg := ""
	if d.SampleMessage != nil {
		trimmed := strings.TrimSpace(*d.SampleMessage)
		if trimmed != "" && trimmed != strings.TrimSpace(d.NormalizedMessage) {
			sampleMsg = *d.SampleMessage
		}
	}

	var lastISO, lastRel string
	if d.LastSeenAt != nil {
		lastISO, lastRel = formatKPITimestamp(*d.LastSeenAt, now)
	} else {
		lastRel = kpiNotAvailable
	}

	fsISO, fsRel := formatKPITimestamp(d.FirstSeen.FirstSeenAt, now)
	firstSeen := KPIFailureFirstSeenView{
		HasData:      !d.FirstSeen.FirstSeenAt.IsZero(),
		TimestampISO: fsISO,
		TimestampRel: fsRel,
		Repository:   derefString(d.FirstSeen.Repository),
		Lane:         derefString(d.FirstSeen.Lane),
		Branch:       derefString(d.FirstSeen.Branch),
		CommitSHA:    derefString(d.FirstSeen.CommitSHA),
		ShortSHA:     shortSHA(derefString(d.FirstSeen.CommitSHA)),
	}
	if d.FirstSeen.GHPRNumber != nil && *d.FirstSeen.GHPRNumber > 0 {
		firstSeen.PRNumber = *d.FirstSeen.GHPRNumber
		firstSeen.HasPR = true
	}

	var verdictView *KPIFailureVerdictView
	if d.LatestVerdict != nil {
		v := d.LatestVerdict
		vISO, vRel := formatKPITimestamp(v.CreatedAt, now)
		conf := ""
		if v.Confidence != nil {
			conf = fmt.Sprintf("%.0f%%", *v.Confidence*100)
			if *v.Confidence > 1.0 {
				conf = fmt.Sprintf("%.0f%%", *v.Confidence)
			}
		}
		verdictView = &KPIFailureVerdictView{
			Label:      v.Label,
			DecidedBy:  derefString(v.DecidedBy),
			Rule:       derefString(v.Rule),
			Confidence: conf,
			Model:      derefString(v.Model),
			Summary:    derefString(v.Summary),
			TriageURL:  safeExternalURL(v.TriageURL),
			CreatedISO: vISO,
			CreatedRel: vRel,
		}
	}

	dailyBars, dailyW, dailyRows, activeDailyRows := buildFailureDailyChart(d.DailySeries)

	tests := make([]KPIFailureTestRow, 0, len(d.AffectedTests))
	for _, t := range d.AffectedTests {
		tISO, tRel := formatKPITimestamp(t.LastSeenAt, now)
		runsText := pluralize(t.RunCount, "CI run", "CI runs")
		if t.RunCount != t.GroupCount {
			runsText = fmt.Sprintf("%s (%s)", pluralize(t.RunCount, "run", "runs"), pluralize(t.GroupCount, "group", "groups"))
		}
		tests = append(tests, KPIFailureTestRow{
			TestID:      t.TestID,
			Title:       t.FullTitle,
			File:        t.File,
			ZephyrKey:   derefString(t.ZephyrKey),
			Repository:  t.Repository,
			Framework:   t.Framework,
			FailCount:   t.FailCount,
			RunCount:    t.RunCount,
			GroupCount:  t.GroupCount,
			RunsText:    runsText,
			LastSeenISO: tISO,
			LastSeenRel: tRel,
		})
	}

	repos := make([]KPIFailureRepoRow, 0, len(d.AffectedRepos))
	for _, r := range d.AffectedRepos {
		repoRate, _ := formatFailureRate(r.RatePer1k, r.OccurrenceCount, r.AffectedRuns)
		repos = append(repos, KPIFailureRepoRow{
			Repository:      r.Repository,
			App:             r.App,
			OccurrenceCount: r.OccurrenceCount,
			GroupCount:      r.GroupCount,
			OccurrencesText: fmt.Sprintf("%s (%s)", pluralize(r.OccurrenceCount, "failure", "failures"), pluralize(r.GroupCount, "run", "runs")),
			AffectedRuns:    r.AffectedRuns,
			RatePer1k:       repoRate,
			LanesSummary:    strings.Join(r.Lanes, ", "),
		})
	}

	shortLabel := shortSignatureLabel(d.ErrorClass, d.NormalizedMessage)
	rateText, isLowSample := formatFailureRate(d.RatePer1k, d.OccurrenceCount, d.AffectedTestRuns)
	windowPrimaryValue := rateText
	windowMeta := fmt.Sprintf(
		"%s (%s) across %s · %s",
		pluralize(d.OccurrenceCount, "occurrence", "occurrences"),
		pluralize(d.RawAttemptCount, "raw attempt", "raw attempts"),
		pluralize(d.AffectedTestRuns, "affected test run", "affected test runs"),
		pluralize(d.AffectedTestCount, "test", "tests"),
	)
	if isLowSample {
		windowPrimaryValue = pluralize(d.OccurrenceCount, "failure", "failures")
		windowMeta = fmt.Sprintf(
			"%s across %s · %s",
			rateText,
			pluralize(d.AffectedTestCount, "test", "tests"),
			pluralize(d.RawAttemptCount, "total attempt", "total attempts"),
		)
	}

	rate14d, _ := formatFailureRate(d.RatePer1k14d, d.Occurrences14d, d.AffectedRuns14d)
	delta14d, delta14dClass := formatFailureDelta14d(d.DeltaRatePer1k, d.Occurrences14d, d.AffectedRuns14d, d.PriorOccurrences14d, d.PriorAffectedRuns14d)
	cur14dText := fmt.Sprintf("%s in %s", pluralize(d.Occurrences14d, "failure", "failures"), pluralize(d.AffectedRuns14d, "run", "runs"))
	prior14dText := "no runs"
	if d.PriorAffectedRuns14d > 0 {
		priorRate14d, _ := formatFailureRate(d.PriorRatePer1k14d, d.PriorOccurrences14d, d.PriorAffectedRuns14d)
		prior14dText = fmt.Sprintf("%s (%s in %s)", priorRate14d, pluralize(d.PriorOccurrences14d, "failure", "failures"), pluralize(d.PriorAffectedRuns14d, "run", "runs"))
	}

	return KPIFailureDetailPage{
		Title:                  shortLabel + " · KPIs · Test System IO",
		Site:                   site,
		Filters:                filterState,
		OverviewHref:           overviewHref,
		PRsHref:                prsHref,
		RequestedSignatureID:   d.RequestedSignatureID,
		CanonicalSignatureID:   d.CanonicalSignatureID,
		IsMerged:               d.MergedIntoID != nil && *d.MergedIntoID != "",
		MergedCount:            len(d.MergedSignatureIDs),
		Fingerprint:            d.Fingerprint,
		ShortFingerprint:       shortFingerprint(d.Fingerprint),
		AlgoVersion:            d.AlgoVersion,
		ErrorClass:             errClass,
		NormalizedMessage:      d.NormalizedMessage,
		TopFrame:               topFrame,
		SampleMessage:          sampleMsg,
		StateLabel:             stateLabel,
		StateClass:             stateClass,
		OccurrenceCount:        d.OccurrenceCount,
		RawAttemptCount:        d.RawAttemptCount,
		GroupCount:             d.GroupCount,
		GroupCountText:         pluralize(d.GroupCount, "CI run", "CI runs"),
		AffectedTestCount:      d.AffectedTestCount,
		AffectedTestRuns:       d.AffectedTestRuns,
		RatePer1k:              formatRatePer1k(d.RatePer1k),
		IsLowSample:            isLowSample,
		WindowPrimaryValue:     windowPrimaryValue,
		WindowMeta:             windowMeta,
		Rate14d:                rate14d,
		Occ14d:                 d.Occurrences14d,
		Runs14d:                d.AffectedRuns14d,
		Current14dText:         cur14dText,
		PriorRate14d:           formatRatePer1k(d.PriorRatePer1k14d),
		PriorOcc14d:            d.PriorOccurrences14d,
		PriorRuns14d:           d.PriorAffectedRuns14d,
		Prior14dText:           prior14dText,
		Delta14d:               delta14d,
		Delta14dClass:          delta14dClass,
		Occ7d:                  d.Occurrences7d,
		Runs7d:                 d.AffectedRuns7d,
		Trailing7dText:         fmt.Sprintf("%s in %s", pluralize(d.Occurrences7d, "failure", "failures"), pluralize(d.AffectedRuns7d, "run", "runs")),
		LastSeenISO:            lastISO,
		LastSeenRel:            lastRel,
		FirstSeen:              firstSeen,
		LatestVerdict:          verdictView,
		HasDaily:               len(dailyRows) > 0,
		DailyBars:              dailyBars,
		DailyChartW:            dailyW,
		DailyRows:              dailyRows,
		ActiveDailyRows:        activeDailyRows,
		InactiveDaysCount:      len(dailyRows) - len(activeDailyRows),
		ActiveDaysText:         pluralize(len(activeDailyRows), "active day", "active days"),
		AffectedTests:          tests,
		AffectedTestsCountText: pluralize(len(tests), "test", "tests"),
		AffectedRepos:          repos,
		AffectedReposCountText: pluralize(len(repos), "repository", "repositories"),
		PRNumbers:              append([]int(nil), d.PRNumbers...),
	}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func buildFailureDailyChart(series []kpi.FailureDailyPoint) (bars []KPIChartBar, chartW int, rows, activeRows []KPIFailureDailyRow) {
	if len(series) == 0 {
		return nil, 0, nil, nil
	}
	maxOcc := 1
	for _, pt := range series {
		if pt.OccurrenceCount > maxOcc {
			maxOcc = pt.OccurrenceCount
		}
	}

	const (
		barW   = 18
		gap    = 6
		padX   = 10
		maxH   = 76
		baseY  = 84
		minBar = 3
	)
	bars = make([]KPIChartBar, 0, len(series))
	rows = make([]KPIFailureDailyRow, 0, len(series))

	for i, pt := range series {
		rateText, _ := formatFailureRate(pt.RatePer1k, pt.OccurrenceCount, pt.AffectedTestRuns)
		row := KPIFailureDailyRow{
			Date:             pt.Date,
			OccurrenceCount:  pt.OccurrenceCount,
			RawAttemptCount:  pt.RawAttemptCount,
			GroupCount:       pt.GroupCount,
			AffectedTestRuns: pt.AffectedTestRuns,
			RatePer1k:        rateText,
		}
		rows = append(rows, row)
		if pt.OccurrenceCount > 0 {
			activeRows = append(activeRows, row)
		}

		h := minBar
		colorClass := "kpi-bar-empty"
		if pt.OccurrenceCount > 0 {
			h = int(float64(pt.OccurrenceCount)/float64(maxOcc)*float64(maxH) + 0.5)
			if h < minBar {
				h = minBar
			}
			colorClass = "kpi-bar-bad"
		}
		x := padX + i*(barW+gap)
		y := baseY - h
		label := pt.Date
		if len(label) >= 10 {
			label = label[5:10]
		}
		bars = append(bars, KPIChartBar{
			Label:      label,
			ValueText:  strconv.Itoa(pt.OccurrenceCount),
			Tooltip:    fmt.Sprintf("%s: %s (%s, %s)", pt.Date, pluralize(pt.OccurrenceCount, "failure", "failures"), rateText, pluralize(pt.AffectedTestRuns, "test run", "test runs")),
			X:          x,
			Y:          y,
			Width:      barW,
			Height:     h,
			ColorClass: colorClass,
		})
	}
	chartW = padX*2 + len(series)*barW + (len(series)-1)*gap
	if chartW < 200 {
		chartW = 200
	}
	return bars, chartW, rows, activeRows
}
