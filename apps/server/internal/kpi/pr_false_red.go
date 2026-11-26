package kpi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	settledAfterHours    = 24
	rateQualifierAtLeast = "at least"
)

type e2eLaneEntry struct {
	repo string
	lane string
}

func parseConfiguredE2ELanes(raw []string) []e2eLaneEntry {
	if len(raw) == 0 {
		return nil
	}
	seen := map[e2eLaneEntry]struct{}{}
	out := make([]e2eLaneEntry, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		var entry e2eLaneEntry
		if idx := strings.Index(trimmed, ":"); idx >= 0 {
			entry.repo = strings.TrimSpace(trimmed[:idx])
			entry.lane = strings.TrimSpace(trimmed[idx+1:])
		} else {
			entry.lane = trimmed
		}
		if entry.lane == "" {
			continue
		}
		key := e2eLaneEntry{repo: strings.ToLower(entry.repo), lane: entry.lane}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func appendE2ELanesJoin(b *sqlFilterBuilder, alias string, override []e2eLaneEntry) string {
	if len(override) == 0 {
		return fmt.Sprintf(`
			JOIN kpi_e2e_lanes el
			  ON lower(el.repository) = lower(%s.repository)
			 AND el.lane = %s.lane
			 AND el.enabled = true`, alias, alias)
	}
	repos := make([]string, len(override))
	lanes := make([]string, len(override))
	for i, e := range override {
		repos[i] = e.repo
		lanes[i] = e.lane
	}
	repoPH := b.add(repos)
	lanePH := b.add(lanes)
	return fmt.Sprintf(`
		JOIN (
			SELECT DISTINCT e.repo, e.lane
			  FROM UNNEST(%s::text[], %s::text[]) AS e(repo, lane)
		) el
		  ON (el.repo = '' OR lower(el.repo) = lower(%s.repository))
		 AND el.lane = %s.lane`, repoPH, lanePH, alias, alias)
}

func (s *Store) effectiveRefNow(f FilterParams) time.Time {
	if !f.To.IsZero() {
		return f.To.UTC()
	}
	return s.now()
}

// QueryPRsRed computes GET /api/v1/kpi/prs/red strictly from pre-aggregated rollup tables.
func (s *Store) QueryPRsRed(ctx context.Context, f FilterParams) (PRRedResponse, error) {
	if s == nil || s.Pool == nil {
		return PRRedResponse{}, errors.New("kpi store pool is nil")
	}

	refNow := s.effectiveRefNow(f)
	settledCutoff := refNow.Add(-settledAfterHours * time.Hour)

	var notes []string
	if f.Stage != "" && f.Stage != "pr" {
		notes = append(notes, fmt.Sprintf("stage=%q filter ignored for KPI 2: pr_false_red always evaluates PR lanes (gh_pr_number > 0)", f.Stage))
	}

	cov, err := s.queryPRLaneCoverage(ctx, f, notes)
	if err != nil {
		return PRRedResponse{}, err
	}

	configuredLanes, err := s.queryConfiguredE2ELanes(ctx, f)
	if err != nil {
		return PRRedResponse{}, err
	}

	curAgg, err := s.queryPRRedTotals(ctx, f, f.CurFromDate, f.CurToDate, settledCutoff)
	if err != nil {
		return PRRedResponse{}, err
	}

	priorFalseRed, priorSettledRed, err := s.queryPRFalseRedRateForDates(ctx, f, f.PriorFromDate, f.PriorToDate, settledCutoff)
	if err != nil {
		return PRRedResponse{}, err
	}

	lanes, err := s.queryRedPRLaneRows(ctx, f, refNow, settledCutoff)
	if err != nil {
		return PRRedResponse{}, err
	}

	curRate := pctPtr(curAgg.settledFalseRed, curAgg.settledRed)
	priorRate := pctPtr(priorFalseRed, priorSettledRed)

	total := curAgg.settledRed + curAgg.unsettledRed
	totalPages := 0
	if total > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(f.Limit)))
	}

	return PRRedResponse{
		Filters:              f,
		WindowDays:           f.WindowDays,
		SettledAfterHours:    settledAfterHours,
		IsLowerBound:         true,
		RateQualifier:        rateQualifierAtLeast,
		FalseRedRate:         curRate,
		PriorFalseRedRate:    priorRate,
		DeltaPP:              diffFloatPtr(curRate, priorRate),
		SettledRedLanes:      curAgg.settledRed,
		SettledFalseRedLanes: curAgg.settledFalseRed,
		UnsettledRedLanes:    curAgg.unsettledRed,
		StillRedLanes:        curAgg.stillRed,
		ReasonMix:            curAgg.reasonMix,
		ConfiguredE2ELanes:   configuredLanes,
		LaneCoverage:         cov,
		Pagination: Pagination{
			Page:       f.Page,
			Limit:      f.Limit,
			Total:      total,
			TotalPages: totalPages,
		},
		Lanes: lanes,
	}, nil
}

func (s *Store) queryPRFalseRedSummary(ctx context.Context, f FilterParams, extraNotes []string) (PRFalseRedSummary, error) {
	refNow := s.effectiveRefNow(f)
	settledCutoff := refNow.Add(-settledAfterHours * time.Hour)

	cov, err := s.queryPRLaneCoverage(ctx, f, extraNotes)
	if err != nil {
		return PRFalseRedSummary{}, err
	}

	override := parseConfiguredE2ELanes(s.E2ELanes)
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	cutoffPH := b.add(settledCutoff)
	joinSQL := appendE2ELanesJoin(b, "gr", override)

	var where strings.Builder
	fmt.Fprintf(&where, "gr.gh_pr_number IS NOT NULL AND gr.gh_pr_number > 0 AND gr.is_red = true AND gr.metric_date >= %s AND gr.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("gr", f, false))
	where.WriteString(appendPlatformScope(b, "gr", f.Platform))

	rows, err := s.Pool.Query(ctx, `
		WITH red_lanes AS (
			SELECT
				gr.app,
				gr.repository,
				gr.lane,
				gr.has_orchestration,
				(gr.group_created_at < `+cutoffPH+`) AS settled,
				`+rerunGreenExpr("gr")+` AS went_green_on_rerun,
				`+allSigsFailingElsewhereExpr("gr")+` AS all_signatures_failing_elsewhere
			  FROM kpi_group_rollups gr`+joinSQL+`
			 WHERE `+where.String()+`
		)
		SELECT
			MAX(app) AS app,
			repository,
			lane,
			COUNT(*) FILTER (WHERE settled)::integer AS settled_red_lanes,
			COUNT(*) FILTER (WHERE settled AND went_green_on_rerun)::integer AS settled_false_red_lanes,
			COUNT(*) FILTER (WHERE NOT settled)::integer AS unsettled_red_lanes,
			COUNT(*) FILTER (WHERE settled AND went_green_on_rerun)::integer AS went_green_on_rerun,
			COUNT(*) FILTER (WHERE settled AND NOT went_green_on_rerun AND all_signatures_failing_elsewhere)::integer AS failing_elsewhere,
			COUNT(*) FILTER (WHERE settled AND NOT went_green_on_rerun AND NOT all_signatures_failing_elsewhere)::integer AS other,
			COUNT(*) FILTER (WHERE has_orchestration)::integer AS orchestrated_runs,
			COUNT(*) FILTER (WHERE NOT has_orchestration)::integer AS upload_only_runs
		  FROM red_lanes
		 GROUP BY repository, lane
		 ORDER BY lane ASC, app ASC, repository ASC
	`, b.args...)
	if err != nil {
		return PRFalseRedSummary{}, fmt.Errorf("query pr false-red summary: %w", err)
	}
	defer rows.Close()

	byLane := []PRFalseRedLaneRow{}
	var settledRed, settledFalseRed, unsettledRed int
	var mix PRFalseRedReasonMix
	for rows.Next() {
		var r PRFalseRedLaneRow
		if err := rows.Scan(
			&r.App, &r.Repository, &r.Lane,
			&r.SettledRedLanes, &r.SettledFalseRedLanes, &r.UnsettledRedLanes,
			&r.WentGreenOnRerun, &r.FailingElsewhere, &r.Other,
			&r.OrchestratedRuns, &r.UploadOnlyRuns,
		); err != nil {
			return PRFalseRedSummary{}, fmt.Errorf("scan pr false-red summary row: %w", err)
		}
		r.FalseRedRate = pctPtr(r.SettledFalseRedLanes, r.SettledRedLanes)
		settledRed += r.SettledRedLanes
		settledFalseRed += r.SettledFalseRedLanes
		unsettledRed += r.UnsettledRedLanes
		mix.WentGreenOnRerun += r.WentGreenOnRerun
		mix.FailingElsewhere += r.FailingElsewhere
		mix.Other += r.Other
		mix.Total += r.SettledRedLanes
		byLane = append(byLane, r)
	}
	if err := rows.Err(); err != nil {
		return PRFalseRedSummary{}, err
	}

	priorFalseRed, priorSettledRed, err := s.queryPRFalseRedRateForDates(ctx, f, f.PriorFromDate, f.PriorToDate, settledCutoff)
	if err != nil {
		return PRFalseRedSummary{}, err
	}

	curRate := pctPtr(settledFalseRed, settledRed)
	priorRate := pctPtr(priorFalseRed, priorSettledRed)

	return PRFalseRedSummary{
		IsLowerBound:         true,
		RateQualifier:        rateQualifierAtLeast,
		SettledAfterHours:    settledAfterHours,
		FalseRedRate:         curRate,
		PriorFalseRedRate:    priorRate,
		DeltaPP:              diffFloatPtr(curRate, priorRate),
		SettledRedLanes:      settledRed,
		SettledFalseRedLanes: settledFalseRed,
		UnsettledRedLanes:    unsettledRed,
		ReasonMix:            mix,
		ByLane:               byLane,
		LaneCoverage:         cov,
	}, nil
}

type prRedTotals struct {
	settledRed      int
	settledFalseRed int
	unsettledRed    int
	stillRed        int
	reasonMix       PRFalseRedReasonMix
}

func (s *Store) queryPRRedTotals(
	ctx context.Context,
	f FilterParams,
	fromDate, toDate, settledCutoff time.Time,
) (prRedTotals, error) {
	override := parseConfiguredE2ELanes(s.E2ELanes)
	b := &sqlFilterBuilder{}
	fromPH := b.add(fromDate)
	toPH := b.add(toDate)
	cutoffPH := b.add(settledCutoff)
	joinSQL := appendE2ELanesJoin(b, "gr", override)

	var where strings.Builder
	fmt.Fprintf(&where, "gr.gh_pr_number IS NOT NULL AND gr.gh_pr_number > 0 AND gr.is_red = true AND gr.metric_date >= %s AND gr.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("gr", f, false))
	where.WriteString(appendPlatformScope(b, "gr", f.Platform))

	var t prRedTotals
	err := s.Pool.QueryRow(ctx, `
		WITH red_lanes AS (
			SELECT
				(gr.group_created_at < `+cutoffPH+`) AS settled,
				`+rerunGreenExpr("gr")+` AS went_green_on_rerun,
				`+allSigsFailingElsewhereExpr("gr")+` AS all_signatures_failing_elsewhere,
				`+stillRedExpr("gr")+` AS still_red
			  FROM kpi_group_rollups gr`+joinSQL+`
			 WHERE `+where.String()+`
		)
		SELECT
			COUNT(*) FILTER (WHERE settled)::integer,
			COUNT(*) FILTER (WHERE settled AND went_green_on_rerun)::integer,
			COUNT(*) FILTER (WHERE NOT settled)::integer,
			COUNT(*) FILTER (WHERE still_red)::integer,
			COUNT(*) FILTER (WHERE settled AND went_green_on_rerun)::integer,
			COUNT(*) FILTER (WHERE settled AND NOT went_green_on_rerun AND all_signatures_failing_elsewhere)::integer,
			COUNT(*) FILTER (WHERE settled AND NOT went_green_on_rerun AND NOT all_signatures_failing_elsewhere)::integer
		  FROM red_lanes
	`, b.args...).Scan(
		&t.settledRed,
		&t.settledFalseRed,
		&t.unsettledRed,
		&t.stillRed,
		&t.reasonMix.WentGreenOnRerun,
		&t.reasonMix.FailingElsewhere,
		&t.reasonMix.Other,
	)
	if err != nil {
		return prRedTotals{}, fmt.Errorf("query pr red totals: %w", err)
	}
	t.reasonMix.Total = t.settledRed
	return t, nil
}

func (s *Store) queryPRFalseRedRateForDates(
	ctx context.Context,
	f FilterParams,
	fromDate, toDate, settledCutoff time.Time,
) (falseRed, settledRed int, err error) {
	override := parseConfiguredE2ELanes(s.E2ELanes)
	b := &sqlFilterBuilder{}
	fromPH := b.add(fromDate)
	toPH := b.add(toDate)
	cutoffPH := b.add(settledCutoff)
	joinSQL := appendE2ELanesJoin(b, "gr", override)

	var where strings.Builder
	fmt.Fprintf(&where, "gr.gh_pr_number IS NOT NULL AND gr.gh_pr_number > 0 AND gr.is_red = true AND gr.metric_date >= %s AND gr.metric_date <= %s AND gr.group_created_at < %s", fromPH, toPH, cutoffPH)
	where.WriteString(b.appendCommonFilters("gr", f, false))
	where.WriteString(appendPlatformScope(b, "gr", f.Platform))

	err = s.Pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE `+rerunGreenExpr("gr")+`)::integer,
			COUNT(*)::integer
		  FROM kpi_group_rollups gr`+joinSQL+`
		 WHERE `+where.String(),
		b.args...,
	).Scan(&falseRed, &settledRed)
	if err != nil {
		return 0, 0, fmt.Errorf("query pr false-red rate: %w", err)
	}
	return falseRed, settledRed, nil
}

func (s *Store) queryRedPRLaneRows(
	ctx context.Context,
	f FilterParams,
	refNow, settledCutoff time.Time,
) ([]RedPRLaneRow, error) {
	override := parseConfiguredE2ELanes(s.E2ELanes)
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	cutoffPH := b.add(settledCutoff)
	joinSQL := appendE2ELanesJoin(b, "gr", override)

	var where strings.Builder
	fmt.Fprintf(&where, "gr.gh_pr_number IS NOT NULL AND gr.gh_pr_number > 0 AND gr.is_red = true AND gr.metric_date >= %s AND gr.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("gr", f, false))
	where.WriteString(appendPlatformScope(b, "gr", f.Platform))

	orderBy := prRedOrderByClause(f.Sort)
	limitPH := b.add(f.Limit)
	offsetPH := b.add((f.Page - 1) * f.Limit)

	rows, err := s.Pool.Query(ctx, `
		WITH base AS (
			SELECT
				gr.report_group_id,
				gr.repository,
				gr.app,
				gr.lane,
				COALESCE(rg.branch, '') AS branch,
				COALESCE(rg.run_group, '') AS run_group,
				gr.gh_pr_number,
				gr.commit_sha,
				gr.gh_run_id,
				gr.gh_run_attempt,
				NULLIF(gr.platform, '') AS platform,
				gr.framework,
				gr.has_orchestration,
				gr.orch_status,
				gr.group_created_at,
				(gr.group_created_at < `+cutoffPH+`) AS settled,
				`+stillRedExpr("gr")+` AS still_red,
				`+rerunGreenExpr("gr")+` AS went_green_on_rerun,
				sa.failing_on_main,
				sa.other_prs_count,
				sa.all_signatures_failing_elsewhere,
				sa.failed_signature_count,
				gr.tests_failed
			  FROM kpi_group_rollups gr`+joinSQL+`
			  JOIN report_groups rg ON rg.id = gr.report_group_id
			  LEFT JOIN LATERAL (
			      WITH lane_sigs AS (
			          SELECT DISTINCT gs.signature_id
			            FROM kpi_group_signature_rollups gs
			           WHERE gs.report_group_id = gr.report_group_id
			             AND gs.has_failed_test = true
			             AND (gr.is_all_platforms OR gs.platform = gr.platform)
			      ),
			      sig_checks AS (
			          SELECT
			              ls.signature_id,
			              EXISTS (
			                  SELECT 1
			                    FROM kpi_group_signature_rollups gs2
			                    JOIN kpi_group_rollups gr2
			                      ON gr2.report_group_id = gs2.report_group_id
			                     AND gr2.is_all_platforms = true
			                   WHERE gs2.has_failed_test = true
			                     AND gs2.repository = gr.repository
			                     AND gs2.signature_id = ls.signature_id
			                     AND gs2.stage = 'main'
			                     AND gs2.metric_date >= ((gr.group_created_at - interval '24 hours') AT TIME ZONE 'UTC')::date - 1
			                     AND gs2.metric_date <= ((gr.group_created_at + interval '24 hours') AT TIME ZONE 'UTC')::date + 1
			                     AND gr2.group_created_at BETWEEN gr.group_created_at - interval '24 hours'
			                                                  AND gr.group_created_at + interval '24 hours'
			              ) AS on_main_24h,
			              (
			                  SELECT COUNT(DISTINCT gs2.gh_pr_number)::integer
			                    FROM kpi_group_signature_rollups gs2
			                    JOIN kpi_group_rollups gr2
			                      ON gr2.report_group_id = gs2.report_group_id
			                     AND gr2.is_all_platforms = true
			                   WHERE gs2.has_failed_test = true
			                     AND gs2.repository = gr.repository
			                     AND gs2.signature_id = ls.signature_id
			                     AND gs2.gh_pr_number IS NOT NULL
			                     AND gs2.gh_pr_number > 0
			                     AND gs2.gh_pr_number <> gr.gh_pr_number
			                     AND gs2.metric_date >= ((gr.group_created_at - interval '24 hours') AT TIME ZONE 'UTC')::date - 1
			                     AND gs2.metric_date <= ((gr.group_created_at + interval '24 hours') AT TIME ZONE 'UTC')::date + 1
			                     AND gr2.group_created_at BETWEEN gr.group_created_at - interval '24 hours'
			                                                  AND gr.group_created_at + interval '24 hours'
			              ) AS other_prs_24h
			            FROM lane_sigs ls
			      )
			      SELECT
			          COUNT(*)::integer AS failed_signature_count,
			          COALESCE(BOOL_OR(sc.on_main_24h), false) AS failing_on_main,
			          (
			              SELECT COUNT(DISTINCT gs2.gh_pr_number)::integer
			                FROM lane_sigs ls
			                JOIN kpi_group_signature_rollups gs2
			                  ON gs2.has_failed_test = true
			                 AND gs2.repository = gr.repository
			                 AND gs2.signature_id = ls.signature_id
			                 AND gs2.gh_pr_number IS NOT NULL
			                 AND gs2.gh_pr_number > 0
			                 AND gs2.gh_pr_number <> gr.gh_pr_number
			                 AND gs2.metric_date >= ((gr.group_created_at - interval '24 hours') AT TIME ZONE 'UTC')::date - 1
			                 AND gs2.metric_date <= ((gr.group_created_at + interval '24 hours') AT TIME ZONE 'UTC')::date + 1
			                JOIN kpi_group_rollups gr2
			                  ON gr2.report_group_id = gs2.report_group_id
			                 AND gr2.is_all_platforms = true
			               WHERE gr2.group_created_at BETWEEN gr.group_created_at - interval '24 hours'
			                                            AND gr.group_created_at + interval '24 hours'
			          ) AS other_prs_count,
			          COALESCE(COUNT(*) > 0 AND BOOL_AND(sc.on_main_24h OR sc.other_prs_24h >= 3), false) AS all_signatures_failing_elsewhere
			        FROM sig_checks sc
			  ) sa ON true
			 WHERE `+where.String()+`
		),
		ranked AS (
			SELECT
				b.*,
				CASE
					WHEN b.went_green_on_rerun THEN '`+ReasonWentGreenOnRerun+`'
					WHEN b.all_signatures_failing_elsewhere THEN '`+ReasonFailingElsewhere+`'
					ELSE '`+ReasonOther+`'
				END AS reason
			  FROM base b
		)
		SELECT
			report_group_id::text,
			repository,
			app,
			lane,
			branch,
			run_group,
			gh_pr_number,
			commit_sha,
			gh_run_id,
			gh_run_attempt,
			platform,
			framework,
			has_orchestration,
			orch_status,
			group_created_at,
			settled,
			still_red,
			went_green_on_rerun,
			failing_on_main,
			other_prs_count,
			all_signatures_failing_elsewhere,
			failed_signature_count,
			tests_failed,
			reason
		  FROM ranked
		 ORDER BY `+orderBy+`
		 LIMIT `+limitPH+` OFFSET `+offsetPH,
		b.args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query red pr lane rows: %w", err)
	}
	defer rows.Close()

	out := []RedPRLaneRow{}
	for rows.Next() {
		var r RedPRLaneRow
		var rawBranch, runGroup string
		if err := rows.Scan(
			&r.ReportGroupID, &r.Repository, &r.App, &r.Lane,
			&rawBranch, &runGroup,
			&r.GHPRNumber, &r.CommitSHA, &r.GHRunID, &r.GHRunAttempt,
			&r.Platform, &r.Framework, &r.HasOrchestration, &r.OrchStatus,
			&r.CreatedAt, &r.Settled, &r.StillRed, &r.WentGreenOnRerun,
			&r.FailingOnMain, &r.OtherPRsCount, &r.AllSignaturesFailingElsewhere,
			&r.FailedSignatureCount, &r.TestsFailed, &r.Reason,
		); err != nil {
			return nil, fmt.Errorf("scan red pr lane row: %w", err)
		}
		r.Branch = stripBranchRefPrefix(rawBranch)
		age := int64(refNow.Sub(r.CreatedAt).Seconds())
		if age < 0 {
			age = 0
		}
		r.AgeSeconds = age
		r.RunURL = buildRunURL(r.Repository, rawBranch, r.CommitSHA, r.Lane, runGroup, r.GHRunID, r.GHRunAttempt)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) queryConfiguredE2ELanes(ctx context.Context, f FilterParams) ([]ConfiguredE2ELane, error) {
	if override := parseConfiguredE2ELanes(s.E2ELanes); len(override) > 0 {
		out := make([]ConfiguredE2ELane, 0, len(override))
		for _, e := range override {
			repo := e.repo
			if repo == "" {
				repo = "*"
			}
			if f.Repo != "" && repo != "*" {
				if strings.Contains(f.Repo, "/") {
					if !strings.EqualFold(repo, f.Repo) {
						continue
					}
				} else {
					parts := strings.SplitN(repo, "/", 2)
					if !strings.EqualFold(repo, f.Repo) && (len(parts) < 2 || !strings.EqualFold(parts[1], f.Repo)) {
						continue
					}
				}
			}
			if f.Lane != "" && e.lane != f.Lane {
				continue
			}
			out = append(out, ConfiguredE2ELane{Repository: repo, Lane: e.lane})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Repository != out[j].Repository {
				return out[i].Repository < out[j].Repository
			}
			return out[i].Lane < out[j].Lane
		})
		return out, nil
	}

	b := &sqlFilterBuilder{}
	var where strings.Builder
	where.WriteString("el.enabled = true")
	if f.App != "" {
		appPH := b.add(f.App)
		fmt.Fprintf(&where, " AND COALESCE(a.app, NULLIF(split_part(el.repository, '/', 2), ''), el.repository) = %s", appPH)
	}
	if f.Repo != "" {
		repoPH := b.add(f.Repo)
		if strings.Contains(f.Repo, "/") {
			fmt.Fprintf(&where, " AND lower(el.repository) = lower(%s)", repoPH)
		} else {
			fmt.Fprintf(&where, " AND (lower(el.repository) = lower(%s) OR lower(split_part(el.repository, '/', 2)) = lower(%s))", repoPH, repoPH)
		}
	}
	if f.Lane != "" {
		lanePH := b.add(f.Lane)
		fmt.Fprintf(&where, " AND el.lane = %s", lanePH)
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT el.repository, el.lane
		  FROM kpi_e2e_lanes el
		  LEFT JOIN LATERAL (
		      SELECT ap.app
		        FROM apps ap
		       WHERE lower(ap.repository) = lower(el.repository)
		       ORDER BY (ap.repository = el.repository) DESC
		       LIMIT 1
		  ) a ON true
		 WHERE `+where.String()+`
		 ORDER BY el.repository ASC, el.lane ASC
	`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("query configured e2e lanes: %w", err)
	}
	defer rows.Close()

	out := []ConfiguredE2ELane{}
	for rows.Next() {
		var item ConfiguredE2ELane
		if err := rows.Scan(&item.Repository, &item.Lane); err != nil {
			return nil, fmt.Errorf("scan configured e2e lane: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) queryPRLaneCoverage(ctx context.Context, f FilterParams, extraNotes []string) (LaneCoverage, error) {
	override := parseConfiguredE2ELanes(s.E2ELanes)
	b := &sqlFilterBuilder{}
	fromPH := b.add(f.CurFromDate)
	toPH := b.add(f.CurToDate)
	joinSQL := appendE2ELanesJoin(b, "gr", override)

	var where strings.Builder
	fmt.Fprintf(&where, "gr.gh_pr_number IS NOT NULL AND gr.gh_pr_number > 0 AND gr.metric_date >= %s AND gr.metric_date <= %s", fromPH, toPH)
	where.WriteString(b.appendCommonFilters("gr", f, false))
	where.WriteString(appendPlatformScope(b, "gr", f.Platform))

	rows, err := s.Pool.Query(ctx, `
		SELECT gr.repository, gr.lane,
		       COUNT(*)::integer,
		       COUNT(*) FILTER (WHERE gr.has_orchestration)::integer,
		       COUNT(*) FILTER (WHERE NOT gr.has_orchestration)::integer
		  FROM kpi_group_rollups gr`+joinSQL+`
		 WHERE `+where.String()+`
		 GROUP BY gr.repository, gr.lane
		 ORDER BY gr.lane ASC, gr.repository ASC
	`, b.args...)
	if err != nil {
		return LaneCoverage{}, fmt.Errorf("query pr lane coverage: %w", err)
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
			return LaneCoverage{}, fmt.Errorf("scan pr lane coverage: %w", err)
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
	cov.Notes = append(cov.Notes, "lower bound ('at least'): excludes lanes <= 24h old until settled and excludes failures fixed by pushing a new commit")
	if cov.UploadOnlyRuns > 0 {
		cov.Notes = append(cov.Notes, "upload-only lanes use test_stats_json failed counts; orchestration lease history unavailable")
	}
	return cov, nil
}

func rerunGreenExpr(alias string) string {
	return fmt.Sprintf(`EXISTS (
		SELECT 1
		  FROM kpi_group_rollups r2
		 WHERE r2.repository = %s.repository
		   AND r2.commit_sha = %s.commit_sha
		   AND r2.lane = %s.lane
		   AND r2.is_all_platforms = %s.is_all_platforms
		   AND r2.platform = %s.platform
		   AND r2.gh_pr_number IS NOT NULL
		   AND r2.gh_pr_number > 0
		   AND r2.is_completed = true
		   AND r2.is_red = false
		   AND (NOT r2.has_orchestration OR r2.orch_status = 'completed')
		   AND (
		       r2.gh_run_attempt_num > %s.gh_run_attempt_num
		       OR (r2.gh_run_attempt_num = 0 AND %s.gh_run_attempt_num = 0 AND r2.group_created_at > %s.group_created_at)
		   )
	)`, alias, alias, alias, alias, alias, alias, alias, alias)
}

func stillRedExpr(alias string) string {
	return fmt.Sprintf(`(
		NOT `+rerunGreenExpr(alias)+`
		AND NOT EXISTS (
			SELECT 1
			  FROM kpi_group_rollups r3
			 WHERE r3.repository = %s.repository
			   AND r3.gh_pr_number = %s.gh_pr_number
			   AND r3.lane = %s.lane
			   AND r3.is_all_platforms = %s.is_all_platforms
			   AND r3.platform = %s.platform
			   AND r3.is_completed = true
			   AND r3.is_red = false
			   AND (NOT r3.has_orchestration OR r3.orch_status = 'completed')
			   AND r3.group_created_at > %s.group_created_at
		)
	)`, alias, alias, alias, alias, alias, alias)
}

func allSigsFailingElsewhereExpr(alias string) string {
	return fmt.Sprintf(`COALESCE((
		WITH lane_sigs AS (
			SELECT DISTINCT gs.signature_id
			  FROM kpi_group_signature_rollups gs
			 WHERE gs.report_group_id = %s.report_group_id
			   AND gs.has_failed_test = true
			   AND (%s.is_all_platforms OR gs.platform = %s.platform)
		)
		SELECT
			COUNT(*) > 0
			AND BOOL_AND(
				EXISTS (
					SELECT 1
					  FROM kpi_group_signature_rollups gs2
					  JOIN kpi_group_rollups gr2
					    ON gr2.report_group_id = gs2.report_group_id
					   AND gr2.is_all_platforms = true
					 WHERE gs2.has_failed_test = true
					   AND gs2.repository = %s.repository
					   AND gs2.signature_id = ls.signature_id
					   AND gs2.stage = 'main'
					   AND gs2.metric_date >= ((%s.group_created_at - interval '24 hours') AT TIME ZONE 'UTC')::date - 1
					   AND gs2.metric_date <= ((%s.group_created_at + interval '24 hours') AT TIME ZONE 'UTC')::date + 1
					   AND gr2.group_created_at BETWEEN %s.group_created_at - interval '24 hours'
					                                AND %s.group_created_at + interval '24 hours'
				)
				OR (
					SELECT COUNT(DISTINCT gs2.gh_pr_number)
					  FROM kpi_group_signature_rollups gs2
					  JOIN kpi_group_rollups gr2
					    ON gr2.report_group_id = gs2.report_group_id
					   AND gr2.is_all_platforms = true
					 WHERE gs2.has_failed_test = true
					   AND gs2.repository = %s.repository
					   AND gs2.signature_id = ls.signature_id
					   AND gs2.gh_pr_number IS NOT NULL
					   AND gs2.gh_pr_number > 0
					   AND gs2.gh_pr_number <> %s.gh_pr_number
					   AND gs2.metric_date >= ((%s.group_created_at - interval '24 hours') AT TIME ZONE 'UTC')::date - 1
					   AND gs2.metric_date <= ((%s.group_created_at + interval '24 hours') AT TIME ZONE 'UTC')::date + 1
					   AND gr2.group_created_at BETWEEN %s.group_created_at - interval '24 hours'
					                                AND %s.group_created_at + interval '24 hours'
				) >= 3
			)
		  FROM lane_sigs ls
	), false)`, alias, alias, alias, alias, alias, alias, alias, alias, alias, alias, alias, alias, alias, alias)
}

func prRedOrderByClause(sortBy string) string {
	switch sortBy {
	case SortAgeDesc:
		return "group_created_at ASC, report_group_id ASC"
	case SortPRDesc:
		return "gh_pr_number DESC, group_created_at DESC, report_group_id DESC"
	case SortLaneAsc:
		return "lane ASC, repository ASC, group_created_at DESC, report_group_id DESC"
	case SortReasonAsc:
		return "reason ASC, group_created_at DESC, report_group_id DESC"
	default: // SortCreatedDesc
		return "group_created_at DESC, report_group_id DESC"
	}
}

func stripBranchRefPrefix(b string) string {
	for _, p := range []string{"refs/heads/", "refs/tags/"} {
		if strings.HasPrefix(b, p) {
			return b[len(p):]
		}
	}
	return b
}

func buildRunURL(repository, branch, commitSHA, lane, runGroup, ghRunID, ghRunAttempt string) string {
	repoName := repository
	if i := strings.LastIndex(repository, "/"); i >= 0 && i < len(repository)-1 {
		repoName = repository[i+1:]
	}
	bSeg := strings.ReplaceAll(stripBranchRefPrefix(branch), "/", "~")
	sha := commitSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	runName := lane
	if runGroup != "" {
		runName = runGroup
	}
	if repoName == "" || bSeg == "" || sha == "" || runName == "" {
		return "/"
	}
	u := "/reports/" +
		url.PathEscape(repoName) + "/" +
		url.PathEscape(bSeg) + "/" +
		url.PathEscape(sha) + "/" +
		url.PathEscape(runName)
	if ghRunID != "" {
		att := ghRunAttempt
		if att == "" {
			att = "1"
		}
		u += "?gh_run_id=" + url.QueryEscape(ghRunID) + "&gh_run_attempt=" + url.QueryEscape(att)
	}
	return u
}
