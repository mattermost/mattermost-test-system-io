package kpi

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// FilterOptions holds distinct filter dropdown values loaded from pre-aggregated
// kpi_daily_lane_metrics (and the tiny apps config table).
type FilterOptions struct {
	Apps      []string `json:"apps"`
	Repos     []string `json:"repos"`
	Stages    []string `json:"stages"`
	Lanes     []string `json:"lanes"`
	Platforms []string `json:"platforms"`
}

// LoadFilterOptions returns available filter values for KPI UI dropdowns
// strictly from pre-aggregated rollup tables within the requested window.
func (s *Store) LoadFilterOptions(ctx context.Context, f FilterParams) (FilterOptions, error) {
	if s == nil || s.Pool == nil {
		return FilterOptions{}, errors.New("kpi store pool is nil")
	}
	return cachedQuery(ctx, s, filterCacheKey("filteropts", f, ""), func(ctx context.Context) (FilterOptions, error) {
		return s.loadFilterOptionsUncached(ctx, f)
	})
}

func (s *Store) loadFilterOptionsUncached(ctx context.Context, f FilterParams) (FilterOptions, error) {

	opts := FilterOptions{
		Apps:      []string{},
		Repos:     []string{},
		Stages:    []string{"pr", "main", "release", "nightly", "manual", "other"},
		Lanes:     []string{},
		Platforms: []string{},
	}

	apps, err := s.queryDistinctStrings(ctx, `
		SELECT DISTINCT v FROM (
			SELECT app AS v
			  FROM kpi_daily_lane_metrics
			 WHERE metric_date BETWEEN $1 AND $2
			   AND is_all_platforms = true
			   AND app <> ''
			UNION
			SELECT app AS v
			  FROM apps
			 WHERE app <> ''
		) t
		ORDER BY v ASC`, f.CurFromDate, f.CurToDate)
	if err != nil {
		return FilterOptions{}, err
	}
	opts.Apps = apps

	repoBuilder := &sqlFilterBuilder{}
	rFrom := repoBuilder.add(f.CurFromDate)
	rTo := repoBuilder.add(f.CurToDate)
	repoWhere := fmt.Sprintf("metric_date BETWEEN %s AND %s AND is_all_platforms = true AND repository <> ''", rFrom, rTo)
	if f.App != "" {
		appPH := repoBuilder.add(f.App)
		repoWhere += " AND app = " + appPH
	}
	repos, err := s.queryDistinctStrings(ctx,
		`SELECT DISTINCT repository FROM kpi_daily_lane_metrics WHERE `+repoWhere+` ORDER BY repository ASC`,
		repoBuilder.args...)
	if err != nil {
		return FilterOptions{}, err
	}
	opts.Repos = repos

	laneBuilder := &sqlFilterBuilder{}
	lFrom := laneBuilder.add(f.CurFromDate)
	lTo := laneBuilder.add(f.CurToDate)
	laneWhere := fmt.Sprintf("metric_date BETWEEN %s AND %s AND is_all_platforms = true AND lane <> ''", lFrom, lTo)
	if f.App != "" {
		laneWhere += " AND app = " + laneBuilder.add(f.App)
	}
	if f.Repo != "" {
		laneWhere += appendRepoClause(laneBuilder, f.Repo)
	}
	lanes, err := s.queryDistinctStrings(ctx,
		`SELECT DISTINCT lane FROM kpi_daily_lane_metrics WHERE `+laneWhere+` ORDER BY lane ASC`,
		laneBuilder.args...)
	if err != nil {
		return FilterOptions{}, err
	}
	opts.Lanes = lanes

	platBuilder := &sqlFilterBuilder{}
	pFrom := platBuilder.add(f.CurFromDate)
	pTo := platBuilder.add(f.CurToDate)
	platWhere := fmt.Sprintf("metric_date BETWEEN %s AND %s AND is_all_platforms = false AND platform <> ''", pFrom, pTo)
	if f.App != "" {
		platWhere += " AND app = " + platBuilder.add(f.App)
	}
	if f.Repo != "" {
		platWhere += appendRepoClause(platBuilder, f.Repo)
	}
	if f.Lane != "" {
		platWhere += " AND lane = " + platBuilder.add(f.Lane)
	}
	platforms, err := s.queryDistinctStrings(ctx,
		`SELECT DISTINCT platform FROM kpi_daily_lane_metrics WHERE `+platWhere+` ORDER BY platform ASC`,
		platBuilder.args...)
	if err != nil {
		return FilterOptions{}, err
	}
	opts.Platforms = platforms

	return opts, nil
}

func appendRepoClause(b *sqlFilterBuilder, repo string) string {
	ph := b.add(repo)
	if strings.Contains(repo, "/") {
		return " AND repository = " + ph
	}
	return fmt.Sprintf(" AND (repository = %s OR split_part(repository, '/', 2) = %s)", ph, ph)
}

func (s *Store) queryDistinctStrings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var val string
		if err := rows.Scan(&val); err != nil {
			return nil, err
		}
		if val != "" {
			out = append(out, val)
		}
	}
	return out, rows.Err()
}
