package htmlpage

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
)

// Home page view model.
type HomePage struct {
	Site               SiteInfo
	LiveRuns           []HomeRunRow
	StaticRuns         []HomeRunRow
	Repositories         []reports.RepositoryOption
	SelectedRepository   string
	BranchFilterOptions   []reports.BranchFilterOption
	SelectedBranchFilter string
	Total                int
	Limit      int
	Offset     int
	Page       int
	TotalPages int
	RowStart   int
	ShowEnd    int
	Title      string
}

// Build home page view model.
func BuildHomePage(ctx context.Context, pool *pgxpool.Pool, site SiteInfo, limit, offset int, repository, branchFilter string) (HomePage, error) {
	if limit <= 0 {
		limit = 50
	}
	repositories, err := reports.LoadReportRepositories(ctx, pool)
	if err != nil {
		return HomePage{}, err
	}
	branchOptions, err := reports.LoadBranchFilterOptions(ctx, pool, repository)
	if err != nil {
		return HomePage{}, err
	}
	branchFilter = reports.ResolveBranchFilter(branchFilter, branchOptions)
	data, err := reports.LoadHomePageData(ctx, pool, limit, offset, repository, branchFilter)
	if err != nil {
		return HomePage{}, err
	}

	liveRuns := make([]HomeRunRow, 0, len(data.Live))
	for i, r := range data.Live {
		row := mapLiveRun(r)
		row.RowNum = i + 1
		liveRuns = append(liveRuns, row)
	}

	staticRuns := make([]HomeRunRow, 0, len(data.Static))
	for i, r := range data.Static {
		row := mapStaticRun(r)
		row.RowNum = data.Offset + i + 1
		staticRuns = append(staticRuns, row)
	}

	currentPage := 1
	if data.Limit > 0 {
		currentPage = data.Offset/data.Limit + 1
	}
	totalPages := 1
	if data.Total > 0 && data.Limit > 0 {
		totalPages = (data.Total + data.Limit - 1) / data.Limit
	}
	showEnd := data.Offset + len(staticRuns)
	rowStart := data.Offset + 1
	if len(staticRuns) == 0 {
		rowStart = 0
		showEnd = 0
	}
	return HomePage{
		Site:               site,
		LiveRuns:           liveRuns,
		StaticRuns:         staticRuns,
		Repositories:         repositories,
		SelectedRepository:   repository,
		BranchFilterOptions:  branchOptions,
		SelectedBranchFilter: branchFilter,
		Total:                data.Total,
		Limit:      data.Limit,
		Offset:     data.Offset,
		Page:       currentPage,
		TotalPages: totalPages,
		RowStart:   rowStart,
		ShowEnd:    showEnd,
		Title:      "Test System IO",
	}, nil
}

// Build live row models.
func BuildHomeLiveRows(ctx context.Context, pool *pgxpool.Pool, repository, branchFilter string) ([]HomeRunRow, error) {
	live, err := reports.LoadHomeLiveRuns(ctx, pool, repository, branchFilter)
	if err != nil {
		return nil, err
	}
	runs := make([]HomeRunRow, 0, len(live))
	for i, r := range live {
		row := mapLiveRun(r)
		row.RowNum = i + 1
		runs = append(runs, row)
	}
	return runs, nil
}
