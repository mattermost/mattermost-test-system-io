package htmlpage

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

var kpiPRSortOptions = []KPIFilterOption{
	{Value: kpi.SortCreatedDesc, Label: "Newest first"},
	{Value: kpi.SortAgeDesc, Label: "Oldest first"},
	{Value: kpi.SortPRDesc, Label: "PR # high to low"},
	{Value: kpi.SortLaneAsc, Label: "Lane A–Z"},
	{Value: kpi.SortReasonAsc, Label: "Cause"},
}

// KPIReasonMixSegment represents one attribution category in the reason mix bar.
type KPIReasonMixSegment struct {
	Key        string
	Label      string
	Count      int
	SharePct   string
	WidthPct   int
	ColorClass string
}

// KPIRedPRRow represents one red PR E2E lane in the "Red now" queue.
// Author names are intentionally omitted; red statuses use "E2E red" (never "blocked").
type KPIRedPRRow struct {
	ReportGroupID string
	PRNumber      int
	Repository    string
	RepoShort     string
	Lane          string
	Platform      string
	ShortSHA      string
	StatusLabel   string
	StatusClass   string
	CauseLabel    string
	CauseClass    string
	AgeText       string
	CreatedISO    string
	FailedSummary string
	RunURL        string
}

// KPIPRsPage is the V2 view model for GET /kpi/prs.
type KPIPRsPage struct {
	Title                string
	Site                 SiteInfo
	Filters              KPIFilterBar
	OverviewHref         string
	PRsHref              string
	ShowRepoBadge        bool
	HasRateData          bool
	HealthLabel          string
	HealthClass          string
	HeadlineRate         string
	Delta                string
	DeltaClass           string
	SettledFalseRedLanes int
	SettledRedLanes      int
	UnsettledRedLanes    int
	QueueSummary         string
	SettledNote          string
	ReasonMixTotal       int
	ReasonMixTotalText   string
	ReasonMix            []KPIReasonMixSegment
	HasLanes             bool
	Lanes                []KPIRedPRRow
	Total                int
	TotalLanesText       string
	Page                 int
	TotalPages           int
	RowStart             int
	ShowEnd              int
	ShowPagination       bool
	HasPrev              bool
	HasNext              bool
	PrevHref             string
	NextHref             string
}

// BuildKPIPRsPage maps kpi.PRRedResponse into KPIPRsPage.
func BuildKPIPRsPage(
	site SiteInfo,
	opts kpi.FilterOptions,
	data kpi.PRRedResponse,
	now time.Time,
) KPIPRsPage {
	filterBar := buildKPIFilterBar(data.Filters, opts, "/kpi/prs", kpi.SortCreatedDesc, kpiPRSortOptions)
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
	var healthLabel, healthClass string
	reasonTotal := data.ReasonMix.Total
	if reasonTotal == 0 {
		reasonTotal = data.ReasonMix.WentGreenOnRerun + data.ReasonMix.FailingElsewhere + data.ReasonMix.Other
	}
	if data.FalseRedRate != nil {
		headlineRate = fmt.Sprintf("%s %.1f%%", qualifier, *data.FalseRedRate)
		hasRateData = true
		if reasonTotal > 0 && data.ReasonMix.Other == reasonTotal {
			healthLabel = "Unclassified"
			healthClass = kpiBadgeNeutral
		} else {
			healthLabel, healthClass = healthBadgeLowerGood(data.FalseRedRate, 5, 15)
		}
	}

	deltaText, deltaClass := formatPRFalseRedDelta(data.DeltaPP, data.SettledRedLanes, data.PriorSettledRedLanes)
	settledNote := fmt.Sprintf("Settled >%dh runs; excludes failures resolved by pushing a new commit.", settledHours)

	reasonSegments := []KPIReasonMixSegment{
		buildReasonSegment(kpi.ReasonWentGreenOnRerun, "Went green on rerun", data.ReasonMix.WentGreenOnRerun, reasonTotal, "kpi-mix-rerun"),
		buildReasonSegment(kpi.ReasonFailingElsewhere, "Also failing elsewhere", data.ReasonMix.FailingElsewhere, reasonTotal, "kpi-mix-elsewhere"),
		buildReasonSegment(kpi.ReasonOther, "PR-specific / Unclassified", data.ReasonMix.Other, reasonTotal, "kpi-mix-other"),
	}

	rows := make([]KPIRedPRRow, 0, len(data.Lanes))
	for _, lane := range data.Lanes {
		statusLabel := "E2E red"
		statusClass := "kpi-badge kpi-status-e2e-red"
		if !lane.StillRed && lane.WentGreenOnRerun {
			statusLabel = "Went green on rerun"
			statusClass = "kpi-badge kpi-reason-rerun"
		}

		causeLabel, causeClass := resolvePRCause(lane, settledHours)
		createdISO, _ := formatKPITimestamp(lane.CreatedAt, now)
		platform := ""
		if lane.Platform != nil {
			platform = *lane.Platform
		}
		runHref := strings.TrimSpace(lane.RunURL)
		isSameOrigin := strings.HasPrefix(runHref, "/") && !strings.HasPrefix(runHref, "//") && !strings.HasPrefix(runHref, "/\\")
		if runHref == "" || (!isSameOrigin && !strings.HasPrefix(runHref, "https://") && !strings.HasPrefix(runHref, "http://")) {
			runHref = "/reports"
		}

		rows = append(rows, KPIRedPRRow{
			ReportGroupID: lane.ReportGroupID,
			PRNumber:      lane.GHPRNumber,
			Repository:    lane.Repository,
			RepoShort:     friendlyRepoName(lane.Repository),
			Lane:          lane.Lane,
			Platform:      platform,
			ShortSHA:      shortSHA(lane.CommitSHA),
			StatusLabel:   statusLabel,
			StatusClass:   statusClass,
			CauseLabel:    causeLabel,
			CauseClass:    causeClass,
			AgeText:       formatSecondsDuration(lane.AgeSeconds),
			CreatedISO:    createdISO,
			FailedSummary: pluralize(lane.TestsFailed, "test", "tests") + " · " + pluralize(lane.FailedSignatureCount, "error", "errors"),
			RunURL:        runHref,
		})
	}

	pag := buildKPIPagination(filterBar.State, data.Pagination.Page, data.Pagination.Limit, data.Pagination.Total, data.Pagination.TotalPages)
	queueSummary := fmt.Sprintf(
		"%d aged (>%dh) · %d recent (<%dh) · %s total",
		data.SettledRedLanes, settledHours, data.UnsettledRedLanes, settledHours,
		pluralize(data.Pagination.Total, "red PR run", "red PR runs"),
	)

	return KPIPRsPage{
		Title:                "PR E2E · KPIs · Test System IO",
		Site:                 site,
		Filters:              filterBar,
		OverviewHref:         overviewHref,
		PRsHref:              prsHref,
		ShowRepoBadge:        !filterBar.IsSingleRepo,
		HasRateData:          hasRateData,
		HealthLabel:          healthLabel,
		HealthClass:          healthClass,
		HeadlineRate:         headlineRate,
		Delta:                deltaText,
		DeltaClass:           deltaClass,
		SettledFalseRedLanes: data.SettledFalseRedLanes,
		SettledRedLanes:      data.SettledRedLanes,
		UnsettledRedLanes:    data.UnsettledRedLanes,
		QueueSummary:         queueSummary,
		SettledNote:          settledNote,
		ReasonMixTotal:       reasonTotal,
		ReasonMixTotalText:   pluralize(reasonTotal, "aged red run", "aged red runs"),
		ReasonMix:            reasonSegments,
		HasLanes:             len(rows) > 0,
		Lanes:                rows,
		Total:                data.Pagination.Total,
		TotalLanesText:       pluralize(data.Pagination.Total, "E2E red PR lane run", "E2E red PR lane runs"),
		Page:                 pag.Page,
		TotalPages:           pag.TotalPages,
		RowStart:             pag.RowStart,
		ShowEnd:              pag.ShowEnd,
		ShowPagination:       pag.ShowPagination,
		HasPrev:              pag.HasPrev,
		HasNext:              pag.HasNext,
		PrevHref:             pag.PrevHref,
		NextHref:             pag.NextHref,
	}
}

func resolvePRCause(lane kpi.RedPRLaneRow, settledHours int) (label, cssClass string) {
	if lane.WentGreenOnRerun {
		return "Went green on rerun", "kpi-badge kpi-reason-rerun"
	}
	if lane.FailingOnMain && lane.OtherPRsCount > 0 {
		return fmt.Sprintf("Failing on main + %d other PRs", lane.OtherPRsCount), kpiBadgeWarn
	}
	if lane.FailingOnMain {
		return "Failing on main", kpiBadgeWarn
	}
	if lane.Reason == kpi.ReasonFailingElsewhere || lane.OtherPRsCount >= 3 {
		return fmt.Sprintf("Failing on %d other PRs", lane.OtherPRsCount), kpiBadgeWarn
	}
	if !lane.Settled {
		if lane.OtherPRsCount > 0 {
			return fmt.Sprintf("Recent (<%dh) · seen on %s", settledHours, pluralize(lane.OtherPRsCount, "other PR", "other PRs")), kpiBadgeNeutral
		}
		return fmt.Sprintf("Recent (<%dh unsettled)", settledHours), kpiBadgeNeutral
	}
	if lane.OtherPRsCount > 0 {
		return "Seen on " + pluralize(lane.OtherPRsCount, "other PR", "other PRs"), "kpi-badge kpi-reason-other"
	}
	return "PR-specific / Unclassified", "kpi-badge kpi-reason-other"
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
