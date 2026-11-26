package kpi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// QueryTestState computes GET /api/v1/tests/{id}/state from tests,
// kpi_test_commit_outcomes, kpi_daily_test_executions, and verdicts (without error_context).
func (s *Store) QueryTestState(ctx context.Context, testID uuid.UUID, f FilterParams) (TestStateResponse, error) {
	if s == nil || s.Pool == nil {
		return TestStateResponse{}, errors.New("kpi store pool is nil")
	}

	var resp TestStateResponse
	var firstSeenAt, lastSeenAt time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT t.id::text, t.test_key, t.zephyr_key, t.file, t.full_title, t.repository,
		       COALESCE(a.app, NULLIF(split_part(t.repository, '/', 2), ''), t.repository) AS app,
		       t.framework, t.first_seen_at, t.last_seen_at
		  FROM tests t
		  LEFT JOIN LATERAL (
		      SELECT ap.app
		      FROM apps ap
		      WHERE lower(ap.repository) = lower(t.repository)
		      ORDER BY (ap.repository = t.repository) DESC
		      LIMIT 1
		  ) a ON true
		 WHERE t.id = $1
	`, testID).Scan(
		&resp.TestID, &resp.TestKey, &resp.ZephyrKey, &resp.File, &resp.FullTitle,
		&resp.Repository, &resp.App, &resp.Framework, &firstSeenAt, &lastSeenAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TestStateResponse{}, ErrNotFound
	}
	if err != nil {
		return TestStateResponse{}, fmt.Errorf("load test %s: %w", testID, err)
	}

	curFrom14d, curTo14d, _, _ := WindowBounds(s.now(), TrendWindowDays)

	bc := &sqlFilterBuilder{}
	cTestPH := bc.add(testID)
	cFilters := appendCommitFilters(bc, "c", f)

	var (
		totalCommits       int
		minCommitFirstSeen *time.Time
		maxCommitLastSeen  *time.Time
	)
	err = s.Pool.QueryRow(ctx, `
		SELECT
			COUNT(DISTINCT c.commit_sha)::integer,
			MIN(c.first_seen_at),
			MAX(c.last_seen_at)
		  FROM kpi_test_commit_outcomes c
		 WHERE c.test_id = `+cTestPH+cFilters,
		bc.args...,
	).Scan(&totalCommits, &minCommitFirstSeen, &maxCommitLastSeen)
	if err != nil {
		return TestStateResponse{}, fmt.Errorf("query test commit outcomes %s: %w", testID, err)
	}

	bf := &sqlFilterBuilder{}
	fTestPH := bf.add(testID)
	fFromPH := bf.add(curFrom14d)
	fToPH := bf.add(curTo14d)
	fFilters := appendCommitFilters(bf, "c", f)

	var flakyCommits14d int
	err = s.Pool.QueryRow(ctx, `
		WITH commit_14d AS (
			SELECT
				c.repository,
				c.commit_sha,
				c.project,
				c.platform,
				(SUM(c.pass_count) > 0 AND SUM(c.fail_count) > 0) AS is_mixed
			  FROM kpi_test_commit_outcomes c
			 WHERE c.test_id = `+fTestPH+`
			   AND c.metric_date >= `+fFromPH+`
			   AND c.metric_date <= `+fToPH+fFilters+`
			 GROUP BY c.repository, c.commit_sha, c.project, c.platform
		),
		per_slice AS (
			SELECT project, platform, COUNT(*) FILTER (WHERE is_mixed)::integer AS flaky_commits
			  FROM commit_14d
			 GROUP BY project, platform
		)
		SELECT COALESCE(MAX(flaky_commits), 0)::integer
		  FROM per_slice
	`, bf.args...).Scan(&flakyCommits14d)
	if err != nil {
		return TestStateResponse{}, fmt.Errorf("query test flaky commits %s: %w", testID, err)
	}

	be := &sqlFilterBuilder{}
	eTestPH := be.add(testID)
	eFilters := be.appendSignatureExecFilters("e", f)

	var totalRuns int
	err = s.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(e.run_count), 0)::integer
		  FROM kpi_daily_test_executions e
		 WHERE e.test_id = `+eTestPH+eFilters,
		be.args...,
	).Scan(&totalRuns)
	if err != nil {
		return TestStateResponse{}, fmt.Errorf("query test executions %s: %w", testID, err)
	}

	latestLabel, err := s.loadLatestTestVerdictLabel(ctx, testID, f)
	if err != nil {
		return TestStateResponse{}, err
	}

	if minCommitFirstSeen != nil && minCommitFirstSeen.Before(firstSeenAt) {
		firstSeenAt = *minCommitFirstSeen
	}
	if maxCommitLastSeen != nil && maxCommitLastSeen.After(lastSeenAt) {
		lastSeenAt = *maxCommitLastSeen
	}

	state, isFlaky := classifyTestState(flakyCommits14d, totalCommits, firstSeenAt, curFrom14d)

	resp.State = state
	resp.IsFlaky = isFlaky
	resp.FlakyCommitCount14d = flakyCommits14d
	resp.TotalCommits = totalCommits
	resp.TotalRuns = totalRuns
	resp.FirstSeenAt = firstSeenAt
	resp.LastSeenAt = lastSeenAt
	resp.LatestVerdictLabel = latestLabel
	return resp, nil
}

func (s *Store) loadLatestTestVerdictLabel(ctx context.Context, testID uuid.UUID, f FilterParams) (*string, error) {
	b := &sqlFilterBuilder{}
	testPH := b.add(testID)
	var where strings.Builder
	fmt.Fprintf(&where, "v.test_id = %s AND v.superseded_at IS NULL", testPH)
	where.WriteString(appendVerdictScopeFilters(b, f))

	// error_context is deliberately omitted from SELECT so it can never leak.
	var label string
	err := s.Pool.QueryRow(ctx, `
		SELECT v.label
		  FROM verdicts v
		 WHERE `+where.String()+`
		 ORDER BY v.created_at DESC, v.id DESC
		 LIMIT 1
	`, b.args...).Scan(&label)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load latest test verdict label %s: %w", testID, err)
	}
	return &label, nil
}

func classifyTestState(flakyCommits14d, totalCommits int, firstSeenAt, curFrom14d time.Time) (string, bool) {
	if flakyCommits14d >= 2 {
		return TestStateFlaky, true
	}
	if !firstSeenAt.UTC().Before(curFrom14d) || totalCommits <= 1 {
		return TestStateNew, false
	}
	return TestStateKnown, false
}
