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
	LastSeenISO string
	LastSeenRel string
}

// KPIFailureRepoRow summarizes one affected repository.
type KPIFailureRepoRow struct {
	Repository      string
	App             string
	OccurrenceCount int
	GroupCount      int
	AffectedRuns    int
	RatePer1k       string
	LanesSummary    string
}

// KPIFailureDetailPage is the view model for GET /kpi/failures/{id}.
type KPIFailureDetailPage struct {
	Title                string
	Site                 SiteInfo
	Filters              KPIFilterState
	OverviewHref         string
	PRsHref              string
	RequestedSignatureID string
	CanonicalSignatureID string
	IsMerged             bool
	MergedCount          int
	Fingerprint          string
	AlgoVersion          int
	ErrorClass           string
	NormalizedMessage    string
	TopFrame             string
	SampleMessage        string
	StateLabel           string
	StateClass           string
	OccurrenceCount      int
	RawAttemptCount      int
	GroupCount           int
	AffectedTestCount    int
	AffectedTestRuns     int
	RatePer1k            string
	Rate14d              string
	Occ14d               int
	Runs14d              int
	PriorRate14d         string
	PriorOcc14d          int
	PriorRuns14d         int
	Delta14d             string
	Delta14dClass        string
	Occ7d                int
	Runs7d               int
	LastSeenISO          string
	LastSeenRel          string
	FirstSeen            KPIFailureFirstSeenView
	LatestVerdict        *KPIFailureVerdictView
	HasDaily             bool
	DailyBars            []KPIChartBar
	DailyChartW          int
	DailyRows            []KPIFailureDailyRow
	AffectedTests        []KPIFailureTestRow
	AffectedRepos        []KPIFailureRepoRow
	PRNumbers            []int
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
		sampleMsg = *d.SampleMessage
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

	dailyBars, dailyW, dailyRows := buildFailureDailyChart(d.DailySeries)

	tests := make([]KPIFailureTestRow, 0, len(d.AffectedTests))
	for _, t := range d.AffectedTests {
		tISO, tRel := formatKPITimestamp(t.LastSeenAt, now)
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
			LastSeenISO: tISO,
			LastSeenRel: tRel,
		})
	}

	repos := make([]KPIFailureRepoRow, 0, len(d.AffectedRepos))
	for _, r := range d.AffectedRepos {
		repos = append(repos, KPIFailureRepoRow{
			Repository:      r.Repository,
			App:             r.App,
			OccurrenceCount: r.OccurrenceCount,
			GroupCount:      r.GroupCount,
			AffectedRuns:    r.AffectedRuns,
			RatePer1k:       formatRatePer1k(r.RatePer1k),
			LanesSummary:    strings.Join(r.Lanes, ", "),
		})
	}

	shortLabel := shortSignatureLabel(d.ErrorClass, d.NormalizedMessage)

	return KPIFailureDetailPage{
		Title:                shortLabel + " · KPIs · Test System IO",
		Site:                 site,
		Filters:              filterState,
		OverviewHref:         overviewHref,
		PRsHref:              prsHref,
		RequestedSignatureID: d.RequestedSignatureID,
		CanonicalSignatureID: d.CanonicalSignatureID,
		IsMerged:             d.MergedIntoID != nil && *d.MergedIntoID != "",
		MergedCount:          len(d.MergedSignatureIDs),
		Fingerprint:          d.Fingerprint,
		AlgoVersion:          d.AlgoVersion,
		ErrorClass:           errClass,
		NormalizedMessage:    d.NormalizedMessage,
		TopFrame:             topFrame,
		SampleMessage:        sampleMsg,
		StateLabel:           stateLabel,
		StateClass:           stateClass,
		OccurrenceCount:      d.OccurrenceCount,
		RawAttemptCount:      d.RawAttemptCount,
		GroupCount:           d.GroupCount,
		AffectedTestCount:    d.AffectedTestCount,
		AffectedTestRuns:     d.AffectedTestRuns,
		RatePer1k:            formatRatePer1k(d.RatePer1k),
		Rate14d:              formatRatePer1k(d.RatePer1k14d),
		Occ14d:               d.Occurrences14d,
		Runs14d:              d.AffectedRuns14d,
		PriorRate14d:         formatRatePer1k(d.PriorRatePer1k14d),
		PriorOcc14d:          d.PriorOccurrences14d,
		PriorRuns14d:         d.PriorAffectedRuns14d,
		Delta14d:             formatDeltaRatePer1k(d.DeltaRatePer1k),
		Delta14dClass:        deltaClassLowerGood(d.DeltaRatePer1k),
		Occ7d:                d.Occurrences7d,
		Runs7d:               d.AffectedRuns7d,
		LastSeenISO:          lastISO,
		LastSeenRel:          lastRel,
		FirstSeen:            firstSeen,
		LatestVerdict:        verdictView,
		HasDaily:             len(dailyRows) > 0,
		DailyBars:            dailyBars,
		DailyChartW:          dailyW,
		DailyRows:            dailyRows,
		AffectedTests:        tests,
		AffectedRepos:        repos,
		PRNumbers:            append([]int(nil), d.PRNumbers...),
	}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func buildFailureDailyChart(series []kpi.FailureDailyPoint) (bars []KPIChartBar, chartW int, rows []KPIFailureDailyRow) {
	if len(series) == 0 {
		return nil, 0, nil
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
		rateText := formatRatePer1k(pt.RatePer1k)
		rows = append(rows, KPIFailureDailyRow{
			Date:             pt.Date,
			OccurrenceCount:  pt.OccurrenceCount,
			RawAttemptCount:  pt.RawAttemptCount,
			GroupCount:       pt.GroupCount,
			AffectedTestRuns: pt.AffectedTestRuns,
			RatePer1k:        rateText,
		})

		h := 0
		colorClass := "kpi-bar-empty"
		if pt.OccurrenceCount > 0 {
			h = int(float64(pt.OccurrenceCount)/float64(maxOcc)*float64(maxH) + 0.5)
			if h < minBar {
				h = minBar
			}
			colorClass = "kpi-bar-bad"
		} else {
			h = minBar
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
			Tooltip:    fmt.Sprintf("%s: %d occurrences (%s, %d test runs)", pt.Date, pt.OccurrenceCount, rateText, pt.AffectedTestRuns),
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
	return bars, chartW, rows
}
