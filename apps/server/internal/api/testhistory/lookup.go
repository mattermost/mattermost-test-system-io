package testhistory

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultHistoryLimit = 20

// LookupSummary returns the history summary for one test. Used by
// /triage/evidence so an agent pack does not N+1 the public HTTP endpoints.
//
// branch and framework treat empty as "any", matching GET /tests/history.
func LookupSummary(ctx context.Context, pool *pgxpool.Pool, testID, repo, branch, framework string, since *time.Time) (HistorySummary, error) {
	entries, err := loadEntries(ctx, pool, testID, repo, branch, framework, "", since, defaultHistoryLimit)
	if err != nil {
		return HistorySummary{}, err
	}
	return summarize(entries), nil
}

// LookupElsewhereCounts returns how many other PRs and branches currently show
// the same test failing. excludePR is the caller's own PR (use -1 for none).
func LookupElsewhereCounts(ctx context.Context, pool *pgxpool.Pool, testID, repo string, excludePR int, since *time.Time) (distinctPRs, distinctBranches int, err error) {
	err = pool.QueryRow(ctx, groupRollupSQL+`
		SELECT count(DISTINCT gh_pr_number)::int,
		       count(DISTINCT nullif(branch, ''))::int
		FROM outcomes
		WHERE outcome IN ('failed', 'flaky')
		  AND (gh_pr_number IS NULL OR gh_pr_number <> $7)
	`, testID, repo, "", "", "", since, excludePR).Scan(&distinctPRs, &distinctBranches)
	if err != nil {
		return 0, 0, fmt.Errorf("failing-elsewhere counts: %w", err)
	}
	return distinctPRs, distinctBranches, nil
}

// LookupPRFailureCounts returns how many times this test ran on one PR and how
// many of those runs did not cleanly pass. It is the current-rate half of the
// rate-shift comparison (R7-B); LookupSummary supplies the baseline half.
//
// A flaky (failed-then-recovered) run counts as a failure, matching
// HistorySummary.FailureRate — a run that needed a retry did not cleanly pass,
// and the two sides of the comparison must count the same way or the ratio is
// meaningless.
//
// branch is deliberately "any": a PR's runs live on its own head branch, and
// pinning the branch here would return zero rows for every PR.
func LookupPRFailureCounts(ctx context.Context, pool *pgxpool.Pool, testID, repo, framework string, prNumber int, since *time.Time) (runs, failed int, err error) {
	err = pool.QueryRow(ctx, groupRollupSQL+`
		SELECT count(*)::int,
		       count(*) FILTER (WHERE outcome IN ('failed', 'flaky'))::int
		FROM outcomes
		WHERE gh_pr_number = $7
	`, testID, repo, "", framework, "", since, prNumber).Scan(&runs, &failed)
	if err != nil {
		return 0, 0, fmt.Errorf("pr failure counts: %w", err)
	}
	return runs, failed, nil
}

func loadEntries(ctx context.Context, pool *pgxpool.Pool, testID, repo, branch, framework, runGroup string, since *time.Time, limit int) ([]historyEntry, error) {
	rows, err := pool.Query(ctx, groupRollupSQL+`
		SELECT commit_sha, gh_run_id, gh_pr_number, branch, name, run_group,
		       outcome, shard_rows, duration_ms, created_at
		FROM outcomes
		ORDER BY created_at DESC
		LIMIT $7
	`, testID, repo, branch, framework, runGroup, since, limit)
	if err != nil {
		return nil, fmt.Errorf("history query: %w", err)
	}
	defer rows.Close()

	entries := make([]historyEntry, 0, limit)
	for rows.Next() {
		var e historyEntry
		if err := rows.Scan(&e.Commit, &e.GHRunID, &e.GHPRNumber, &e.Branch, &e.Name,
			&e.RunGroup, &e.Outcome, &e.ShardRows, &e.DurationMs, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("history scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history rows: %w", err)
	}
	return entries, nil
}
