package kpi

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// QuerySummary computes GET /api/v1/kpi/summary strictly from pre-aggregated rollup tables.
func (s *Store) QuerySummary(ctx context.Context, f FilterParams) (SummaryResponse, error) {
	if s == nil || s.Pool == nil {
		return SummaryResponse{}, errors.New("kpi store pool is nil")
	}

	topCoverage, err := s.queryLaneCoverage(ctx, f, "", true, nil)
	if err != nil {
		return SummaryResponse{}, err
	}

	var mainNotes []string
	if f.Stage != "" && f.Stage != "main" {
		mainNotes = append(mainNotes, fmt.Sprintf("stage=%q filter ignored for KPI 1: main_pass_rate always evaluates stage='main'", f.Stage))
	}
	mainCoverage, err := s.queryLaneCoverage(ctx, f, "main", false, mainNotes)
	if err != nil {
		return SummaryResponse{}, err
	}

	var e2eNotes []string
	if f.Stage != "" && f.Stage != "pr" {
		e2eNotes = append(e2eNotes, fmt.Sprintf("stage=%q filter ignored for KPI 5: e2e_duration always evaluates stage='pr'", f.Stage))
	}
	e2eCoverage, err := s.queryLaneCoverage(ctx, f, "pr", false, e2eNotes)
	if err != nil {
		return SummaryResponse{}, err
	}

	mainPassRate, err := s.queryMainPassRate(ctx, f, mainCoverage)
	if err != nil {
		return SummaryResponse{}, err
	}

	flakySummary, err := s.querySummaryFlaky(ctx, f)
	if err != nil {
		return SummaryResponse{}, err
	}

	e2eDuration, err := s.queryE2EDuration(ctx, f, e2eCoverage)
	if err != nil {
		return SummaryResponse{}, err
	}

	weekly, err := s.queryWeeklySeries(ctx, f)
	if err != nil {
		return SummaryResponse{}, err
	}

	return SummaryResponse{
		Filters:      f,
		LaneCoverage: topCoverage,
		MainPassRate: mainPassRate,
		FlakyTests:   flakySummary,
		E2EDuration:  e2eDuration,
		WeeklySeries: weekly,
	}, nil
}

func appendPlatformScope(b *sqlFilterBuilder, alias string, platform string) string {
	if platform == "" {
		return " AND " + alias + ".is_all_platforms = true"
	}
	ph := b.add(platform)
	return fmt.Sprintf(" AND %s.is_all_platforms = false AND %s.platform = %s", alias, alias, ph)
}

func (s *Store) queryLaneCoverage(
	ctx context.Context,
	f FilterParams,
	overrideStage string,
	useFilterStage bool,
	extraNotes []string,
) (LaneCoverage, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)

	var where strings.Builder
	fmt.Fprintf(&where, "m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
	if overrideStage != "" {
		stPH := b.add(overrideStage)
		fmt.Fprintf(&where, " AND m.stage = %s", stPH)
	}
	where.WriteString(b.appendCommonFilters("m", f, useFilterStage && overrideStage == ""))
	where.WriteString(appendPlatformScope(b, "m", f.Platform))

	rows, err := s.Pool.Query(ctx, `
		SELECT m.repository, m.lane,
		       COALESCE(SUM(m.total_runs), 0)::integer,
		       COALESCE(SUM(m.orchestrated_runs), 0)::integer,
		       COALESCE(SUM(m.upload_only_runs), 0)::integer
		  FROM kpi_daily_lane_metrics m
		 WHERE `+where.String()+`
		 GROUP BY m.repository, m.lane
		 ORDER BY m.lane ASC, m.repository ASC
	`, b.args...)
	if err != nil {
		return LaneCoverage{}, fmt.Errorf("query lane coverage: %w", err)
	}
	defer rows.Close()

	cov := LaneCoverage{
		CoveredLanes:        []string{},
		UploadOnlyLaneNames: []string{},
		Notes:               []string{},
	}
	for rows.Next() {
		var repo, lane string
		var totalRuns, orchRuns, uploadRuns int
		if err := rows.Scan(&repo, &lane, &totalRuns, &orchRuns, &uploadRuns); err != nil {
			return LaneCoverage{}, fmt.Errorf("scan lane coverage: %w", err)
		}
		cov.TotalLanes++
		cov.TotalRuns += totalRuns
		cov.OrchestratedRuns += orchRuns
		cov.UploadOnlyRuns += uploadRuns
		cov.CoveredLanes = append(cov.CoveredLanes, lane)
		if orchRuns > 0 {
			cov.OrchestratedLanes++
			cov.StrictSupportedLanes++
		} else {
			cov.UploadOnlyLanes++
		}
		if uploadRuns > 0 {
			cov.UploadOnlyLaneNames = append(cov.UploadOnlyLaneNames, lane)
		}
	}
	if err := rows.Err(); err != nil {
		return LaneCoverage{}, err
	}

	cov.CoveredLanes = sortStringsDedup(cov.CoveredLanes)
	cov.UploadOnlyLaneNames = sortStringsDedup(cov.UploadOnlyLaneNames)
	cov.Notes = append(cov.Notes, extraNotes...)
	if cov.UploadOnlyRuns > 0 {
		cov.Notes = append(cov.Notes, "upload-only lanes lack orchestration lease history and total_duration_ms; excluded from strict lane pass rate and p50/p90 E2E duration")
	}
	return cov, nil
}

func (s *Store) queryMainPassRate(ctx context.Context, f FilterParams, cov LaneCoverage) (MainPassRate, error) {
	type mainAgg struct {
		firstAttemptPassed int
		testsRun           int
		lanesPassed        int
		lanesTotal         int
		strictPassed       int
		strictTotal        int
		uploadOnlyRuns     int
	}

	fetchWindow := func(fromDate, toDate time.Time) (mainAgg, error) {
		b := &sqlFilterBuilder{}
		fromPH := b.add(fromDate)
		toPH := b.add(toDate)
		var where strings.Builder
		fmt.Fprintf(&where, "m.stage = 'main' AND m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
		where.WriteString(b.appendCommonFilters("m", f, false))
		where.WriteString(appendPlatformScope(b, "m", f.Platform))

		var a mainAgg
		err := s.Pool.QueryRow(ctx, `
			SELECT
				COALESCE(SUM(m.attempt1_first_attempt_passed_count), 0)::integer,
				COALESCE(SUM(m.attempt1_tests_run), 0)::integer,
				COALESCE(SUM(m.attempt1_lanes_passed), 0)::integer,
				COALESCE(SUM(m.attempt1_lanes_total), 0)::integer,
				COALESCE(SUM(m.attempt1_lanes_strict_pass), 0)::integer,
				COALESCE(SUM(m.attempt1_lanes_strict_elig), 0)::integer,
				COALESCE(SUM(m.upload_only_runs), 0)::integer
			  FROM kpi_daily_lane_metrics m
			 WHERE `+where.String(),
			b.args...,
		).Scan(
			&a.firstAttemptPassed, &a.testsRun,
			&a.lanesPassed, &a.lanesTotal,
			&a.strictPassed, &a.strictTotal,
			&a.uploadOnlyRuns,
		)
		if err != nil {
			return mainAgg{}, fmt.Errorf("query main pass rate: %w", err)
		}
		return a, nil
	}

	cur, err := fetchWindow(f.CurFromDate, f.CurToDate)
	if err != nil {
		return MainPassRate{}, err
	}
	prior, err := fetchWindow(f.PriorFromDate, f.PriorToDate)
	if err != nil {
		return MainPassRate{}, err
	}

	curTestRate := pctPtr(cur.firstAttemptPassed, cur.testsRun)
	priorTestRate := pctPtr(prior.firstAttemptPassed, prior.testsRun)
	curLaneRate := pctPtr(cur.lanesPassed, cur.lanesTotal)
	priorLaneRate := pctPtr(prior.lanesPassed, prior.lanesTotal)
	curStrictRate := pctPtr(cur.strictPassed, cur.strictTotal)
	priorStrictRate := pctPtr(prior.strictPassed, prior.strictTotal)

	byLane, err := s.queryMainByLane(ctx, f)
	if err != nil {
		return MainPassRate{}, err
	}

	return MainPassRate{
		TestLevel: TestLevelPassRate{
			PassRate:           curTestRate,
			PriorPassRate:      priorTestRate,
			DeltaPP:            diffFloatPtr(curTestRate, priorTestRate),
			FirstAttemptPassed: cur.firstAttemptPassed,
			TotalTests:         cur.testsRun,
		},
		LaneLevel: LaneLevelPassRate{
			PassRate:             curLaneRate,
			PriorPassRate:        priorLaneRate,
			DeltaPP:              diffFloatPtr(curLaneRate, priorLaneRate),
			PassedLanes:          cur.lanesPassed,
			TotalLanes:           cur.lanesTotal,
			StrictPassRate:       curStrictRate,
			StrictPriorPassRate:  priorStrictRate,
			StrictDeltaPP:        diffFloatPtr(curStrictRate, priorStrictRate),
			StrictPassedLanes:    cur.strictPassed,
			StrictTotalLanes:     cur.strictTotal,
			UploadOnlyLanesCount: cov.UploadOnlyLanes,
		},
		ByLane:       byLane,
		LaneCoverage: cov,
	}, nil
}

func (s *Store) queryMainByLane(ctx context.Context, f FilterParams) ([]MainLaneRow, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	var where strings.Builder
	fmt.Fprintf(&where, "m.stage = 'main' AND m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("m", f, false))
	where.WriteString(appendPlatformScope(b, "m", f.Platform))

	rows, err := s.Pool.Query(ctx, `
		SELECT
			MAX(m.app),
			m.repository,
			m.lane,
			COALESCE(SUM(m.attempt1_first_attempt_passed_count), 0)::integer,
			COALESCE(SUM(m.attempt1_tests_run), 0)::integer,
			COALESCE(SUM(m.attempt1_lanes_passed), 0)::integer,
			COALESCE(SUM(m.attempt1_lanes_total), 0)::integer,
			COALESCE(SUM(m.attempt1_lanes_strict_pass), 0)::integer,
			COALESCE(SUM(m.attempt1_lanes_strict_elig), 0)::integer,
			COALESCE(SUM(m.orchestrated_runs), 0)::integer,
			COALESCE(SUM(m.upload_only_runs), 0)::integer
		  FROM kpi_daily_lane_metrics m
		 WHERE `+where.String()+`
		 GROUP BY m.repository, m.lane
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("query main by_lane: %w", err)
	}
	defer rows.Close()

	out := []MainLaneRow{}
	for rows.Next() {
		var r MainLaneRow
		if err := rows.Scan(
			&r.App, &r.Repository, &r.Lane,
			&r.FirstAttemptPassed, &r.TestsTotal,
			&r.LanesPassed, &r.LanesTotal,
			&r.StrictLanesPassed, &r.StrictLanesTotal,
			&r.OrchestratedRuns, &r.UploadOnlyRuns,
		); err != nil {
			return nil, fmt.Errorf("scan main by_lane: %w", err)
		}
		r.TestPassRate = pctPtr(r.FirstAttemptPassed, r.TestsTotal)
		r.LanePassRate = pctPtr(r.LanesPassed, r.LanesTotal)
		r.StrictPassRate = pctPtr(r.StrictLanesPassed, r.StrictLanesTotal)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sortMainLaneRows(out, f.Sort)
	return out, nil
}

func sortMainLaneRows(rows []MainLaneRow, sortBy string) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch sortBy {
		case SortPassRateAsc, SortPassRateDesc:
			if (a.TestPassRate == nil) != (b.TestPassRate == nil) {
				return a.TestPassRate != nil // NULLS LAST
			}
			if a.TestPassRate != nil && *a.TestPassRate != *b.TestPassRate {
				if sortBy == SortPassRateAsc {
					return *a.TestPassRate < *b.TestPassRate
				}
				return *a.TestPassRate > *b.TestPassRate
			}
		}
		if a.Lane != b.Lane {
			return a.Lane < b.Lane
		}
		if a.App != b.App {
			return a.App < b.App
		}
		return a.Repository < b.Repository
	})
}

func (s *Store) querySummaryFlaky(ctx context.Context, f FilterParams) (FlakySummary, error) {
	const canonicalFlakyDays = 14
	curTo := f.CurToDate
	curFrom := curTo.AddDate(0, 0, -(canonicalFlakyDays - 1))
	priorTo := curFrom.AddDate(0, 0, -1)
	priorFrom := curFrom.AddDate(0, 0, -canonicalFlakyDays)

	flakyFilter := f
	flakyFilter.CurFromDate = curFrom
	flakyFilter.CurToDate = curTo
	var flakyNotes []string
	if f.WindowDays != canonicalFlakyDays {
		flakyNotes = append(flakyNotes, fmt.Sprintf("window=%q filter overridden for KPI 3: flaky_tests always evaluates trailing 14d", f.Window))
	}
	cov, err := s.queryLaneCoverage(ctx, flakyFilter, "", true, flakyNotes)
	if err != nil {
		return FlakySummary{}, err
	}

	curTotal, curByApp, err := s.queryFlakyCountsByApp(ctx, f, curFrom, curTo)
	if err != nil {
		return FlakySummary{}, err
	}
	priorHasData, priorTotal, priorByApp, priorHasDataByApp, err := s.queryPriorFlakyCountsByApp(ctx, f, priorFrom, priorTo)
	if err != nil {
		return FlakySummary{}, err
	}

	var priorTotalPtr, deltaPtr *int
	if priorHasData {
		priorTotalPtr = &priorTotal
		d := curTotal - priorTotal
		deltaPtr = &d
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

	return FlakySummary{
		WindowDays:        canonicalFlakyDays,
		BaselineWindowDay: canonicalFlakyDays,
		MinFlakyCommits:   2,
		Count:             curTotal,
		PriorCount:        priorTotalPtr,
		Delta:             deltaPtr,
		ByApp:             byApp,
		LaneCoverage:      cov,
	}, nil
}

func (s *Store) queryE2EDuration(ctx context.Context, f FilterParams, cov LaneCoverage) (E2EDurationSummary, error) {
	fetchPercentiles := func(fromDate, toDate time.Time) (p50, p90 *float64, count int, err error) {
		b := &sqlFilterBuilder{}
		fromPH := b.add(fromDate)
		toPH := b.add(toDate)
		var where strings.Builder
		fmt.Fprintf(&where, "m.stage = 'pr' AND m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
		where.WriteString(b.appendCommonFilters("m", f, false))
		where.WriteString(appendPlatformScope(b, "m", f.Platform))

		err = s.Pool.QueryRow(ctx, `
			SELECT
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY d.val),
				PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY d.val),
				COUNT(d.val)::integer
			  FROM kpi_daily_lane_metrics m
			 CROSS JOIN LATERAL UNNEST(m.duration_samples_ms) AS d(val)
			 WHERE `+where.String(),
			b.args...,
		).Scan(&p50, &p90, &count)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("query e2e duration percentiles: %w", err)
		}
		return p50, p90, count, nil
	}

	p50, p90, count, err := fetchPercentiles(f.CurFromDate, f.CurToDate)
	if err != nil {
		return E2EDurationSummary{}, err
	}
	priorP50, priorP90, _, err := fetchPercentiles(f.PriorFromDate, f.PriorToDate)
	if err != nil {
		return E2EDurationSummary{}, err
	}

	byLane, err := s.queryE2EDurationByLane(ctx, f)
	if err != nil {
		return E2EDurationSummary{}, err
	}

	return E2EDurationSummary{
		P50Ms:        p50,
		PriorP50Ms:   priorP50,
		DeltaP50Ms:   diffFloatPtr(p50, priorP50),
		P90Ms:        p90,
		PriorP90Ms:   priorP90,
		DeltaP90Ms:   diffFloatPtr(p90, priorP90),
		SampleCount:  count,
		ByLane:       byLane,
		LaneCoverage: cov,
	}, nil
}

func (s *Store) queryE2EDurationByLane(ctx context.Context, f FilterParams) ([]DurationLaneRow, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	var where strings.Builder
	fmt.Fprintf(&where, "m.stage = 'pr' AND m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("m", f, false))
	where.WriteString(appendPlatformScope(b, "m", f.Platform))

	rows, err := s.Pool.Query(ctx, `
		WITH lane_runs AS (
			SELECT m.repository, m.lane,
			       MAX(m.app) AS app,
			       COALESCE(SUM(m.orchestrated_runs), 0)::integer AS orchestrated_runs,
			       COALESCE(SUM(m.upload_only_runs), 0)::integer AS upload_only_runs
			  FROM kpi_daily_lane_metrics m
			 WHERE `+where.String()+`
			 GROUP BY m.repository, m.lane
		),
		lane_dur AS (
			SELECT m.repository, m.lane,
			       PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY d.val) AS p50_ms,
			       PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY d.val) AS p90_ms,
			       COUNT(d.val)::integer AS sample_count
			  FROM kpi_daily_lane_metrics m
			 CROSS JOIN LATERAL UNNEST(m.duration_samples_ms) AS d(val)
			 WHERE `+where.String()+`
			 GROUP BY m.repository, m.lane
		)
		SELECT lr.app, lr.repository, lr.lane,
		       ld.p50_ms, ld.p90_ms,
		       COALESCE(ld.sample_count, 0)::integer,
		       lr.orchestrated_runs, lr.upload_only_runs
		  FROM lane_runs lr
		  LEFT JOIN lane_dur ld
		    ON ld.repository = lr.repository AND ld.lane = lr.lane
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("query e2e duration by_lane: %w", err)
	}
	defer rows.Close()

	out := []DurationLaneRow{}
	for rows.Next() {
		var r DurationLaneRow
		if err := rows.Scan(
			&r.App, &r.Repository, &r.Lane,
			&r.P50Ms, &r.P90Ms, &r.SampleCount,
			&r.OrchestratedRuns, &r.UploadOnlyRuns,
		); err != nil {
			return nil, fmt.Errorf("scan e2e duration by_lane: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch f.Sort {
		case SortDurationDesc, SortDurationAsc:
			if (a.P50Ms == nil) != (b.P50Ms == nil) {
				return a.P50Ms != nil // NULLS LAST
			}
			if a.P50Ms != nil && *a.P50Ms != *b.P50Ms {
				if f.Sort == SortDurationDesc {
					return *a.P50Ms > *b.P50Ms
				}
				return *a.P50Ms < *b.P50Ms
			}
		}
		if a.Lane != b.Lane {
			return a.Lane < b.Lane
		}
		if a.App != b.App {
			return a.App < b.App
		}
		return a.Repository < b.Repository
	})
	return out, nil
}

func (s *Store) queryWeeklySeries(ctx context.Context, f FilterParams) ([]WeeklyPoint, error) {
	firstWeek := truncateToUTCWeekMonday(f.CurFromDate)
	lastWeek := truncateToUTCWeekMonday(f.CurToDate)

	var weeks []time.Time
	for w := firstWeek; !w.After(lastWeek); w = w.AddDate(0, 0, 7) {
		weeks = append(weeks, w)
	}

	series := make([]WeeklyPoint, 0, len(weeks))
	for _, wStart := range weeks {
		sliceFrom := wStart
		if sliceFrom.Before(f.CurFromDate) {
			sliceFrom = f.CurFromDate
		}
		sliceTo := wStart.AddDate(0, 0, 6)
		if sliceTo.After(f.CurToDate) {
			sliceTo = f.CurToDate
		}

		pt := WeeklyPoint{
			WeekStart: wStart.Format("2006-01-02"),
		}

		// Main pass rate for this week slice.
		bMain := &sqlFilterBuilder{}
		fromPH := bMain.add(sliceFrom)
		toPH := bMain.add(sliceTo)
		var mainWhere strings.Builder
		fmt.Fprintf(&mainWhere, "m.stage = 'main' AND m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
		mainWhere.WriteString(bMain.appendCommonFilters("m", f, false))
		mainWhere.WriteString(appendPlatformScope(bMain, "m", f.Platform))

		var firstPass, testsRun, lanesPass, lanesTotal, strictPass, strictTotal int
		if err := s.Pool.QueryRow(ctx, `
			SELECT
				COALESCE(SUM(m.attempt1_first_attempt_passed_count), 0)::integer,
				COALESCE(SUM(m.attempt1_tests_run), 0)::integer,
				COALESCE(SUM(m.attempt1_lanes_passed), 0)::integer,
				COALESCE(SUM(m.attempt1_lanes_total), 0)::integer,
				COALESCE(SUM(m.attempt1_lanes_strict_pass), 0)::integer,
				COALESCE(SUM(m.attempt1_lanes_strict_elig), 0)::integer
			  FROM kpi_daily_lane_metrics m
			 WHERE `+mainWhere.String(),
			bMain.args...,
		).Scan(&firstPass, &testsRun, &lanesPass, &lanesTotal, &strictPass, &strictTotal); err != nil {
			return nil, fmt.Errorf("weekly main pass rate: %w", err)
		}
		pt.MainTestsTotal = testsRun
		pt.MainLanesTotal = lanesTotal
		pt.MainTestPassRate = pctPtr(firstPass, testsRun)
		pt.MainLanePassRate = pctPtr(lanesPass, lanesTotal)
		pt.MainStrictLanePassRate = pctPtr(strictPass, strictTotal)

		// Flaky test count in trailing 14d ending at sliceTo.
		flakyFrom := sliceTo.AddDate(0, 0, -13)
		flakyCount, _, err := s.queryFlakyCountsByApp(ctx, f, flakyFrom, sliceTo)
		if err != nil {
			return nil, fmt.Errorf("weekly flaky count: %w", err)
		}
		pt.FlakyTestCount = flakyCount

		// E2E duration for PR stage in this week slice.
		bDur := &sqlFilterBuilder{}
		dFromPH := bDur.add(sliceFrom)
		dToPH := bDur.add(sliceTo)
		var durWhere strings.Builder
		fmt.Fprintf(&durWhere, "m.stage = 'pr' AND m.metric_date >= %s AND m.metric_date <= %s", dFromPH, dToPH)
		durWhere.WriteString(bDur.appendCommonFilters("m", f, false))
		durWhere.WriteString(appendPlatformScope(bDur, "m", f.Platform))

		if err := s.Pool.QueryRow(ctx, `
			SELECT
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY d.val),
				PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY d.val),
				COUNT(d.val)::integer
			  FROM kpi_daily_lane_metrics m
			 CROSS JOIN LATERAL UNNEST(m.duration_samples_ms) AS d(val)
			 WHERE `+durWhere.String(),
			bDur.args...,
		).Scan(&pt.E2EP50Ms, &pt.E2EP90Ms, &pt.E2ESampleCount); err != nil {
			return nil, fmt.Errorf("weekly e2e duration: %w", err)
		}

		series = append(series, pt)
	}
	return series, nil
}

func truncateToUTCWeekMonday(d time.Time) time.Time {
	u := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(u.Weekday()) + 6) % 7 // Monday = 0, Sunday = 6
	return u.AddDate(0, 0, -offset)
}

func pctPtr(num, denom int) *float64 {
	if denom <= 0 {
		return nil
	}
	v := float64(num) * 100.0 / float64(denom)
	return &v
}

func diffFloatPtr(cur, prior *float64) *float64 {
	if cur == nil || prior == nil {
		return nil
	}
	d := *cur - *prior
	return &d
}
