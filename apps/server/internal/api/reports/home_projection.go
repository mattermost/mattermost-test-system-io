package reports

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

// HomeLiveRun is an in-progress home row.
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

// HomeStaticRun is a terminal home row.
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

// HomePageData is the home page projection payload.
type HomePageData struct {
	Live   []HomeLiveRun
	Static []HomeStaticRun
	Total  int
	Limit  int
	Offset int
}

const homeLiveRunLimit = 20

// runKeySQL mirrors runEntryMergeKey: groups sharing a run_group merge into one run.
const runKeySQL = `CASE WHEN COALESCE(run_group, '') = '' THEN id::text
	ELSE concat_ws(E'\x1f', COALESCE(gh_run_id, ''), COALESCE(gh_run_attempt, ''),
		regexp_replace(COALESCE(branch, ''), '^refs/(heads|tags)/', ''), COALESCE(commit_sha, ''), run_group)
	END`

// LoadHomePageData loads the home page from report_groups projections using legacy rules.
func LoadHomePageData(ctx context.Context, pool *pgxpool.Pool, limit, offset int, repository, branchFilter string) (HomePageData, error) {
	return LoadHomePageDataWithRule(ctx, pool, limit, offset, repository, branchFilter, teststatus.RuleLegacy)
}

// LoadHomePageDataWithRule loads the home page from report_groups projections using rule.
func LoadHomePageDataWithRule(ctx context.Context, pool *pgxpool.Pool, limit, offset int, repository, branchFilter string, rule teststatus.Rule) (HomePageData, error) {
	if pool == nil {
		return HomePageData{}, errors.New("reports: nil pool")
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

	live, err := LoadHomeLiveRunsWithRule(ctx, pool, repository, branchFilter, rule)
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

	// Page over runs, not groups, so one run's groups never split across pages.
	runsCTE := `
		WITH runs AS (
			SELECT id, created_at, ` + runKeySQL + ` AS run_key
			  FROM report_groups
			 WHERE ` + staticWhere.String() + `
		)`

	var total int
	if err := pool.QueryRow(ctx, runsCTE+`
		SELECT count(DISTINCT run_key) FROM runs
	`, countArgs...).Scan(&total); err != nil {
		return HomePageData{}, err
	}

	listArgs := append([]any{}, countArgs...)
	listArgs = append(listArgs, limit, offset)
	limitPH := fmt.Sprintf("$%d", len(countArgs)+1)
	offsetPH := fmt.Sprintf("$%d", len(countArgs)+2)

	rows, err := pool.Query(ctx, runsCTE+`,
		paged AS (
			SELECT run_key, max(created_at) AS run_at
			  FROM runs
			 GROUP BY run_key
			 ORDER BY run_at DESC, run_key DESC
			 LIMIT `+limitPH+` OFFSET `+offsetPH+`
		)
		SELECT `+groupProjectionSelectCols+`
		  FROM report_groups
		 WHERE id IN (SELECT runs.id FROM runs JOIN paged USING (run_key))
		 ORDER BY created_at DESC, id DESC
	`, listArgs...)
	if err != nil {
		return HomePageData{}, err
	}
	defer rows.Close()

	projRows := make([]groupProjectionRow, 0, limit)
	for rows.Next() {
		row, err := scanGroupProjection(rows)
		if err != nil {
			return HomePageData{}, err
		}
		projRows = append(projRows, row)
	}
	if err := rows.Err(); err != nil {
		return HomePageData{}, err
	}
	if err := overlayOrchestrationCountsForProjectionRows(ctx, pool, projRows, rule); err != nil {
		return HomePageData{}, err
	}
	entries := make([]runEntry, 0, len(projRows))
	for _, row := range projRows {
		entries = append(entries, row.toRunEntry())
	}
	entries = mergeGroupedRunEntries(entries)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].CreatedAt > entries[j].CreatedAt
	})

	static := make([]HomeStaticRun, 0, len(entries))
	for _, e := range entries {
		static = append(static, toHomeStaticRun(e, rule))
	}

	return HomePageData{
		Live:   live,
		Static: static,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

// LoadHomeLiveRuns loads live home rows using legacy rules.
func LoadHomeLiveRuns(ctx context.Context, pool *pgxpool.Pool, repository, branchFilter string) ([]HomeLiveRun, error) {
	return LoadHomeLiveRunsWithRule(ctx, pool, repository, branchFilter, teststatus.RuleLegacy)
}

// LoadHomeLiveRunsWithRule loads live home rows using rule.
func LoadHomeLiveRunsWithRule(ctx context.Context, pool *pgxpool.Pool, repository, branchFilter string, rule teststatus.Rule) ([]HomeLiveRun, error) {
	if pool == nil {
		return nil, errors.New("reports: nil pool")
	}
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

	projRows := make([]groupProjectionRow, 0)
	for rows.Next() {
		row, err := scanGroupProjection(rows)
		if err != nil {
			return nil, err
		}
		projRows = append(projRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := overlayOrchestrationCountsForProjectionRows(ctx, pool, projRows, rule); err != nil {
		return nil, err
	}
	out := make([]HomeLiveRun, 0, len(projRows))
	for _, row := range projRows {
		out = append(out, toHomeLiveRun(row))
	}
	return out, nil
}

// overlayOrchestrationCountsForProjectionRows recomputes orchestration test counts
// from attempts under the active rule so cached orchestration_json (and shardless
// test_stats_json) never serve stale counts across TSIO_STATUS_RULE switches.
func overlayOrchestrationCountsForProjectionRows(
	ctx context.Context,
	pool *pgxpool.Pool,
	projRows []groupProjectionRow,
	rule teststatus.Rule,
) error {
	groupIDs := make([]uuid.UUID, 0, len(projRows))
	for _, r := range projRows {
		if r.Orch != nil {
			groupIDs = append(groupIDs, r.ID)
		}
	}
	if len(groupIDs) == 0 {
		return nil
	}

	cte := orchLegacyCountsCTE
	aggAlias := "legacy_agg"
	if teststatus.NormalizeRule(rule) == teststatus.RuleShared {
		cte = orchSharedCountsCTE
		aggAlias = "shared_agg"
	}
	query := `
		WITH target_groups AS (
			SELECT DISTINCT ON (g.id)
			       g.id AS group_id,
			       o.id AS run_id
			  FROM report_groups g
			  JOIN orchestration_runs o
			    ON o.repository = g.repository
			   AND o.commit_sha = g.commit_sha
			   AND o.gh_run_id = g.gh_run_id
			   AND o.name = g.name
			   AND o.gh_run_attempt = g.gh_run_attempt
			 WHERE g.id = ANY($1)
			 ORDER BY g.id, o.id
		),
		shard_cases AS (
			SELECT report_group_id AS group_id,
			       COALESCE(SUM(total_cases), 0)::int AS total_cases
			  FROM reports
			 WHERE report_group_id = ANY($1)
			 GROUP BY report_group_id
		),
		target_run_ids AS (
			SELECT DISTINCT run_id FROM target_groups
		),
		` + cte + `
		SELECT
			tg.group_id,
			(COALESCE(sc.total_cases, 0) > 0) AS has_shard_cases,
			COALESCE(agg.passed, 0),
			COALESCE(agg.failed, 0),
			COALESCE(agg.flaky, 0),
			COALESCE(agg.skipped, 0),
			COALESCE(agg.total, 0)
		  FROM target_groups tg
		  LEFT JOIN shard_cases sc ON sc.group_id = tg.group_id
		  LEFT JOIN ` + aggAlias + ` agg ON agg.run_id = tg.run_id
	`
	rows, err := pool.Query(ctx, query, groupIDs)
	if err != nil {
		return err
	}
	defer rows.Close()

	type overlay struct {
		hasShardCases bool
		tests         *orchestrationTestCounts
	}
	byGroup := make(map[uuid.UUID]overlay, len(groupIDs))
	for rows.Next() {
		var gid uuid.UUID
		var hasShard bool
		var c orchestrationTestCounts
		if err := rows.Scan(&gid, &hasShard, &c.Passed, &c.Failed, &c.Flaky, &c.Skipped, &c.Total); err != nil {
			return err
		}
		var tp *orchestrationTestCounts
		if c.Total > 0 {
			tp = &c
		}
		byGroup[gid] = overlay{hasShardCases: hasShard, tests: tp}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range projRows {
		if projRows[i].Orch == nil {
			continue
		}
		ov, ok := byGroup[projRows[i].ID]
		if !ok {
			continue
		}
		projRows[i].Orch.Tests = ov.tests
		if !ov.hasShardCases {
			if ov.tests != nil {
				if projRows[i].Stats == nil {
					projRows[i].Stats = &testStats{}
				}
				projRows[i].Stats.Total = ov.tests.Total
				projRows[i].Stats.Passed = ov.tests.Passed
				projRows[i].Stats.Failed = ov.tests.Failed
				projRows[i].Stats.Skipped = ov.tests.Skipped
				projRows[i].Stats.Flaky = ov.tests.Flaky
			} else if projRows[i].Stats != nil {
				projRows[i].Stats.Total = 0
				projRows[i].Stats.Passed = 0
				projRows[i].Stats.Failed = 0
				projRows[i].Stats.Skipped = 0
				projRows[i].Stats.Flaky = 0
			}
		}
	}
	return nil
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

// HomeOrchTestCountsView holds orchestration test counts for HTML rows.
type HomeOrchTestCountsView = orchestrationTestCounts

// HomeOrchView holds orchestration fields for HTML rows.
type HomeOrchView struct {
	Status     string
	TotalUnits int
	Counts     HomeOrchCountsView
	Tests      *orchestrationTestCounts
	Durations  *HomeOrchDurationsView
}

// HomeOrchCountsView holds orchestration unit counts.
type HomeOrchCountsView struct {
	Pending          int
	Leased           int
	CompletedPass    int
	CompletedFail    int
	CompletedSkipped int
	Abandoned        int
	RetestEligible   int
}

// HomeOrchDurationsView holds orchestration durations.
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

func toHomeStaticRun(e runEntry, rule teststatus.Rule) HomeStaticRun {
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
		if rate := passRatePercent(stats, rule); rate >= 0 {
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
