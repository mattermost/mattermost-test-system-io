package reports

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// In-progress home row.
type HomeLiveRun struct {
	GroupID        string
	Href           string
	Name           string
	RepositoryName string
	Branch         string
	ShortSHA       string
	GHRunID        string
	GHRunAttempt   string
	GHPRNumber     *int
	CreatedAt      string
	Pending        int
	Leased         int
	Orch           *HomeOrchView
}

// Terminal home row.
type HomeStaticRun struct {
	GroupID        string
	Href           string
	Name           string
	RepositoryName string
	Branch         string
	Commit         string
	ShortSHA       string
	Status         string
	GHRunID        string
	GHRunAttempt   string
	GHPRNumber     *int
	CreatedAt      string
	LastUploadAt   string
	Stats          *testStats
	Passed         int
	Failed         int
	Flaky          int
	Skipped        int
	Total          int
	WallClockMs    *int64
	RetestWallMs   *int64
	Orch           *HomeOrchView
	HasFailed      bool
	PassRate       *int
}

// Home page projection payload.
type HomePageData struct {
	Live   []HomeLiveRun
	Static []HomeStaticRun
	Total  int
	Limit  int
	Offset int
}

const homeLiveRunLimit = 20

// Load home page from report_groups projections.
func LoadHomePageData(ctx context.Context, pool *pgxpool.Pool, limit, offset int, repository, branchFilter string) (HomePageData, error) {
	if pool == nil {
		return HomePageData{}, fmt.Errorf("reports: nil pool")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	live, err := LoadHomeLiveRuns(ctx, pool, repository, branchFilter)
	if err != nil {
		return HomePageData{}, err
	}

	repoFilter := RepositoryFilter(repository)
	branchF := BranchFilter(branchFilter)
	staticWhere := strings.Builder{}
	staticWhere.WriteString(`(orchestration_json IS NULL OR orchestration_json->>'status' IS DISTINCT FROM 'in_progress')`)
	countArgs := []any{}
	repoFilter.appendSQL(&staticWhere, &countArgs, "repository")
	branchF.appendSQL(&staticWhere, &countArgs)

	var total int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM report_groups
		 WHERE `+staticWhere.String()+`
	`, countArgs...).Scan(&total); err != nil {
		return HomePageData{}, err
	}

	listArgs := append([]any{}, countArgs...)
	listArgs = append(listArgs, limit, offset)
	limitPH := fmt.Sprintf("$%d", len(countArgs)+1)
	offsetPH := fmt.Sprintf("$%d", len(countArgs)+2)

	rows, err := pool.Query(ctx, `
		SELECT `+groupProjectionSelectCols+`
		  FROM report_groups
		 WHERE `+staticWhere.String()+`
		 ORDER BY created_at DESC, id DESC
		 LIMIT `+limitPH+` OFFSET `+offsetPH+`
	`, listArgs...)
	if err != nil {
		return HomePageData{}, err
	}
	defer rows.Close()

	entries := make([]runEntry, 0, limit)
	for rows.Next() {
		row, err := scanGroupProjection(rows)
		if err != nil {
			return HomePageData{}, err
		}
		entries = append(entries, row.toRunEntry())
	}
	if err := rows.Err(); err != nil {
		return HomePageData{}, err
	}
	entries = mergeGroupedRunEntries(entries)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].CreatedAt > entries[j].CreatedAt
	})

	static := make([]HomeStaticRun, 0, len(entries))
	for _, e := range entries {
		static = append(static, toHomeStaticRun(e))
	}

	return HomePageData{
		Live:   live,
		Static: static,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

// Load live home rows.
func LoadHomeLiveRuns(ctx context.Context, pool *pgxpool.Pool, repository, branchFilter string) ([]HomeLiveRun, error) {
	repoFilter := RepositoryFilter(repository)
	branchF := BranchFilter(branchFilter)
	where := strings.Builder{}
	where.WriteString(`orchestration_json IS NOT NULL AND orchestration_json->>'status' = 'in_progress'`)
	args := []any{}
	repoFilter.appendSQL(&where, &args, "repository")
	branchF.appendSQL(&where, &args)
	args = append(args, homeLiveRunLimit)
	limitPH := fmt.Sprintf("$%d", len(args))

	rows, err := pool.Query(ctx, `
		SELECT `+groupProjectionSelectCols+`
		  FROM report_groups
		 WHERE `+where.String()+`
		 ORDER BY created_at DESC, id DESC
		 LIMIT `+limitPH+`
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]HomeLiveRun, 0)
	for rows.Next() {
		row, err := scanGroupProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, toHomeLiveRun(row))
	}
	return out, rows.Err()
}

func toHomeLiveRun(row groupProjectionRow) HomeLiveRun {
	g := row.groupDTO()
	e := toRunEntry(g, groupStats{})
	pending, leased := 0, 0
	if row.Orch != nil {
		pending = row.Orch.Counts.Pending
		leased = row.Orch.Counts.Leased
	}
	return HomeLiveRun{
		GroupID:        row.ID.String(),
		Href:           runPageHref(e.URLPath, e.GHRunID, e.GHRunAttempt),
		Name:           displayRunName(row.Name, row.RunGroup),
		RepositoryName: repositoryDisplayName(row.Repository),
		Branch:         stripRefPrefix(row.Branch),
		ShortSHA:       shortSHA(row.CommitSHA),
		GHRunID:        row.GHRunID,
		GHRunAttempt:   row.GHRunAttempt,
		GHPRNumber:     g.GHPRNumber,
		CreatedAt:      fmtTime(row.CreatedAt),
		Pending:        pending,
		Leased:         leased,
		Orch:           homeOrchView(row.Orch),
	}
}

// Orchestration fields for HTML rows.
type HomeOrchView struct {
	Status     string
	TotalUnits int
	Counts     HomeOrchCountsView
	Tests      *orchestrationTestCounts
	Durations  *HomeOrchDurationsView
}

type HomeOrchCountsView struct {
	Pending          int
	Leased           int
	CompletedPass    int
	CompletedFail    int
	CompletedSkipped int
	Abandoned        int
	RetestEligible   int
}

type HomeOrchDurationsView struct {
	FirstPassMs *int64
	RetestMs    *int64
	BeginAt     time.Time
	FirstTestAt *time.Time
	LastTestAt  *time.Time
}

func homeOrchView(orch *orchestrationSummary) *HomeOrchView {
	if orch == nil {
		return nil
	}
	v := &HomeOrchView{
		Status:     orch.Status,
		TotalUnits: orch.TotalUnits,
		Counts: HomeOrchCountsView{
			Pending:          orch.Counts.Pending,
			Leased:           orch.Counts.Leased,
			CompletedPass:    orch.Counts.CompletedPass,
			CompletedFail:    orch.Counts.CompletedFail,
			CompletedSkipped: orch.Counts.CompletedSkipped,
			Abandoned:        orch.Counts.Abandoned,
			RetestEligible:   orch.Counts.RetestEligible,
		},
		Tests: orch.Tests,
	}
	if orch.Durations != nil {
		d := orch.Durations
		v.Durations = &HomeOrchDurationsView{
			FirstPassMs: d.FirstPassMs,
			RetestMs:    d.RetestMs,
			BeginAt:     d.BeginAt,
			FirstTestAt: d.FirstTestAt,
			LastTestAt:  d.LastTestAt,
		}
	}
	return v
}

func toHomeStaticRun(e runEntry) HomeStaticRun {
	stats := homeDisplayStats(e.TestStats, e.Orchestration)
	v := HomeStaticRun{
		GroupID:        e.ReportID,
		Href:           runPageHref(e.URLPath, e.GHRunID, e.GHRunAttempt),
		Name:           displayRunName(e.Name, e.RunGroup),
		RepositoryName: repositoryDisplayName(e.Repository),
		Branch:         e.Branch,
		Commit:         e.Commit,
		ShortSHA:       e.ShortSHA,
		Status:         e.Status,
		GHRunID:        e.GHRunID,
		GHRunAttempt:   e.GHRunAttempt,
		GHPRNumber:     e.GHPRNumber,
		CreatedAt:      e.CreatedAt,
		LastUploadAt:   e.LastUploadAt,
		Stats:          stats,
		Orch:           homeOrchView(e.Orchestration),
	}
	if stats != nil && stats.Total > 0 {
		v.HasFailed = stats.Failed > 0
		v.Passed = stats.Passed
		v.Failed = stats.Failed
		v.Flaky = stats.Flaky
		v.Skipped = stats.Skipped
		v.Total = stats.Total
		v.WallClockMs = stats.WallClockMs
		v.RetestWallMs = stats.RetestWallClockMs
		if rate := passRatePercent(stats); rate >= 0 {
			v.PassRate = &rate
		}
	}
	return v
}

// Orchestration test rollups, else shard test_stats.
func homeDisplayStats(stats *testStats, orch *orchestrationSummary) *testStats {
	if orch != nil && orch.Tests != nil && orch.Tests.Total > 0 {
		t := orch.Tests
		out := &testStats{
			Total:   t.Total,
			Passed:  t.Passed,
			Failed:  t.Failed,
			Skipped: t.Skipped,
			Flaky:   t.Flaky,
		}
		if stats != nil {
			out.DurationMs = stats.DurationMs
			out.WallClockMs = stats.WallClockMs
			out.RetestWallClockMs = stats.RetestWallClockMs
		}
		return out
	}
	return stats
}
