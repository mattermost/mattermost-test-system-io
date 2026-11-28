package htmlpage

import (
	"fmt"
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
	Tooltip    string
	Count      int
	SharePct   string
	WidthPct   int
	ColorClass string
}

// KPIRedPRRow represents one red PR E2E lane in the "Red PR runs" queue.
// Author names are intentionally omitted; red statuses use "E2E red" (never "blocked").
type KPIRedPRRow struct {
	ReportGroupID   string
	PRNumber        int
	Repository      string
	RepoShort       string
	RepoHref        string
	Lane            string
	LaneHref        string
	Platform        string
	ShortSHA        string
	GitHubPRURL     string
	GitHubCommitURL string
	StatusLabel     string
	StatusClass     string
	CauseLabel      string
	CauseClass      string
	AgeText         string
	CreatedISO      string
	FailedSummary   string
	RunURL          string
}

// KPIPRsPage is the V2 view model for GET /kpi/prs.
type KPIPRsPage struct {
	Title                string
	Site                 SiteInfo
	Filters              KPIFilterBar
	OverviewHref         string
	PRsHref              string
	FlakyHref            string
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
	flakyHref := kpiFlakyHref(filterBar.State)

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
			healthLabel = "0% passed rerun (>24h)"
			healthClass = kpiBadgeNeutral
		} else {
			healthLabel, healthClass = healthBadgeLowerGood(data.FalseRedRate, 5, 15)
		}
	}

	deltaText, deltaClass := formatPRFalseRedDelta(data.DeltaPP, data.SettledRedLanes, data.PriorSettledRedLanes)
	settledNote := fmt.Sprintf("Runs >%dh old; excludes failures resolved by pushing a new commit.", settledHours)

	reasonSegments := []KPIReasonMixSegment{
		buildReasonSegment(kpi.ReasonWentGreenOnRerun, "Passed on rerun", "Same PR & commit later passed on CI rerun without new commits", data.ReasonMix.WentGreenOnRerun, reasonTotal, "kpi-mix-rerun"),
		buildReasonSegment(kpi.ReasonFailingElsewhere, "Failing on main or ≥3 PRs", "Every failing error signature on this run also failed on main or ≥3 other PRs within ±24h", data.ReasonMix.FailingElsewhere, reasonTotal, "kpi-mix-elsewhere"),
		buildReasonSegment(kpi.ReasonOther, "Only this PR (or <3 PRs)", "At least one failing error signature was not shared by main and all other failing PRs within ±24h", data.ReasonMix.Other, reasonTotal, "kpi-mix-other"),
	}

	rows := make([]KPIRedPRRow, 0, len(data.Lanes))
	for _, lane := range data.Lanes {
		statusLabel := "E2E red"
		statusClass := "kpi-badge kpi-status-e2e-red"
		var causeLabel, causeClass string
		if !lane.StillRed && lane.WentGreenOnRerun {
			statusLabel = "Passed on rerun"
			statusClass = "kpi-badge kpi-reason-rerun"
		} else {
			if !lane.StillRed {
				statusLabel = "Newer commit green"
				statusClass = kpiBadgeGood
			}
			causeLabel, causeClass = resolvePRCause(lane, settledHours)
		}

		createdISO, _ := formatKPITimestamp(lane.CreatedAt, now)
		platform := ""
		if lane.Platform != nil {
			platform = *lane.Platform
		}
		runHref := safeRunHref(lane.RunURL)
		rows = append(rows, KPIRedPRRow{
			ReportGroupID:   lane.ReportGroupID,
			PRNumber:        lane.GHPRNumber,
			Repository:      lane.Repository,
			RepoShort:       friendlyRepoName(lane.Repository),
			RepoHref:        kpiScopeHref(filterBar.State, lane.Repository, ""),
			Lane:            lane.Lane,
			LaneHref:        kpiScopeHref(filterBar.State, lane.Repository, lane.Lane),
			Platform:        platform,
			ShortSHA:        shortSHA(lane.CommitSHA),
			GitHubPRURL:     githubPRURL(lane.Repository, lane.GHPRNumber),
			GitHubCommitURL: githubCommitURL(lane.Repository, lane.CommitSHA),
			StatusLabel:     statusLabel,
			StatusClass:     statusClass,
			CauseLabel:      causeLabel,
			CauseClass:      causeClass,
			AgeText:         formatSecondsDuration(lane.AgeSeconds),
			CreatedISO:      createdISO,
			FailedSummary:   pluralize(lane.TestsFailed, "test", "tests") + " · " + pluralize(lane.FailedSignatureCount, "error", "errors"),
			RunURL:          runHref,
		})
	}

	pag := buildKPIPagination(filterBar.State, data.Pagination.Page, data.Pagination.Limit, data.Pagination.Total, data.Pagination.TotalPages)
	queueSummary := fmt.Sprintf(
		"%d (>%dh) · %d (<%dh) · %s total",
		data.SettledRedLanes, settledHours, data.UnsettledRedLanes, settledHours,
		pluralize(data.Pagination.Total, "red PR run", "red PR runs"),
	)
	totalLanesText := pluralize(data.Pagination.Total, "red PR run", "red PR runs")
	if data.Pagination.Total > 0 {
		totalLanesText = fmt.Sprintf("%s total · %d still red", totalLanesText, data.StillRedLanes)
	}

	return KPIPRsPage{
		Title:                "PR E2E · KPIs · Test System IO",
		Site:                 site,
		Filters:              filterBar,
		OverviewHref:         overviewHref,
		PRsHref:              prsHref,
		FlakyHref:            flakyHref,
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
		ReasonMixTotalText:   pluralize(reasonTotal, "red run (>24h)", "red runs (>24h)"),
		ReasonMix:            reasonSegments,
		HasLanes:             len(rows) > 0,
		Lanes:                rows,
		Total:                data.Pagination.Total,
		TotalLanesText:       totalLanesText,
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
		return "Passed on rerun", "kpi-badge kpi-reason-rerun"
	}
	if lane.FailingOnMain && lane.Reason != kpi.ReasonFailingElsewhere {
		if lane.OtherPRsCount > 0 {
			return "Some errors on main + " + pluralize(lane.OtherPRsCount, "other PR", "other PRs"), kpiBadgeReasonOther
		}
		return "Some errors on main", kpiBadgeReasonOther
	}
	if lane.FailingOnMain && lane.OtherPRsCount > 0 {
		return "Failing on main + " + pluralize(lane.OtherPRsCount, "other PR", "other PRs"), kpiBadgeWarn
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
		return fmt.Sprintf("Recent (<%dh)", settledHours), kpiBadgeNeutral
	}
	if lane.OtherPRsCount > 0 {
		return fmt.Sprintf("Seen on %s (<3 PR threshold)", pluralize(lane.OtherPRsCount, "other PR", "other PRs")), kpiBadgeReasonOther
	}
	return "Only this PR so far", kpiBadgeReasonOther
}

func buildReasonSegment(key, label, tooltip string, count, total int, colorClass string) KPIReasonMixSegment {
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
		Tooltip:    tooltip,
		Count:      count,
		SharePct:   fmt.Sprintf("%.1f%%", share),
		WidthPct:   width,
		ColorClass: colorClass,
	}
}
