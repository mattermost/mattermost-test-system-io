//go:build e2e
// +build e2e

package reportse2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestStatusShadow_PostgresDeltasAndFilters(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := context.Background()

	const (
		repoA = "mattermost/repo-a"
		repoB = "mattermost/repo-b"
	)
	now := time.Now().UTC()

	insertRunWithCases := func(repo, name, ghRunID string, startedAt time.Time, cases []map[string]any) uuid.UUID {
		t.Helper()
		var runID uuid.UUID
		err := env.Pool.QueryRow(ctx, `
			INSERT INTO orchestration_runs
				(repository, commit_sha, gh_run_id, name, gh_run_attempt, branch, framework, status,
				 total_units, pending_count, leased_count, completed_pass_count, completed_fail_count,
				 completed_skipped_count, abandoned_count, retest_eligible_count,
				 lease_timeout_ms, idle_timeout_ms, dispatch_units_hash, started_at, terminal_at, owner_oidc_subject)
			VALUES ($1, $2, $3, $4, '1', 'main', 'playwright', 'completed',
				1, 0, 0, 1, 0, 0, 0, 0, 60000, 60000, '\x00'::bytea, $5, $5, 'test-owner')
			RETURNING id
		`, repo, "0000000000000000000000000000000000000001", ghRunID, name, startedAt).Scan(&runID)
		if err != nil {
			t.Fatalf("insert orchestration_runs %s: %v", name, err)
		}
		var unitID uuid.UUID
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO dispatch_units (run_id, dispatch_seq, spec_path, state, outcome_set_at)
			VALUES ($1, 0, 'specs/a.spec.ts', 'completed_pass', $2)
			RETURNING id
		`, runID, startedAt).Scan(&unitID); err != nil {
			t.Fatalf("insert dispatch_units %s: %v", name, err)
		}
		var leaseID uuid.UUID
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO leases (run_id, gh_job_name, gh_job_id, unit_ids, issued_at, deadline, released_at, release_reason)
			VALUES ($1, 'worker-1', $2, ARRAY[$3::uuid], $4, $4, $4, 'completed')
			RETURNING id
		`, runID, "job-"+ghRunID, unitID, startedAt).Scan(&leaseID); err != nil {
			t.Fatalf("insert leases %s: %v", name, err)
		}
		casesJSON, err := json.Marshal(cases)
		if err != nil {
			t.Fatalf("marshal cases: %v", err)
		}
		if _, err := env.Pool.Exec(ctx, `
			INSERT INTO attempts (run_id, dispatch_unit_id, lease_id, spec_path, status, reported_at, test_cases)
			VALUES ($1, $2, $3, 'specs/a.spec.ts', 'passed', $4, $5::jsonb)
		`, runID, unitID, leaseID, startedAt, string(casesJSON)); err != nil {
			t.Fatalf("insert attempts %s: %v", name, err)
		}
		return runID
	}

	// Run A (repo-a): flaky-only, pending, unknown, blank full_title, empty status, skipped, passed, failed.
	runAID := insertRunWithCases(repoA, "run-edge", "101", now.Add(-1*time.Minute), []map[string]any{
		{"full_title": "t > passed", "status": "passed"},
		{"full_title": "t > flaky", "status": "flaky"},
		{"full_title": "t > pending", "status": "pending"},
		{"full_title": "t > unknown", "status": "unknown"},
		{"full_title": "   ", "status": "failed"},
		{"full_title": "t > empty-status", "status": ""},
		{"full_title": "t > skipped", "status": "skipped"},
		{"full_title": "t > failed", "status": "failed"},
	})
	// Run B (repo-b): all skipped -> legacy rate 0%, shared rate nil (changed=true).
	runBID := insertRunWithCases(repoB, "run-all-skipped", "102", now.Add(-2*time.Minute), []map[string]any{
		{"full_title": "t > s1", "status": "skipped"},
		{"full_title": "t > s2", "status": "skipped"},
	})
	// Run C (repo-a): identical under both rules (changed=false).
	runCID := insertRunWithCases(repoA, "run-unchanged", "103", now.Add(-3*time.Minute), []map[string]any{
		{"full_title": "t > p1", "status": "passed"},
		{"full_title": "t > f1", "status": "failed"},
	})

	// Report group 1 (repo-a): projected test_stats_json with skipped tests -> 60% legacy vs 90% shared.
	var groupAID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups
			(framework, name, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
			 created_at, updated_at, last_upload_at, test_stats_json)
		VALUES ('playwright', 'grp-skipped', 'completed', $1, 'main', '0000000000000000000000000000000000000001', '201', '1',
			$2, $2, $2, '{"TotalSuites":1,"TotalCases":15,"Passed":8,"Failed":1,"Skipped":5,"Flaky":1}'::jsonb)
		RETURNING id
	`, repoA, now.Add(-1*time.Minute)).Scan(&groupAID); err != nil {
		t.Fatalf("insert groupA: %v", err)
	}

	// Report group 2 (repo-b): NULL test_stats_json falling back to reports table (all skipped).
	var groupBID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups
			(framework, name, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
			 created_at, updated_at, last_upload_at, test_stats_json)
		VALUES ('cypress', 'grp-null-fallback', 'completed', $1, 'main', '0000000000000000000000000000000000000001', '202', '1',
			$2, $2, $2, NULL)
		RETURNING id
	`, repoB, now.Add(-2*time.Minute)).Scan(&groupBID); err != nil {
		t.Fatalf("insert groupB: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO reports (report_group_id, name, status, total_suites, total_cases, passed_cases, failed_cases, skipped_cases, flaky_cases)
		VALUES ($1, 'shard-1', 'complete', 1, 3, 0, 0, 3, 0)
	`, groupBID); err != nil {
		t.Fatalf("insert fallback report: %v", err)
	}

	// Report group 3 (repo-a): shardless orchestration-backed group linked to runAID,
	// with stale shared-rule counts cached in test_stats_json. Shadow comparison must
	// compute rule-specific counts from attempts (25% legacy vs 50% shared, +25pp).
	var groupCID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups
			(framework, name, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
			 created_at, updated_at, last_upload_at, test_stats_json)
		VALUES ('playwright', 'run-edge', 'in_progress', $1, 'main', '0000000000000000000000000000000000000001', '101', '1',
			$2, $2, $2, '{"TotalSuites":0,"TotalCases":6,"Passed":1,"Failed":2,"Skipped":2,"Flaky":1}'::jsonb)
		RETURNING id
	`, repoA, now.Add(-30*time.Second)).Scan(&groupCID); err != nil {
		t.Fatalf("insert groupC: %v", err)
	}

	report, err := reports.CompareStatusRules(ctx, env.Pool, reports.StatusShadowOpts{Limit: 50})
	if err != nil {
		t.Fatalf("CompareStatusRules: %v", err)
	}
	if report.OrchestrationRuns.Checked != 3 || report.OrchestrationRuns.Changed != 2 {
		t.Fatalf("orch checked/changed = %d/%d, want 3/2", report.OrchestrationRuns.Checked, report.OrchestrationRuns.Changed)
	}
	if report.ReportGroups.Checked != 3 || report.ReportGroups.Changed != 3 {
		t.Fatalf("groups checked/changed = %d/%d, want 3/3", report.ReportGroups.Checked, report.ReportGroups.Changed)
	}

	byRun := map[uuid.UUID]reports.OrchRunShadowDelta{}
	for _, r := range report.OrchestrationRuns.Rows {
		byRun[r.RunID] = r
	}
	gotA := byRun[runAID]
	if !gotA.Changed ||
		gotA.Legacy.Passed != 2 || gotA.Legacy.Failed != 2 || gotA.Legacy.Flaky != 0 || gotA.Legacy.Skipped != 1 || gotA.Legacy.Total != 8 ||
		gotA.Shared.Passed != 1 || gotA.Shared.Failed != 2 || gotA.Shared.Flaky != 1 || gotA.Shared.Skipped != 2 || gotA.Shared.Total != 6 {
		t.Fatalf("runA = %+v", gotA)
	}
	if gotA.Legacy.PassRate == nil || *gotA.Legacy.PassRate != 25 ||
		gotA.Shared.PassRate == nil || *gotA.Shared.PassRate != 50 ||
		gotA.Delta.PassRate == nil || *gotA.Delta.PassRate != 25 {
		t.Fatalf("runA pass rates = legacy:%v shared:%v delta:%v, want 25/50/+25",
			gotA.Legacy.PassRate, gotA.Shared.PassRate, gotA.Delta.PassRate)
	}
	gotB := byRun[runBID]
	if !gotB.Changed || gotB.Legacy.PassRate == nil || *gotB.Legacy.PassRate != 0 ||
		gotB.Shared.PassRate != nil || gotB.Delta.PassRate != nil {
		t.Fatalf("runB = %+v", gotB)
	}
	gotC := byRun[runCID]
	if gotC.Changed {
		t.Fatalf("runC expected unchanged, got %+v", gotC)
	}

	byGrp := map[uuid.UUID]reports.ReportGroupShadowDelta{}
	for _, g := range report.ReportGroups.Rows {
		byGrp[g.GroupID] = g
	}
	grpA := byGrp[groupAID]
	if !grpA.Changed || grpA.LegacyPassRate == nil || *grpA.LegacyPassRate != 60 ||
		grpA.SharedPassRate == nil || *grpA.SharedPassRate != 90 ||
		grpA.DeltaPassRate == nil || *grpA.DeltaPassRate != 30 {
		t.Fatalf("groupA = %+v", grpA)
	}
	grpB := byGrp[groupBID]
	if !grpB.Changed || grpB.Stats.Skipped != 3 || grpB.Stats.Total != 3 ||
		grpB.LegacyPassRate == nil || *grpB.LegacyPassRate != 0 || grpB.SharedPassRate != nil {
		t.Fatalf("groupB fallback = %+v", grpB)
	}
	grpC := byGrp[groupCID]
	if !grpC.Changed || grpC.Stats.Total != 8 ||
		grpC.LegacyPassRate == nil || *grpC.LegacyPassRate != 25 ||
		grpC.SharedPassRate == nil || *grpC.SharedPassRate != 50 ||
		grpC.DeltaPassRate == nil || *grpC.DeltaPassRate != 25 {
		t.Fatalf("groupC shardless orch = %+v, want total=8 25/50/+25", grpC)
	}

	// Verify --repo filter and --limit bounding.
	filtered, err := reports.CompareStatusRules(ctx, env.Pool, reports.StatusShadowOpts{Limit: 1, Repository: repoA})
	if err != nil {
		t.Fatalf("CompareStatusRules filtered: %v", err)
	}
	if filtered.OrchestrationRuns.Checked != 1 || filtered.OrchestrationRuns.Rows[0].RunID != runAID {
		t.Fatalf("filtered orch = %+v", filtered.OrchestrationRuns)
	}
	if filtered.ReportGroups.Checked != 1 || filtered.ReportGroups.Rows[0].GroupID != groupCID {
		t.Fatalf("filtered groups = %+v", filtered.ReportGroups)
	}
}
