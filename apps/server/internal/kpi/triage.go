package kpi

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type dailyTriageAgg struct {
	totalRuns              int
	completedRuns          int
	incompleteRuns         int
	inProgressRuns         int
	failedRunsCount        int
	triagedRunsCount       int
	failedTestOccurrences  int
	triagedTestOccurrences int
	verdictsTotal          int
	verdictsReused         int
}

type timedOutSplit struct {
	total      int
	completed  int
	incomplete int
	inProgress int
}

func (s *Store) queryTriageSummary(ctx context.Context, f FilterParams) (*TriageSummary, error) {
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)

	var where strings.Builder
	fmt.Fprintf(&where, "m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("m", f, true))
	where.WriteString(appendPlatformScope(b, "m", f.Platform))

	var agg dailyTriageAgg
	err := s.Pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(m.total_runs), 0)::integer,
			COALESCE(SUM(m.completed_runs), 0)::integer,
			COALESCE(SUM(m.incomplete_runs), 0)::integer,
			COALESCE(SUM(m.in_progress_runs), 0)::integer,
			COALESCE(SUM(m.failed_runs_count), 0)::integer,
			COALESCE(SUM(m.triaged_runs_count), 0)::integer,
			COALESCE(SUM(m.failed_test_occurrences), 0)::integer,
			COALESCE(SUM(m.triaged_test_occurrences), 0)::integer,
			COALESCE(SUM(m.verdicts_total), 0)::integer,
			COALESCE(SUM(m.verdicts_reused), 0)::integer
		  FROM kpi_daily_lane_metrics m
		 WHERE `+where.String(),
		b.args...,
	).Scan(
		&agg.totalRuns, &agg.completedRuns, &agg.incompleteRuns, &agg.inProgressRuns,
		&agg.failedRunsCount, &agg.triagedRunsCount,
		&agg.failedTestOccurrences, &agg.triagedTestOccurrences,
		&agg.verdictsTotal, &agg.verdictsReused,
	)
	if err != nil {
		return nil, fmt.Errorf("query triage summary: %w", err)
	}

	// Query kpi_group_rollups only for the explicit orch_status='timed_out' split
	// so timed-out runs are neither double-counted nor dropped from completion_rate.
	bt := &sqlFilterBuilder{}
	tFromPH := bt.add(f.CurFromDate)
	tToPH := bt.add(f.CurToDate)
	var tWhere strings.Builder
	fmt.Fprintf(&tWhere, "gr.orch_status = 'timed_out' AND gr.metric_date >= %s AND gr.metric_date <= %s", tFromPH, tToPH)
	tWhere.WriteString(bt.appendCommonFilters("gr", f, true))
	tWhere.WriteString(appendPlatformScope(bt, "gr", f.Platform))

	var toSplit timedOutSplit
	err = s.Pool.QueryRow(ctx, `
		SELECT
			COUNT(*)::integer,
			COUNT(*) FILTER (WHERE gr.is_completed)::integer,
			COUNT(*) FILTER (WHERE gr.group_status = 'incomplete')::integer,
			COUNT(*) FILTER (WHERE gr.group_status = 'in_progress')::integer
		  FROM kpi_group_rollups gr
		 WHERE `+tWhere.String(),
		bt.args...,
	).Scan(&toSplit.total, &toSplit.completed, &toSplit.incomplete, &toSplit.inProgress)
	if err != nil {
		return nil, fmt.Errorf("query timed out runs split: %w", err)
	}

	labelCounts, err := s.queryVerdictLabelMix(ctx, where.String(), b.args)
	if err != nil {
		return nil, err
	}

	summary := buildTriageSummary(agg, toSplit, labelCounts)
	return &summary, nil
}

func (s *Store) queryVerdictLabelMix(ctx context.Context, where string, args []any) (map[string]int, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.key, COALESCE(SUM(e.value::bigint), 0)::integer
		  FROM kpi_daily_lane_metrics m
		 CROSS JOIN LATERAL jsonb_each_text(m.verdict_label_counts) AS e(key, value)
		 WHERE `+where+`
		 GROUP BY e.key
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query verdict label mix: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var label string
		var cnt int
		if err := rows.Scan(&label, &cnt); err != nil {
			return nil, fmt.Errorf("scan verdict label mix: %w", err)
		}
		if cnt > 0 {
			counts[label] += cnt
		}
	}
	return counts, rows.Err()
}

func buildTriageSummary(agg dailyTriageAgg, toSplit timedOutSplit, rawLabels map[string]int) TriageSummary {
	totalLabels := 0
	for _, cnt := range rawLabels {
		if cnt > 0 {
			totalLabels += cnt
		}
	}
	labelMix := make([]TriageLabelMix, 0, len(rawLabels))
	for label, cnt := range rawLabels {
		if cnt <= 0 || totalLabels <= 0 {
			continue
		}
		labelMix = append(labelMix, TriageLabelMix{
			Label: label,
			Count: cnt,
			Share: float64(cnt) * 100.0 / float64(totalLabels),
		})
	}
	sort.Slice(labelMix, func(i, j int) bool {
		if labelMix[i].Count != labelMix[j].Count {
			return labelMix[i].Count > labelMix[j].Count
		}
		return labelMix[i].Label < labelMix[j].Label
	})

	completed := max(0, agg.completedRuns-toSplit.completed)
	incomplete := max(0, agg.incompleteRuns-toSplit.incomplete)
	inProgress := max(0, agg.inProgressRuns-toSplit.inProgress)
	denom := agg.totalRuns - inProgress

	return TriageSummary{
		RunCoverageRate:        pctPtr(agg.triagedRunsCount, agg.failedRunsCount),
		TestCoverageRate:       pctPtr(agg.triagedTestOccurrences, agg.failedTestOccurrences),
		FailedRunsCount:        agg.failedRunsCount,
		TriagedRunsCount:       agg.triagedRunsCount,
		FailedTestOccurrences:  agg.failedTestOccurrences,
		TriagedTestOccurrences: agg.triagedTestOccurrences,
		VerdictReuseRate:       pctPtr(agg.verdictsReused, agg.verdictsTotal),
		VerdictsTotal:          agg.verdictsTotal,
		VerdictsReused:         agg.verdictsReused,
		LabelMix:               labelMix,
		RunCompletion: RunCompletionSummary{
			TotalRuns:      agg.totalRuns,
			CompletedRuns:  completed,
			IncompleteRuns: incomplete,
			InProgressRuns: inProgress,
			TimedOutRuns:   toSplit.total,
			CompletionRate: pctPtr(completed, denom),
		},
	}
}

func (s *Store) queryWeeklyTriageCoverage(ctx context.Context, f FilterParams, weeks []time.Time) (map[string]*float64, error) {
	out := make(map[string]*float64, len(weeks))
	if len(weeks) == 0 {
		return out, nil
	}

	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)

	var where strings.Builder
	fmt.Fprintf(&where, "m.metric_date >= %s AND m.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("m", f, true))
	where.WriteString(appendPlatformScope(b, "m", f.Platform))

	rows, err := s.Pool.Query(ctx, `
		SELECT
			date_trunc('week', m.metric_date::timestamp)::date AS week_start,
			COALESCE(SUM(m.failed_runs_count), 0)::integer,
			COALESCE(SUM(m.triaged_runs_count), 0)::integer
		  FROM kpi_daily_lane_metrics m
		 WHERE `+where.String()+`
		 GROUP BY week_start
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("query weekly triage coverage: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ws time.Time
		var failedRuns, triagedRuns int
		if err := rows.Scan(&ws, &failedRuns, &triagedRuns); err != nil {
			return nil, fmt.Errorf("scan weekly triage coverage: %w", err)
		}
		out[ws.UTC().Format("2006-01-02")] = pctPtr(triagedRuns, failedRuns)
	}
	return out, rows.Err()
}
