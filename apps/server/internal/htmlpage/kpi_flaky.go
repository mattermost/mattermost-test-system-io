package htmlpage

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
)

// KPIFlakyTestRow represents one flaky test row on GET /kpi/flaky.
type KPIFlakyTestRow struct {
	TestID         string
	Title          string
	File           string
	ZephyrKey      string
	ScopeMeta      string
	Repository     string
	RepoShort      string
	RepoHref       string
	Lanes          []KPILink
	FlakyCommits   string
	FlakeRateText  string
	FlakeRateClass string
	TotalRuns      int
	LastFlakyISO   string
	LastFlakyRel   string
	LastShortSHA   string
	LastCommitURL  string
	LastRunURL     string
}

// KPIFlakyPage is the minimal view model for GET /kpi/flaky.
type KPIFlakyPage struct {
	Title          string
	Site           SiteInfo
	Filters        KPIFilterBar
	OverviewHref   string
	PRsHref        string
	FlakyHref      string
	WindowDays     int
	Subtitle       string
	Total          int
	TotalTestsText string
	HasTests       bool
	Tests          []KPIFlakyTestRow
	Page           int
	TotalPages     int
	RowStart       int
	ShowEnd        int
	ShowPagination bool
	HasPrev        bool
	HasNext        bool
	PrevHref       string
	NextHref       string
}

// BuildKPIFlakyPage maps kpi.FlakyResponse into KPIFlakyPage.
func BuildKPIFlakyPage(
	site SiteInfo,
	opts kpi.FilterOptions,
	data kpi.FlakyResponse,
	now time.Time,
) KPIFlakyPage {
	filterBar := buildKPIFilterBar(data.Filters, opts, "/kpi/flaky", kpi.SortFlakyCommitsDesc, kpiFlakySortOptions)
	// Avoid leaking /kpi/flaky's default 14d window onto 30d-default Overview and PR E2E links.
	navState := filterBar.State
	if navState.Window == kpi.Window14d {
		navState.Window = ""
	}
	overviewHref := kpiHref("/kpi", navState, 1, "", false)
	prsHref := kpiHref("/kpi/prs", navState, 1, "", false)
	flakyHref := kpiHref("/kpi/flaky", filterBar.State, 1, "", false)

	rows := make([]KPIFlakyTestRow, 0, len(data.Tests))
	for _, t := range data.Tests {
		lastISO, lastRel := formatKPITimestamp(t.LastFlakyAt, now)
		laneLinks := make([]KPILink, 0, len(t.Lanes))
		for _, ln := range t.Lanes {
			laneLinks = append(laneLinks, KPILink{
				Label: ln,
				Href:  kpiScopeHref(navState, t.Repository, ln),
			})
		}

		var scopeParts []string
		if t.Framework != "" {
			scopeParts = append(scopeParts, t.Framework)
		}
		if t.Project != nil && *t.Project != "" {
			scopeParts = append(scopeParts, *t.Project)
		}
		if t.Platform != nil && *t.Platform != "" {
			scopeParts = append(scopeParts, *t.Platform)
		}

		rateClass := kpiBadgeWarn
		if t.FlakeRate >= 50 {
			rateClass = kpiBadgeBad
		} else if t.FlakeRate < 10 {
			rateClass = kpiBadgeNeutral
		}

		rows = append(rows, KPIFlakyTestRow{
			TestID:         t.TestID,
			Title:          t.FullTitle,
			File:           t.File,
			ZephyrKey:      derefString(t.ZephyrKey),
			ScopeMeta:      strings.Join(scopeParts, " · "),
			Repository:     t.Repository,
			RepoShort:      friendlyRepoName(t.Repository),
			RepoHref:       kpiScopeHref(navState, t.Repository, ""),
			Lanes:          laneLinks,
			FlakyCommits:   fmt.Sprintf("%d / %d", t.FlakyCommitCount, t.TotalCommitCount),
			FlakeRateText:  fmt.Sprintf("%.1f%%", t.FlakeRate),
			FlakeRateClass: rateClass,
			TotalRuns:      t.TotalRuns,
			LastFlakyISO:   lastISO,
			LastFlakyRel:   lastRel,
			LastShortSHA:   shortSHA(t.LastCommitSHA),
			LastCommitURL:  githubCommitURL(t.Repository, t.LastCommitSHA),
			LastRunURL:     safeRunHref(t.LastRunURL),
		})
	}

	windowDays := data.WindowDays
	if windowDays <= 0 {
		windowDays = 14
	}
	minCommits := data.MinFlakyCommits
	if minCommits <= 0 {
		minCommits = 2
	}
	pag := buildKPIPagination(filterBar.State, data.Pagination.Page, data.Pagination.Limit, data.Pagination.Total, data.Pagination.TotalPages)

	return KPIFlakyPage{
		Title:          "Flaky Tests · KPIs · Test System IO",
		Site:           site,
		Filters:        filterBar,
		OverviewHref:   overviewHref,
		PRsHref:        prsHref,
		FlakyHref:      flakyHref,
		WindowDays:     windowDays,
		Subtitle:       fmt.Sprintf("Last %dd · tests that passed and failed on the same commit across ≥%s", windowDays, pluralize(minCommits, "commit", "commits")),
		Total:          data.Pagination.Total,
		TotalTestsText: pluralize(data.Pagination.Total, "flaky test", "flaky tests"),
		HasTests:       len(rows) > 0,
		Tests:          rows,
		Page:           pag.Page,
		TotalPages:     pag.TotalPages,
		RowStart:       pag.RowStart,
		ShowEnd:        pag.ShowEnd,
		ShowPagination: pag.ShowPagination,
		HasPrev:        pag.HasPrev,
		HasNext:        pag.HasNext,
		PrevHref:       pag.PrevHref,
		NextHref:       pag.NextHref,
	}
}
