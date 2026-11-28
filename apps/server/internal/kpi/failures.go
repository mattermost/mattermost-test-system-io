package kpi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	failureHistoryMaxDays         = 365
	maxSignatureMergeHops         = 32
	maxFailureDetailAffectedTests = 100
)

// canonicalRootExprSQL resolves the canonical signature UUID from a terminal sig_up row:
// - acyclic root (merged_into_id IS NULL): cur_id
// - cycle closed (merged_into_id = ANY(path)): minimum UUID strictly among cycle members
// - hop-limit fallback: cur_id
const canonicalRootExprSQL = `CASE
	WHEN merged_into_id IS NULL THEN cur_id
	WHEN merged_into_id = ANY(path) THEN (
		SELECT x
		  FROM unnest(path[array_position(path, merged_into_id):]) AS x
		 ORDER BY x ASC
		 LIMIT 1
	)
	ELSE cur_id
END`

// QueryFailures computes GET /api/v1/kpi/failures strictly from pre-aggregated rollup tables.
func (s *Store) QueryFailures(ctx context.Context, f FilterParams) (FailuresResponse, error) {
	return s.QueryFailuresFiltered(ctx, f, false)
}

// QueryFailuresFiltered computes recurring failures, optionally restricting paginated rows to
// Active and Resurfaced patterns while keeping Summary counts unfiltered.
func (s *Store) QueryFailuresFiltered(ctx context.Context, f FilterParams, activeOnly bool) (FailuresResponse, error) {
	if s == nil || s.Pool == nil {
		return FailuresResponse{}, errors.New("kpi store pool is nil")
	}
	if err := validatePaginationParams(f); err != nil {
		return FailuresResponse{}, err
	}
	extra := "all"
	if activeOnly {
		extra = "active"
	}
	return cachedQuery(ctx, s, filterCacheKey("failures", f, extra), func(ctx context.Context) (FailuresResponse, error) {
		return s.queryFailuresFilteredUncached(ctx, f, activeOnly)
	})
}

func (s *Store) queryFailuresFilteredUncached(ctx context.Context, f FilterParams, activeOnly bool) (FailuresResponse, error) {

	cov, err := s.queryLaneCoverage(ctx, f, "", true, nil)
	if err != nil {
		return FailuresResponse{}, err
	}

	rows, summary, err := s.computeRecurringFailures(ctx, f)
	if err != nil {
		return FailuresResponse{}, err
	}

	if activeOnly {
		filtered := make([]RecurringFailureRow, 0, len(rows))
		for _, r := range rows {
			if r.State == FailureStateActive || r.State == FailureStateResurfaced {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	sortRecurringFailures(rows, f.Sort)

	total := len(rows)
	totalPages := 0
	if total > 0 && f.Limit > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(f.Limit)))
	}

	pageRows := paginateRecurringFailures(rows, f.Page, f.Limit)

	return FailuresResponse{
		Filters:      f,
		WindowDays:   f.WindowDays,
		Summary:      summary,
		LaneCoverage: cov,
		Pagination: Pagination{
			Page:       f.Page,
			Limit:      f.Limit,
			Total:      total,
			TotalPages: totalPages,
		},
		Failures: pageRows,
	}, nil
}

func paginateRecurringFailures(rows []RecurringFailureRow, page, limit int) []RecurringFailureRow {
	start, ok := safePageOffset(page, limit)
	if !ok || start >= len(rows) {
		return []RecurringFailureRow{}
	}
	end := len(rows)
	if end-start > limit {
		end = start + limit
	}
	return rows[start:end]
}

// QueryFailureDetail computes GET /api/v1/kpi/failures/{id} strictly from pre-aggregated rollup tables.
func (s *Store) QueryFailureDetail(ctx context.Context, sigID uuid.UUID, f FilterParams) (FailureDetailResponse, error) {
	if s == nil || s.Pool == nil {
		return FailureDetailResponse{}, errors.New("kpi store pool is nil")
	}
	return cachedQuery(ctx, s, filterCacheKey("faildetail", f, sigID.String()), func(ctx context.Context) (FailureDetailResponse, error) {
		return s.queryFailureDetailUncached(ctx, sigID, f)
	})
}

func (s *Store) queryFailureDetailUncached(ctx context.Context, sigID uuid.UUID, f FilterParams) (FailureDetailResponse, error) {

	fam, found, err := s.loadSignatureFamilyByID(ctx, sigID)
	if err != nil {
		return FailureDetailResponse{}, err
	}
	if !found {
		return FailureDetailResponse{}, ErrNotFound
	}

	firstSeen, err := s.loadFailureFirstSeen(ctx, fam.FirstSeenAt, fam.FirstSeenGroupID)
	if err != nil {
		return FailureDetailResponse{}, err
	}

	curToDay := utcDayNum(f.CurToDate)
	curFromDay := utcDayNum(f.CurFromDate)
	historyFrom := failureHistoryStart(f)

	metrics, err := s.loadDailySignatureMetrics(ctx, fam.MemberIDs, historyFrom, f.CurToDate, f)
	if err != nil {
		return FailureDetailResponse{}, err
	}

	acc := newSigAccum(nil)
	repoAccums := map[string]*repoAccum{}
	testLastSeen := map[uuid.UUID]time.Time{}
	testGroupCount := map[uuid.UUID]int{}

	for _, row := range metrics {
		acc.addMetricRow(row, curFromDay, curToDay)
		for _, tid := range row.AffectedTestIDs {
			if curTS, ok := testLastSeen[tid]; !ok || row.LastSeenAt.After(curTS) {
				testLastSeen[tid] = row.LastSeenAt
			}
		}
		dNum := utcDayNum(row.MetricDate)
		if dNum >= curFromDay && dNum <= curToDay {
			for _, tid := range row.AffectedTestIDs {
				testGroupCount[tid] += row.GroupCount
			}
			addRepoAccumRow(repoAccums, row)
		}
	}

	testList := mapKeysUUID(acc.historyTestIDs)
	execRows, err := s.loadDailyTestExecutions(ctx, testList, historyFrom, f.CurToDate, f)
	if err != nil {
		return FailureDetailResponse{}, err
	}

	runsByDay, testWinRuns, testWinFails, repoTestWinRuns := aggregateDetailExecs(execRows, curFromDay, curToDay)
	var firstSeenBoundDay *int64
	if hasScopeFilters(f) {
		memberToCanon := make(map[uuid.UUID]uuid.UUID, len(fam.MemberIDs))
		for _, mid := range fam.MemberIDs {
			memberToCanon[mid] = fam.CanonicalID
		}
		scopedFirst, err := s.loadScopedFirstSeenDays(ctx, memberToCanon, f.CurToDate, f)
		if err != nil {
			return FailureDetailResponse{}, err
		}
		if d, ok := scopedFirst[fam.CanonicalID]; ok {
			firstSeenBoundDay = &d
		}
	} else {
		d := utcDayNum(fam.FirstSeenAt)
		firstSeenBoundDay = &d
	}
	rates := computeWindowRates(acc, runsByDay, curFromDay, curToDay, firstSeenBoundDay)

	affectedTests, err := s.loadAffectedTestsMetadata(ctx, testList, testGroupCount, testWinRuns, testWinFails, testLastSeen)
	if err != nil {
		return FailureDetailResponse{}, err
	}
	latestVerdict, err := s.loadLatestFailureVerdict(ctx, fam.MemberIDs, f)
	if err != nil {
		return FailureDetailResponse{}, err
	}
	latestRunURL, err := s.loadFailureLatestRunURL(ctx, fam.MemberIDs, f)
	if err != nil {
		return FailureDetailResponse{}, err
	}

	var lastSeenPtr *time.Time
	if acc.hasLastSeen {
		ts := acc.lastSeenAtAny
		lastSeenPtr = &ts
	}

	return FailureDetailResponse{
		Filters:              f,
		RequestedSignatureID: sigID.String(),
		CanonicalSignatureID: fam.CanonicalID.String(),
		MergedIntoID:         fam.RequestedMergedIntoID,
		MergedSignatureIDs:   fam.MergedSignatureIDs,
		Fingerprint:          fam.Fingerprint,
		AlgoVersion:          fam.AlgoVersion,
		ErrorClass:           fam.ErrorClass,
		NormalizedMessage:    fam.NormalizedMessage,
		TopFrame:             fam.TopFrame,
		SampleMessage:        fam.SampleMessage,
		State:                rates.state,
		QuietDays:            rates.quietDays,
		OccurrenceCount:      acc.windowOcc,
		RawAttemptCount:      acc.windowRaw,
		GroupCount:           acc.windowGroups,
		AffectedTestCount:    len(acc.windowTestIDs),
		AffectedTestRuns:     rates.affectedRunsWin,
		RatePer1k:            rates.rateWin,
		Occurrences14d:       acc.occ14d,
		AffectedRuns14d:      rates.affectedRuns14d,
		RatePer1k14d:         rates.rate14d,
		PriorOccurrences14d:  acc.priorOcc14d,
		PriorAffectedRuns14d: rates.priorAffectedRuns14d,
		PriorRatePer1k14d:    rates.priorRate14d,
		DeltaRatePer1k:       rates.deltaRate,
		Occurrences7d:        acc.occ7d,
		AffectedRuns7d:       rates.affectedRuns7d,
		LastSeenAt:           lastSeenPtr,
		FirstSeen:            firstSeen,
		LatestRunURL:         latestRunURL,
		LatestVerdict:        latestVerdict,
		DailySeries:          buildFailureDailySeries(f, acc, runsByDay),
		AffectedTests:        affectedTests,
		AffectedRepos:        buildAffectedRepos(repoAccums, repoTestWinRuns),
		PRNumbers:            sortedSetInts(acc.prs),
	}, nil
}

func (s *Store) queryRecurringFailuresSummary(ctx context.Context, f FilterParams) (*RecurringFailuresSummary, error) {
	_, summary, err := s.computeRecurringFailures(ctx, f)
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

func (s *Store) queryWeeklyRecurringFailureCounts(ctx context.Context, f FilterParams, weeks []time.Time) (map[string]int, error) {
	counts := make(map[string]int, len(weeks))
	if len(weeks) == 0 {
		return counts, nil
	}

	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	where := fmt.Sprintf("m.metric_date >= %s AND m.metric_date <= %s AND m.occurrence_count > 0%s",
		fromPH, toPH, b.appendSignatureExecFilters("m", f))

	rows, err := s.Pool.Query(ctx, `
		WITH RECURSIVE
		window_sigs AS (
			SELECT m.metric_date, m.signature_id
			  FROM kpi_daily_signature_metrics m
			 WHERE `+where+`
		),
		distinct_sigs AS (
			SELECT DISTINCT signature_id FROM window_sigs
		),
		sig_up AS (
			SELECT fs.id AS start_id, fs.id AS cur_id, fs.merged_into_id, ARRAY[fs.id] AS path, 0 AS depth
			  FROM failure_signatures fs
			  JOIN distinct_sigs d ON d.signature_id = fs.id
			UNION ALL
			SELECT u.start_id, p.id AS cur_id, p.merged_into_id, u.path || p.id, u.depth + 1
			  FROM sig_up u
			  JOIN failure_signatures p ON p.id = u.merged_into_id
			 WHERE u.merged_into_id IS NOT NULL
			   AND NOT (p.id = ANY(u.path))
			   AND u.depth < `+strconv.Itoa(maxSignatureMergeHops)+`
		),
		terminal_up AS (
			SELECT DISTINCT ON (start_id)
			       start_id,
			       cur_id,
			       merged_into_id,
			       path
			  FROM sig_up
			 ORDER BY start_id, depth DESC
		),
		sig_root AS (
			SELECT start_id AS sig_id,
			       `+canonicalRootExprSQL+` AS canonical_id
			  FROM terminal_up
		),
		weekly_distinct AS (
			SELECT DISTINCT
			       date_trunc('week', ws.metric_date::timestamp)::date AS week_start,
			       sr.canonical_id
			  FROM window_sigs ws
			  JOIN sig_root sr ON sr.sig_id = ws.signature_id
		)
		SELECT week_start, COUNT(*)::integer
		  FROM weekly_distinct
		 GROUP BY week_start
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("query weekly recurring failure counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ws time.Time
		var cnt int
		if err := rows.Scan(&ws, &cnt); err != nil {
			return nil, fmt.Errorf("scan weekly recurring failure count: %w", err)
		}
		counts[ws.UTC().Format("2006-01-02")] = cnt
	}
	return counts, rows.Err()
}

type canonicalSigInfo struct {
	ID                uuid.UUID
	Fingerprint       string
	AlgoVersion       int
	ErrorClass        *string
	NormalizedMessage string
	TopFrame          *string
	FirstSeenAt       time.Time
	MemberIDs         []uuid.UUID
	MergedIDs         []string
}

type dailySigMetricRow struct {
	MetricDate      time.Time
	Repository      string
	App             string
	Lane            string
	SignatureID     uuid.UUID
	OccurrenceCount int
	RawAttemptCount int
	GroupCount      int
	AffectedTestIDs []uuid.UUID
	PRNumbers       []int
	LastSeenAt      time.Time
}

type dailyExecRow struct {
	TestID     uuid.UUID
	MetricDate time.Time
	Repository string
	RunCount   int
	FailCount  int
}

type sigAccum struct {
	info           *canonicalSigInfo
	occByDay       map[int64]int
	rawByDay       map[int64]int
	groupByDay     map[int64]int
	historyTestIDs map[uuid.UUID]struct{}
	windowTestIDs  map[uuid.UUID]struct{}
	repos          map[string]struct{}
	apps           map[string]struct{}
	lanes          map[string]struct{}
	prs            map[int]struct{}
	hasOcc         bool
	firstSeenDay   int64
	windowOcc      int
	windowRaw      int
	windowGroups   int
	occ14d         int
	priorOcc14d    int
	occ7d          int
	lastSeenAt     time.Time
	hasLastSeen    bool
	lastSeenAtAny  time.Time
}

func newSigAccum(info *canonicalSigInfo) *sigAccum {
	return &sigAccum{
		info:           info,
		occByDay:       map[int64]int{},
		rawByDay:       map[int64]int{},
		groupByDay:     map[int64]int{},
		historyTestIDs: map[uuid.UUID]struct{}{},
		windowTestIDs:  map[uuid.UUID]struct{}{},
		repos:          map[string]struct{}{},
		apps:           map[string]struct{}{},
		lanes:          map[string]struct{}{},
		prs:            map[int]struct{}{},
	}
}

func (a *sigAccum) addMetricRow(row dailySigMetricRow, curFromDay, curToDay int64) {
	dNum := utcDayNum(row.MetricDate)
	if row.OccurrenceCount > 0 {
		a.occByDay[dNum] += row.OccurrenceCount
		a.rawByDay[dNum] += row.RawAttemptCount
		a.groupByDay[dNum] += row.GroupCount
		if !a.hasOcc || dNum < a.firstSeenDay {
			a.firstSeenDay = dNum
			a.hasOcc = true
		}
	}
	if !a.hasLastSeen || row.LastSeenAt.After(a.lastSeenAtAny) {
		a.lastSeenAtAny = row.LastSeenAt
		a.hasLastSeen = true
	}
	for _, tid := range row.AffectedTestIDs {
		a.historyTestIDs[tid] = struct{}{}
	}
	if dNum >= curFromDay && dNum <= curToDay {
		a.addWindowFields(row)
	}
	if dNum >= curToDay-(TrendWindowDays-1) && dNum <= curToDay {
		a.occ14d += row.OccurrenceCount
	}
	priorEnd := curToDay - TrendWindowDays
	priorStart := priorEnd - (TrendWindowDays - 1)
	if dNum >= priorStart && dNum <= priorEnd {
		a.priorOcc14d += row.OccurrenceCount
	}
	if dNum >= curToDay-(StateWindowDays-1) && dNum <= curToDay {
		a.occ7d += row.OccurrenceCount
	}
}

func (a *sigAccum) addWindowFields(row dailySigMetricRow) {
	a.windowOcc += row.OccurrenceCount
	a.windowRaw += row.RawAttemptCount
	a.windowGroups += row.GroupCount
	if row.LastSeenAt.After(a.lastSeenAt) {
		a.lastSeenAt = row.LastSeenAt
	}
	for _, tid := range row.AffectedTestIDs {
		a.windowTestIDs[tid] = struct{}{}
	}
	if row.Repository != "" {
		a.repos[row.Repository] = struct{}{}
	}
	if row.App != "" {
		a.apps[row.App] = struct{}{}
	}
	if row.Lane != "" {
		a.lanes[row.Lane] = struct{}{}
	}
	for _, pr := range row.PRNumbers {
		if pr > 0 {
			a.prs[pr] = struct{}{}
		}
	}
}

type windowRates struct {
	affectedRunsWin      int
	affectedRuns14d      int
	priorAffectedRuns14d int
	affectedRuns7d       int
	rateWin              *float64
	rate14d              *float64
	priorRate14d         *float64
	deltaRate            *float64
	state                string
	quietDays            *int
}

func computeWindowRates(
	acc *sigAccum,
	runsByDay map[int64]int,
	curFromDay, curToDay int64,
	firstSeenBoundDay *int64,
) windowRates {
	trailing7dStart := curToDay - (StateWindowDays - 1)
	trailing14dStart := curToDay - (TrendWindowDays - 1)
	prior14dEnd := trailing14dStart - 1
	prior14dStart := trailing14dStart - TrendWindowDays

	runsWin := sumDays(runsByDay, curFromDay, curToDay)
	runs14d := sumDays(runsByDay, trailing14dStart, curToDay)
	priorRuns14d := sumDays(runsByDay, prior14dStart, prior14dEnd)
	runs7d := sumDays(runsByDay, trailing7dStart, curToDay)

	rate14d := ratePer1kPtr(acc.occ14d, runs14d)
	priorRate14d := ratePer1kPtr(acc.priorOcc14d, priorRuns14d)

	firstSeenDay := acc.firstSeenDay
	if acc.hasOcc && firstSeenBoundDay != nil && *firstSeenBoundDay < firstSeenDay {
		firstSeenDay = *firstSeenBoundDay
	}
	state, quietDays := computeFailureState(curToDay, acc.hasOcc, firstSeenDay, acc.occByDay, runsByDay)

	return windowRates{
		affectedRunsWin:      runsWin,
		affectedRuns14d:      runs14d,
		priorAffectedRuns14d: priorRuns14d,
		affectedRuns7d:       runs7d,
		rateWin:              ratePer1kPtr(acc.windowOcc, runsWin),
		rate14d:              rate14d,
		priorRate14d:         priorRate14d,
		deltaRate:            diffFloatPtr(rate14d, priorRate14d),
		state:                state,
		quietDays:            quietDays,
	}
}

func (s *Store) computeRecurringFailures(ctx context.Context, f FilterParams) ([]RecurringFailureRow, RecurringFailuresSummary, error) {
	canonMap, memberToCanon, allMemberIDs, err := s.loadActiveCanonicalFamilies(ctx, f)
	if err != nil {
		return nil, RecurringFailuresSummary{}, err
	}
	if len(canonMap) == 0 {
		return []RecurringFailureRow{}, RecurringFailuresSummary{}, nil
	}

	curToDay := utcDayNum(f.CurToDate)
	curFromDay := utcDayNum(f.CurFromDate)
	historyFrom := failureHistoryStart(f)

	metrics, err := s.loadDailySignatureMetrics(ctx, allMemberIDs, historyFrom, f.CurToDate, f)
	if err != nil {
		return nil, RecurringFailuresSummary{}, err
	}

	accums := make(map[uuid.UUID]*sigAccum, len(canonMap))
	for cid, info := range canonMap {
		accums[cid] = newSigAccum(info)
	}
	allTestIDSet := map[uuid.UUID]struct{}{}
	for _, row := range metrics {
		cid, ok := memberToCanon[row.SignatureID]
		if !ok {
			continue
		}
		accums[cid].addMetricRow(row, curFromDay, curToDay)
		for _, tid := range row.AffectedTestIDs {
			allTestIDSet[tid] = struct{}{}
		}
	}

	execRows, err := s.loadDailyTestExecutions(ctx, mapKeysUUID(allTestIDSet), historyFrom, f.CurToDate, f)
	if err != nil {
		return nil, RecurringFailuresSummary{}, err
	}
	execByTestDay := groupExecRunsByTestDay(execRows)

	verdictLabels, err := s.loadLatestVerdictLabelsBatch(ctx, memberToCanon, f)
	if err != nil {
		return nil, RecurringFailuresSummary{}, err
	}

	var scopedFirstDays map[uuid.UUID]int64
	if hasScopeFilters(f) {
		scopedFirstDays, err = s.loadScopedFirstSeenDays(ctx, memberToCanon, f.CurToDate, f)
		if err != nil {
			return nil, RecurringFailuresSummary{}, err
		}
	}

	rows := make([]RecurringFailureRow, 0, len(accums))
	var summary RecurringFailuresSummary
	for cid, acc := range accums {
		if acc.windowOcc <= 0 {
			continue
		}
		runsByDay := collectRunsForTests(acc.historyTestIDs, execByTestDay)
		var firstSeenBoundDay *int64
		if hasScopeFilters(f) {
			if d, ok := scopedFirstDays[cid]; ok {
				firstSeenBoundDay = &d
			}
		} else {
			d := utcDayNum(acc.info.FirstSeenAt)
			firstSeenBoundDay = &d
		}
		rates := computeWindowRates(acc, runsByDay, curFromDay, curToDay, firstSeenBoundDay)

		var labelPtr *string
		if lbl, ok := verdictLabels[cid]; ok {
			l := lbl
			labelPtr = &l
		}
		recordSummaryState(&summary, acc.windowOcc, rates.state, labelPtr != nil)
		rows = append(rows, buildRecurringFailureRow(cid, acc, rates, labelPtr))
	}
	return rows, summary, nil
}

func failureHistoryStart(f FilterParams) time.Time {
	historyDays := max(f.WindowDays+30, 90)
	historyFrom := f.CurToDate.AddDate(0, 0, -historyDays)
	if f.CurFromDate.Before(historyFrom) {
		return f.CurFromDate
	}
	return historyFrom
}

func groupExecRunsByTestDay(execRows []dailyExecRow) map[uuid.UUID]map[int64]int {
	out := map[uuid.UUID]map[int64]int{}
	for _, er := range execRows {
		dm := out[er.TestID]
		if dm == nil {
			dm = map[int64]int{}
			out[er.TestID] = dm
		}
		dm[utcDayNum(er.MetricDate)] += er.RunCount
	}
	return out
}

func collectRunsForTests(testIDs map[uuid.UUID]struct{}, execByTestDay map[uuid.UUID]map[int64]int) map[int64]int {
	runsByDay := map[int64]int{}
	for tid := range testIDs {
		for dNum, cnt := range execByTestDay[tid] {
			runsByDay[dNum] += cnt
		}
	}
	return runsByDay
}

func recordSummaryState(summary *RecurringFailuresSummary, occ int, state string, triaged bool) {
	summary.TotalSignatures++
	summary.TotalOccurrences += occ
	if triaged {
		summary.TriagedCount++
	}
	switch state {
	case FailureStateActive:
		summary.ActiveCount++
	case FailureStateResurfaced:
		summary.ResurfacedCount++
	case FailureStateQuiet:
		summary.QuietCount++
	case FailureStateNotEnoughRuns:
		summary.NotEnoughRunsCount++
	}
}

func buildRecurringFailureRow(
	cid uuid.UUID,
	acc *sigAccum,
	rates windowRates,
	labelPtr *string,
) RecurringFailureRow {
	return RecurringFailureRow{
		SignatureID:          cid.String(),
		Fingerprint:          acc.info.Fingerprint,
		AlgoVersion:          acc.info.AlgoVersion,
		ErrorClass:           acc.info.ErrorClass,
		NormalizedMessage:    acc.info.NormalizedMessage,
		TopFrame:             acc.info.TopFrame,
		MergedSignatureIDs:   ensureNonNilSlice(acc.info.MergedIDs),
		State:                rates.state,
		QuietDays:            rates.quietDays,
		OccurrenceCount:      acc.windowOcc,
		RawAttemptCount:      acc.windowRaw,
		GroupCount:           acc.windowGroups,
		AffectedTestCount:    len(acc.windowTestIDs),
		AffectedTestRuns:     rates.affectedRunsWin,
		RatePer1k:            rates.rateWin,
		Occurrences14d:       acc.occ14d,
		AffectedRuns14d:      rates.affectedRuns14d,
		RatePer1k14d:         rates.rate14d,
		PriorOccurrences14d:  acc.priorOcc14d,
		PriorAffectedRuns14d: rates.priorAffectedRuns14d,
		PriorRatePer1k14d:    rates.priorRate14d,
		DeltaRatePer1k:       rates.deltaRate,
		Occurrences7d:        acc.occ7d,
		AffectedRuns7d:       rates.affectedRuns7d,
		FirstSeenAt:          acc.info.FirstSeenAt,
		LastSeenAt:           acc.lastSeenAt,
		Repositories:         sortedSetStrings(acc.repos),
		Apps:                 sortedSetStrings(acc.apps),
		Lanes:                sortedSetStrings(acc.lanes),
		PRNumbers:            sortedSetInts(acc.prs),
		LatestVerdictLabel:   labelPtr,
	}
}

type repoAccum struct {
	app       string
	occ       int
	groups    int
	lanes     map[string]struct{}
	testIDSet map[uuid.UUID]struct{}
}

func addRepoAccumRow(repoAccums map[string]*repoAccum, row dailySigMetricRow) {
	ra := repoAccums[row.Repository]
	if ra == nil {
		ra = &repoAccum{
			app:       row.App,
			lanes:     map[string]struct{}{},
			testIDSet: map[uuid.UUID]struct{}{},
		}
		repoAccums[row.Repository] = ra
	}
	if ra.app == "" && row.App != "" {
		ra.app = row.App
	}
	ra.occ += row.OccurrenceCount
	ra.groups += row.GroupCount
	ra.lanes[row.Lane] = struct{}{}
	for _, tid := range row.AffectedTestIDs {
		ra.testIDSet[tid] = struct{}{}
	}
}

func aggregateDetailExecs(
	execRows []dailyExecRow,
	curFromDay, curToDay int64,
) (
	runsByDay map[int64]int,
	testWinRuns map[uuid.UUID]int,
	testWinFails map[uuid.UUID]int,
	repoTestWinRuns map[string]map[uuid.UUID]int,
) {
	runsByDay = map[int64]int{}
	testWinRuns = map[uuid.UUID]int{}
	testWinFails = map[uuid.UUID]int{}
	repoTestWinRuns = map[string]map[uuid.UUID]int{}

	for _, er := range execRows {
		dNum := utcDayNum(er.MetricDate)
		runsByDay[dNum] += er.RunCount
		if dNum >= curFromDay && dNum <= curToDay {
			testWinRuns[er.TestID] += er.RunCount
			testWinFails[er.TestID] += er.FailCount
			rm := repoTestWinRuns[er.Repository]
			if rm == nil {
				rm = map[uuid.UUID]int{}
				repoTestWinRuns[er.Repository] = rm
			}
			rm[er.TestID] += er.RunCount
		}
	}
	return runsByDay, testWinRuns, testWinFails, repoTestWinRuns
}

func buildFailureDailySeries(f FilterParams, acc *sigAccum, runsByDay map[int64]int) []FailureDailyPoint {
	series := make([]FailureDailyPoint, 0, f.WindowDays)
	for d := f.CurFromDate; !d.After(f.CurToDate); d = d.AddDate(0, 0, 1) {
		dNum := utcDayNum(d)
		occ := acc.occByDay[dNum]
		runs := runsByDay[dNum]
		series = append(series, FailureDailyPoint{
			Date:             d.Format("2006-01-02"),
			OccurrenceCount:  occ,
			RawAttemptCount:  acc.rawByDay[dNum],
			GroupCount:       acc.groupByDay[dNum],
			AffectedTestRuns: runs,
			RatePer1k:        ratePer1kPtr(occ, runs),
		})
	}
	return series
}

func buildAffectedRepos(
	repoAccums map[string]*repoAccum,
	repoTestWinRuns map[string]map[uuid.UUID]int,
) []FailureAffectedRepo {
	affectedRepos := make([]FailureAffectedRepo, 0, len(repoAccums))
	for repo, ra := range repoAccums {
		runs := 0
		if rm := repoTestWinRuns[repo]; rm != nil {
			for tid := range ra.testIDSet {
				runs += rm[tid]
			}
		}
		affectedRepos = append(affectedRepos, FailureAffectedRepo{
			Repository:      repo,
			App:             ra.app,
			OccurrenceCount: ra.occ,
			GroupCount:      ra.groups,
			AffectedRuns:    runs,
			RatePer1k:       ratePer1kPtr(ra.occ, runs),
			Lanes:           sortedSetStrings(ra.lanes),
		})
	}
	sort.Slice(affectedRepos, func(i, j int) bool {
		if affectedRepos[i].OccurrenceCount != affectedRepos[j].OccurrenceCount {
			return affectedRepos[i].OccurrenceCount > affectedRepos[j].OccurrenceCount
		}
		return affectedRepos[i].Repository < affectedRepos[j].Repository
	})
	return affectedRepos
}

func (s *Store) loadActiveCanonicalFamilies(
	ctx context.Context,
	f FilterParams,
) (map[uuid.UUID]*canonicalSigInfo, map[uuid.UUID]uuid.UUID, []uuid.UUID, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	where := fmt.Sprintf("m.metric_date >= %s AND m.metric_date <= %s AND m.occurrence_count > 0%s",
		fromPH, toPH, b.appendSignatureExecFilters("m", f))

	rows, err := s.Pool.Query(ctx, `
		WITH RECURSIVE
		active_sigs AS (
			SELECT DISTINCT m.signature_id
			  FROM kpi_daily_signature_metrics m
			 WHERE `+where+`
		),
		sig_up AS (
			SELECT fs.id AS start_id, fs.id AS cur_id, fs.merged_into_id, ARRAY[fs.id] AS path, 0 AS depth
			  FROM failure_signatures fs
			  JOIN active_sigs a ON a.signature_id = fs.id
			UNION ALL
			SELECT u.start_id, p.id AS cur_id, p.merged_into_id, u.path || p.id, u.depth + 1
			  FROM sig_up u
			  JOIN failure_signatures p ON p.id = u.merged_into_id
			 WHERE u.merged_into_id IS NOT NULL
			   AND NOT (p.id = ANY(u.path))
			   AND u.depth < `+strconv.Itoa(maxSignatureMergeHops)+`
		),
		terminal_up AS (
			SELECT DISTINCT ON (start_id)
			       cur_id,
			       merged_into_id,
			       path
			  FROM sig_up
			 ORDER BY start_id, depth DESC
		),
		distinct_roots AS (
			SELECT `+canonicalRootExprSQL+` AS canonical_id
			  FROM terminal_up
		),
		unique_roots AS (
			SELECT DISTINCT canonical_id FROM distinct_roots
		),
		family AS (
			SELECT r.canonical_id, r.canonical_id AS member_id, ARRAY[r.canonical_id] AS path, 0 AS depth
			  FROM unique_roots r
			UNION ALL
			SELECT fam.canonical_id, c.id AS member_id, fam.path || c.id, fam.depth + 1
			  FROM family fam
			  JOIN failure_signatures c ON c.merged_into_id = fam.member_id
			 WHERE NOT (c.id = ANY(fam.path))
			   AND fam.depth < `+strconv.Itoa(maxSignatureMergeHops)+`
		)
		SELECT DISTINCT
		       fam.canonical_id,
		       fam.member_id,
		       c.fingerprint,
		       c.algo_version,
		       c.error_class,
		       c.normalized_message,
		       c.top_frame,
		       mfs.first_seen_at
		  FROM family fam
		  JOIN failure_signatures c ON c.id = fam.canonical_id
		  JOIN failure_signatures mfs ON mfs.id = fam.member_id
	`, b.args...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load active canonical signature families: %w", err)
	}
	defer rows.Close()

	canonMap := map[uuid.UUID]*canonicalSigInfo{}
	memberToCanon := map[uuid.UUID]uuid.UUID{}
	memberSeen := map[uuid.UUID]struct{}{}

	for rows.Next() {
		var (
			canonID, memberID uuid.UUID
			fp, normMsg       string
			algo              int
			errCls, topFrame  *string
			firstSeen         time.Time
		)
		if err := rows.Scan(&canonID, &memberID, &fp, &algo, &errCls, &normMsg, &topFrame, &firstSeen); err != nil {
			return nil, nil, nil, fmt.Errorf("scan canonical signature family: %w", err)
		}
		info := canonMap[canonID]
		if info == nil {
			info = &canonicalSigInfo{
				ID:                canonID,
				Fingerprint:       fp,
				AlgoVersion:       algo,
				ErrorClass:        errCls,
				NormalizedMessage: normMsg,
				TopFrame:          topFrame,
				FirstSeenAt:       firstSeen,
				MergedIDs:         []string{},
			}
			canonMap[canonID] = info
		} else if firstSeen.Before(info.FirstSeenAt) {
			info.FirstSeenAt = firstSeen
		}
		memberToCanon[memberID] = canonID
		if _, exists := memberSeen[memberID]; !exists {
			memberSeen[memberID] = struct{}{}
			info.MemberIDs = append(info.MemberIDs, memberID)
			if memberID != canonID {
				info.MergedIDs = append(info.MergedIDs, memberID.String())
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}

	allMembers := make([]uuid.UUID, 0, len(memberSeen))
	for id := range memberSeen {
		allMembers = append(allMembers, id)
	}
	for _, info := range canonMap {
		sort.Strings(info.MergedIDs)
	}
	return canonMap, memberToCanon, allMembers, nil
}

type signatureFamilyDetail struct {
	CanonicalID           uuid.UUID
	RequestedMergedIntoID *string
	Fingerprint           string
	AlgoVersion           int
	ErrorClass            *string
	NormalizedMessage     string
	TopFrame              *string
	SampleMessage         *string
	FirstSeenAt           time.Time
	FirstSeenGroupID      *uuid.UUID
	MemberIDs             []uuid.UUID
	MergedSignatureIDs    []string
}

func (s *Store) loadSignatureFamilyByID(ctx context.Context, sigID uuid.UUID) (signatureFamilyDetail, bool, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH RECURSIVE
		seed AS (
			SELECT id, merged_into_id
			  FROM failure_signatures
			 WHERE id = $1
		),
		up AS (
			SELECT fs.id AS cur_id, fs.merged_into_id, ARRAY[fs.id] AS path, 0 AS depth
			  FROM failure_signatures fs
			  JOIN seed s ON s.id = fs.id
			UNION ALL
			SELECT p.id AS cur_id, p.merged_into_id, u.path || p.id, u.depth + 1
			  FROM up u
			  JOIN failure_signatures p ON p.id = u.merged_into_id
			 WHERE u.merged_into_id IS NOT NULL
			   AND NOT (p.id = ANY(u.path))
			   AND u.depth < `+strconv.Itoa(maxSignatureMergeHops)+`
		),
		terminal_up AS (
			SELECT cur_id, merged_into_id, path
			  FROM up
			 ORDER BY depth DESC
			 LIMIT 1
		),
		root AS (
			SELECT `+canonicalRootExprSQL+` AS canonical_id
			  FROM terminal_up
		),
		family AS (
			SELECT r.canonical_id, r.canonical_id AS member_id, ARRAY[r.canonical_id] AS path, 0 AS depth
			  FROM root r
			UNION ALL
			SELECT fam.canonical_id, c.id AS member_id, fam.path || c.id, fam.depth + 1
			  FROM family fam
			  JOIN failure_signatures c ON c.merged_into_id = fam.member_id
			 WHERE NOT (c.id = ANY(fam.path))
			   AND fam.depth < `+strconv.Itoa(maxSignatureMergeHops)+`
		)
		SELECT DISTINCT
		       req.merged_into_id::text,
		       fam.canonical_id,
		       c.fingerprint,
		       c.algo_version,
		       c.error_class,
		       c.normalized_message,
		       c.top_frame,
		       c.sample_message,
		       fam.member_id,
		       mfs.first_seen_at,
		       mfs.first_seen_report_group_id
		  FROM family fam
		  JOIN failure_signatures req ON req.id = $1
		  JOIN failure_signatures c ON c.id = fam.canonical_id
		  JOIN failure_signatures mfs ON mfs.id = fam.member_id
	`, sigID)
	if err != nil {
		return signatureFamilyDetail{}, false, fmt.Errorf("load signature family %s: %w", sigID, err)
	}
	defer rows.Close()

	var (
		out      signatureFamilyDetail
		found    bool
		seenMem  = map[uuid.UUID]struct{}{}
		earliest time.Time
	)
	for rows.Next() {
		var (
			reqMergedInto     *string
			canonID, memberID uuid.UUID
			fp, normMsg       string
			algo              int
			errCls, topFrame  *string
			sampleMsg         *string
			memberFirstSeen   time.Time
			memberFirstGroup  *uuid.UUID
		)
		if err := rows.Scan(
			&reqMergedInto, &canonID, &fp, &algo, &errCls, &normMsg, &topFrame, &sampleMsg,
			&memberID, &memberFirstSeen, &memberFirstGroup,
		); err != nil {
			return signatureFamilyDetail{}, false, fmt.Errorf("scan signature family %s: %w", sigID, err)
		}
		if !found {
			found = true
			out = signatureFamilyDetail{
				CanonicalID:           canonID,
				RequestedMergedIntoID: reqMergedInto,
				Fingerprint:           fp,
				AlgoVersion:           algo,
				ErrorClass:            errCls,
				NormalizedMessage:     normMsg,
				TopFrame:              topFrame,
				SampleMessage:         sampleMsg,
				FirstSeenAt:           memberFirstSeen,
				FirstSeenGroupID:      memberFirstGroup,
				MergedSignatureIDs:    []string{},
			}
			earliest = memberFirstSeen
		} else if memberFirstSeen.Before(earliest) || (memberFirstSeen.Equal(earliest) && out.FirstSeenGroupID == nil && memberFirstGroup != nil) {
			earliest = memberFirstSeen
			out.FirstSeenAt = memberFirstSeen
			out.FirstSeenGroupID = memberFirstGroup
		}
		if _, ok := seenMem[memberID]; !ok {
			seenMem[memberID] = struct{}{}
			out.MemberIDs = append(out.MemberIDs, memberID)
			if memberID != canonID {
				out.MergedSignatureIDs = append(out.MergedSignatureIDs, memberID.String())
			}
		}
	}
	if err := rows.Err(); err != nil {
		return signatureFamilyDetail{}, false, err
	}
	if !found {
		return signatureFamilyDetail{}, false, nil
	}
	sort.Strings(out.MergedSignatureIDs)
	return out, true, nil
}

func (s *Store) loadFailureFirstSeen(ctx context.Context, firstSeenAt time.Time, groupID *uuid.UUID) (FailureFirstSeen, error) {
	fs := FailureFirstSeen{
		FirstSeenAt: firstSeenAt,
	}
	if groupID == nil || *groupID == uuid.Nil {
		return fs, nil
	}
	gidStr := groupID.String()
	fs.ReportGroupID = &gidStr

	var (
		repo, lane, rawBranch, runGroup, commitSHA, ghRunID, ghRunAttempt string
		prNum                                                             *int
	)
	err := s.Pool.QueryRow(ctx, `
		SELECT repository, name, COALESCE(branch, ''), COALESCE(run_group, ''), commit_sha,
		       COALESCE(gh_run_id, ''), COALESCE(gh_run_attempt, ''), gh_pr_number
		  FROM report_groups
		 WHERE id = $1
	`, *groupID).Scan(&repo, &lane, &rawBranch, &runGroup, &commitSHA, &ghRunID, &ghRunAttempt, &prNum)
	if errors.Is(err, pgx.ErrNoRows) {
		return fs, nil
	}
	if err != nil {
		return FailureFirstSeen{}, fmt.Errorf("load first_seen report_group: %w", err)
	}
	branch := stripBranchRefPrefix(rawBranch)
	fs.Repository = &repo
	fs.Lane = &lane
	fs.Branch = &branch
	fs.CommitSHA = &commitSHA
	fs.GHPRNumber = prNum
	fs.RunURL = buildRunURL(repo, rawBranch, commitSHA, lane, runGroup, ghRunID, ghRunAttempt)
	return fs, nil
}

func (s *Store) loadFailureLatestRunURL(ctx context.Context, memberIDs []uuid.UUID, f FilterParams) (string, error) {
	if len(memberIDs) == 0 {
		return "", nil
	}
	b := &sqlFilterBuilder{}
	sigPH := b.add(memberIDs)
	fromPH := b.add(failureHistoryStart(f))
	toPH := b.add(f.CurToDate)
	where := fmt.Sprintf(
		"gs.signature_id = ANY(%s) AND gs.occurrence_count > 0 AND gs.metric_date >= %s AND gs.metric_date <= %s%s",
		sigPH, fromPH, toPH, b.appendSignatureExecFilters("gs", f),
	)

	var repo, lane, rawBranch, runGroup, commitSHA, ghRunID, ghRunAttempt string
	err := s.Pool.QueryRow(ctx, `
		SELECT rg.repository, rg.name, COALESCE(rg.branch, ''), COALESCE(rg.run_group, ''),
		       rg.commit_sha, COALESCE(rg.gh_run_id, ''), COALESCE(rg.gh_run_attempt, '')
		  FROM kpi_group_signature_rollups gs
		  JOIN report_groups rg ON rg.id = gs.report_group_id
		 WHERE `+where+`
		 ORDER BY gs.last_seen_at DESC, rg.created_at DESC
		 LIMIT 1
	`, b.args...).Scan(&repo, &lane, &rawBranch, &runGroup, &commitSHA, &ghRunID, &ghRunAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load latest failure run url: %w", err)
	}
	u := buildRunURL(repo, rawBranch, commitSHA, lane, runGroup, ghRunID, ghRunAttempt)
	if u == "/" {
		return "", nil
	}
	return u, nil
}

func hasScopeFilters(f FilterParams) bool {
	return f.App != "" || f.Repo != "" || f.Stage != "" || f.Lane != "" || f.Platform != ""
}

func (s *Store) loadScopedFirstSeenDays(
	ctx context.Context,
	memberToCanon map[uuid.UUID]uuid.UUID,
	toDate time.Time,
	f FilterParams,
) (map[uuid.UUID]int64, error) {
	if len(memberToCanon) == 0 || !hasScopeFilters(f) {
		return nil, nil
	}
	sigIDs := make([]uuid.UUID, 0, len(memberToCanon))
	for mid := range memberToCanon {
		sigIDs = append(sigIDs, mid)
	}
	b := &sqlFilterBuilder{}
	sigPH := b.add(sigIDs)
	toPH := b.add(toDate)
	where := fmt.Sprintf("m.signature_id = ANY(%s) AND m.metric_date <= %s AND m.occurrence_count > 0%s",
		sigPH, toPH, b.appendSignatureExecFilters("m", f))

	rows, err := s.Pool.Query(ctx, `
		SELECT m.signature_id, MIN(m.metric_date)
		  FROM kpi_daily_signature_metrics m
		 WHERE `+where+`
		 GROUP BY m.signature_id
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("load scoped signature first seen dates: %w", err)
	}
	defer rows.Close()

	out := map[uuid.UUID]int64{}
	for rows.Next() {
		var sigID uuid.UUID
		var minDate time.Time
		if err := rows.Scan(&sigID, &minDate); err != nil {
			return nil, fmt.Errorf("scan scoped signature first seen date: %w", err)
		}
		cid, ok := memberToCanon[sigID]
		if !ok {
			continue
		}
		d := utcDayNum(minDate)
		if cur, exists := out[cid]; !exists || d < cur {
			out[cid] = d
		}
	}
	return out, rows.Err()
}

func (s *Store) loadDailySignatureMetrics(
	ctx context.Context,
	memberIDs []uuid.UUID,
	fromDate, toDate time.Time,
	f FilterParams,
) ([]dailySigMetricRow, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}
	b := &sqlFilterBuilder{}
	sigPH := b.add(memberIDs)
	fromPH := b.add(fromDate)
	toPH := b.add(toDate)
	where := fmt.Sprintf("m.signature_id = ANY(%s) AND m.metric_date >= %s AND m.metric_date <= %s%s",
		sigPH, fromPH, toPH, b.appendSignatureExecFilters("m", f))

	rows, err := s.Pool.Query(ctx, `
		SELECT m.metric_date, m.repository, m.app, m.lane, m.signature_id,
		       m.occurrence_count, m.raw_attempt_count, m.group_count,
		       m.affected_test_ids, m.pr_numbers, m.last_seen_at
		  FROM kpi_daily_signature_metrics m
		 WHERE `+where,
		b.args...,
	)
	if err != nil {
		return nil, fmt.Errorf("load daily signature metrics: %w", err)
	}
	defer rows.Close()

	var out []dailySigMetricRow
	for rows.Next() {
		var r dailySigMetricRow
		if err := rows.Scan(
			&r.MetricDate, &r.Repository, &r.App, &r.Lane, &r.SignatureID,
			&r.OccurrenceCount, &r.RawAttemptCount, &r.GroupCount,
			&r.AffectedTestIDs, &r.PRNumbers, &r.LastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("scan daily signature metric: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) loadDailyTestExecutions(
	ctx context.Context,
	testIDs []uuid.UUID,
	fromDate, toDate time.Time,
	f FilterParams,
) ([]dailyExecRow, error) {
	if len(testIDs) == 0 {
		return nil, nil
	}
	b := &sqlFilterBuilder{}
	testPH := b.add(testIDs)
	fromPH := b.add(fromDate)
	toPH := b.add(toDate)
	where := fmt.Sprintf("e.test_id = ANY(%s) AND e.metric_date >= %s AND e.metric_date <= %s%s",
		testPH, fromPH, toPH, b.appendSignatureExecFilters("e", f))

	rows, err := s.Pool.Query(ctx, `
		SELECT e.test_id, e.metric_date, e.repository,
		       COALESCE(SUM(e.run_count), 0)::integer,
		       COALESCE(SUM(e.fail_count), 0)::integer
		  FROM kpi_daily_test_executions e
		 WHERE `+where+`
		 GROUP BY e.test_id, e.metric_date, e.repository
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("load daily test executions: %w", err)
	}
	defer rows.Close()

	var out []dailyExecRow
	for rows.Next() {
		var r dailyExecRow
		if err := rows.Scan(&r.TestID, &r.MetricDate, &r.Repository, &r.RunCount, &r.FailCount); err != nil {
			return nil, fmt.Errorf("scan daily test execution: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) loadAffectedTestsMetadata(
	ctx context.Context,
	testIDs []uuid.UUID,
	groupCounts map[uuid.UUID]int,
	runCounts map[uuid.UUID]int,
	failCounts map[uuid.UUID]int,
	lastSeenMap map[uuid.UUID]time.Time,
) ([]FailureAffectedTest, error) {
	if len(testIDs) == 0 {
		return []FailureAffectedTest{}, nil
	}
	selected := testIDs
	if len(selected) > maxFailureDetailAffectedTests {
		selected = append([]uuid.UUID(nil), testIDs...)
		sort.Slice(selected, func(i, j int) bool {
			gi, gj := groupCounts[selected[i]], groupCounts[selected[j]]
			if gi != gj {
				return gi > gj
			}
			ri, rj := runCounts[selected[i]], runCounts[selected[j]]
			if ri != rj {
				return ri > rj
			}
			return selected[i].String() < selected[j].String()
		})
		selected = selected[:maxFailureDetailAffectedTests]
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, test_key, zephyr_key, file, full_title, repository, framework
		  FROM tests
		 WHERE id = ANY($1)
	`, selected)
	if err != nil {
		return nil, fmt.Errorf("load affected tests metadata: %w", err)
	}
	defer rows.Close()

	out := make([]FailureAffectedTest, 0, len(testIDs))
	for rows.Next() {
		var (
			id                                 uuid.UUID
			testKey, file, fullTitle, repo, fw string
			zephyr                             *string
		)
		if err := rows.Scan(&id, &testKey, &zephyr, &file, &fullTitle, &repo, &fw); err != nil {
			return nil, fmt.Errorf("scan affected test metadata: %w", err)
		}
		out = append(out, FailureAffectedTest{
			TestID:     id.String(),
			TestKey:    testKey,
			ZephyrKey:  zephyr,
			File:       file,
			FullTitle:  fullTitle,
			Repository: repo,
			Framework:  fw,
			GroupCount: groupCounts[id],
			RunCount:   runCounts[id],
			FailCount:  failCounts[id],
			LastSeenAt: lastSeenMap[id],
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupCount != out[j].GroupCount {
			return out[i].GroupCount > out[j].GroupCount
		}
		if out[i].RunCount != out[j].RunCount {
			return out[i].RunCount > out[j].RunCount
		}
		if out[i].FullTitle != out[j].FullTitle {
			return out[i].FullTitle < out[j].FullTitle
		}
		return out[i].TestID < out[j].TestID
	})
	return out, nil
}

func appendVerdictScopeFilters(b *sqlFilterBuilder, f FilterParams) string {
	var sb strings.Builder
	if f.Repo != "" {
		ph := b.add(f.Repo)
		if strings.Contains(f.Repo, "/") {
			fmt.Fprintf(&sb, " AND lower(v.repository) = lower(%s)", ph)
		} else {
			fmt.Fprintf(&sb, " AND (lower(v.repository) = lower(%s) OR lower(split_part(v.repository, '/', 2)) = lower(%s))", ph, ph)
		}
	}
	if f.App != "" {
		ph := b.add(f.App)
		fmt.Fprintf(&sb, ` AND COALESCE(
			(SELECT ap.app FROM apps ap WHERE lower(ap.repository) = lower(v.repository) ORDER BY (ap.repository = v.repository) DESC LIMIT 1),
			NULLIF(split_part(v.repository, '/', 2), ''),
			v.repository
		) = %s`, ph)
	}
	return sb.String()
}

func (s *Store) loadLatestVerdictLabelsBatch(
	ctx context.Context,
	memberToCanon map[uuid.UUID]uuid.UUID,
	f FilterParams,
) (map[uuid.UUID]string, error) {
	if len(memberToCanon) == 0 {
		return map[uuid.UUID]string{}, nil
	}
	sigIDs := make([]uuid.UUID, 0, len(memberToCanon))
	canonIDs := make([]uuid.UUID, 0, len(memberToCanon))
	for mid, cid := range memberToCanon {
		sigIDs = append(sigIDs, mid)
		canonIDs = append(canonIDs, cid)
	}

	b := &sqlFilterBuilder{}
	sigPH := b.add(sigIDs)
	canonPH := b.add(canonIDs)
	scopeWhere := appendVerdictScopeFilters(b, f)

	// error_context is deliberately omitted from SELECT so it can never leak.
	rows, err := s.Pool.Query(ctx, `
		WITH sig_map AS (
			SELECT * FROM UNNEST(`+sigPH+`::uuid[], `+canonPH+`::uuid[]) AS m(sig_id, canonical_id)
		)
		SELECT DISTINCT ON (sm.canonical_id)
		       sm.canonical_id,
		       v.label
		  FROM verdicts v
		  JOIN sig_map sm ON sm.sig_id = v.signature_id
		 WHERE v.superseded_at IS NULL`+scopeWhere+`
		 ORDER BY sm.canonical_id, v.created_at DESC, v.id DESC
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("load latest verdict labels: %w", err)
	}
	defer rows.Close()

	out := map[uuid.UUID]string{}
	for rows.Next() {
		var cid uuid.UUID
		var label string
		if err := rows.Scan(&cid, &label); err != nil {
			return nil, fmt.Errorf("scan latest verdict label: %w", err)
		}
		out[cid] = label
	}
	return out, rows.Err()
}

func (s *Store) loadLatestFailureVerdict(
	ctx context.Context,
	memberIDs []uuid.UUID,
	f FilterParams,
) (*FailureVerdict, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}
	b := &sqlFilterBuilder{}
	sigPH := b.add(memberIDs)
	scopeWhere := appendVerdictScopeFilters(b, f)

	// error_context is deliberately omitted from SELECT so it can never leak.
	var v FailureVerdict
	err := s.Pool.QueryRow(ctx, `
		SELECT v.id::text, v.label, v.decided_by, v.rule, v.confidence::float8,
		       v.summary, v.model, v.triage_url, v.created_at
		  FROM verdicts v
		 WHERE v.signature_id = ANY(`+sigPH+`)
		   AND v.superseded_at IS NULL`+scopeWhere+`
		 ORDER BY v.created_at DESC, v.id DESC
		 LIMIT 1
	`, b.args...).Scan(
		&v.ID, &v.Label, &v.DecidedBy, &v.Rule, &v.Confidence,
		&v.Summary, &v.Model, &v.TriageURL, &v.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load latest failure verdict: %w", err)
	}
	return &v, nil
}

// computeFailureState evaluates Active / Resurfaced / Quiet / Not enough runs
// (never "Fixed") and quiet_days from daily occurrence and execution counts.
func computeFailureState(
	todayDay int64,
	hasOcc bool,
	firstSeenDay int64,
	occByDay map[int64]int,
	runsByDay map[int64]int,
) (string, *int) {
	trailing7dStart := todayDay - (StateWindowDays - 1)
	occ7d := sumDays(occByDay, trailing7dStart, todayDay)
	runs7d := sumDays(runsByDay, trailing7dStart, todayDay)

	if occ7d == 0 {
		var quietDays *int
		if hasOcc {
			var lastOccDay int64
			foundLast := false
			for d, cnt := range occByDay {
				if cnt > 0 && d <= todayDay && (!foundLast || d > lastOccDay) {
					lastOccDay = d
					foundLast = true
				}
			}
			if foundLast {
				qd := int(todayDay - lastOccDay)
				quietDays = &qd
			}
		}
		if runs7d >= QuietRunThreshold {
			return FailureStateQuiet, quietDays
		}
		return FailureStateNotEnoughRuns, quietDays
	}

	// occ7d > 0: check whether a 7-consecutive-day Quiet window (0 occurrences and >= 500 runs)
	// existed strictly after firstSeenDay and strictly before the latest occurrence in trailing 7d.
	var maxRecurrenceDay int64
	for d := trailing7dStart; d <= todayDay; d++ {
		if occByDay[d] > 0 {
			maxRecurrenceDay = d
		}
	}

	scanStart := firstSeenDay + 1
	if minBound := todayDay - failureHistoryMaxDays; scanStart < minBound {
		scanStart = minBound
	}
	lastWindowStart := maxRecurrenceDay - StateWindowDays
	if scanStart <= lastWindowStart {
		// Initialize sliding 7-day sums over [scanStart, scanStart + StateWindowDays - 1].
		var winOcc, winRuns int
		for d := scanStart; d < scanStart+StateWindowDays; d++ {
			winOcc += occByDay[d]
			winRuns += runsByDay[d]
		}
		for wStart := scanStart; wStart <= lastWindowStart; wStart++ {
			if winOcc == 0 && winRuns >= QuietRunThreshold {
				return FailureStateResurfaced, nil
			}
			winOcc -= occByDay[wStart]
			winRuns -= runsByDay[wStart]
			nextDay := wStart + StateWindowDays
			winOcc += occByDay[nextDay]
			winRuns += runsByDay[nextDay]
		}
	}

	return FailureStateActive, nil
}

func sortRecurringFailures(rows []RecurringFailureRow, sortBy string) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch sortBy {
		case SortRateDesc:
			if cmp, decided := cmpFloatPtrDescNullsLast(a.RatePer1k, b.RatePer1k); decided {
				return cmp
			}
		case SortTrendDesc:
			if cmp, decided := cmpFloatPtrDescNullsLast(a.DeltaRatePer1k, b.DeltaRatePer1k); decided {
				return cmp
			}
			if cmp, decided := cmpFloatPtrDescNullsLast(a.RatePer1k14d, b.RatePer1k14d); decided {
				return cmp
			}
		case SortLastSeenDesc:
			if !a.LastSeenAt.Equal(b.LastSeenAt) {
				return a.LastSeenAt.After(b.LastSeenAt)
			}
		case SortNameAsc:
			if a.NormalizedMessage != b.NormalizedMessage {
				return a.NormalizedMessage < b.NormalizedMessage
			}
		}

		if a.OccurrenceCount != b.OccurrenceCount {
			return a.OccurrenceCount > b.OccurrenceCount
		}
		if cmp, decided := cmpFloatPtrDescNullsLast(a.RatePer1k, b.RatePer1k); decided {
			return cmp
		}
		if !a.LastSeenAt.Equal(b.LastSeenAt) {
			return a.LastSeenAt.After(b.LastSeenAt)
		}
		return a.SignatureID < b.SignatureID
	})
}

func cmpFloatPtrDescNullsLast(a, b *float64) (less bool, decided bool) {
	if (a == nil) != (b == nil) {
		return a != nil, true
	}
	if a != nil && *a != *b {
		return *a > *b, true
	}
	return false, false
}

func ratePer1kPtr(occurrences, affectedRuns int) *float64 {
	if affectedRuns <= 0 {
		return nil
	}
	v := float64(occurrences) * 1000.0 / float64(affectedRuns)
	return &v
}

func utcDayNum(t time.Time) int64 {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400
}

func sumDays(m map[int64]int, fromDay, toDay int64) int {
	total := 0
	for d := fromDay; d <= toDay; d++ {
		total += m[d]
	}
	return total
}

func mapKeysUUID(m map[uuid.UUID]struct{}) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	return out
}

func sortedSetStrings(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortedSetInts(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}
