package kpi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// QueryFlaky computes GET /api/v1/kpi/flaky strictly from pre-aggregated rollup tables.
func (s *Store) QueryFlaky(ctx context.Context, f FilterParams) (FlakyResponse, error) {
	if s == nil || s.Pool == nil {
		return FlakyResponse{}, errors.New("kpi store pool is nil")
	}
	if err := validatePaginationParams(f); err != nil {
		return FlakyResponse{}, err
	}
	return cachedQuery(ctx, s, filterCacheKey("flaky", f, ""), func(ctx context.Context) (FlakyResponse, error) {
		return s.queryFlakyUncached(ctx, f)
	})
}

func (s *Store) queryFlakyUncached(ctx context.Context, f FilterParams) (FlakyResponse, error) {

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return FlakyResponse{}, fmt.Errorf("begin flaky read tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cov, err := s.queryLaneCoverageQ(ctx, tx, f, "", true, nil)
	if err != nil {
		return FlakyResponse{}, err
	}

	curTotal, curByApp, err := s.queryFlakyCountsByAppQ(ctx, tx, f, f.CurFromDate, f.CurToDate)
	if err != nil {
		return FlakyResponse{}, err
	}
	priorHasData, _, priorByApp, priorHasDataByApp, err := s.queryPriorFlakyCountsByAppQ(ctx, tx, f, f.PriorFromDate, f.PriorToDate)
	if err != nil {
		return FlakyResponse{}, err
	}

	appSet := map[string]struct{}{}
	for app := range curByApp {
		appSet[app] = struct{}{}
	}
	if priorHasData {
		for app := range priorByApp {
			appSet[app] = struct{}{}
		}
	}
	byApp := make([]AppFlakyCount, 0, len(appSet))
	for app := range appSet {
		c := curByApp[app]
		var pPtr, dPtr *int
		if priorHasDataByApp[app] {
			p := priorByApp[app]
			pPtr = &p
			d := c - p
			dPtr = &d
		}
		byApp = append(byApp, AppFlakyCount{
			App:        app,
			Count:      c,
			PriorCount: pPtr,
			Delta:      dPtr,
		})
	}
	sort.Slice(byApp, func(i, j int) bool {
		if byApp[i].Count != byApp[j].Count {
			return byApp[i].Count > byApp[j].Count
		}
		return byApp[i].App < byApp[j].App
	})

	tests, err := s.queryFlakyTestRows(ctx, tx, f)
	if err != nil {
		return FlakyResponse{}, err
	}

	totalPages := 0
	if curTotal > 0 {
		totalPages = int(math.Ceil(float64(curTotal) / float64(f.Limit)))
	}

	return FlakyResponse{
		Filters:         f,
		WindowDays:      f.WindowDays,
		MinFlakyCommits: 2,
		TotalFlakyTests: curTotal,
		ByApp:           byApp,
		LaneCoverage:    cov,
		Pagination: Pagination{
			Page:       f.Page,
			Limit:      f.Limit,
			Total:      curTotal,
			TotalPages: totalPages,
		},
		Tests: tests,
	}, nil
}

func appendCommitFilters(b *sqlFilterBuilder, alias string, f FilterParams) string {
	var sb strings.Builder
	sb.WriteString(b.appendCommonFilters(alias, f, true))
	if f.Platform != "" {
		ph := b.add(f.Platform)
		fmt.Fprintf(&sb, " AND %s.platform = %s", alias, ph)
	}
	return sb.String()
}

func (s *Store) queryFlakyCountsByApp(
	ctx context.Context,
	f FilterParams,
	fromDate, toDate time.Time,
) (int, map[string]int, error) {
	return s.queryFlakyCountsByAppQ(ctx, s.Pool, f, fromDate, toDate)
}

func (s *Store) queryFlakyCountsByAppQ(
	ctx context.Context,
	q dbQuerier,
	f FilterParams,
	fromDate, toDate time.Time,
) (int, map[string]int, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(fromDate)
	toPH := b.add(toDate)
	var where strings.Builder
	fmt.Fprintf(&where, "c.metric_date >= %s AND c.metric_date <= %s", fromPH, toPH)
	where.WriteString(appendCommitFilters(b, "c", f))

	rows, err := q.Query(ctx, `
		WITH commit_agg AS (
			SELECT
				c.repository,
				c.commit_sha,
				c.test_id,
				c.project,
				c.platform,
				MAX(c.app) AS app,
				(SUM(c.pass_count) > 0 AND SUM(c.fail_count) > 0) AS is_mixed
			  FROM kpi_test_commit_outcomes c
			 WHERE `+where.String()+`
			 GROUP BY c.repository, c.commit_sha, c.test_id, c.project, c.platform
		),
		flaky_tests AS (
			SELECT
				test_id,
				project,
				platform,
				MAX(app) AS app
			  FROM commit_agg
			 WHERE is_mixed
			 GROUP BY test_id, project, platform
			HAVING COUNT(*) >= 2
		)
		SELECT app, COUNT(*)::integer
		  FROM flaky_tests
		 GROUP BY app
	`, b.args...)
	if err != nil {
		return 0, nil, fmt.Errorf("query flaky counts by app: %w", err)
	}
	defer rows.Close()

	byApp := map[string]int{}
	total := 0
	for rows.Next() {
		var app string
		var cnt int
		if err := rows.Scan(&app, &cnt); err != nil {
			return 0, nil, fmt.Errorf("scan flaky count by app: %w", err)
		}
		byApp[app] = cnt
		total += cnt
	}
	return total, byApp, rows.Err()
}

func (s *Store) queryPriorFlakyCountsByApp(
	ctx context.Context,
	f FilterParams,
	fromDate, toDate time.Time,
) (bool, int, map[string]int, map[string]bool, error) {
	return s.queryPriorFlakyCountsByAppQ(ctx, s.Pool, f, fromDate, toDate)
}

func (s *Store) queryPriorFlakyCountsByAppQ(
	ctx context.Context,
	q dbQuerier,
	f FilterParams,
	fromDate, toDate time.Time,
) (bool, int, map[string]int, map[string]bool, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(fromDate)
	toPH := b.add(toDate)
	var where strings.Builder
	fmt.Fprintf(&where, "c.metric_date >= %s AND c.metric_date <= %s", fromPH, toPH)
	where.WriteString(appendCommitFilters(b, "c", f))

	rows, err := q.Query(ctx, `
		SELECT DISTINCT c.app
		  FROM kpi_test_commit_outcomes c
		 WHERE `+where.String(),
		b.args...,
	)
	if err != nil {
		return false, 0, nil, nil, fmt.Errorf("check prior flaky baseline: %w", err)
	}
	defer rows.Close()

	priorHasDataByApp := map[string]bool{}
	for rows.Next() {
		var app string
		if err := rows.Scan(&app); err != nil {
			return false, 0, nil, nil, fmt.Errorf("scan prior flaky baseline app: %w", err)
		}
		priorHasDataByApp[app] = true
	}
	if err := rows.Err(); err != nil {
		return false, 0, nil, nil, fmt.Errorf("iterate prior flaky baseline: %w", err)
	}
	if len(priorHasDataByApp) == 0 {
		return false, 0, map[string]int{}, priorHasDataByApp, nil
	}

	total, byApp, err := s.queryFlakyCountsByAppQ(ctx, q, f, fromDate, toDate)
	if err != nil {
		return false, 0, nil, nil, err
	}
	return true, total, byApp, priorHasDataByApp, nil
}

func (s *Store) queryFlakyTestRows(ctx context.Context, q dbQuerier, f FilterParams) ([]FlakyTestRow, error) {
	offset, ok := safePageOffset(f.Page, f.Limit)
	if !ok {
		return []FlakyTestRow{}, nil
	}

	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	var where strings.Builder
	fmt.Fprintf(&where, "c.metric_date >= %s AND c.metric_date <= %s", fromPH, toPH)
	where.WriteString(appendCommitFilters(b, "c", f))

	if err := validatePaginationParams(f); err != nil {
		return nil, err
	}
	orderBy := flakyOrderByClause(f.Sort)
	limitPH := b.add(f.Limit)
	offsetPH := b.add(offset)

	rows, err := q.Query(ctx, `
		WITH commit_agg AS (
			SELECT
				c.repository,
				c.commit_sha,
				c.test_id,
				c.project,
				c.platform,
				MAX(c.app) AS app,
				MAX(c.last_seen_at) AS last_seen_at,
				SUM(c.run_count)::integer AS commit_runs,
				(SUM(c.pass_count) > 0 AND SUM(c.fail_count) > 0) AS is_mixed,
				ARRAY_AGG(DISTINCT c.lane) AS commit_lanes,
				COALESCE(ARRAY_AGG(DISTINCT c.lane) FILTER (WHERE c.fail_count > 0), '{}'::text[]) AS fail_lanes
			  FROM kpi_test_commit_outcomes c
			 WHERE `+where.String()+`
			 GROUP BY c.repository, c.commit_sha, c.test_id, c.project, c.platform
		),
		test_agg AS (
			SELECT
				ca.test_id,
				ca.project,
				ca.platform,
				MAX(ca.repository) AS repository,
				MAX(ca.app) AS app,
				COUNT(*) FILTER (WHERE ca.is_mixed)::integer AS flaky_commit_count,
				COUNT(*) FILTER (WHERE ca.commit_runs > 0 OR ca.is_mixed)::integer AS total_commit_count,
				COALESCE(SUM(ca.commit_runs), 0)::integer AS total_runs,
				MAX(ca.last_seen_at) FILTER (WHERE ca.is_mixed) AS last_flaky_at,
				(ARRAY_AGG(ca.commit_sha ORDER BY ca.last_seen_at DESC, ca.commit_sha ASC)
					FILTER (WHERE ca.is_mixed))[1] AS last_commit_sha
			  FROM commit_agg ca
			 GROUP BY ca.test_id, ca.project, ca.platform
			HAVING COUNT(*) FILTER (WHERE ca.is_mixed) >= 2
		),
		scored AS (
			SELECT
				ta.test_id,
				t.test_key,
				t.zephyr_key,
				t.file,
				t.full_title,
				ta.repository,
				NULLIF(ta.app, '') AS app,
				t.framework,
				ta.project,
				ta.platform,
				ta.flaky_commit_count,
				ta.total_commit_count,
				ta.total_runs,
				CASE
					WHEN ta.total_commit_count > 0
					THEN (ta.flaky_commit_count::double precision * 100.0) / ta.total_commit_count::double precision
					ELSE 0.0
				END AS flake_rate,
				ta.last_flaky_at,
				ta.last_commit_sha
			  FROM test_agg ta
			  JOIN tests t ON t.id = ta.test_id
		),
		paged AS (
			SELECT
				ROW_NUMBER() OVER (ORDER BY `+orderBy+`) AS rn,
				s.*
			  FROM scored s
			 ORDER BY `+orderBy+`
			 LIMIT `+limitPH+` OFFSET `+offsetPH+`
		),
		test_lanes AS (
			SELECT
				ca.test_id,
				ca.project,
				ca.platform,
				COALESCE(ARRAY_AGG(DISTINCT l.lane ORDER BY l.lane), '{}'::text[]) AS lanes
			  FROM commit_agg ca
			  JOIN paged p
			    ON p.test_id = ca.test_id AND p.project = ca.project AND p.platform = ca.platform
			 CROSS JOIN LATERAL UNNEST(ca.commit_lanes) AS l(lane)
			 GROUP BY ca.test_id, ca.project, ca.platform
		)
		SELECT
			p.test_id::text,
			p.test_key,
			p.zephyr_key,
			p.file,
			p.full_title,
			p.repository,
			p.app,
			p.framework,
			NULLIF(p.project, ''),
			NULLIF(p.platform, ''),
			p.flaky_commit_count,
			p.total_commit_count,
			p.total_runs,
			p.flake_rate,
			p.last_flaky_at,
			p.last_commit_sha,
			COALESCE(tl.lanes, '{}'::text[]),
			COALESCE((
				SELECT ARRAY_AGG(DISTINCT fl ORDER BY fl)
				  FROM commit_agg ca2
				 CROSS JOIN LATERAL UNNEST(ca2.fail_lanes) AS fl
				 WHERE ca2.test_id = p.test_id AND ca2.project = p.project AND ca2.platform = p.platform
				   AND ca2.is_mixed AND ca2.commit_sha = p.last_commit_sha
			), '{}'::text[])
		  FROM paged p
		  LEFT JOIN test_lanes tl
		    ON tl.test_id = p.test_id AND tl.project = p.project AND tl.platform = p.platform
		 ORDER BY p.rn ASC`,
		b.args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query flaky test rows: %w", err)
	}
	defer rows.Close()

	out := []FlakyTestRow{}
	lastFlakyLanes := [][]string{}
	for rows.Next() {
		var r FlakyTestRow
		var lastLanes []string
		if err := rows.Scan(
			&r.TestID, &r.TestKey, &r.ZephyrKey, &r.File, &r.FullTitle,
			&r.Repository, &r.App, &r.Framework, &r.Project, &r.Platform,
			&r.FlakyCommitCount, &r.TotalCommitCount, &r.TotalRuns,
			&r.FlakeRate, &r.LastFlakyAt, &r.LastCommitSHA, &r.Lanes, &lastLanes,
		); err != nil {
			return nil, fmt.Errorf("scan flaky test row: %w", err)
		}
		r.Lanes = ensureNonNilSlice(r.Lanes)
		out = append(out, r)
		lastFlakyLanes = append(lastFlakyLanes, ensureNonNilSlice(lastLanes))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.populateFlakyLastRunURLs(ctx, q, f, out, lastFlakyLanes); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) populateFlakyLastRunURLs(
	ctx context.Context,
	q dbQuerier,
	f FilterParams,
	rows []FlakyTestRow,
	lastFlakyLanes [][]string,
) error {
	if len(rows) == 0 {
		return nil
	}
	type repoSHA struct{ repo, sha string }
	seen := make(map[repoSHA]struct{}, len(rows))
	repos := make([]string, 0, len(rows))
	shas := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Repository == "" || r.LastCommitSHA == "" {
			continue
		}
		k := repoSHA{repo: r.Repository, sha: r.LastCommitSHA}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		repos = append(repos, r.Repository)
		shas = append(shas, r.LastCommitSHA)
	}
	if len(repos) == 0 {
		return nil
	}

	b := &sqlFilterBuilder{}
	reposPH := b.add(repos)
	shasPH := b.add(shas)
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	stageFilter := ""
	if f.Stage != "" {
		stageFilter = " AND rg.stage = " + b.add(f.Stage)
	}
	dbRows, err := q.Query(ctx, `
		SELECT DISTINCT ON (rg.repository, rg.commit_sha, rg.name)
		       rg.repository, rg.commit_sha, COALESCE(rg.branch, ''), rg.name,
		       COALESCE(rg.run_group, ''), COALESCE(rg.gh_run_id, ''), COALESCE(rg.gh_run_attempt, '')
		  FROM UNNEST(`+reposPH+`::text[], `+shasPH+`::text[]) AS u(repo, sha)
		  JOIN report_groups rg
		    ON rg.repository = u.repo AND rg.commit_sha = u.sha
		 WHERE (rg.created_at AT TIME ZONE 'UTC')::date >= `+fromPH+`
		   AND (rg.created_at AT TIME ZONE 'UTC')::date <= `+toPH+stageFilter+`
		 ORDER BY rg.repository, rg.commit_sha, rg.name, rg.created_at DESC
	`, b.args...)
	if err != nil {
		return fmt.Errorf("query flaky last run urls: %w", err)
	}
	defer dbRows.Close()

	type runEntry struct {
		lane string
		url  string
	}
	byKey := map[repoSHA][]runEntry{}
	for dbRows.Next() {
		var repo, sha, branch, name, runGroup, ghRunID, ghRunAttempt string
		if err := dbRows.Scan(&repo, &sha, &branch, &name, &runGroup, &ghRunID, &ghRunAttempt); err != nil {
			return fmt.Errorf("scan flaky last run url: %w", err)
		}
		u := buildRunURL(repo, branch, sha, name, runGroup, ghRunID, ghRunAttempt)
		if u != "/" {
			k := repoSHA{repo: repo, sha: sha}
			byKey[k] = append(byKey[k], runEntry{lane: name, url: u})
		}
	}
	if err := dbRows.Err(); err != nil {
		return fmt.Errorf("iterate flaky last run urls: %w", err)
	}

	for i := range rows {
		entries := byKey[repoSHA{repo: rows[i].Repository, sha: rows[i].LastCommitSHA}]
		if len(entries) == 0 {
			continue
		}
		matchLanes := rows[i].Lanes
		if i < len(lastFlakyLanes) && len(lastFlakyLanes[i]) > 0 {
			matchLanes = lastFlakyLanes[i]
		}
		laneSet := make(map[string]struct{}, len(matchLanes))
		for _, ln := range matchLanes {
			laneSet[ln] = struct{}{}
		}
		for _, e := range entries {
			if _, ok := laneSet[e.lane]; ok {
				rows[i].LastRunURL = e.url
				break
			}
		}
	}
	return nil
}

func safePageOffset(page, limit int) (int, bool) {
	if page < 1 || limit < 1 || page-1 > math.MaxInt/limit {
		return 0, false
	}
	return (page - 1) * limit, true
}

func flakyOrderByClause(sortBy string) string {
	switch sortBy {
	case SortFlakeRateDesc:
		return "flake_rate DESC, flaky_commit_count DESC, total_runs DESC, test_id ASC, platform ASC, project ASC"
	case SortTotalRunsDesc:
		return "total_runs DESC, flaky_commit_count DESC, test_id ASC, platform ASC, project ASC"
	case SortLastFlakyDesc:
		return "last_flaky_at DESC, flaky_commit_count DESC, test_id ASC, platform ASC, project ASC"
	case SortNameAsc:
		return "full_title ASC, platform ASC, project ASC, test_id ASC"
	default: // SortFlakyCommitsDesc
		return "flaky_commit_count DESC, flake_rate DESC, total_runs DESC, test_id ASC, platform ASC, project ASC"
	}
}
