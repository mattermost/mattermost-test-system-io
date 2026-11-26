package htmlpage

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

// PR E2E sort & attribution literals aligned with SEC-11509 (internal/kpi/types.go).
const (
	kpiPRSortCreatedDesc = "created_desc"
	kpiPRSortAgeDesc     = "age_desc"
	kpiPRSortPRDesc      = "pr_desc"
	kpiPRSortLaneAsc     = "lane_asc"
	kpiPRSortReasonAsc   = "reason_asc"

	kpiReasonWentGreenOnRerun = "went_green_on_rerun"
	kpiReasonFailingElsewhere = "failing_elsewhere"
	kpiReasonOther            = "other"
)

var kpiPRSortOptions = []KPIFilterOption{
	{Value: kpiPRSortCreatedDesc, Label: "Newest first"},
	{Value: kpiPRSortAgeDesc, Label: "Oldest first"},
	{Value: kpiPRSortPRDesc, Label: "PR # high to low"},
	{Value: kpiPRSortLaneAsc, Label: "Lane A–Z"},
	{Value: kpiPRSortReasonAsc, Label: "Reason"},
}

// ParseKPIPRsParams validates query parameters for GET /kpi/prs matching SEC-11509 ParsePRsRedParams.
func ParseKPIPRsParams(q url.Values, now time.Time) (kpi.FilterParams, error) {
	sortBy := strings.TrimSpace(q.Get("sort"))
	if sortBy == "" {
		sortBy = kpiPRSortCreatedDesc
	}
	switch sortBy {
	case kpiPRSortCreatedDesc, kpiPRSortAgeDesc, kpiPRSortPRDesc, kpiPRSortLaneAsc, kpiPRSortReasonAsc:
	default:
		return kpi.FilterParams{}, fmt.Errorf("%w: invalid sort %q", kpi.ErrValidation, sortBy)
	}

	cleanQ := make(url.Values, len(q))
	for k, v := range q {
		cleanQ[k] = v
	}
	cleanQ.Del("sort")
	p, err := kpi.ParseSummaryParams(cleanQ, now)
	if err != nil {
		return kpi.FilterParams{}, err
	}
	p.Sort = sortBy
	return p, nil
}

// KPIPRReasonMixInput mirrors SEC-11509 PRFalseRedReasonMix.
type KPIPRReasonMixInput struct {
	WentGreenOnRerun int
	FailingElsewhere int
	Other            int
	Total            int
}

// KPIConfiguredLane mirrors SEC-11509 ConfiguredE2ELane.
type KPIConfiguredLane struct {
	Repository string
	Lane       string
}

// KPIRedLaneInput mirrors SEC-11509 RedPRLaneRow (no author names).
type KPIRedLaneInput struct {
	ReportGroupID                 string
	Repository                    string
	App                           string
	Lane                          string
	Branch                        string
	GHPRNumber                    int
	CommitSHA                     string
	GHRunID                       string
	GHRunAttempt                  string
	Platform                      *string
	Framework                     string
	HasOrchestration              bool
	OrchStatus                    *string
	CreatedAt                     time.Time
	AgeSeconds                    int64
	Settled                       bool
	StillRed                      bool
	WentGreenOnRerun              bool
	FailingOnMain                 bool
	OtherPRsCount                 int
	AllSignaturesFailingElsewhere bool
	FailedSignatureCount          int
	TestsFailed                   int
	Reason                        string
	RunURL                        string
}

// KPIPRsInput mirrors SEC-11509 PRRedResponse for Phase A/B compatibility.
type KPIPRsInput struct {
	Filters              kpi.FilterParams
	WindowDays           int
	SettledAfterHours    int
	IsLowerBound         bool
	RateQualifier        string
	FalseRedRate         *float64
	PriorFalseRedRate    *float64
	DeltaPP              *float64
	SettledRedLanes      int
	SettledFalseRedLanes int
	UnsettledRedLanes    int
	StillRedLanes        int
	ReasonMix            KPIPRReasonMixInput
	ConfiguredE2ELanes   []KPIConfiguredLane
	LaneCoverage         kpi.LaneCoverage
	Pagination           kpi.Pagination
	Lanes                []KPIRedLaneInput
}

// KPIReasonMixSegment represents one reason category in the reason mix bar.
type KPIReasonMixSegment struct {
	Key        string
	Label      string
	Count      int
	SharePct   string
	WidthPct   int
	ColorClass string
}

// KPIRedPRRow represents one red PR E2E lane in the "Red now" queue.
// Author names are intentionally omitted; status always says "E2E red" (never "blocked").
type KPIRedPRRow struct {
	ReportGroupID     string
	PRNumber          int
	Repository        string
	App               string
	Lane              string
	Platform          string
	ShortSHA          string
	StatusLabel       string
	StatusClass       string
	SettledLabel      string
	SettledClass      string
	AgeText           string
	CreatedISO        string
	ReasonLabel       string
	ReasonClass       string
	ElsewhereSummary  string
	FailingOnMain     bool
	OtherPRsCount     int
	HasElsewhereMatch bool
	TestsFailed       int
	FailedSignatures  int
	RunURL            string
}

// KPIPRsPage is the view model for GET /kpi/prs.
type KPIPRsPage struct {
	Title                string
	Site                 SiteInfo
	Filters              KPIFilterBar
	OverviewHref         string
	PRsHref              string
	HasRateData          bool
	HeadlineRate         string
	Delta                string
	DeltaClass           string
	SettledFalseRedLanes int
	SettledRedLanes      int
	UnsettledRedLanes    int
	StillRedLanes        int
	SettledNote          string
	ReasonMixTotal       int
	ReasonMix            []KPIReasonMixSegment
	ConfiguredLanes      []KPIConfiguredLane
	CoverageSummary      string
	CoverageNotes        []string
	HasLanes             bool
	Lanes                []KPIRedPRRow
	Total                int
	Page                 int
	TotalPages           int
	RowStart             int
	ShowEnd              int
}

// BuildKPIPRsPage maps KPIPRsInput into KPIPRsPage.
func BuildKPIPRsPage(
	site SiteInfo,
	opts kpi.FilterOptions,
	data KPIPRsInput,
	now time.Time,
) KPIPRsPage {
	filterBar := buildKPIFilterBar(data.Filters, opts, "/kpi/prs", kpiPRSortCreatedDesc, kpiPRSortOptions)
	overviewHref := kpiHref("/kpi", filterBar.State, 1, "", false)
	prsHref := kpiHref("/kpi/prs", filterBar.State, 1, "", false)

	settledHours := data.SettledAfterHours
	if settledHours <= 0 {
		settledHours = 24
	}
	qualifier := data.RateQualifier
	if qualifier == "" {
		qualifier = "at least"
	}

	headlineRate := kpiNotAvailable
	hasRateData := false
	if data.FalseRedRate != nil {
		headlineRate = fmt.Sprintf("%s %.1f%%", qualifier, *data.FalseRedRate)
		hasRateData = true
	}

	settledNote := fmt.Sprintf(
		"Lower bound only: excludes red PR lanes fixed by pushing a new commit. Runs younger than %d hours are unsettled and excluded from the rate denominator.",
		settledHours,
	)

	reasonTotal := data.ReasonMix.Total
	if reasonTotal == 0 {
		reasonTotal = data.ReasonMix.WentGreenOnRerun + data.ReasonMix.FailingElsewhere + data.ReasonMix.Other
	}
	reasonSegments := []KPIReasonMixSegment{
		buildReasonSegment(kpiReasonWentGreenOnRerun, "Went green on rerun", data.ReasonMix.WentGreenOnRerun, reasonTotal, "kpi-mix-rerun"),
		buildReasonSegment(kpiReasonFailingElsewhere, "Also failing elsewhere", data.ReasonMix.FailingElsewhere, reasonTotal, "kpi-mix-elsewhere"),
		buildReasonSegment(kpiReasonOther, "Other", data.ReasonMix.Other, reasonTotal, "kpi-mix-other"),
	}

	rows := make([]KPIRedPRRow, 0, len(data.Lanes))
	for _, lane := range data.Lanes {
		statusLabel := "E2E red"
		statusClass := "kpi-badge kpi-status-e2e-red"
		if !lane.StillRed && lane.WentGreenOnRerun {
			statusLabel = "Went green on rerun"
			statusClass = "kpi-badge kpi-reason-rerun"
		}

		settledLabel := fmt.Sprintf("Unsettled (<%dh)", settledHours)
		settledClass := "kpi-badge kpi-badge-neutral"
		if lane.Settled {
			settledLabel = "Settled"
			settledClass = "kpi-badge kpi-badge-muted"
		}

		reasonLabel, reasonClass := mapPRReasonBadge(lane.Reason)
		elsewhereSummary, hasElsewhere := formatElsewhereMatch(lane.FailingOnMain, lane.OtherPRsCount)
		createdISO, _ := formatKPITimestamp(lane.CreatedAt, now)
		platform := ""
		if lane.Platform != nil {
			platform = *lane.Platform
		}
		runHref := lane.RunURL
		if runHref == "" || (!strings.HasPrefix(runHref, "/") && !strings.HasPrefix(runHref, "https://") && !strings.HasPrefix(runHref, "http://")) {
			runHref = "/reports"
		}

		rows = append(rows, KPIRedPRRow{
			ReportGroupID:     lane.ReportGroupID,
			PRNumber:          lane.GHPRNumber,
			Repository:        lane.Repository,
			App:               lane.App,
			Lane:              lane.Lane,
			Platform:          platform,
			ShortSHA:          shortSHA(lane.CommitSHA),
			StatusLabel:       statusLabel,
			StatusClass:       statusClass,
			SettledLabel:      settledLabel,
			SettledClass:      settledClass,
			AgeText:           formatSecondsDuration(lane.AgeSeconds),
			CreatedISO:        createdISO,
			ReasonLabel:       reasonLabel,
			ReasonClass:       reasonClass,
			ElsewhereSummary:  elsewhereSummary,
			FailingOnMain:     lane.FailingOnMain,
			OtherPRsCount:     lane.OtherPRsCount,
			HasElsewhereMatch: hasElsewhere,
			TestsFailed:       lane.TestsFailed,
			FailedSignatures:  lane.FailedSignatureCount,
			RunURL:            runHref,
		})
	}

	totalPages, rowStart, showEnd := paginateRange(data.Pagination.Page, data.Pagination.Limit, data.Pagination.Total)
	if data.Pagination.TotalPages > 0 {
		totalPages = data.Pagination.TotalPages
	}
	pageNum := data.Pagination.Page
	if pageNum < 1 {
		pageNum = 1
	}

	return KPIPRsPage{
		Title:                "PR E2E · KPIs · Test System IO",
		Site:                 site,
		Filters:              filterBar,
		OverviewHref:         overviewHref,
		PRsHref:              prsHref,
		HasRateData:          hasRateData,
		HeadlineRate:         headlineRate,
		Delta:                formatDeltaPP(data.DeltaPP),
		DeltaClass:           deltaClassLowerGood(data.DeltaPP),
		SettledFalseRedLanes: data.SettledFalseRedLanes,
		SettledRedLanes:      data.SettledRedLanes,
		UnsettledRedLanes:    data.UnsettledRedLanes,
		StillRedLanes:        data.StillRedLanes,
		SettledNote:          settledNote,
		ReasonMixTotal:       reasonTotal,
		ReasonMix:            reasonSegments,
		ConfiguredLanes:      append([]KPIConfiguredLane(nil), data.ConfiguredE2ELanes...),
		CoverageSummary:      formatLaneCoverageSummary(data.LaneCoverage),
		CoverageNotes:        append([]string(nil), data.LaneCoverage.Notes...),
		HasLanes:             len(rows) > 0,
		Lanes:                rows,
		Total:                data.Pagination.Total,
		Page:                 pageNum,
		TotalPages:           totalPages,
		RowStart:             rowStart,
		ShowEnd:              showEnd,
	}
}

func buildReasonSegment(key, label string, count, total int, colorClass string) KPIReasonMixSegment {
	share := 0.0
	width := 0
	if total > 0 {
		share = float64(count) * 100.0 / float64(total)
		width = int(share + 0.5)
		if count > 0 && width < 2 {
			width = 2
		}
	}
	return KPIReasonMixSegment{
		Key:        key,
		Label:      label,
		Count:      count,
		SharePct:   fmt.Sprintf("%.1f%%", share),
		WidthPct:   width,
		ColorClass: colorClass,
	}
}

func mapPRReasonBadge(reason string) (label, cssClass string) {
	switch reason {
	case kpiReasonWentGreenOnRerun:
		return "Went green on rerun", "kpi-badge kpi-reason-rerun"
	case kpiReasonFailingElsewhere:
		return "Also failing elsewhere", "kpi-badge kpi-reason-elsewhere"
	default:
		return "Other", "kpi-badge kpi-reason-other"
	}
}

func formatElsewhereMatch(failingOnMain bool, otherPRs int) (string, bool) {
	switch {
	case failingOnMain && otherPRs > 0:
		return fmt.Sprintf("Failing on main + %d other PRs", otherPRs), true
	case failingOnMain:
		return "Failing on main", true
	case otherPRs >= 3:
		return fmt.Sprintf("Failing on %d other PRs", otherPRs), true
	case otherPRs > 0:
		return fmt.Sprintf("Seen on %d other PR(s)", otherPRs), false
	default:
		return "No cross-PR/main match", false
	}
}
