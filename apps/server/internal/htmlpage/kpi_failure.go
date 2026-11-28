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
	Confidence string
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
	RepoHref     string
	Lane         string
	LaneHref     string
	Branch       string
	ShortSHA     string
	CommitURL    string
	RunURL       string
	PRNumber     int
	PRURL        string
	HasPR        bool
}

// KPIFailureTestRow summarizes one affected test.
type KPIFailureTestRow struct {
	TestID      string
	Title       string
	File        string
	ZephyrKey   string
	Repository  string
	RepoHref    string
	Framework   string
	FailCount   int
	RunsText    string
	LastSeenISO string
	LastSeenRel string
}

// KPIFailureRepoRow summarizes one affected repository.
type KPIFailureRepoRow struct {
	Repository      string
	RepoHref        string
	App             string
	OccurrencesText string
	AffectedRuns    int
	RatePer1k       string
	Lanes           []KPILink
}

// KPIFailureDetailPage is the V2 view model for GET /kpi/failures/{id}.
type KPIFailureDetailPage struct {
	Title                  string
	Site                   SiteInfo
	Filters                KPIFilterState
	OverviewHref           string
	PRsHref                string
	Breadcrumbs            []Crumb
	WindowDays             int
	ErrorClass             string
	NormalizedMessage      string
	TopFrame               string
	SampleMessage          string
	StateLabel             string
	StateClass             string
	GroupCountText         string
	IsLowSample            bool
	WindowPrimaryValue     string
	WindowMeta             string
	Rate14d                string
	Current14dText         string
	Prior14dText           string
	Delta14d               string
	Delta14dClass          string
	Trailing7dText         string
	LastSeenISO            string
	LastSeenRel            string
	LatestRunURL           string
	FirstSeen              KPIFailureFirstSeenView
	LatestVerdict          *KPIFailureVerdictView
	HasDaily               bool
	DailyBars              []KPIChartBar
	DailyChartW            int
	ActiveDaysText         string
	AffectedTests          []KPIFailureTestRow
	AffectedTestsCountText string
	AffectedRepos          []KPIFailureRepoRow
	AffectedReposCountText string
	SingleRepo             *KPIFailureRepoRow
	PRLinks                []KPILink
}

// BuildKPIFailureDetailPage maps kpi.FailureDetailResponse into KPIFailureDetailPage.
func BuildKPIFailureDetailPage(site SiteInfo, d kpi.FailureDetailResponse, now time.Time) KPIFailureDetailPage {
	return BuildKPIFailureDetailPageWithShow(site, d, now, false)
}

// BuildKPIFailureDetailPageWithShow maps kpi.FailureDetailResponse into KPIFailureDetailPage while preserving ?show=all.
func BuildKPIFailureDetailPageWithShow(site SiteInfo, d kpi.FailureDetailResponse, now time.Time, showAll bool) KPIFailureDetailPage {
	filterState := kpiFilterStateFromParams(d.Filters, "/kpi", kpi.SortOccurrencesDesc)
	filterState.ShowAll = showAll
	overviewHref := kpiHref("/kpi", filterState, 1, "", false)
	prsHref := kpiHref("/kpi/prs", filterState, 1, "", false)

	stateLabel, stateClass := mapFailureStateBadge(d.State, d.QuietDays)
	errClass := derefString(d.ErrorClass)
	topFrame := derefString(d.TopFrame)
	breadcrumbs := buildFailureBreadcrumbs(filterState, overviewHref, errClass)
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

	latestRunHref := safeRunHref(d.LatestRunURL)

	firstSeen := buildFailureFirstSeenView(d.FirstSeen, filterState, now)
	verdictView := buildFailureVerdictView(d.LatestVerdict, now)
	dailyBars, dailyW, activeDays := buildFailureDailyBars(d.DailySeries)
	tests := buildFailureAffectedTests(d.AffectedTests, filterState, now)
	repos, singleRepo := buildFailureAffectedRepos(d.AffectedRepos, filterState)
	prLinks := buildFailurePRLinks(d.PRNumbers, d.AffectedRepos)

	shortLabel := shortSignatureLabel(d.ErrorClass, d.NormalizedMessage)
	rateText, isLowSample := formatFailureRate(d.RatePer1k, d.OccurrenceCount, d.AffectedTestRuns)
	windowPrimaryValue := rateText
	windowMeta := fmt.Sprintf(
		"%s across %s · %s",
		pluralize(d.OccurrenceCount, "failure", "failures"),
		pluralize(d.AffectedTestRuns, "affected test run", "affected test runs"),
		pluralize(d.AffectedTestCount, "test", "tests"),
	)
	if isLowSample {
		windowPrimaryValue = pluralize(d.OccurrenceCount, "failure", "failures")
		windowMeta = fmt.Sprintf("%s of %s · %s", lowSamplePct(rateText), pluralize(d.AffectedTestRuns, "affected test run", "affected test runs"), pluralize(d.AffectedTestCount, "test", "tests"))
	}

	rate14d, _ := formatFailureRate(d.RatePer1k14d, d.Occurrences14d, d.AffectedRuns14d)
	delta14d, delta14dClass := formatFailureDelta14d(d.DeltaRatePer1k, d.Occurrences14d, d.AffectedRuns14d, d.PriorOccurrences14d, d.PriorAffectedRuns14d)
	cur14dText := formatFailurePeriodSummary(d.RatePer1k14d, d.Occurrences14d, d.AffectedRuns14d)
	prior14dText := "no prior affected test runs"
	if d.PriorAffectedRuns14d > 0 {
		prior14dText = formatFailurePeriodSummary(d.PriorRatePer1k14d, d.PriorOccurrences14d, d.PriorAffectedRuns14d)
	}

	windowDays := d.Filters.WindowDays
	if windowDays <= 0 {
		windowDays = 30
	}

	return KPIFailureDetailPage{
		Title:                  shortLabel + " · KPIs · Test System IO",
		Site:                   site,
		Filters:                filterState,
		OverviewHref:           overviewHref,
		PRsHref:                prsHref,
		Breadcrumbs:            breadcrumbs,
		WindowDays:             windowDays,
		ErrorClass:             errClass,
		NormalizedMessage:      d.NormalizedMessage,
		TopFrame:               topFrame,
		SampleMessage:          sampleMsg,
		StateLabel:             stateLabel,
		StateClass:             stateClass,
		GroupCountText:         pluralize(d.GroupCount, "CI run", "CI runs"),
		IsLowSample:            isLowSample,
		WindowPrimaryValue:     windowPrimaryValue,
		WindowMeta:             windowMeta,
		Rate14d:                rate14d,
		Current14dText:         cur14dText,
		Prior14dText:           prior14dText,
		Delta14d:               delta14d,
		Delta14dClass:          delta14dClass,
		Trailing7dText:         fmt.Sprintf("%s in %s", pluralize(d.Occurrences7d, "failure", "failures"), pluralize(d.AffectedRuns7d, "affected test run", "affected test runs")),
		LastSeenISO:            lastISO,
		LastSeenRel:            lastRel,
		LatestRunURL:           latestRunHref,
		FirstSeen:              firstSeen,
		LatestVerdict:          verdictView,
		HasDaily:               len(dailyBars) > 0,
		DailyBars:              dailyBars,
		DailyChartW:            dailyW,
		ActiveDaysText:         pluralize(activeDays, "active day", "active days"),
		AffectedTests:          tests,
		AffectedTestsCountText: pluralize(len(tests), "test", "tests"),
		AffectedRepos:          repos,
		AffectedReposCountText: pluralize(len(repos), "repository", "repositories"),
		SingleRepo:             singleRepo,
		PRLinks:                prLinks,
	}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func lowSamplePct(rateText string) string {
	if idx := strings.IndexByte(rateText, ' '); idx > 0 {
		return rateText[:idx]
	}
	return rateText
}

func formatFailurePeriodSummary(ratePer1k *float64, occurrences, affectedRuns int) string {
	if affectedRuns <= 0 {
		return "0 affected test runs"
	}
	rateText, isLowSample := formatFailureRate(ratePer1k, occurrences, affectedRuns)
	if isLowSample {
		return fmt.Sprintf("%s (%d of %s)", lowSamplePct(rateText), occurrences, pluralize(affectedRuns, "affected test run", "affected test runs"))
	}
	return fmt.Sprintf("%s (%s in %s)", rateText, pluralize(occurrences, "failure", "failures"), pluralize(affectedRuns, "affected test run", "affected test runs"))
}

func buildFailureBreadcrumbs(filterState KPIFilterState, overviewHref, errClass string) []Crumb {
	crumbTitle := errClass
	if crumbTitle == "" {
		crumbTitle = "Failure pattern"
	}
	breadcrumbs := []Crumb{{
		Label: "KPIs",
		Href:  kpiScopeHref(filterState, "", ""),
	}}
	if filterState.Repo != "" {
		breadcrumbs = append(breadcrumbs, Crumb{
			Label: friendlyRepoName(filterState.Repo),
			Href:  kpiScopeHref(filterState, filterState.Repo, ""),
		})
	} else if filterState.App != "" {
		appState := KPIFilterState{
			App:     filterState.App,
			Window:  filterState.Window,
			Limit:   filterState.Limit,
			ShowAll: filterState.ShowAll,
		}
		breadcrumbs = append(breadcrumbs, Crumb{
			Label: friendlyAppName(filterState.App),
			Href:  kpiHref("/kpi", appState, 1, "", false),
		})
	}
	if filterState.Lane != "" {
		breadcrumbs = append(breadcrumbs, Crumb{
			Label: filterState.Lane,
			Href:  overviewHref,
		})
	}
	return append(breadcrumbs, Crumb{Label: crumbTitle})
}

func buildFailureFirstSeenView(fs kpi.FailureFirstSeen, filterState KPIFilterState, now time.Time) KPIFailureFirstSeenView {
	fsRepo := derefString(fs.Repository)
	fsLane := derefString(fs.Lane)
	fsSHA := derefString(fs.CommitSHA)
	fsRunURL := safeRunHref(fs.RunURL)
	fsRepoHref := ""
	if fsRepo != "" {
		fsRepoHref = kpiScopeHref(filterState, fsRepo, "")
	}
	fsLaneHref := ""
	if fsLane != "" {
		fsLaneHref = kpiScopeHref(filterState, fsRepo, fsLane)
	}
	fsISO, fsRel := formatKPITimestamp(fs.FirstSeenAt, now)
	view := KPIFailureFirstSeenView{
		HasData:      !fs.FirstSeenAt.IsZero(),
		TimestampISO: fsISO,
		TimestampRel: fsRel,
		Repository:   fsRepo,
		RepoHref:     fsRepoHref,
		Lane:         fsLane,
		LaneHref:     fsLaneHref,
		Branch:       derefString(fs.Branch),
		ShortSHA:     shortSHA(fsSHA),
		CommitURL:    githubCommitURL(fsRepo, fsSHA),
		RunURL:       fsRunURL,
	}
	if fs.GHPRNumber != nil && *fs.GHPRNumber > 0 {
		view.PRNumber = *fs.GHPRNumber
		view.PRURL = githubPRURL(fsRepo, *fs.GHPRNumber)
		view.HasPR = true
	}
	return view
}

func buildFailureVerdictView(v *kpi.FailureVerdict, now time.Time) *KPIFailureVerdictView {
	if v == nil {
		return nil
	}
	vISO, vRel := formatKPITimestamp(v.CreatedAt, now)
	conf := ""
	if v.Confidence != nil {
		pct := *v.Confidence * 100
		if *v.Confidence > 1.0 {
			pct = *v.Confidence
		}
		conf = fmt.Sprintf("%.0f%%", pct)
	}
	return &KPIFailureVerdictView{
		Label:      "Run verdict: " + humanizeVerdictLabel(v.Label),
		DecidedBy:  derefString(v.DecidedBy),
		Confidence: conf,
		Summary:    derefString(v.Summary),
		TriageURL:  safeExternalURL(v.TriageURL),
		CreatedISO: vISO,
		CreatedRel: vRel,
	}
}

func buildFailureAffectedTests(src []kpi.FailureAffectedTest, filterState KPIFilterState, now time.Time) []KPIFailureTestRow {
	tests := make([]KPIFailureTestRow, 0, len(src))
	for _, t := range src {
		tISO, tRel := formatKPITimestamp(t.LastSeenAt, now)
		runsText := pluralize(t.RunCount, "test run", "test runs")
		if t.GroupCount > 0 {
			runsText = fmt.Sprintf("%s · %s", pluralize(t.RunCount, "test run", "test runs"), pluralize(t.GroupCount, "CI run", "CI runs"))
		}
		repoHref := ""
		if t.Repository != "" {
			repoHref = kpiScopeHref(filterState, t.Repository, "")
		}
		tests = append(tests, KPIFailureTestRow{
			TestID:      t.TestID,
			Title:       t.FullTitle,
			File:        t.File,
			ZephyrKey:   derefString(t.ZephyrKey),
			Repository:  t.Repository,
			RepoHref:    repoHref,
			Framework:   t.Framework,
			FailCount:   t.FailCount,
			RunsText:    runsText,
			LastSeenISO: tISO,
			LastSeenRel: tRel,
		})
	}
	return tests
}

func buildFailureAffectedRepos(src []kpi.FailureAffectedRepo, filterState KPIFilterState) ([]KPIFailureRepoRow, *KPIFailureRepoRow) {
	repos := make([]KPIFailureRepoRow, 0, len(src))
	for _, r := range src {
		repoRate, _ := formatFailureRate(r.RatePer1k, r.OccurrenceCount, r.AffectedRuns)
		repoHref := ""
		if r.Repository != "" {
			repoHref = kpiScopeHref(filterState, r.Repository, "")
		}
		laneLinks := make([]KPILink, 0, len(r.Lanes))
		for _, ln := range r.Lanes {
			laneLinks = append(laneLinks, KPILink{
				Label: ln,
				Href:  kpiScopeHref(filterState, r.Repository, ln),
			})
		}
		repos = append(repos, KPIFailureRepoRow{
			Repository:      r.Repository,
			RepoHref:        repoHref,
			App:             r.App,
			OccurrencesText: fmt.Sprintf("%s (%s)", pluralize(r.OccurrenceCount, "failure", "failures"), pluralize(r.GroupCount, "CI run", "CI runs")),
			AffectedRuns:    r.AffectedRuns,
			RatePer1k:       repoRate,
			Lanes:           laneLinks,
		})
	}
	var singleRepo *KPIFailureRepoRow
	if len(repos) == 1 {
		cp := repos[0]
		singleRepo = &cp
	}
	return repos, singleRepo
}

func buildFailurePRLinks(prNums []int, affectedRepos []kpi.FailureAffectedRepo) []KPILink {
	prRepo := ""
	if len(affectedRepos) == 1 {
		prRepo = affectedRepos[0].Repository
	}
	prLinks := make([]KPILink, 0, len(prNums))
	for _, prNum := range prNums {
		href := ""
		if prRepo != "" {
			href = githubPRURL(prRepo, prNum)
		}
		prLinks = append(prLinks, KPILink{
			Label: fmt.Sprintf("PR #%d", prNum),
			Href:  href,
		})
	}
	return prLinks
}

func buildFailureDailyBars(series []kpi.FailureDailyPoint) (bars []KPIChartBar, chartW int, activeDays int) {
	if len(series) == 0 {
		return nil, 0, 0
	}
	maxOcc := 1
	for _, pt := range series {
		if pt.OccurrenceCount > maxOcc {
			maxOcc = pt.OccurrenceCount
		}
		if pt.OccurrenceCount > 0 {
			activeDays++
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
	for i, pt := range series {
		rateText, _ := formatFailureRate(pt.RatePer1k, pt.OccurrenceCount, pt.AffectedTestRuns)
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
	return bars, chartW, activeDays
}
