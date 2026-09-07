package htmlpage

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
)

// Repo page view model.
type RepoPage struct {
	Site                 SiteInfo
	RepoSlug             string
	RepoDisplayName      string
	BranchFilterOptions  []reports.BranchFilterOption
	SelectedBranchFilter string
	LiveRuns             []HomeRunRow
	StaticRuns           []HomeRunRow
	Total                int
	Limit                int
	Offset               int
	Page                 int
	TotalPages           int
	RowStart             int
	ShowEnd              int
	Title                string
}

// Build repo page view model.
func BuildRepoPage(ctx context.Context, pool *pgxpool.Pool, site SiteInfo, repoSlug string, limit, offset int, branchFilter string) (RepoPage, error) {
	if limit <= 0 {
		limit = 50
	}
	branchOptions, err := reports.LoadBranchFilterOptions(ctx, pool, repoSlug)
	if err != nil {
		return RepoPage{}, err
	}
	branchFilter = reports.ResolveBranchFilter(branchFilter, branchOptions)
	data, err := reports.LoadHomePageData(ctx, pool, limit, offset, repoSlug, branchFilter)
	if err != nil {
		return RepoPage{}, err
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

	displayName := repoSlug
	if len(staticRuns) > 0 && staticRuns[0].RepositoryName != "" {
		displayName = staticRuns[0].RepositoryName
	} else if len(liveRuns) > 0 && liveRuns[0].RepositoryName != "" {
		displayName = liveRuns[0].RepositoryName
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

	return RepoPage{
		Site:                 site,
		RepoSlug:             repoSlug,
		RepoDisplayName:      displayName,
		BranchFilterOptions:  branchOptions,
		SelectedBranchFilter: branchFilter,
		LiveRuns:             liveRuns,
		StaticRuns:           staticRuns,
		Total:                data.Total,
		Limit:                data.Limit,
		Offset:               data.Offset,
		Page:                 currentPage,
		TotalPages:           totalPages,
		RowStart:             rowStart,
		ShowEnd:              showEnd,
		Title:                displayName + " · Test System IO",
	}, nil
}
