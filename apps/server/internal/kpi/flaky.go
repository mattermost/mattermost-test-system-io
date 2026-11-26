package kpi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// QueryFlaky computes GET /api/v1/kpi/flaky strictly from pre-aggregated rollup tables.
func (s *Store) QueryFlaky(ctx context.Context, f FilterParams) (FlakyResponse, error) {
	if s == nil || s.Pool == nil {
		return FlakyResponse{}, errors.New("kpi store pool is nil")
	}

	cov, err := s.queryLaneCoverage(ctx, f, "", true, nil)
	if err != nil {
		return FlakyResponse{}, err
	}

	curTotal, curByApp, err := s.queryFlakyCountsByApp(ctx, f, f.CurFromDate, f.CurToDate)
	if err != nil {
		return FlakyResponse{}, err
	}
	priorHasData, _, priorByApp, priorHasDataByApp, err := s.queryPriorFlakyCountsByApp(ctx, f, f.PriorFromDate, f.PriorToDate)
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

	tests, err := s.queryFlakyTestRows(ctx, f)
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
	b := &sqlFilterBuilder{}
	fromPH := b.add(fromDate)
	toPH := b.add(toDate)
	var where strings.Builder
	fmt.Fprintf(&where, "c.metric_date >= %s AND c.metric_date <= %s", fromPH, toPH)
	where.WriteString(appendCommitFilters(b, "c", f))

	rows, err := s.Pool.Query(ctx, `
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
	b := &sqlFilterBuilder{}
	fromPH := b.add(fromDate)
	toPH := b.add(toDate)
	var where strings.Builder
	fmt.Fprintf(&where, "c.metric_date >= %s AND c.metric_date <= %s", fromPH, toPH)
	where.WriteString(appendCommitFilters(b, "c", f))

	rows, err := s.Pool.Query(ctx, `
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

	total, byApp, err := s.queryFlakyCountsByApp(ctx, f, fromDate, toDate)
	if err != nil {
		return false, 0, nil, nil, err
	}
	return true, total, byApp, priorHasDataByApp, nil
}

func (s *Store) queryFlakyTestRows(ctx context.Context, f FilterParams) ([]FlakyTestRow, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	var where strings.Builder
	fmt.Fprintf(&where, "c.metric_date >= %s AND c.metric_date <= %s", fromPH, toPH)
	where.WriteString(appendCommitFilters(b, "c", f))

	orderBy := flakyOrderByClause(f.Sort)
	limitPH := b.add(f.Limit)
	offsetPH := b.add((f.Page - 1) * f.Limit)

	rows, err := s.Pool.Query(ctx, `
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
				ARRAY_AGG(DISTINCT c.lane) AS commit_lanes
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
		test_lanes AS (
			SELECT
				ca.test_id,
				ca.project,
				ca.platform,
				COALESCE(ARRAY_AGG(DISTINCT l.lane ORDER BY l.lane), '{}'::text[]) AS lanes
			  FROM commit_agg ca
			 CROSS JOIN LATERAL UNNEST(ca.commit_lanes) AS l(lane)
			 GROUP BY ca.test_id, ca.project, ca.platform
		)
		SELECT
			ta.test_id::text,
			t.test_key,
			t.zephyr_key,
			t.file,
			t.full_title,
			ta.repository,
			NULLIF(ta.app, ''),
			t.framework,
			NULLIF(ta.project, ''),
			NULLIF(ta.platform, ''),
			ta.flaky_commit_count,
			ta.total_commit_count,
			ta.total_runs,
			CASE
				WHEN ta.total_commit_count > 0
				THEN (ta.flaky_commit_count::double precision * 100.0) / ta.total_commit_count::double precision
				ELSE 0.0
			END AS flake_rate,
			ta.last_flaky_at,
			ta.last_commit_sha,
			COALESCE(tl.lanes, '{}'::text[])
		  FROM test_agg ta
		  JOIN tests t ON t.id = ta.test_id
		  LEFT JOIN test_lanes tl
		    ON tl.test_id = ta.test_id AND tl.project = ta.project AND tl.platform = ta.platform
		 ORDER BY `+orderBy+`
		 LIMIT `+limitPH+` OFFSET `+offsetPH,
		b.args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query flaky test rows: %w", err)
	}
	defer rows.Close()

	out := []FlakyTestRow{}
	for rows.Next() {
		var r FlakyTestRow
		if err := rows.Scan(
			&r.TestID, &r.TestKey, &r.ZephyrKey, &r.File, &r.FullTitle,
			&r.Repository, &r.App, &r.Framework, &r.Project, &r.Platform,
			&r.FlakyCommitCount, &r.TotalCommitCount, &r.TotalRuns,
			&r.FlakeRate, &r.LastFlakyAt, &r.LastCommitSHA, &r.Lanes,
		); err != nil {
			return nil, fmt.Errorf("scan flaky test row: %w", err)
		}
		r.Lanes = ensureNonNilSlice(r.Lanes)
		out = append(out, r)
	}
	return out, rows.Err()
}

func flakyOrderByClause(sortBy string) string {
	switch sortBy {
	case SortFlakeRateDesc:
		return "flake_rate DESC, ta.flaky_commit_count DESC, ta.total_runs DESC, ta.test_id ASC, ta.platform ASC, ta.project ASC"
	case SortTotalRunsDesc:
		return "ta.total_runs DESC, ta.flaky_commit_count DESC, ta.test_id ASC, ta.platform ASC, ta.project ASC"
	case SortLastFlakyDesc:
		return "ta.last_flaky_at DESC, ta.flaky_commit_count DESC, ta.test_id ASC, ta.platform ASC, ta.project ASC"
	case SortNameAsc:
		return "t.full_title ASC, ta.platform ASC, ta.project ASC, ta.test_id ASC"
	default: // SortFlakyCommitsDesc
		return "ta.flaky_commit_count DESC, flake_rate DESC, ta.total_runs DESC, ta.test_id ASC, ta.platform ASC, ta.project ASC"
	}
}
