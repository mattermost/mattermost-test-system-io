package kpi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
)

type bucketKey struct {
	MetricDate time.Time
	Repository string
	Stage      string
	Lane       string
}

type commitKey struct {
	Repository string
	CommitSHA  string
}

type testExecKey struct {
	MetricDate time.Time
	Repository string
	Stage      string
	Lane       string
	Platform   string
	TestID     uuid.UUID
}

type testExecCounts struct {
	Run   int `json:"run"`
	Pass  int `json:"pass"`
	Fail  int `json:"fail"`
	Flaky int `json:"flaky"`
	Skip  int `json:"skip"`
}

type testExecDelta struct {
	App string
	testExecCounts
}

type oldGroupRollup struct {
	MetricDate       time.Time
	Repository       string
	Stage            string
	Lane             string
	IsAllPlatforms   bool
	Platform         string
	CommitSHA        string
	HasOrchestration bool
	OrchStatus       *string
	IsRed            bool
	HasRetriedUnit   bool
	TotalDurationMs  *int64
	TestCountsJSON   []byte
}

type groupRow struct {
	ID                uuid.UUID
	CreatedAt         time.Time
	MetricDate        time.Time
	Repository        string
	App               string
	Framework         string
	Stage             string
	Lane              string
	CommitSHA         string
	GHRunID           string
	GHRunAttempt      string
	GHPRNumber        *int
	Status            string
	Platform          string
	TotalDurationMs   *int64
	TestStatsJSON     []byte
	OrchestrationJSON []byte
	SummaryVersion    int64
}

type orchState struct {
	HasOrchestration bool
	OrchStatus       *string
	FailCount        int
	AbandonedCount   int
	HasRetriedUnit   bool
	TotalDurationMs  *int64
	PreservedIsRed   *bool
}

type testResultRow struct {
	TestID             uuid.UUID
	Project            string
	Platform           string
	Verdict            string
	FirstAttemptPassed bool
	SignatureIDs       []uuid.UUID
}

type verdictRow struct {
	TestID      *uuid.UUID
	SignatureID *uuid.UUID
	Label       string
	IsReused    bool
}

type sliceCounts struct {
	TestsTotal              int
	TestsPassed             int
	TestsFailed             int
	TestsFlaky              int
	TestsSkipped            int
	FirstAttemptPassedCount int
	Attempt1TestsRun        int
	Attempt1FirstAttempt    int
	FailedTestOccurrences   int
	TriagedTestOccurrences  int
	TestCounts              map[uuid.UUID]testExecCounts
}

type orchJSONPayload struct {
	Status string `json:"status"`
	Counts struct {
		CompletedFail int `json:"completed_fail"`
		Abandoned     int `json:"abandoned"`
	} `json:"counts"`
	Durations *struct {
		RetestUnitCount int        `json:"retest_unit_count"`
		BeginAt         time.Time  `json:"begin_at"`
		LastTestAt      *time.Time `json:"last_test_at"`
	} `json:"durations"`
}

type testStatsJSONPayload struct {
	Total  int `json:"total"`
	Failed int `json:"failed"`
}

// RefreshGroupRollupsBestEffort refreshes a group's KPI rollups and logs any error
// without failing the caller's primary write operation.
func RefreshGroupRollupsBestEffort(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, groupID uuid.UUID) {
	if pool == nil || groupID == uuid.Nil {
		return
	}
	if err := RefreshGroupRollups(ctx, pool, groupID); err != nil {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("refresh_group_rollups failed",
			slog.String("group_id", groupID.String()),
			slog.String("error", err.Error()))
	}
}

// RefreshGroupRollups incrementally refreshes kpi_group_rollups,
// kpi_group_signature_rollups, kpi_daily_test_executions, kpi_daily_lane_metrics,
// kpi_daily_signature_metrics, and kpi_test_commit_outcomes for groupID.
func RefreshGroupRollups(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) error {
	if pool == nil || groupID == uuid.Nil {
		return nil
	}
	return db.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended('kpi_group:' || $1, 0))`,
			groupID.String(),
		); err != nil {
			return fmt.Errorf("lock group: %w", err)
		}

		var snapshotAt time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&snapshotAt); err != nil {
			return fmt.Errorf("snapshot clock: %w", err)
		}

		oldRows, err := loadOldGroupRollups(ctx, tx, groupID)
		if err != nil {
			return err
		}

		g, found, err := loadGroupRow(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if !found {
			return cleanupMissingGroupTx(ctx, tx, oldRows)
		}

		var oldAll *oldGroupRollup
		bucketSet := map[bucketKey]struct{}{}
		commitSet := map[commitKey]struct{}{}
		deltas := map[testExecKey]testExecDelta{}

		for i := range oldRows {
			r := &oldRows[i]
			bucketSet[bucketKey{MetricDate: r.MetricDate.UTC(), Repository: r.Repository, Stage: r.Stage, Lane: r.Lane}] = struct{}{}
			commitSet[commitKey{Repository: r.Repository, CommitSHA: r.CommitSHA}] = struct{}{}
			if r.IsAllPlatforms {
				oldAll = r
				continue
			}
			if err := addTestCountsJSON(deltas, r.MetricDate, r.Repository, "", r.Stage, r.Lane, r.Platform, r.TestCountsJSON, -1); err != nil {
				return err
			}
		}

		newBucket := bucketKey{MetricDate: g.MetricDate.UTC(), Repository: g.Repository, Stage: g.Stage, Lane: g.Lane}
		newCommit := commitKey{Repository: g.Repository, CommitSHA: g.CommitSHA}
		bucketSet[newBucket] = struct{}{}
		commitSet[newCommit] = struct{}{}

		orch, err := loadOrchState(ctx, tx, g, oldAll)
		if err != nil {
			return err
		}

		results, err := loadGroupTestResults(ctx, tx, groupID)
		if err != nil {
			return err
		}
		platforms, err := loadGroupPlatforms(ctx, tx, groupID, g.Platform, results)
		if err != nil {
			return err
		}
		verdicts, err := loadGroupVerdicts(ctx, tx, groupID)
		if err != nil {
			return err
		}

		if err := acquireDeterministicLocks(ctx, tx, commitSet, bucketSet); err != nil {
			return err
		}

		allSlice, perPlatformSlices := aggregateGroupSlices(results, platforms, verdicts, g.GHRunAttempt)
		for p, sc := range perPlatformSlices {
			for tid, c := range sc.TestCounts {
				k := testExecKey{
					MetricDate: g.MetricDate.UTC(),
					Repository: g.Repository,
					Stage:      g.Stage,
					Lane:       g.Lane,
					Platform:   p,
					TestID:     tid,
				}
				cur := deltas[k]
				if g.App != "" {
					cur.App = g.App
				}
				cur.Run += c.Run
				cur.Pass += c.Pass
				cur.Fail += c.Fail
				cur.Flaky += c.Flaky
				cur.Skip += c.Skip
				deltas[k] = cur
			}
		}

		if err := replaceGroupRollupsTx(ctx, tx, g, orch, verdicts, platforms, allSlice, perPlatformSlices); err != nil {
			return err
		}
		if err := replaceGroupSignatureRollupsTx(ctx, tx, g, results); err != nil {
			return err
		}
		if err := applyTestExecDeltasTx(ctx, tx, deltas); err != nil {
			return err
		}
		if err := recomputeBucketsAndCommitsTx(ctx, tx, bucketSet, commitSet); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE report_groups
			   SET kpi_rollups_refreshed_at = $2,
			       kpi_rollups_version      = GREATEST(kpi_rollups_version, $3)
			 WHERE id = $1
			   AND (
			       kpi_rollups_refreshed_at IS NULL
			       OR kpi_rollups_refreshed_at < $2
			       OR kpi_rollups_version < $3
			   )
		`, groupID, snapshotAt, g.SummaryVersion); err != nil {
			return fmt.Errorf("stamp kpi_rollups_refreshed_at: %w", err)
		}
		return nil
	})
}

// DeleteGroupAndRecomputeRollups atomically deletes a report_group and recomputes
// affected daily and commit KPI rollups inside the same transaction under the strict
// advisory lock ordering (`kpi_group` -> sorted `kpi_commit` -> sorted `kpi_bucket`).
// Returns (false, nil) when no report_group with groupID exists.
func DeleteGroupAndRecomputeRollups(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) (bool, error) {
	if pool == nil || groupID == uuid.Nil {
		return false, nil
	}
	var deleted bool
	err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended('kpi_group:' || $1, 0))`,
			groupID.String(),
		); err != nil {
			return fmt.Errorf("lock group: %w", err)
		}

		oldRows, err := loadOldGroupRollups(ctx, tx, groupID)
		if err != nil {
			return err
		}

		var rgRepo, rgCommit string
		var rgExists bool
		err = tx.QueryRow(ctx, `
			SELECT repository, commit_sha
			  FROM report_groups
			 WHERE id = $1
		`, groupID).Scan(&rgRepo, &rgCommit)
		if err == nil {
			rgExists = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("load report_group for delete: %w", err)
		}

		if !rgExists && len(oldRows) == 0 {
			deleted = false
			return nil
		}

		bucketSet := map[bucketKey]struct{}{}
		commitSet := map[commitKey]struct{}{}
		deltas := map[testExecKey]testExecDelta{}

		if rgExists {
			commitSet[commitKey{Repository: rgRepo, CommitSHA: rgCommit}] = struct{}{}
		}
		for i := range oldRows {
			r := &oldRows[i]
			bucketSet[bucketKey{MetricDate: r.MetricDate.UTC(), Repository: r.Repository, Stage: r.Stage, Lane: r.Lane}] = struct{}{}
			commitSet[commitKey{Repository: r.Repository, CommitSHA: r.CommitSHA}] = struct{}{}
			if !r.IsAllPlatforms {
				if err := addTestCountsJSON(deltas, r.MetricDate, r.Repository, "", r.Stage, r.Lane, r.Platform, r.TestCountsJSON, -1); err != nil {
					return err
				}
			}
		}

		if err := acquireDeterministicLocks(ctx, tx, commitSet, bucketSet); err != nil {
			return err
		}

		tag, err := tx.Exec(ctx, `DELETE FROM report_groups WHERE id = $1`, groupID)
		if err != nil {
			return fmt.Errorf("delete report_group: %w", err)
		}
		deleted = tag.RowsAffected() > 0

		if err := applyTestExecDeltasTx(ctx, tx, deltas); err != nil {
			return err
		}
		return recomputeBucketsAndCommitsTx(ctx, tx, bucketSet, commitSet)
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

const staleGroupRollupsPredicate = `
	(
		g.kpi_rollups_refreshed_at IS NULL
		OR g.kpi_rollups_version < g.kpi_summary_version
		OR g.kpi_rollups_refreshed_at < GREATEST(g.updated_at, g.last_upload_at)
		OR EXISTS (
			SELECT 1 FROM reports r
			 WHERE r.report_group_id = g.id
			   AND r.updated_at > g.kpi_rollups_refreshed_at)
		OR EXISTS (
			SELECT 1 FROM verdicts v
			 WHERE v.report_group_id = g.id
			   AND GREATEST(v.created_at, COALESCE(v.superseded_at, v.created_at)) > g.kpi_rollups_refreshed_at)
		OR EXISTS (
			SELECT 1 FROM orchestration_runs o
			 WHERE o.repository = g.repository
			   AND o.commit_sha = g.commit_sha
			   AND o.gh_run_id = g.gh_run_id
			   AND o.name = g.name
			   AND o.gh_run_attempt = g.gh_run_attempt
			   AND o.updated_at > g.kpi_rollups_refreshed_at)
	)`

// RefreshStaleGroupRollups sweeps unrefreshed or stale report_groups and refreshes their KPI rollups.
// It splits the batch between newest and oldest stale groups so a burst of new activity cannot
// starve older pending groups indefinitely.
func RefreshStaleGroupRollups(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, limit int) error {
	if pool == nil || limit <= 0 {
		return nil
	}
	newestLimit := (limit + 1) / 2
	rows, err := pool.Query(ctx, `
		SELECT g.id
		  FROM report_groups g
		 WHERE `+staleGroupRollupsPredicate+`
		 ORDER BY g.created_at DESC
		 LIMIT $1
	`, newestLimit)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	if remaining := limit - len(ids); remaining > 0 {
		oldRows, err := pool.Query(ctx, `
			SELECT g.id
			  FROM report_groups g
			 WHERE `+staleGroupRollupsPredicate+`
			   AND NOT (g.id = ANY($2::uuid[]))
			 ORDER BY g.created_at ASC
			 LIMIT $1
		`, remaining, ids)
		if err != nil {
			return err
		}
		oldestIDs, err := pgx.CollectRows(oldRows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		ids = append(ids, oldestIDs...)
	}

	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		RefreshGroupRollupsBestEffort(ctx, pool, logger, id)
	}
	return nil
}

func loadOldGroupRollups(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]oldGroupRollup, error) {
	rows, err := tx.Query(ctx, `
		SELECT metric_date, repository, stage, lane, is_all_platforms, platform, commit_sha,
		       has_orchestration, orch_status, is_red, has_retried_unit, total_duration_ms, test_counts
		  FROM kpi_group_rollups
		 WHERE report_group_id = $1
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("load old kpi_group_rollups: %w", err)
	}
	defer rows.Close()

	var out []oldGroupRollup
	for rows.Next() {
		var r oldGroupRollup
		if err := rows.Scan(
			&r.MetricDate, &r.Repository, &r.Stage, &r.Lane, &r.IsAllPlatforms, &r.Platform, &r.CommitSHA,
			&r.HasOrchestration, &r.OrchStatus, &r.IsRed, &r.HasRetriedUnit, &r.TotalDurationMs, &r.TestCountsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan old kpi_group_rollups: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadGroupRow(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) (groupRow, bool, error) {
	var g groupRow
	err := tx.QueryRow(ctx, `
		SELECT g.id, g.created_at, (g.created_at AT TIME ZONE 'UTC')::date, g.repository,
		       COALESCE(a.app, NULLIF(split_part(g.repository, '/', 2), ''), g.repository),
		       g.framework, g.stage, g.name, g.commit_sha, g.gh_run_id, g.gh_run_attempt,
		       g.gh_pr_number, g.status, COALESCE(g.platform, ''), g.total_duration_ms,
		       g.test_stats_json, g.orchestration_json, g.kpi_summary_version
		  FROM report_groups g
		  LEFT JOIN LATERAL (
		      SELECT ap.app
		      FROM apps ap
		      WHERE lower(ap.repository) = lower(g.repository)
		      ORDER BY (ap.repository = g.repository) DESC, ap.repository ASC
		      LIMIT 1
		  ) a ON true
		 WHERE g.id = $1
	`, groupID).Scan(
		&g.ID, &g.CreatedAt, &g.MetricDate, &g.Repository, &g.App,
		&g.Framework, &g.Stage, &g.Lane, &g.CommitSHA, &g.GHRunID, &g.GHRunAttempt,
		&g.GHPRNumber, &g.Status, &g.Platform, &g.TotalDurationMs,
		&g.TestStatsJSON, &g.OrchestrationJSON, &g.SummaryVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return groupRow{}, false, nil
	}
	if err != nil {
		return groupRow{}, false, fmt.Errorf("load report_group: %w", err)
	}
	return g, true, nil
}

func cleanupMissingGroupTx(ctx context.Context, tx pgx.Tx, oldRows []oldGroupRollup) error {
	if len(oldRows) == 0 {
		return nil
	}
	bucketSet := map[bucketKey]struct{}{}
	commitSet := map[commitKey]struct{}{}
	deltas := map[testExecKey]testExecDelta{}
	for i := range oldRows {
		r := &oldRows[i]
		bucketSet[bucketKey{MetricDate: r.MetricDate.UTC(), Repository: r.Repository, Stage: r.Stage, Lane: r.Lane}] = struct{}{}
		commitSet[commitKey{Repository: r.Repository, CommitSHA: r.CommitSHA}] = struct{}{}
		if !r.IsAllPlatforms {
			if err := addTestCountsJSON(deltas, r.MetricDate, r.Repository, "", r.Stage, r.Lane, r.Platform, r.TestCountsJSON, -1); err != nil {
				return err
			}
		}
	}
	if err := acquireDeterministicLocks(ctx, tx, commitSet, bucketSet); err != nil {
		return err
	}
	if err := applyTestExecDeltasTx(ctx, tx, deltas); err != nil {
		return err
	}
	return recomputeBucketsAndCommitsTx(ctx, tx, bucketSet, commitSet)
}

func loadOrchState(ctx context.Context, tx pgx.Tx, g groupRow, oldAll *oldGroupRollup) (orchState, error) {
	var (
		runID          uuid.UUID
		status         string
		failCount      int
		abandonedCount int
		startedAt      time.Time
		hasRetried     bool
		lastTestAt     *time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT o.id, o.status, o.completed_fail_count, o.abandoned_count, o.started_at,
		       (
		           EXISTS (SELECT 1 FROM dispatch_units du WHERE du.run_id = o.id AND du.fail_count > 0)
		           OR EXISTS (SELECT 1 FROM leases l WHERE l.run_id = o.id AND l.kind = 'retest')
		       ) AS has_retried_unit,
		       (SELECT MAX(a.reported_at) FROM attempts a WHERE a.run_id = o.id AND a.reported_at IS NOT NULL) AS last_test_at
		  FROM orchestration_runs o
		 WHERE o.repository = $1
		   AND o.commit_sha = $2
		   AND o.gh_run_id = $3
		   AND o.name = $4
		   AND o.gh_run_attempt = $5
		 LIMIT 1
		   FOR SHARE OF o
	`, g.Repository, g.CommitSHA, g.GHRunID, g.Lane, g.GHRunAttempt).Scan(
		&runID, &status, &failCount, &abandonedCount, &startedAt, &hasRetried, &lastTestAt,
	)
	if err == nil {
		dur := g.TotalDurationMs
		if dur == nil && lastTestAt != nil {
			ms := max(int64(0), lastTestAt.Sub(startedAt).Milliseconds())
			dur = &ms
		}
		return orchState{
			HasOrchestration: true,
			OrchStatus:       &status,
			FailCount:        failCount,
			AbandonedCount:   abandonedCount,
			HasRetriedUnit:   hasRetried,
			TotalDurationMs:  dur,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return orchState{}, fmt.Errorf("load orchestration_runs: %w", err)
	}

	// orchestration_runs row missing (possibly pruned via `tsioctl orchestration prune`).
	if len(g.OrchestrationJSON) > 0 {
		var oj orchJSONPayload
		if uerr := json.Unmarshal(g.OrchestrationJSON, &oj); uerr == nil && oj.Status != "" {
			isTerminalJSON := oj.Status == statusCompleted || oj.Status == statusTimedOut
			hasTerminalOld := oldAll != nil && oldAll.HasOrchestration && oldAll.OrchStatus != nil &&
				(*oldAll.OrchStatus == statusCompleted || *oldAll.OrchStatus == statusTimedOut)
			if isTerminalJSON || !hasTerminalOld {
				dur := g.TotalDurationMs
				if dur == nil && oj.Durations != nil && oj.Durations.LastTestAt != nil {
					ms := max(int64(0), oj.Durations.LastTestAt.Sub(oj.Durations.BeginAt).Milliseconds())
					dur = &ms
				}
				if dur == nil && oldAll != nil {
					dur = oldAll.TotalDurationMs
				}
				return orchState{
					HasOrchestration: true,
					OrchStatus:       &oj.Status,
					FailCount:        oj.Counts.CompletedFail,
					AbandonedCount:   oj.Counts.Abandoned,
					HasRetriedUnit:   oj.Durations != nil && oj.Durations.RetestUnitCount > 0,
					TotalDurationMs:  dur,
				}, nil
			}
		}
	}

	if oldAll != nil && oldAll.HasOrchestration {
		dur := g.TotalDurationMs
		if dur == nil {
			dur = oldAll.TotalDurationMs
		}
		isRed := oldAll.IsRed
		return orchState{
			HasOrchestration: true,
			OrchStatus:       oldAll.OrchStatus,
			HasRetriedUnit:   oldAll.HasRetriedUnit,
			TotalDurationMs:  dur,
			PreservedIsRed:   &isRed,
		}, nil
	}

	return orchState{TotalDurationMs: g.TotalDurationMs}, nil
}

func loadGroupTestResults(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]testResultRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT test_id, project, platform, verdict, first_attempt_passed, signature_ids
		  FROM kpi_test_results
		 WHERE report_group_id = $1
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("query kpi_test_results: %w", err)
	}
	defer rows.Close()

	var out []testResultRow
	for rows.Next() {
		var r testResultRow
		if err := rows.Scan(&r.TestID, &r.Project, &r.Platform, &r.Verdict, &r.FirstAttemptPassed, &r.SignatureIDs); err != nil {
			return nil, fmt.Errorf("scan kpi_test_results: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadGroupPlatforms(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, groupPlatform string, results []testResultRow) ([]string, error) {
	set := map[string]struct{}{}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT COALESCE(NULLIF(r.platform, ''), NULLIF($2, ''), '')
		  FROM reports r
		 WHERE r.report_group_id = $1
	`, groupID, groupPlatform)
	if err != nil {
		return nil, fmt.Errorf("query report platforms: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan report platform: %w", err)
		}
		set[p] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range results {
		set[r.Platform] = struct{}{}
	}
	if len(set) == 0 {
		set[strings.TrimSpace(groupPlatform)] = struct{}{}
	}
	platforms := make([]string, 0, len(set))
	for p := range set {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	return platforms, nil
}

func loadGroupVerdicts(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]verdictRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT test_id, signature_id, label, (reused_from_id IS NOT NULL)
		  FROM verdicts
		 WHERE report_group_id = $1
		   AND superseded_at IS NULL
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("query group verdicts: %w", err)
	}
	defer rows.Close()

	var out []verdictRow
	for rows.Next() {
		var v verdictRow
		if err := rows.Scan(&v.TestID, &v.SignatureID, &v.Label, &v.IsReused); err != nil {
			return nil, fmt.Errorf("scan group verdict: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func aggregateGroupSlices(
	results []testResultRow,
	platforms []string,
	verdicts []verdictRow,
	ghRunAttempt string,
) (sliceCounts, map[string]*sliceCounts) {
	triagedTests := map[uuid.UUID]struct{}{}
	triagedSigs := map[uuid.UUID]struct{}{}
	hasLaneVerdict := false
	for _, v := range verdicts {
		if v.TestID == nil && v.SignatureID == nil {
			hasLaneVerdict = true
			continue
		}
		if v.TestID != nil {
			triagedTests[*v.TestID] = struct{}{}
		}
		if v.SignatureID != nil {
			triagedSigs[*v.SignatureID] = struct{}{}
		}
	}

	isAttempt1 := ghRunAttempt == "1" || ghRunAttempt == ""
	all := sliceCounts{TestCounts: map[uuid.UUID]testExecCounts{}}
	perPlat := make(map[string]*sliceCounts, len(platforms))
	for _, p := range platforms {
		perPlat[p] = &sliceCounts{TestCounts: map[uuid.UUID]testExecCounts{}}
	}

	for _, r := range results {
		ps := perPlat[r.Platform]
		if ps == nil {
			ps = &sliceCounts{TestCounts: map[uuid.UUID]testExecCounts{}}
			perPlat[r.Platform] = ps
		}

		applyResult := func(sc *sliceCounts, recordTestMap bool) {
			sc.TestsTotal++
			var tc testExecCounts
			switch r.Verdict {
			case "passed":
				sc.TestsPassed++
				tc.Run = 1
				tc.Pass = 1
			case "failed":
				sc.TestsFailed++
				sc.FailedTestOccurrences++
				if isTriaged(r, hasLaneVerdict, triagedTests, triagedSigs) {
					sc.TriagedTestOccurrences++
				}
				tc.Run = 1
				tc.Fail = 1
			case "flaky":
				sc.TestsFlaky++
				tc.Run = 1
				tc.Flaky = 1
			case "skipped":
				sc.TestsSkipped++
				tc.Skip = 1
			}
			if r.FirstAttemptPassed {
				sc.FirstAttemptPassedCount++
			}
			if isAttempt1 && r.Verdict != "skipped" {
				sc.Attempt1TestsRun++
				if r.FirstAttemptPassed {
					sc.Attempt1FirstAttempt++
				}
			}
			if recordTestMap {
				cur := sc.TestCounts[r.TestID]
				cur.Run += tc.Run
				cur.Pass += tc.Pass
				cur.Fail += tc.Fail
				cur.Flaky += tc.Flaky
				cur.Skip += tc.Skip
				sc.TestCounts[r.TestID] = cur
			}
		}

		applyResult(&all, false)
		applyResult(ps, true)
	}
	return all, perPlat
}

func isTriaged(r testResultRow, hasLane bool, tests map[uuid.UUID]struct{}, sigs map[uuid.UUID]struct{}) bool {
	if hasLane {
		return true
	}
	if _, ok := tests[r.TestID]; ok {
		return true
	}
	for _, sid := range r.SignatureIDs {
		if _, ok := sigs[sid]; ok {
			return true
		}
	}
	return false
}

func acquireDeterministicLocks(
	ctx context.Context,
	tx pgx.Tx,
	commitSet map[commitKey]struct{},
	bucketSet map[bucketKey]struct{},
) error {
	commits := make([]commitKey, 0, len(commitSet))
	for c := range commitSet {
		commits = append(commits, c)
	}
	sort.Slice(commits, func(i, j int) bool {
		if commits[i].Repository != commits[j].Repository {
			return commits[i].Repository < commits[j].Repository
		}
		return commits[i].CommitSHA < commits[j].CommitSHA
	})
	for _, c := range commits {
		key := "kpi_commit:" + c.Repository + ":" + c.CommitSHA
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return fmt.Errorf("lock commit %s: %w", key, err)
		}
	}

	buckets := make([]bucketKey, 0, len(bucketSet))
	for b := range bucketSet {
		buckets = append(buckets, b)
	}
	sort.Slice(buckets, func(i, j int) bool {
		di := buckets[i].MetricDate.UTC().Format("2006-01-02")
		dj := buckets[j].MetricDate.UTC().Format("2006-01-02")
		if di != dj {
			return di < dj
		}
		if buckets[i].Repository != buckets[j].Repository {
			return buckets[i].Repository < buckets[j].Repository
		}
		if buckets[i].Stage != buckets[j].Stage {
			return buckets[i].Stage < buckets[j].Stage
		}
		return buckets[i].Lane < buckets[j].Lane
	})
	for _, b := range buckets {
		key := fmt.Sprintf("kpi_bucket:%s:%s:%s:%s", b.MetricDate.UTC().Format("2006-01-02"), b.Repository, b.Stage, b.Lane)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return fmt.Errorf("lock bucket %s: %w", key, err)
		}
	}
	return nil
}

const (
	statusCompleted = "completed"
	statusTimedOut  = "timed_out"
)

type groupFlags struct {
	IsRed                  bool
	IsAttempt1Passed       bool
	IsAttempt1StrictPassed bool
}

func computeGroupFlags(
	orch orchState,
	statsPayload testStatsJSONPayload,
	sc sliceCounts,
	numPlatforms int,
	isAll, isAttempt1, isCompleted bool,
) groupFlags {
	if orch.HasOrchestration && (isAll || numPlatforms == 1) {
		orchStatus := ""
		if orch.OrchStatus != nil {
			orchStatus = *orch.OrchStatus
		}
		isRed := orchStatus == statusTimedOut || orch.FailCount > 0 || orch.AbandonedCount > 0
		if orch.PreservedIsRed != nil {
			isRed = *orch.PreservedIsRed
		}
		passed := orchStatus == statusCompleted && orch.FailCount == 0 && orch.AbandonedCount == 0 && !isRed
		att1Pass := isAttempt1 && isCompleted && passed
		return groupFlags{
			IsRed:                  isRed,
			IsAttempt1Passed:       att1Pass,
			IsAttempt1StrictPassed: att1Pass && !orch.HasRetriedUnit,
		}
	}

	var isRed, att1Pass, att1Strict bool
	if sc.TestsTotal > 0 {
		isRed = sc.TestsFailed > 0
		if isAttempt1 && isCompleted {
			att1Pass = sc.TestsFailed == 0 && (sc.TestsPassed+sc.TestsFlaky > 0)
		}
	} else if isAll || numPlatforms == 1 {
		isRed = statsPayload.Failed > 0
		if isAttempt1 && isCompleted {
			att1Pass = statsPayload.Failed == 0 && statsPayload.Total > 0
		}
	}
	if orch.HasOrchestration && att1Pass && orch.OrchStatus != nil && *orch.OrchStatus == statusCompleted && !orch.HasRetriedUnit {
		att1Strict = true
	}
	return groupFlags{
		IsRed:                  isRed,
		IsAttempt1Passed:       att1Pass,
		IsAttempt1StrictPassed: att1Strict,
	}
}

func replaceGroupRollupsTx(
	ctx context.Context,
	tx pgx.Tx,
	g groupRow,
	orch orchState,
	verdicts []verdictRow,
	platforms []string,
	allSlice sliceCounts,
	perPlatformSlices map[string]*sliceCounts,
) error {
	if _, err := tx.Exec(ctx, `DELETE FROM kpi_group_rollups WHERE report_group_id = $1`, g.ID); err != nil {
		return fmt.Errorf("delete kpi_group_rollups: %w", err)
	}

	var statsPayload testStatsJSONPayload
	if len(g.TestStatsJSON) > 0 {
		_ = json.Unmarshal(g.TestStatsJSON, &statsPayload)
	}

	hasActiveVerdict := len(verdicts) > 0
	verdictsTotal := len(verdicts)
	verdictsReused := 0
	labelCounts := map[string]int{}
	for _, v := range verdicts {
		if v.IsReused {
			verdictsReused++
		}
		labelCounts[v.Label]++
	}
	labelJSON, err := json.Marshal(labelCounts)
	if err != nil {
		return err
	}

	isCompleted := g.Status == statusCompleted
	isAttempt1 := g.GHRunAttempt == "1" || g.GHRunAttempt == ""

	insertRow := func(isAll bool, platform string, sc sliceCounts, testCountsJSON []byte) error {
		flags := computeGroupFlags(orch, statsPayload, sc, len(platforms), isAll, isAttempt1, isCompleted)
		_, execErr := tx.Exec(ctx, `
			INSERT INTO kpi_group_rollups (
				report_group_id, is_all_platforms, platform, metric_date, group_created_at,
				repository, app, framework, stage, lane, commit_sha, gh_run_id, gh_run_attempt, gh_pr_number,
				group_status, has_orchestration, orch_status, is_completed, is_red,
				is_attempt1, is_attempt1_passed, is_attempt1_strict_passed, has_retried_unit,
				total_duration_ms,
				tests_total, tests_passed, tests_failed, tests_flaky, tests_skipped,
				first_attempt_passed_count, attempt1_tests_run, attempt1_first_attempt_passed_count,
				failed_test_occurrences, triaged_test_occurrences,
				has_active_verdict, verdicts_total, verdicts_reused, verdict_label_counts,
				test_counts, updated_at
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, $7, $8, $9, $10, $11, $12, $13, $14,
				$15, $16, $17, $18, $19,
				$20, $21, $22, $23,
				$24,
				$25, $26, $27, $28, $29,
				$30, $31, $32,
				$33, $34,
				$35, $36, $37, $38,
				$39, now()
			)
		`,
			g.ID, isAll, platform, g.MetricDate, g.CreatedAt,
			g.Repository, g.App, g.Framework, g.Stage, g.Lane, g.CommitSHA, g.GHRunID, g.GHRunAttempt, g.GHPRNumber,
			g.Status, orch.HasOrchestration, orch.OrchStatus, isCompleted, flags.IsRed,
			isAttempt1, flags.IsAttempt1Passed, flags.IsAttempt1StrictPassed, orch.HasRetriedUnit,
			orch.TotalDurationMs,
			sc.TestsTotal, sc.TestsPassed, sc.TestsFailed, sc.TestsFlaky, sc.TestsSkipped,
			sc.FirstAttemptPassedCount, sc.Attempt1TestsRun, sc.Attempt1FirstAttempt,
			sc.FailedTestOccurrences, sc.TriagedTestOccurrences,
			hasActiveVerdict, verdictsTotal, verdictsReused, labelJSON,
			testCountsJSON,
		)
		return execErr
	}

	if err := insertRow(true, "", allSlice, []byte("{}")); err != nil {
		return fmt.Errorf("insert kpi_group_rollups all: %w", err)
	}
	for _, p := range platforms {
		sc := perPlatformSlices[p]
		if sc == nil {
			sc = &sliceCounts{TestCounts: map[uuid.UUID]testExecCounts{}}
		}
		tcMap := map[string]testExecCounts{}
		for tid, c := range sc.TestCounts {
			tcMap[tid.String()] = c
		}
		tcBytes, merr := json.Marshal(tcMap)
		if merr != nil {
			return merr
		}
		if err := insertRow(false, p, *sc, tcBytes); err != nil {
			return fmt.Errorf("insert kpi_group_rollups platform %q: %w", p, err)
		}
	}
	return nil
}

func replaceGroupSignatureRollupsTx(ctx context.Context, tx pgx.Tx, g groupRow, results []testResultRow) error {
	if _, err := tx.Exec(ctx, `DELETE FROM kpi_group_signature_rollups WHERE report_group_id = $1`, g.ID); err != nil {
		return fmt.Errorf("delete kpi_group_signature_rollups: %w", err)
	}

	type platformSigKey struct {
		Platform    string
		SignatureID uuid.UUID
	}
	failedSigs := map[platformSigKey]struct{}{}
	for _, r := range results {
		if r.Verdict != "failed" {
			continue
		}
		for _, sid := range r.SignatureIDs {
			failedSigs[platformSigKey{Platform: r.Platform, SignatureID: sid}] = struct{}{}
		}
	}
	failedPlatforms := make([]string, 0, len(failedSigs))
	failedSigIDs := make([]uuid.UUID, 0, len(failedSigs))
	for k := range failedSigs {
		failedPlatforms = append(failedPlatforms, k.Platform)
		failedSigIDs = append(failedSigIDs, k.SignatureID)
	}

	_, err := tx.Exec(ctx, `
		WITH sigs AS (
			SELECT
				COALESCE(NULLIF(r.platform, ''), NULLIF($6, ''), '') AS platform,
				tc.signature_id,
				COUNT(DISTINCT (tc.test_id, COALESCE(tc.project, '')))::integer AS occurrence_count,
				COUNT(*)::integer AS raw_attempt_count,
				ARRAY_AGG(DISTINCT tc.test_id) AS affected_test_ids,
				MAX(COALESCE(s.start_time, r.start_time, r.created_at, g.created_at)) AS last_seen_at
			FROM test_cases tc
			JOIN suites s ON s.id = tc.suite_id
			JOIN reports r ON r.id = s.report_id
			JOIN report_groups g ON g.id = r.report_group_id
			WHERE g.id = $1
			  AND tc.test_id IS NOT NULL
			  AND tc.signature_id IS NOT NULL
			GROUP BY COALESCE(NULLIF(r.platform, ''), NULLIF($6, ''), ''), tc.signature_id
		)
		INSERT INTO kpi_group_signature_rollups (
			report_group_id, platform, signature_id,
			metric_date, repository, app, stage, lane, gh_pr_number,
			occurrence_count, raw_attempt_count, affected_test_ids, has_failed_test, last_seen_at
		)
		SELECT
			$1,
			sigs.platform,
			sigs.signature_id,
			$2, $3, $4, $5, $7, $8,
			sigs.occurrence_count,
			sigs.raw_attempt_count,
			sigs.affected_test_ids,
			EXISTS (
				SELECT 1
				FROM UNNEST($9::text[], $10::uuid[]) AS fs(platform, signature_id)
				WHERE fs.platform = sigs.platform
				  AND fs.signature_id = sigs.signature_id
			),
			sigs.last_seen_at
		FROM sigs
	`, g.ID, g.MetricDate, g.Repository, g.App, g.Stage, g.Platform, g.Lane, g.GHPRNumber, failedPlatforms, failedSigIDs)
	if err != nil {
		return fmt.Errorf("insert kpi_group_signature_rollups: %w", err)
	}
	return nil
}

func applyTestExecDeltasTx(ctx context.Context, tx pgx.Tx, deltas map[testExecKey]testExecDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	keys := make([]testExecKey, 0, len(deltas))
	for k, c := range deltas {
		if c.Run == 0 && c.Pass == 0 && c.Fail == 0 && c.Flaky == 0 && c.Skip == 0 && c.App == "" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Slice(keys, func(i, j int) bool {
		di := keys[i].MetricDate.UTC().Format("2006-01-02")
		dj := keys[j].MetricDate.UTC().Format("2006-01-02")
		if di != dj {
			return di < dj
		}
		if keys[i].Repository != keys[j].Repository {
			return keys[i].Repository < keys[j].Repository
		}
		if keys[i].Stage != keys[j].Stage {
			return keys[i].Stage < keys[j].Stage
		}
		if keys[i].Lane != keys[j].Lane {
			return keys[i].Lane < keys[j].Lane
		}
		if keys[i].Platform != keys[j].Platform {
			return keys[i].Platform < keys[j].Platform
		}
		return keys[i].TestID.String() < keys[j].TestID.String()
	})

	batch := &pgx.Batch{}
	for _, k := range keys {
		c := deltas[k]
		if c.Run == 0 && c.Pass == 0 && c.Fail == 0 && c.Flaky == 0 && c.Skip == 0 {
			batch.Queue(`
				UPDATE kpi_daily_test_executions
				   SET app        = $7,
				       updated_at = now()
				 WHERE metric_date = $1 AND repository = $2 AND stage = $3 AND lane = $4 AND platform = $5 AND test_id = $6
				   AND app IS DISTINCT FROM $7
			`, k.MetricDate, k.Repository, k.Stage, k.Lane, k.Platform, k.TestID, c.App)
			continue
		}
		app := c.App
		if app == "" {
			app = k.Repository
		}
		if c.Run <= 0 && c.Pass <= 0 && c.Fail <= 0 && c.Flaky <= 0 && c.Skip <= 0 {
			batch.Queue(`
				UPDATE kpi_daily_test_executions
				   SET app         = COALESCE(NULLIF($12, ''), app),
				       run_count   = run_count   + $7,
				       pass_count  = pass_count  + $8,
				       fail_count  = fail_count  + $9,
				       flaky_count = flaky_count + $10,
				       skip_count  = skip_count  + $11,
				       updated_at  = now()
				 WHERE metric_date = $1 AND repository = $2 AND stage = $3 AND lane = $4 AND platform = $5 AND test_id = $6
			`, k.MetricDate, k.Repository, k.Stage, k.Lane, k.Platform, k.TestID,
				c.Run, c.Pass, c.Fail, c.Flaky, c.Skip, c.App)
		} else {
			batch.Queue(`
				INSERT INTO kpi_daily_test_executions (
					metric_date, repository, app, stage, lane, platform, test_id,
					run_count, pass_count, fail_count, flaky_count, skip_count, updated_at
				)
				SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now()
				 WHERE EXISTS (SELECT 1 FROM tests WHERE id = $7)
				ON CONFLICT (metric_date, repository, stage, lane, platform, test_id) DO UPDATE SET
					app         = COALESCE(NULLIF($13, ''), kpi_daily_test_executions.app),
					run_count   = kpi_daily_test_executions.run_count   + EXCLUDED.run_count,
					pass_count  = kpi_daily_test_executions.pass_count  + EXCLUDED.pass_count,
					fail_count  = kpi_daily_test_executions.fail_count  + EXCLUDED.fail_count,
					flaky_count = kpi_daily_test_executions.flaky_count + EXCLUDED.flaky_count,
					skip_count  = kpi_daily_test_executions.skip_count  + EXCLUDED.skip_count,
					updated_at  = now()
			`, k.MetricDate, k.Repository, app, k.Stage, k.Lane, k.Platform, k.TestID,
				c.Run, c.Pass, c.Fail, c.Flaky, c.Skip, c.App)
		}
		batch.Queue(`
			DELETE FROM kpi_daily_test_executions
			 WHERE metric_date = $1 AND repository = $2 AND stage = $3 AND lane = $4 AND platform = $5 AND test_id = $6
			   AND run_count <= 0 AND pass_count <= 0 AND fail_count <= 0 AND flaky_count <= 0 AND skip_count <= 0
		`, k.MetricDate, k.Repository, k.Stage, k.Lane, k.Platform, k.TestID)
	}
	br := tx.SendBatch(ctx, batch)
	if err := br.Close(); err != nil {
		return fmt.Errorf("apply test execution deltas: %w", err)
	}
	return nil
}

func recomputeBucketsAndCommitsTx(
	ctx context.Context,
	tx pgx.Tx,
	bucketSet map[bucketKey]struct{},
	commitSet map[commitKey]struct{},
) error {
	for b := range bucketSet {
		if _, err := tx.Exec(ctx, `
			DELETE FROM kpi_daily_lane_metrics
			 WHERE metric_date = $1 AND repository = $2 AND stage = $3 AND lane = $4
		`, b.MetricDate, b.Repository, b.Stage, b.Lane); err != nil {
			return fmt.Errorf("delete kpi_daily_lane_metrics: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO kpi_daily_lane_metrics (
				metric_date, repository, app, framework, stage, lane, is_all_platforms, platform,
				tests_total, tests_passed, tests_failed, tests_flaky, tests_skipped,
				first_attempt_passed_count, attempt1_tests_run, attempt1_first_attempt_passed_count,
				total_runs, completed_runs, incomplete_runs, in_progress_runs,
				orchestrated_runs, upload_only_runs,
				attempt1_lanes_total, attempt1_lanes_passed,
				attempt1_lanes_strict_elig, attempt1_lanes_strict_pass,
				duration_samples_ms, duration_sum_ms, duration_count,
				failed_runs_count, triaged_runs_count,
				failed_test_occurrences, triaged_test_occurrences,
				verdicts_total, verdicts_reused, verdict_label_counts, updated_at
			)
			SELECT
				gr.metric_date,
				gr.repository,
				MAX(gr.app),
				MAX(gr.framework),
				gr.stage,
				gr.lane,
				gr.is_all_platforms,
				gr.platform,
				COALESCE(SUM(gr.tests_total), 0)::integer,
				COALESCE(SUM(gr.tests_passed), 0)::integer,
				COALESCE(SUM(gr.tests_failed), 0)::integer,
				COALESCE(SUM(gr.tests_flaky), 0)::integer,
				COALESCE(SUM(gr.tests_skipped), 0)::integer,
				COALESCE(SUM(gr.first_attempt_passed_count), 0)::integer,
				COALESCE(SUM(gr.attempt1_tests_run), 0)::integer,
				COALESCE(SUM(gr.attempt1_first_attempt_passed_count), 0)::integer,
				COUNT(*)::integer,
				COUNT(*) FILTER (WHERE gr.is_completed)::integer,
				COUNT(*) FILTER (WHERE gr.group_status = 'incomplete')::integer,
				COUNT(*) FILTER (WHERE gr.group_status = 'in_progress')::integer,
				COUNT(*) FILTER (WHERE gr.has_orchestration)::integer,
				COUNT(*) FILTER (WHERE NOT gr.has_orchestration)::integer,
				COUNT(*) FILTER (WHERE gr.is_attempt1 AND gr.is_completed)::integer,
				COUNT(*) FILTER (WHERE gr.is_attempt1 AND gr.is_completed AND gr.is_attempt1_passed)::integer,
				COUNT(*) FILTER (WHERE gr.is_attempt1 AND gr.is_completed AND gr.has_orchestration AND gr.orch_status IN ('completed', 'timed_out'))::integer,
				COUNT(*) FILTER (WHERE gr.is_attempt1 AND gr.is_completed AND gr.is_attempt1_strict_passed)::integer,
				COALESCE(ARRAY_AGG(gr.total_duration_ms ORDER BY gr.report_group_id)
					FILTER (WHERE gr.is_completed AND gr.has_orchestration AND gr.total_duration_ms IS NOT NULL), '{}'::bigint[]),
				COALESCE(SUM(gr.total_duration_ms)
					FILTER (WHERE gr.is_completed AND gr.has_orchestration AND gr.total_duration_ms IS NOT NULL), 0)::bigint,
				COUNT(*) FILTER (WHERE gr.is_completed AND gr.has_orchestration AND gr.total_duration_ms IS NOT NULL)::integer,
				COUNT(*) FILTER (WHERE gr.is_red)::integer,
				COUNT(*) FILTER (WHERE gr.is_red AND gr.has_active_verdict)::integer,
				COALESCE(SUM(gr.failed_test_occurrences), 0)::integer,
				COALESCE(SUM(gr.triaged_test_occurrences), 0)::integer,
				COALESCE(SUM(gr.verdicts_total), 0)::integer,
				COALESCE(SUM(gr.verdicts_reused), 0)::integer,
				COALESCE((
					SELECT jsonb_object_agg(kv.key, kv.val)
					FROM (
						SELECT e.key, SUM(e.value::bigint) AS val
						FROM kpi_group_rollups gr2
						CROSS JOIN LATERAL jsonb_each_text(gr2.verdict_label_counts) AS e(key, value)
						WHERE gr2.metric_date = $1
						  AND gr2.repository = $2
						  AND gr2.stage = $3
						  AND gr2.lane = $4
						  AND gr2.is_all_platforms = gr.is_all_platforms
						  AND gr2.platform = gr.platform
						GROUP BY e.key
					) kv
				), '{}'::jsonb),
				now()
			FROM kpi_group_rollups gr
			WHERE gr.metric_date = $1
			  AND gr.repository = $2
			  AND gr.stage = $3
			  AND gr.lane = $4
			GROUP BY gr.metric_date, gr.repository, gr.stage, gr.lane, gr.is_all_platforms, gr.platform
		`, b.MetricDate, b.Repository, b.Stage, b.Lane); err != nil {
			return fmt.Errorf("reaggregate kpi_daily_lane_metrics: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			DELETE FROM kpi_daily_signature_metrics
			 WHERE metric_date = $1 AND repository = $2 AND stage = $3 AND lane = $4
		`, b.MetricDate, b.Repository, b.Stage, b.Lane); err != nil {
			return fmt.Errorf("delete kpi_daily_signature_metrics: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO kpi_daily_signature_metrics (
				metric_date, repository, app, stage, lane, platform, signature_id,
				occurrence_count, raw_attempt_count, group_count,
				affected_test_ids, pr_numbers, last_seen_at, updated_at
			)
			SELECT
				gs.metric_date,
				gs.repository,
				MAX(gs.app),
				gs.stage,
				gs.lane,
				gs.platform,
				gs.signature_id,
				COALESCE(SUM(gs.occurrence_count), 0)::integer,
				COALESCE(SUM(gs.raw_attempt_count), 0)::integer,
				COUNT(DISTINCT gs.report_group_id)::integer,
				COALESCE((
					SELECT ARRAY_AGG(DISTINCT tid)
					FROM kpi_group_signature_rollups gs2
					CROSS JOIN LATERAL UNNEST(gs2.affected_test_ids) AS tid
					WHERE gs2.metric_date = gs.metric_date
					  AND gs2.repository = gs.repository
					  AND gs2.stage = gs.stage
					  AND gs2.lane = gs.lane
					  AND gs2.platform = gs.platform
					  AND gs2.signature_id = gs.signature_id
				), '{}'::uuid[]),
				COALESCE(ARRAY_AGG(DISTINCT gs.gh_pr_number ORDER BY gs.gh_pr_number)
					FILTER (WHERE gs.gh_pr_number IS NOT NULL AND gs.gh_pr_number > 0), '{}'::integer[]),
				MAX(gs.last_seen_at),
				now()
			FROM kpi_group_signature_rollups gs
			WHERE gs.metric_date = $1
			  AND gs.repository = $2
			  AND gs.stage = $3
			  AND gs.lane = $4
			GROUP BY gs.metric_date, gs.repository, gs.stage, gs.lane, gs.platform, gs.signature_id
		`, b.MetricDate, b.Repository, b.Stage, b.Lane); err != nil {
			return fmt.Errorf("reaggregate kpi_daily_signature_metrics: %w", err)
		}
	}

	for c := range commitSet {
		if _, err := tx.Exec(ctx, `
			DELETE FROM kpi_test_commit_outcomes
			 WHERE repository = $1 AND commit_sha = $2
		`, c.Repository, c.CommitSHA); err != nil {
			return fmt.Errorf("delete kpi_test_commit_outcomes: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO kpi_test_commit_outcomes (
				repository, commit_sha, stage, lane, test_id, project, platform,
				app, metric_date, first_seen_at, last_seen_at,
				pass_count, fail_count, flaky_count, skip_count, run_count, updated_at
			)
			SELECT
				r.repository,
				r.commit_sha,
				r.stage,
				r.lane,
				r.test_id,
				r.project,
				r.platform,
				MAX(r.app),
				r.metric_date,
				MIN(r.group_created_at),
				MAX(r.group_created_at),
				COUNT(*) FILTER (WHERE r.verdict IN ('passed', 'flaky'))::integer,
				COUNT(*) FILTER (WHERE r.verdict IN ('failed', 'flaky'))::integer,
				COUNT(*) FILTER (WHERE r.verdict = 'flaky')::integer,
				COUNT(*) FILTER (WHERE r.verdict = 'skipped')::integer,
				COUNT(*) FILTER (WHERE r.verdict <> 'skipped')::integer,
				now()
			FROM kpi_test_results r
			WHERE r.repository = $1 AND r.commit_sha = $2
			GROUP BY r.metric_date, r.repository, r.commit_sha, r.stage, r.lane, r.test_id, r.project, r.platform
		`, c.Repository, c.CommitSHA); err != nil {
			return fmt.Errorf("recompute kpi_test_commit_outcomes: %w", err)
		}
	}
	return nil
}

func addTestCountsJSON(
	deltas map[testExecKey]testExecDelta,
	metricDate time.Time,
	repo, app, stage, lane, platform string,
	raw []byte,
	sign int,
) error {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]testExecCounts
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("unmarshal test_counts: %w", err)
	}
	for idStr, c := range m {
		tid, err := uuid.Parse(idStr)
		if err != nil {
			return fmt.Errorf("parse test_id %q in test_counts: %w", idStr, err)
		}
		k := testExecKey{
			MetricDate: metricDate.UTC(),
			Repository: repo,
			Stage:      stage,
			Lane:       lane,
			Platform:   platform,
			TestID:     tid,
		}
		cur := deltas[k]
		if app != "" {
			cur.App = app
		}
		cur.Run += sign * c.Run
		cur.Pass += sign * c.Pass
		cur.Fail += sign * c.Fail
		cur.Flaky += sign * c.Flaky
		cur.Skip += sign * c.Skip
		deltas[k] = cur
	}
	return nil
}

func ensureNonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func sortStringsDedup(s []string) []string {
	if len(s) == 0 {
		return []string{}
	}
	slices.Sort(s)
	return slices.Compact(s)
}
