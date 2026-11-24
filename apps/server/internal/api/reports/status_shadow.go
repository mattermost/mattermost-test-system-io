package reports

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

const (
	defaultShadowLimit = 50
	maxShadowLimit     = 500
)

// StatusShadowOpts configures a read-only legacy-vs-shared shadow comparison.
type StatusShadowOpts struct {
	Limit      int
	Repository string
}

// StatusCounts holds test outcome counts and pass rate under one status rule.
type StatusCounts struct {
	Passed   int  `json:"passed"`
	Failed   int  `json:"failed"`
	Flaky    int  `json:"flaky"`
	Skipped  int  `json:"skipped"`
	Total    int  `json:"total"`
	PassRate *int `json:"pass_rate"`
}

// StatusCountDelta holds (shared - legacy) differences for counts and pass rate.
type StatusCountDelta struct {
	Passed   int  `json:"passed"`
	Failed   int  `json:"failed"`
	Flaky    int  `json:"flaky"`
	Skipped  int  `json:"skipped"`
	Total    int  `json:"total"`
	PassRate *int `json:"pass_rate"`
}

// OrchRunShadowDelta records legacy vs shared counts and pass rate for one orchestration run.
type OrchRunShadowDelta struct {
	RunID        uuid.UUID        `json:"run_id"`
	Repository   string           `json:"repository"`
	Name         string           `json:"name"`
	GHRunID      string           `json:"gh_run_id"`
	GHRunAttempt string           `json:"gh_run_attempt"`
	Legacy       StatusCounts     `json:"legacy"`
	Shared       StatusCounts     `json:"shared"`
	Delta        StatusCountDelta `json:"delta"`
	Changed      bool             `json:"changed"`
}

// ReportGroupShadowDelta records legacy vs shared JSON/home pass rate for one report group.
type ReportGroupShadowDelta struct {
	GroupID        uuid.UUID    `json:"group_id"`
	Repository     string       `json:"repository"`
	Name           string       `json:"name"`
	GHRunID        string       `json:"gh_run_id"`
	GHRunAttempt   string       `json:"gh_run_attempt"`
	Stats          StatusCounts `json:"stats"`
	LegacyPassRate *int         `json:"legacy_pass_rate"`
	SharedPassRate *int         `json:"shared_pass_rate"`
	DeltaPassRate  *int         `json:"delta_pass_rate"`
	Changed        bool         `json:"changed"`
}

// OrchShadowSection summarizes orchestration run comparison results.
type OrchShadowSection struct {
	Checked      int                  `json:"checked"`
	Changed      int                  `json:"changed"`
	DeltaPassed  int                  `json:"delta_passed"`
	DeltaFailed  int                  `json:"delta_failed"`
	DeltaFlaky   int                  `json:"delta_flaky"`
	DeltaSkipped int                  `json:"delta_skipped"`
	DeltaTotal   int                  `json:"delta_total"`
	Rows         []OrchRunShadowDelta `json:"rows"`
}

// ReportGroupShadowSection summarizes report group JSON pass-rate comparison results.
type ReportGroupShadowSection struct {
	Checked int                      `json:"checked"`
	Changed int                      `json:"changed"`
	Rows    []ReportGroupShadowDelta `json:"rows"`
}

// StatusShadowReport is the complete legacy-vs-shared shadow comparison output.
type StatusShadowReport struct {
	Limit             int                      `json:"limit"`
	Repository        string                   `json:"repository,omitempty"`
	OrchestrationRuns OrchShadowSection        `json:"orchestration_runs"`
	ReportGroups      ReportGroupShadowSection `json:"report_groups"`
}

// CompareStatusRules compares legacy vs shared read-time semantics across recent
// orchestration_runs and report_groups inside a single READ ONLY transaction
// with a 30s statement timeout. It never scans suites or test_cases.
func CompareStatusRules(ctx context.Context, pool *pgxpool.Pool, opts StatusShadowOpts) (StatusShadowReport, error) {
	if pool == nil {
		return StatusShadowReport{}, errors.New("status shadow: nil pool")
	}
	limit := opts.Limit
	if limit == 0 {
		limit = defaultShadowLimit
	}
	if limit < 1 || limit > maxShadowLimit {
		return StatusShadowReport{}, fmt.Errorf("status shadow: limit %d out of range [1, %d]", limit, maxShadowLimit)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return StatusShadowReport{}, fmt.Errorf("status shadow: begin read-only tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '30s'`); err != nil {
		return StatusShadowReport{}, fmt.Errorf("status shadow: set statement_timeout: %w", err)
	}

	orchSec, err := compareOrchestrationRunsTx(ctx, tx, limit, opts.Repository)
	if err != nil {
		return StatusShadowReport{}, err
	}
	groupSec, err := compareReportGroupsTx(ctx, tx, limit, opts.Repository)
	if err != nil {
		return StatusShadowReport{}, err
	}

	return StatusShadowReport{
		Limit:             limit,
		Repository:        opts.Repository,
		OrchestrationRuns: orchSec,
		ReportGroups:      groupSec,
	}, nil
}

func compareOrchestrationRunsTx(ctx context.Context, tx pgx.Tx, limit int, repo string) (OrchShadowSection, error) {
	rows, err := tx.Query(ctx, `
		WITH target_runs AS (
			SELECT id, repository, name, gh_run_id, gh_run_attempt, started_at
			  FROM orchestration_runs
			 WHERE ($2 = '' OR repository = $2)
			 ORDER BY started_at DESC, id DESC
			 LIMIT $1
		),
		legacy_per_test AS (
			SELECT
				a.run_id,
				a.dispatch_unit_id,
				tc->>'full_title' AS full_title,
				BOOL_OR(tc->>'status' IN ('passed', 'flaky')) AS ever_passed,
				BOOL_OR(tc->>'status' IN ('failed', 'timedOut', 'interrupted')) AS ever_failed,
				BOOL_OR(tc->>'status' = 'skipped') AS ever_skipped
			  FROM target_runs r
			  JOIN attempts a ON a.run_id = r.id
			 CROSS JOIN LATERAL jsonb_array_elements(a.test_cases) AS tc
			 WHERE a.test_cases IS NOT NULL
			   AND jsonb_typeof(a.test_cases) = 'array'
			 GROUP BY a.run_id, a.dispatch_unit_id, tc->>'full_title'
		),
		legacy_agg AS (
			SELECT
				run_id,
				COUNT(*) FILTER (WHERE ever_passed AND NOT ever_failed) AS passed,
				COUNT(*) FILTER (WHERE ever_failed AND NOT ever_passed) AS failed,
				COUNT(*) FILTER (WHERE ever_passed AND ever_failed) AS flaky,
				COUNT(*) FILTER (WHERE ever_skipped AND NOT ever_passed AND NOT ever_failed) AS skipped,
				COUNT(*) AS total
			  FROM legacy_per_test
			 GROUP BY run_id
		),
		shared_per_test AS (
			SELECT
				a.run_id,
				a.dispatch_unit_id,
				tc->>'full_title' AS full_title,
				tsio_test_verdict(ARRAY_AGG(tc->>'status')) AS verdict
			  FROM target_runs r
			  JOIN attempts a ON a.run_id = r.id
			 CROSS JOIN LATERAL jsonb_array_elements(a.test_cases) AS tc
			 WHERE a.test_cases IS NOT NULL
			   AND jsonb_typeof(a.test_cases) = 'array'
			   AND BTRIM(COALESCE(tc->>'full_title', ''), E' \t\n\r\u000b\f\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000') <> ''
			   AND COALESCE(tc->>'status', '') <> ''
			 GROUP BY a.run_id, a.dispatch_unit_id, tc->>'full_title'
		),
		shared_agg AS (
			SELECT
				run_id,
				COUNT(*) FILTER (WHERE verdict = 'passed') AS passed,
				COUNT(*) FILTER (WHERE verdict = 'failed') AS failed,
				COUNT(*) FILTER (WHERE verdict = 'flaky') AS flaky,
				COUNT(*) FILTER (WHERE verdict = 'skipped') AS skipped,
				COUNT(*) FILTER (WHERE verdict IS NOT NULL) AS total
			  FROM shared_per_test
			 GROUP BY run_id
		)
		SELECT
			r.id, r.repository, r.name, r.gh_run_id, r.gh_run_attempt,
			COALESCE(l.passed, 0), COALESCE(l.failed, 0), COALESCE(l.flaky, 0), COALESCE(l.skipped, 0), COALESCE(l.total, 0),
			COALESCE(s.passed, 0), COALESCE(s.failed, 0), COALESCE(s.flaky, 0), COALESCE(s.skipped, 0), COALESCE(s.total, 0)
		  FROM target_runs r
		  LEFT JOIN legacy_agg l ON l.run_id = r.id
		  LEFT JOIN shared_agg s ON s.run_id = r.id
		 ORDER BY r.started_at DESC, r.id DESC
	`, limit, repo)
	if err != nil {
		return OrchShadowSection{}, fmt.Errorf("status shadow: query orchestration runs: %w", err)
	}
	defer rows.Close()

	sec := OrchShadowSection{Rows: make([]OrchRunShadowDelta, 0)}
	for rows.Next() {
		var (
			runID                             uuid.UUID
			repository, name, ghRunID, att    string
			lPass, lFail, lFlaky, lSkip, lTot int
			sPass, sFail, sFlaky, sSkip, sTot int
		)
		if err := rows.Scan(
			&runID, &repository, &name, &ghRunID, &att,
			&lPass, &lFail, &lFlaky, &lSkip, &lTot,
			&sPass, &sFail, &sFlaky, &sSkip, &sTot,
		); err != nil {
			return OrchShadowSection{}, fmt.Errorf("status shadow: scan orchestration run: %w", err)
		}
		row := buildOrchRunShadowDelta(
			runID, repository, name, ghRunID, att,
			StatusCounts{Passed: lPass, Failed: lFail, Flaky: lFlaky, Skipped: lSkip, Total: lTot},
			StatusCounts{Passed: sPass, Failed: sFail, Flaky: sFlaky, Skipped: sSkip, Total: sTot},
		)
		sec.Checked++
		if row.Changed {
			sec.Changed++
			sec.DeltaPassed += row.Delta.Passed
			sec.DeltaFailed += row.Delta.Failed
			sec.DeltaFlaky += row.Delta.Flaky
			sec.DeltaSkipped += row.Delta.Skipped
			sec.DeltaTotal += row.Delta.Total
		}
		sec.Rows = append(sec.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return OrchShadowSection{}, fmt.Errorf("status shadow: iterate orchestration runs: %w", err)
	}
	return sec, nil
}

func compareReportGroupsTx(ctx context.Context, tx pgx.Tx, limit int, repo string) (ReportGroupShadowSection, error) {
	rows, err := tx.Query(ctx, `
		SELECT
			g.id, g.repository, g.name, g.gh_run_id, g.gh_run_attempt,
			g.test_stats_json,
			COALESCE(r.total_cases, 0),
			COALESCE(r.passed_cases, 0),
			COALESCE(r.failed_cases, 0),
			COALESCE(r.skipped_cases, 0),
			COALESCE(r.flaky_cases, 0)
		  FROM report_groups g
		  LEFT JOIN LATERAL (
			SELECT
				SUM(total_cases)::int AS total_cases,
				SUM(passed_cases)::int AS passed_cases,
				SUM(failed_cases)::int AS failed_cases,
				SUM(skipped_cases)::int AS skipped_cases,
				SUM(flaky_cases)::int AS flaky_cases
			  FROM reports
			 WHERE report_group_id = g.id
		  ) r ON (g.test_stats_json IS NULL OR g.test_stats_json = 'null'::jsonb)
		 WHERE ($2 = '' OR g.repository = $2)
		 ORDER BY g.created_at DESC, g.id DESC
		 LIMIT $1
	`, limit, repo)
	if err != nil {
		return ReportGroupShadowSection{}, fmt.Errorf("status shadow: query report groups: %w", err)
	}
	defer rows.Close()

	sec := ReportGroupShadowSection{Rows: make([]ReportGroupShadowDelta, 0)}
	for rows.Next() {
		var (
			groupID                        uuid.UUID
			repository, name, ghRunID, att string
			statsRaw                       []byte
			fbTot, fbPass, fbFail          int
			fbSkip, fbFlaky                int
		)
		if err := rows.Scan(
			&groupID, &repository, &name, &ghRunID, &att,
			&statsRaw,
			&fbTot, &fbPass, &fbFail, &fbSkip, &fbFlaky,
		); err != nil {
			return ReportGroupShadowSection{}, fmt.Errorf("status shadow: scan report group: %w", err)
		}
		stats := parseProjectedTestStats(statsRaw)
		if stats == nil {
			stats = &testStats{
				Total:   fbTot,
				Passed:  fbPass,
				Failed:  fbFail,
				Skipped: fbSkip,
				Flaky:   fbFlaky,
			}
		}
		row := buildReportGroupShadowDelta(groupID, repository, name, ghRunID, att, stats)
		sec.Checked++
		if row.Changed {
			sec.Changed++
		}
		sec.Rows = append(sec.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return ReportGroupShadowSection{}, fmt.Errorf("status shadow: iterate report groups: %w", err)
	}
	return sec, nil
}

func buildOrchRunShadowDelta(
	runID uuid.UUID,
	repository, name, ghRunID, ghRunAttempt string,
	legacyCounts, sharedCounts StatusCounts,
) OrchRunShadowDelta {
	legacyCounts.PassRate = passRatePtr(passRatePercent(&testStats{
		Total:   legacyCounts.Total,
		Passed:  legacyCounts.Passed,
		Failed:  legacyCounts.Failed,
		Skipped: legacyCounts.Skipped,
		Flaky:   legacyCounts.Flaky,
	}, teststatus.RuleLegacy))
	sharedCounts.PassRate = passRatePtr(passRatePercent(&testStats{
		Total:   sharedCounts.Total,
		Passed:  sharedCounts.Passed,
		Failed:  sharedCounts.Failed,
		Skipped: sharedCounts.Skipped,
		Flaky:   sharedCounts.Flaky,
	}, teststatus.RuleShared))

	delta := StatusCountDelta{
		Passed:   sharedCounts.Passed - legacyCounts.Passed,
		Failed:   sharedCounts.Failed - legacyCounts.Failed,
		Flaky:    sharedCounts.Flaky - legacyCounts.Flaky,
		Skipped:  sharedCounts.Skipped - legacyCounts.Skipped,
		Total:    sharedCounts.Total - legacyCounts.Total,
		PassRate: intPtrDelta(legacyCounts.PassRate, sharedCounts.PassRate),
	}
	changed := delta.Passed != 0 || delta.Failed != 0 || delta.Flaky != 0 ||
		delta.Skipped != 0 || delta.Total != 0 ||
		!intPtrEqual(legacyCounts.PassRate, sharedCounts.PassRate)

	return OrchRunShadowDelta{
		RunID:        runID,
		Repository:   repository,
		Name:         name,
		GHRunID:      ghRunID,
		GHRunAttempt: ghRunAttempt,
		Legacy:       legacyCounts,
		Shared:       sharedCounts,
		Delta:        delta,
		Changed:      changed,
	}
}

func buildReportGroupShadowDelta(
	groupID uuid.UUID,
	repository, name, ghRunID, ghRunAttempt string,
	stats *testStats,
) ReportGroupShadowDelta {
	if stats == nil {
		stats = &testStats{}
	}
	legacyRate := passRatePtr(passRatePercent(stats, teststatus.RuleLegacy))
	sharedRate := passRatePtr(passRatePercent(stats, teststatus.RuleShared))
	return ReportGroupShadowDelta{
		GroupID:      groupID,
		Repository:   repository,
		Name:         name,
		GHRunID:      ghRunID,
		GHRunAttempt: ghRunAttempt,
		Stats: StatusCounts{
			Passed:  stats.Passed,
			Failed:  stats.Failed,
			Flaky:   stats.Flaky,
			Skipped: stats.Skipped,
			Total:   stats.Total,
		},
		LegacyPassRate: legacyRate,
		SharedPassRate: sharedRate,
		DeltaPassRate:  intPtrDelta(legacyRate, sharedRate),
		Changed:        !intPtrEqual(legacyRate, sharedRate),
	}
}

func passRatePtr(v int) *int {
	if v < 0 {
		return nil
	}
	return &v
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func intPtrDelta(before, after *int) *int {
	if before == nil || after == nil {
		return nil
	}
	d := *after - *before
	return &d
}
