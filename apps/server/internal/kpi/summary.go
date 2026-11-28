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
	return s.QuerySummaryWithRecurring(ctx, f, nil)
}

// QuerySummaryWithRecurring computes GET /api/v1/kpi/summary, reusing a precomputed
// RecurringFailuresSummary when provided so callers like /kpi SSR avoid scanning signatures twice.
func (s *Store) QuerySummaryWithRecurring(
	ctx context.Context,
	f FilterParams,
	precomputed *RecurringFailuresSummary,
) (SummaryResponse, error) {
	if s == nil || s.Pool == nil {
		return SummaryResponse{}, errors.New("kpi store pool is nil")
	}
	return cachedQuery(ctx, s, filterCacheKey("summary", f, ""), func(ctx context.Context) (SummaryResponse, error) {
		return s.querySummaryUncached(ctx, f, precomputed)
	})
}

func (s *Store) querySummaryUncached(
	ctx context.Context,
	f FilterParams,
	precomputed *RecurringFailuresSummary,
) (SummaryResponse, error) {

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

	var prFalseRedNotes []string
	if f.Stage != "" && f.Stage != "pr" {
		prFalseRedNotes = append(prFalseRedNotes, fmt.Sprintf("stage=%q filter ignored for KPI 2: pr_false_red always evaluates PR lanes (gh_pr_number > 0)", f.Stage))
	}
	prFalseRed, err := s.queryPRFalseRedSummary(ctx, f, prFalseRedNotes)
	if err != nil {
		return SummaryResponse{}, err
	}

	recurring := precomputed
	if recurring == nil {
		recurring, err = s.queryRecurringFailuresSummary(ctx, f)
		if err != nil {
			return SummaryResponse{}, err
		}
	} else {
		cp := *recurring
		recurring = &cp
	}

	triage, err := s.queryTriageSummary(ctx, f)
	if err != nil {
		return SummaryResponse{}, err
	}

	weekly, err := s.queryWeeklySeries(ctx, f)
	if err != nil {
		return SummaryResponse{}, err
	}

	return SummaryResponse{
		Filters:           f,
		LaneCoverage:      topCoverage,
		MainPassRate:      mainPassRate,
		FlakyTests:        flakySummary,
		E2EDuration:       e2eDuration,
		WeeklySeries:      weekly,
		PRFalseRed:        &prFalseRed,
		RecurringFailures: recurring,
		Triage:            triage,
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
	return s.queryLaneCoverageQ(ctx, s.Pool, f, overrideStage, useFilterStage, extraNotes)
}

func (s *Store) queryLaneCoverageQ(
	ctx context.Context,
	q dbQuerier,
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

	rows, err := q.Query(ctx, `
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
		cov.Notes = append(cov.Notes, "upload-only runs lack orchestration lease history and total_duration_ms; excluded from strict lane pass rate and p50/p90 E2E duration")
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
	priorHasData, _, priorByApp, priorHasDataByApp, err := s.queryPriorFlakyCountsByApp(ctx, f, priorFrom, priorTo)
	if err != nil {
		return FlakySummary{}, err
	}

	var priorTotalPtr, deltaPtr *int
	if priorHasData {
		var compCur, compPrior int
		hasComparable := false
		for app := range priorHasDataByApp {
			hasComparable = true
			compCur += curByApp[app]
			compPrior += priorByApp[app]
		}
		if hasComparable {
			priorTotalPtr = &compPrior
			d := compCur - compPrior
			deltaPtr = &d
		}
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

type weeklyMainBucket struct {
	firstPass, testsRun     int
	lanesPass, lanesTotal   int
	strictPass, strictTotal int
}

type weeklyDurationBucket struct {
	p50Ms, p90Ms *float64
	sampleCount  int
}

type weeklyPRFalseRedBucket struct {
	falseRed   int
	settledRed int
}

func (s *Store) queryWeeklySeries(ctx context.Context, f FilterParams) ([]WeeklyPoint, error) {
	refNow := s.effectiveRefNow(f)
	settledCutoff := refNow.Add(-settledAfterHours * time.Hour)

	firstWeek := truncateToUTCWeekMonday(f.CurFromDate)
	lastWeek := truncateToUTCWeekMonday(f.CurToDate)

	var weeks []time.Time
	for w := firstWeek; !w.After(lastWeek); w = w.AddDate(0, 0, 7) {
		weeks = append(weeks, w)
	}

	weeklyFailures, err := s.queryWeeklyRecurringFailureCounts(ctx, f, weeks)
	if err != nil {
		return nil, err
	}
	weeklyTriage, err := s.queryWeeklyTriageCoverage(ctx, f, weeks)
	if err != nil {
		return nil, err
	}
	weeklyMain, err := s.queryWeeklyMainPassRates(ctx, f)
	if err != nil {
		return nil, err
	}
	weeklyFlaky, err := s.queryWeeklyFlakyCounts(ctx, f, weeks)
	if err != nil {
		return nil, err
	}
	weeklyDur, err := s.queryWeeklyE2EDuration(ctx, f)
	if err != nil {
		return nil, err
	}
	weeklyFalseRed, err := s.queryWeeklyPRFalseRedRates(ctx, f, settledCutoff)
	if err != nil {
		return nil, err
	}

	series := make([]WeeklyPoint, 0, len(weeks))
	for _, wStart := range weeks {
		wkKey := wStart.Format("2006-01-02")
		mb := weeklyMain[wkKey]
		db := weeklyDur[wkKey]
		fb := weeklyFalseRed[wkKey]
		rfCount := weeklyFailures[wkKey]

		series = append(series, WeeklyPoint{
			WeekStart:              wkKey,
			MainTestsTotal:         mb.testsRun,
			MainLanesTotal:         mb.lanesTotal,
			MainTestPassRate:       pctPtr(mb.firstPass, mb.testsRun),
			MainLanePassRate:       pctPtr(mb.lanesPass, mb.lanesTotal),
			MainStrictLanePassRate: pctPtr(mb.strictPass, mb.strictTotal),
			FlakyTestCount:         weeklyFlaky[wkKey],
			E2EP50Ms:               db.p50Ms,
			E2EP90Ms:               db.p90Ms,
			E2ESampleCount:         db.sampleCount,
			PRFalseRedRate:         pctPtr(fb.falseRed, fb.settledRed),
			RecurringFailuresCount: &rfCount,
			TriageCoverageRate:     weeklyTriage[wkKey],
		})
	}
	return series, nil
}

const pgWeekStartExpr = "(%s.metric_date - (EXTRACT(ISODOW FROM %s.metric_date)::int - 1))"

func (s *Store) queryWeeklyMainPassRates(ctx context.Context, f FilterParams) (map[string]weeklyMainBucket, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	var where strings.Builder
	fmt.Fprintf(&where, "m.stage = 'main' AND m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("m", f, false))
	where.WriteString(appendPlatformScope(b, "m", f.Platform))

	wkExpr := fmt.Sprintf("to_char("+pgWeekStartExpr+", 'YYYY-MM-DD')", "m", "m")
	rows, err := s.Pool.Query(ctx, `
		SELECT
			`+wkExpr+` AS wk,
			COALESCE(SUM(m.attempt1_first_attempt_passed_count), 0)::integer,
			COALESCE(SUM(m.attempt1_tests_run), 0)::integer,
			COALESCE(SUM(m.attempt1_lanes_passed), 0)::integer,
			COALESCE(SUM(m.attempt1_lanes_total), 0)::integer,
			COALESCE(SUM(m.attempt1_lanes_strict_pass), 0)::integer,
			COALESCE(SUM(m.attempt1_lanes_strict_elig), 0)::integer
		  FROM kpi_daily_lane_metrics m
		 WHERE `+where.String()+`
		 GROUP BY 1
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("weekly main pass rate: %w", err)
	}
	defer rows.Close()

	out := map[string]weeklyMainBucket{}
	for rows.Next() {
		var wk string
		var val weeklyMainBucket
		if err := rows.Scan(&wk, &val.firstPass, &val.testsRun, &val.lanesPass, &val.lanesTotal, &val.strictPass, &val.strictTotal); err != nil {
			return nil, fmt.Errorf("scan weekly main pass rate: %w", err)
		}
		out[wk] = val
	}
	return out, rows.Err()
}

func (s *Store) queryWeeklyE2EDuration(ctx context.Context, f FilterParams) (map[string]weeklyDurationBucket, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	var where strings.Builder
	fmt.Fprintf(&where, "m.stage = 'pr' AND m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("m", f, false))
	where.WriteString(appendPlatformScope(b, "m", f.Platform))

	wkExpr := fmt.Sprintf("to_char("+pgWeekStartExpr+", 'YYYY-MM-DD')", "m", "m")
	rows, err := s.Pool.Query(ctx, `
		SELECT
			`+wkExpr+` AS wk,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY d.val),
			PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY d.val),
			COUNT(d.val)::integer
		  FROM kpi_daily_lane_metrics m
		 CROSS JOIN LATERAL UNNEST(m.duration_samples_ms) AS d(val)
		 WHERE `+where.String()+`
		 GROUP BY 1
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("weekly e2e duration: %w", err)
	}
	defer rows.Close()

	out := map[string]weeklyDurationBucket{}
	for rows.Next() {
		var wk string
		var val weeklyDurationBucket
		if err := rows.Scan(&wk, &val.p50Ms, &val.p90Ms, &val.sampleCount); err != nil {
			return nil, fmt.Errorf("scan weekly e2e duration: %w", err)
		}
		out[wk] = val
	}
	return out, rows.Err()
}

func (s *Store) queryWeeklyPRFalseRedRates(
	ctx context.Context,
	f FilterParams,
	settledCutoff time.Time,
) (map[string]weeklyPRFalseRedBucket, error) {
	override := parseConfiguredE2ELanes(s.E2ELanes)
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	cutoffPH := b.add(settledCutoff)

	var where strings.Builder
	fmt.Fprintf(&where, "gr.gh_pr_number IS NOT NULL AND gr.gh_pr_number > 0 AND gr.is_red = true AND gr.metric_date >= %s AND gr.metric_date <= %s AND gr.group_created_at < %s", fromPH, toPH, cutoffPH)
	where.WriteString(appendE2ELanesFilter(b, "gr", override))
	where.WriteString(b.appendCommonFilters("gr", f, false))
	where.WriteString(appendPlatformScope(b, "gr", f.Platform))

	wkExpr := fmt.Sprintf("to_char("+pgWeekStartExpr+", 'YYYY-MM-DD')", "gr", "gr")
	rows, err := s.Pool.Query(ctx, `
		SELECT
			`+wkExpr+` AS wk,
			COUNT(*) FILTER (WHERE `+rerunGreenExpr("gr")+`)::integer,
			COUNT(*)::integer
		  FROM kpi_group_rollups gr
		 WHERE `+where.String()+`
		 GROUP BY 1
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("weekly pr false-red rate: %w", err)
	}
	defer rows.Close()

	out := map[string]weeklyPRFalseRedBucket{}
	for rows.Next() {
		var wk string
		var val weeklyPRFalseRedBucket
		if err := rows.Scan(&wk, &val.falseRed, &val.settledRed); err != nil {
			return nil, fmt.Errorf("scan weekly pr false-red rate: %w", err)
		}
		out[wk] = val
	}
	return out, rows.Err()
}

func (s *Store) queryWeeklyFlakyCounts(ctx context.Context, f FilterParams, weeks []time.Time) (map[string]int, error) {
	out := map[string]int{}
	if len(weeks) == 0 {
		return out, nil
	}

	weekStarts := make([]string, len(weeks))
	flakyFroms := make([]string, len(weeks))
	flakyTos := make([]string, len(weeks))
	var minFrom, maxTo time.Time

	for i, wStart := range weeks {
		sliceTo := wStart.AddDate(0, 0, 6)
		if sliceTo.After(f.CurToDate) {
			sliceTo = f.CurToDate
		}
		flakyFrom := sliceTo.AddDate(0, 0, -13)
		if i == 0 || flakyFrom.Before(minFrom) {
			minFrom = flakyFrom
		}
		if i == 0 || sliceTo.After(maxTo) {
			maxTo = sliceTo
		}
		weekStarts[i] = wStart.Format("2006-01-02")
		flakyFroms[i] = flakyFrom.Format("2006-01-02")
		flakyTos[i] = sliceTo.Format("2006-01-02")
	}

	b := &sqlFilterBuilder{}
	wkArrPH := b.add(weekStarts)
	fromArrPH := b.add(flakyFroms)
	toArrPH := b.add(flakyTos)
	minPH := b.add(minFrom)
	maxPH := b.add(maxTo)

	commitWhere := appendCommitFilters(b, "c", f)
	rows, err := s.Pool.Query(ctx, `
		WITH week_windows AS (
			SELECT *
			  FROM UNNEST(`+wkArrPH+`::date[], `+fromArrPH+`::date[], `+toArrPH+`::date[]) AS w(week_start, flaky_from, flaky_to)
		),
		commit_agg AS (
			SELECT
				w.week_start,
				c.repository,
				c.commit_sha,
				c.test_id,
				c.project,
				c.platform,
				(SUM(c.pass_count) > 0 AND SUM(c.fail_count) > 0) AS is_mixed
			  FROM week_windows w
			  JOIN kpi_test_commit_outcomes c
			    ON c.metric_date >= `+minPH+`
			   AND c.metric_date <= `+maxPH+`
			   AND c.metric_date >= w.flaky_from
			   AND c.metric_date <= w.flaky_to`+commitWhere+`
			 GROUP BY w.week_start, c.repository, c.commit_sha, c.test_id, c.project, c.platform
		),
		flaky_tests AS (
			SELECT
				week_start,
				test_id,
				project,
				platform
			  FROM commit_agg
			 WHERE is_mixed
			 GROUP BY week_start, test_id, project, platform
			HAVING COUNT(*) >= 2
		)
		SELECT to_char(week_start, 'YYYY-MM-DD'), COUNT(*)::integer
		  FROM flaky_tests
		 GROUP BY week_start
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("weekly flaky count: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var wk string
		var cnt int
		if err := rows.Scan(&wk, &cnt); err != nil {
			return nil, fmt.Errorf("scan weekly flaky count: %w", err)
		}
		out[wk] = cnt
	}
	return out, rows.Err()
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
