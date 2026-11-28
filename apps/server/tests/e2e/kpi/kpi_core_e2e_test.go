//go:build e2e
// +build e2e

package kpie2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/errorsig"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
	"github.com/mattermost/mattermost-test-system-io/apps/server/migrations"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestMigration000036_UpDownAndNoConcurrentOrTestCasesIndex(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()

	down37SQL, err := migrations.FS.ReadFile("000037_kpi_e2e_lanes.down.sql")
	if err != nil {
		t.Fatalf("read 000037 down: %v", err)
	}
	up37SQL, err := migrations.FS.ReadFile("000037_kpi_e2e_lanes.up.sql")
	if err != nil {
		t.Fatalf("read 000037 up: %v", err)
	}
	upSQL, err := migrations.FS.ReadFile("000036_kpi_rollups.up.sql")
	if err != nil {
		t.Fatalf("read 000036 up: %v", err)
	}
	downSQL, err := migrations.FS.ReadFile("000036_kpi_rollups.down.sql")
	if err != nil {
		t.Fatalf("read 000036 down: %v", err)
	}
	if strings.Contains(strings.ToUpper(string(upSQL)), "CONCURRENTLY") ||
		strings.Contains(strings.ToUpper(string(downSQL)), "CONCURRENTLY") {
		t.Fatal("000036 migration must not contain CREATE/DROP INDEX CONCURRENTLY")
	}

	// Verify zero indexes added to test_cases or suites by 000036.
	rows, err := env.Pool.Query(ctx, `
		SELECT tablename, indexname, indexdef
		  FROM pg_indexes
		 WHERE tablename IN ('test_cases', 'suites')
	`)
	if err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	for rows.Next() {
		var tbl, name, def string
		if err := rows.Scan(&tbl, &name, &def); err != nil {
			rows.Close()
			t.Fatalf("scan pg_indexes: %v", err)
		}
		if strings.Contains(name, "kpi") || strings.Contains(def, "test_id") || strings.Contains(def, "signature_id") {
			rows.Close()
			t.Fatalf("unexpected index on %s: %s (%s)", tbl, name, def)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pg_indexes: %v", err)
	}

	objects := []string{
		"public.kpi_test_results",
		"public.kpi_group_rollups",
		"public.kpi_group_signature_rollups",
		"public.kpi_daily_lane_metrics",
		"public.kpi_daily_signature_metrics",
		"public.kpi_test_commit_outcomes",
		"public.kpi_daily_test_executions",
	}

	assertRegclass := func(wantPresent bool) {
		t.Helper()
		for _, obj := range objects {
			var reg *string
			if err := env.Pool.QueryRow(ctx, `SELECT to_regclass($1)`, obj).Scan(&reg); err != nil {
				t.Fatalf("to_regclass(%s): %v", obj, err)
			}
			if wantPresent && reg == nil {
				t.Fatalf("expected %s to exist", obj)
			}
			if !wantPresent && reg != nil {
				t.Fatalf("expected %s to be dropped, got %s", obj, *reg)
			}
		}
	}

	assertRegclass(true)

	if _, err := env.Pool.Exec(ctx, string(down37SQL)); err != nil {
		t.Fatalf("exec 000037 down: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("exec 000036 down: %v", err)
	}
	assertRegclass(false)

	if _, err := env.Pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("re-exec 000036 up: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, string(up37SQL)); err != nil {
		t.Fatalf("re-exec 000037 up: %v", err)
	}
	assertRegclass(true)
}

func TestKPI_HandComputedSummaryAndFlakyFixtures(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, time.UTC)

	curDay := today.AddDate(0, 0, -2)
	priorDay := today.AddDate(0, 0, -35)

	// Seed tests across Playwright, Cypress, Detox (mobile).
	pwPass1 := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:pw-pass-1", "Login > pass 1")
	pwPass2 := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:pw-pass-2", "Login > pass 2")
	pwFlaky := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:pw-flaky", "Channel > flaky retry")
	pwFail := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:pw-fail", "Channel > always fail")
	pwSkip := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:pw-skip", "Channel > skipped")
	pwOnceFlaky := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:pw-once-flaky", "Search > single commit flaky")

	cyPass := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "cypress", "h:cy-pass", "Cypress > pass")
	cyFlaky := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "cypress", "h:cy-flaky", "Cypress > flaky single row")

	mobDetox := insertTest(t, ctx, env.Pool, "mattermost/mattermost-mobile", "detox", "h:mob-detox", "Mobile > login")
	desktopTest := insertTest(t, ctx, env.Pool, "mattermost/desktop", "playwright", "h:desk-pass", "Desktop > launch")

	// ---- KPI 1 Main Fixtures ----
	// 1. Current main Playwright orchestrated lane (attempt 1, has retried unit, completed green).
	// Tests: pwPass1 (pass), pwPass2 (pass), pwFlaky (failed+passed -> flaky), pwFail (fail), pwSkip (skipped -> excluded).
	// Attempt 1 tests run = 4, first_attempt_passed = 2.
	gMainPW := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-pw-main", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "main", Commit: "main-sha-1",
		RunID: "main-1", Attempt: "1", Stage: "main", CreatedAt: curDay,
		DurMs: int64Ptr(50000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "main-sha-1", RunID: "main-1",
		Name: "e2e-pw-main", Attempt: "1", Status: "completed",
		FailCount: 0, HasRetriedUnit: true, StartedAt: curDay,
	})
	rMainPW := insertReport(t, ctx, env.Pool, gMainPW, "e2e-pw-main", "")
	sMainPW := insertSuite(t, ctx, env.Pool, rMainPW, "Suite")
	insertCase(t, ctx, env.Pool, sMainPW, pwPass1, "pass 1", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sMainPW, pwPass2, "pass 2", "passed", 0, "", 1)
	insertCase(t, ctx, env.Pool, sMainPW, pwFlaky, "flaky retry", "failed", 0, "", 2)
	insertCase(t, ctx, env.Pool, sMainPW, pwFlaky, "flaky retry", "passed", 1, "", 3)
	insertCase(t, ctx, env.Pool, sMainPW, pwFail, "always fail", "failed", 0, "", 4)
	insertCase(t, ctx, env.Pool, sMainPW, pwSkip, "skipped", "skipped", 0, "", 5)
	mustRefresh(t, ctx, env.Pool, gMainPW)

	// 2. Current main Playwright attempt 2 (must be IGNORED by KPI 1 primary & secondary!).
	gMainPWAtt2 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-pw-main", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "main", Commit: "main-sha-1",
		RunID: "main-1", Attempt: "2", Stage: "main", CreatedAt: curDay,
		DurMs: int64Ptr(15000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "main-sha-1", RunID: "main-1",
		Name: "e2e-pw-main", Attempt: "2", Status: "completed",
		FailCount: 0, HasRetriedUnit: false, StartedAt: curDay,
	})
	rMainPW2 := insertReport(t, ctx, env.Pool, gMainPWAtt2, "e2e-pw-main", "")
	sMainPW2 := insertSuite(t, ctx, env.Pool, rMainPW2, "Suite")
	insertCase(t, ctx, env.Pool, sMainPW2, pwPass1, "pass 1", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sMainPW2, pwPass2, "pass 2", "passed", 0, "", 1)
	insertCase(t, ctx, env.Pool, sMainPW2, pwFail, "always fail", "passed", 0, "", 2)
	mustRefresh(t, ctx, env.Pool, gMainPWAtt2)

	// 3. Current main Cypress orchestrated lane (attempt 1, 0 retried units, completed green).
	// Tests: cyPass (passed, retry 0 -> first_attempt_passed=true), cyFlaky (single row status='flaky', retry_count=1 -> NOT first_attempt_passed!).
	// Attempt 1 tests run = 2, first_attempt_passed = 1.
	gMainCY := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "cypress", Name: "e2e-cy-main", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "main", Commit: "main-sha-2",
		RunID: "main-2", Attempt: "1", Stage: "main", CreatedAt: curDay,
		DurMs: int64Ptr(40000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "main-sha-2", RunID: "main-2",
		Name: "e2e-cy-main", Attempt: "1", Status: "completed",
		FailCount: 0, HasRetriedUnit: false, StartedAt: curDay,
	})
	rMainCY := insertReport(t, ctx, env.Pool, gMainCY, "e2e-cy-main", "")
	sMainCY := insertSuite(t, ctx, env.Pool, rMainCY, "Cypress")
	insertCase(t, ctx, env.Pool, sMainCY, cyPass, "cy pass", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sMainCY, cyFlaky, "cy flaky", "flaky", 1, "", 1)
	mustRefresh(t, ctx, env.Pool, gMainCY)

	// 4. Current main Desktop upload-only lane (attempt 1, no orchestration -> excluded from strict lane pass rate).
	// Tests: desktopTest (passed, retry 0 -> first_attempt_passed=true).
	gMainDesk := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "desktop-main", Status: "completed",
		Repo: "mattermost/desktop", Branch: "main", Commit: "desk-sha-1",
		RunID: "desk-1", Attempt: "1", Stage: "main", CreatedAt: curDay,
	})
	rMainDesk := insertReport(t, ctx, env.Pool, gMainDesk, "desktop-main", "")
	sMainDesk := insertSuite(t, ctx, env.Pool, rMainDesk, "Desktop")
	insertCase(t, ctx, env.Pool, sMainDesk, desktopTest, "launch", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gMainDesk)

	// 5. Prior window main Playwright lane (1 passed, 1 failed -> 50% test rate, 0% lane rate).
	gPriorMain := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-pw-main", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "main", Commit: "prior-main-sha",
		RunID: "prior-main-1", Attempt: "1", Stage: "main", CreatedAt: priorDay,
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "prior-main-sha", RunID: "prior-main-1",
		Name: "e2e-pw-main", Attempt: "1", Status: "completed",
		FailCount: 1, HasRetriedUnit: true, StartedAt: priorDay,
	})
	rPriorMain := insertReport(t, ctx, env.Pool, gPriorMain, "e2e-pw-main", "")
	sPriorMain := insertSuite(t, ctx, env.Pool, rPriorMain, "Suite")
	insertCase(t, ctx, env.Pool, sPriorMain, pwPass1, "pass 1", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sPriorMain, pwFail, "always fail", "failed", 0, "", 1)
	mustRefresh(t, ctx, env.Pool, gPriorMain)

	// ---- KPI 5 PR Duration Fixtures + KPI 3 Flaky Fixtures ----
	// PR durations in current window (4 completed orchestrated PR runs): 10000, 20000, 30000, 100000 ms.
	// Hand-computed: p50 = 25000.0, p90 = 79000.0.
	// Also test pwFlaky cross-shard mixed outcome on commit "pr-sha-2" (shard 1 pass, shard 2 fail).
	// Also test pwOnceFlaky single-commit mixed outcome (must be excluded from KPI 3).
	// Also test cyFlaky on a second commit ("pr-sha-3") -> cyFlaky now has 2 flaky commits!
	prNum1 := 101
	gPR1 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-pr", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-101", Commit: "pr-sha-1",
		RunID: "pr-1", Attempt: "1", PRNum: &prNum1, Stage: "pr", CreatedAt: curDay,
		DurMs: int64Ptr(10000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "pr-sha-1", RunID: "pr-1",
		Name: "e2e-pr", Attempt: "1", Status: "completed", StartedAt: curDay,
	})
	rPR1 := insertReport(t, ctx, env.Pool, gPR1, "e2e-pr", "")
	sPR1 := insertSuite(t, ctx, env.Pool, rPR1, "Suite")
	insertCase(t, ctx, env.Pool, sPR1, pwOnceFlaky, "once flaky", "failed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sPR1, pwOnceFlaky, "once flaky", "passed", 1, "", 1)
	mustRefresh(t, ctx, env.Pool, gPR1)

	prNum2 := 102
	gPR2 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-pr", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-102", Commit: "pr-sha-2",
		RunID: "pr-2", Attempt: "1", PRNum: &prNum2, Stage: "pr", CreatedAt: curDay,
		DurMs: int64Ptr(20000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "pr-sha-2", RunID: "pr-2",
		Name: "e2e-pr", Attempt: "1", Status: "completed", StartedAt: curDay,
	})
	rPR2a := insertReport(t, ctx, env.Pool, gPR2, "shard-1", "")
	sPR2a := insertSuite(t, ctx, env.Pool, rPR2a, "Suite")
	insertCase(t, ctx, env.Pool, sPR2a, pwFlaky, "flaky retry", "passed", 0, "", 0)
	rPR2b := insertReport(t, ctx, env.Pool, gPR2, "shard-2", "")
	sPR2b := insertSuite(t, ctx, env.Pool, rPR2b, "Suite")
	insertCase(t, ctx, env.Pool, sPR2b, pwFlaky, "flaky retry", "failed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPR2)

	prNum3 := 103
	gPR3 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "cypress", Name: "e2e-cy-pr", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-103", Commit: "pr-sha-3",
		RunID: "pr-3", Attempt: "1", PRNum: &prNum3, Stage: "pr", CreatedAt: curDay,
		DurMs: int64Ptr(30000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "pr-sha-3", RunID: "pr-3",
		Name: "e2e-cy-pr", Attempt: "1", Status: "completed", StartedAt: curDay,
	})
	rPR3 := insertReport(t, ctx, env.Pool, gPR3, "e2e-cy-pr", "")
	sPR3 := insertSuite(t, ctx, env.Pool, rPR3, "Cypress")
	insertCase(t, ctx, env.Pool, sPR3, cyFlaky, "cy flaky", "flaky", 1, "", 0)
	mustRefresh(t, ctx, env.Pool, gPR3)

	// 4th completed PR orchestrated run: two-platform mobile run (ios pass + android fail on same test_id & commit!).
	// Must contribute ONE sample (100000ms) to unfiltered p50/p90, and NEVER mark mobDetox as flaky!
	prNum4 := 104
	gPRMob1 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "detox", Name: "detox-pr", Status: "completed",
		Repo: "mattermost/mattermost-mobile", Branch: "pr-104", Commit: "mob-sha-1",
		RunID: "mob-1", Attempt: "1", PRNum: &prNum4, Stage: "pr", CreatedAt: curDay,
		DurMs: int64Ptr(100000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost-mobile", Commit: "mob-sha-1", RunID: "mob-1",
		Name: "detox-pr", Attempt: "1", Status: "completed", StartedAt: curDay,
	})
	rMob1IOS := insertReport(t, ctx, env.Pool, gPRMob1, "detox-ios", "ios")
	sMob1IOS := insertSuite(t, ctx, env.Pool, rMob1IOS, "Mobile")
	insertCase(t, ctx, env.Pool, sMob1IOS, mobDetox, "login", "passed", 0, "", 0)
	rMob1And := insertReport(t, ctx, env.Pool, gPRMob1, "detox-android", "android")
	sMob1And := insertSuite(t, ctx, env.Pool, rMob1And, "Mobile")
	insertCase(t, ctx, env.Pool, sMob1And, mobDetox, "login", "failed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPRMob1)

	// 2nd mobile commit with same platform split (ios pass, android fail) -> STILL NOT flaky!
	prNum5 := 105
	gPRMob2 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "detox", Name: "detox-pr", Status: "in_progress", // in_progress -> excluded from KPI 5!
		Repo: "mattermost/mattermost-mobile", Branch: "pr-105", Commit: "mob-sha-2",
		RunID: "mob-2", Attempt: "1", PRNum: &prNum5, Stage: "pr", CreatedAt: curDay,
		DurMs: int64Ptr(999999),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost-mobile", Commit: "mob-sha-2", RunID: "mob-2",
		Name: "detox-pr", Attempt: "1", Status: "in_progress", StartedAt: curDay,
	})
	rMob2IOS := insertReport(t, ctx, env.Pool, gPRMob2, "detox-ios", "ios")
	sMob2IOS := insertSuite(t, ctx, env.Pool, rMob2IOS, "Mobile")
	insertCase(t, ctx, env.Pool, sMob2IOS, mobDetox, "login", "passed", 0, "", 0)
	rMob2And := insertReport(t, ctx, env.Pool, gPRMob2, "detox-android", "android")
	sMob2And := insertSuite(t, ctx, env.Pool, rMob2And, "Mobile")
	insertCase(t, ctx, env.Pool, sMob2And, mobDetox, "login", "failed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPRMob2)

	// Completed PR upload-only lane (total_duration_ms = NULL -> excluded from percentiles, listed in coverage).
	prNum6 := 106
	gPRUpload := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "desktop-pr-upload", Status: "completed",
		Repo: "mattermost/desktop", Branch: "pr-106", Commit: "desk-pr-1",
		RunID: "desk-pr-1", Attempt: "1", PRNum: &prNum6, Stage: "pr", CreatedAt: curDay,
	})
	rPRUpload := insertReport(t, ctx, env.Pool, gPRUpload, "desktop-pr-upload", "")
	sPRUpload := insertSuite(t, ctx, env.Pool, rPRUpload, "Desktop")
	insertCase(t, ctx, env.Pool, sPRUpload, desktopTest, "launch", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPRUpload)

	// Prior window PR duration sample (50000ms) for delta checks.
	prNumPrior := 99
	gPriorPR := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-pr", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-99", Commit: "prior-pr-sha",
		RunID: "prior-pr-1", Attempt: "1", PRNum: &prNumPrior, Stage: "pr", CreatedAt: priorDay,
		DurMs: int64Ptr(50000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "prior-pr-sha", RunID: "prior-pr-1",
		Name: "e2e-pr", Attempt: "1", Status: "completed", StartedAt: priorDay,
	})
	mustRefresh(t, ctx, env.Pool, gPriorPR)

	// ---- Assert GET /api/v1/kpi/summary (anonymous public read) ----
	summary := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?window=30d")

	// KPI 1:
	// Current attempt-1 main tests: e2e-pw-main (4 run, 2 first-attempt passed) + e2e-cy-main (2 run, 1 passed) + desktop-main (1 run, 1 passed)
	// Total = 7, Passed = 4 => 4/7 * 100 = 57.142857...
	// Prior: 2 run, 1 passed => 50.0. Delta = +7.142857...
	if summary.MainPassRate.TestLevel.TotalTests != 7 || summary.MainPassRate.TestLevel.FirstAttemptPassed != 4 {
		t.Fatalf("KPI 1 test counts = %d/%d, want 4/7",
			summary.MainPassRate.TestLevel.FirstAttemptPassed, summary.MainPassRate.TestLevel.TotalTests)
	}
	assertNearPtr(t, "KPI 1 test pass_rate", summary.MainPassRate.TestLevel.PassRate, 400.0/7.0, 1e-6)
	assertNearPtr(t, "KPI 1 prior_pass_rate", summary.MainPassRate.TestLevel.PriorPassRate, 50.0, 1e-6)
	assertNearPtr(t, "KPI 1 delta_pp", summary.MainPassRate.TestLevel.DeltaPP, 400.0/7.0-50.0, 1e-6)

	// KPI 1 Secondary (lane-level):
	// Standard: 3/3 = 100%. Strict (0 retried units, orchestrated only): 1/2 = 50%. Upload-only lanes = 1.
	if summary.MainPassRate.LaneLevel.PassedLanes != 3 || summary.MainPassRate.LaneLevel.TotalLanes != 3 {
		t.Fatalf("KPI 1 lane counts = %d/%d, want 3/3",
			summary.MainPassRate.LaneLevel.PassedLanes, summary.MainPassRate.LaneLevel.TotalLanes)
	}
	assertNearPtr(t, "KPI 1 lane pass_rate", summary.MainPassRate.LaneLevel.PassRate, 100.0, 1e-6)
	if summary.MainPassRate.LaneLevel.StrictPassedLanes != 1 || summary.MainPassRate.LaneLevel.StrictTotalLanes != 2 {
		t.Fatalf("KPI 1 strict lane counts = %d/%d, want 1/2",
			summary.MainPassRate.LaneLevel.StrictPassedLanes, summary.MainPassRate.LaneLevel.StrictTotalLanes)
	}
	assertNearPtr(t, "KPI 1 strict_pass_rate", summary.MainPassRate.LaneLevel.StrictPassRate, 50.0, 1e-6)
	if summary.MainPassRate.LaneCoverage.UploadOnlyLanes != 1 {
		t.Fatalf("KPI 1 upload_only_lanes = %d, want 1", summary.MainPassRate.LaneCoverage.UploadOnlyLanes)
	}

	// KPI 3:
	// pwFlaky (2 commits: main-sha-1, pr-sha-2) + cyFlaky (2 commits: main-sha-2, pr-sha-3) = 2 flaky tests (all in mattermost).
	// pwOnceFlaky (1 commit) excluded, mobDetox (ios pass vs android fail) excluded!
	if summary.FlakyTests.Count != 2 {
		t.Fatalf("KPI 3 flaky count = %d, want 2 (by_app=%+v)", summary.FlakyTests.Count, summary.FlakyTests.ByApp)
	}
	if len(summary.FlakyTests.ByApp) != 1 || summary.FlakyTests.ByApp[0].App != "mattermost" || summary.FlakyTests.ByApp[0].Count != 2 {
		t.Fatalf("KPI 3 by_app = %+v, want [{mattermost 2}]", summary.FlakyTests.ByApp)
	}

	// KPI 5:
	// Completed PR orchestrated durations: [10000, 20000, 30000, 100000] -> sample_count=4, p50=25000, p90=79000.
	// Prior: [50000] -> p50=50000, delta_p50=-25000.
	if summary.E2EDuration.SampleCount != 4 {
		t.Fatalf("KPI 5 sample_count = %d, want 4", summary.E2EDuration.SampleCount)
	}
	assertNearPtr(t, "KPI 5 p50_ms", summary.E2EDuration.P50Ms, 25000.0, 1e-6)
	assertNearPtr(t, "KPI 5 p90_ms", summary.E2EDuration.P90Ms, 79000.0, 1e-6)
	assertNearPtr(t, "KPI 5 delta_p50_ms", summary.E2EDuration.DeltaP50Ms, -25000.0, 1e-6)
	if summary.E2EDuration.LaneCoverage.UploadOnlyLanes != 1 ||
		len(summary.E2EDuration.LaneCoverage.UploadOnlyLaneNames) != 1 ||
		summary.E2EDuration.LaneCoverage.UploadOnlyLaneNames[0] != "desktop-pr-upload" {
		t.Fatalf("KPI 5 upload-only coverage = %+v", summary.E2EDuration.LaneCoverage)
	}

	if len(summary.WeeklySeries) == 0 {
		t.Fatal("expected non-empty weekly_series")
	}

	// ---- Assert platform slicing & mobile non-flaky behavior ----
	mobSummary := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?app=mobile&window=30d")
	if mobSummary.E2EDuration.SampleCount != 1 {
		t.Fatalf("mobile e2e sample_count = %d, want 1 (must not double-count ios+android)", mobSummary.E2EDuration.SampleCount)
	}
	assertNearPtr(t, "mobile p50_ms", mobSummary.E2EDuration.P50Ms, 100000.0, 1e-6)
	if mobSummary.FlakyTests.Count != 0 {
		t.Fatalf("mobile flaky count = %d, want 0 (ios pass vs android fail is not flaky)", mobSummary.FlakyTests.Count)
	}

	iosSummary := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?app=mobile&platform=ios&window=30d")
	if iosSummary.E2EDuration.SampleCount != 1 {
		t.Fatalf("mobile ios sample_count = %d, want 1", iosSummary.E2EDuration.SampleCount)
	}

	// ---- Assert GET /api/v1/kpi/flaky pagination & sorting ----
	flakyP1 := getJSON[kpi.FlakyResponse](t, env.ServerURL+"/api/v1/kpi/flaky?window=14d&sort=name_asc&page=1&limit=1")
	if flakyP1.TotalFlakyTests != 2 || flakyP1.Pagination.TotalPages != 2 || len(flakyP1.Tests) != 1 {
		t.Fatalf("flaky page 1 = %+v", flakyP1)
	}
	if flakyP1.Tests[0].TestID != pwFlaky.String() {
		t.Fatalf("page 1 sorted name_asc got %s, want %s (%s)", flakyP1.Tests[0].FullTitle, pwFlaky, flakyP1.Tests[0].TestID)
	}
	flakyP2 := getJSON[kpi.FlakyResponse](t, env.ServerURL+"/api/v1/kpi/flaky?window=14d&sort=name_asc&page=2&limit=1")
	if len(flakyP2.Tests) != 1 || flakyP2.Tests[0].TestID != cyFlaky.String() {
		t.Fatalf("page 2 sorted name_asc got %+v, want %s", flakyP2.Tests, cyFlaky)
	}
}

func TestKPI_IncrementalRefreshIdempotencyConcurrencySweepVerdictDeleteAndPrune(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()
	now := time.Now().UTC()

	apiKey := env.IssueAPIKey(t, "kpi-lifecycle")
	sigFP := strings.Repeat("a", 64)

	var sigID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO failure_signatures (fingerprint, algo_version, normalized_message, first_seen_at)
		VALUES ($1, $2, 'timeout waiting for selector', $3)
		RETURNING id
	`, sigFP, errorsig.AlgoVersion, now).Scan(&sigID); err != nil {
		t.Fatalf("insert signature: %v", err)
	}

	tPass := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:life-pass", "Life > pass")
	tFail := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:life-fail", "Life > fail")

	gid := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-life", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-777", Commit: "life-sha-1",
		RunID: "life-1", Attempt: "1", Stage: "pr", CreatedAt: now.Add(-1 * time.Hour),
		DurMs: int64Ptr(42000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "life-sha-1", RunID: "life-1",
		Name: "e2e-life", Attempt: "1", Status: "completed",
		FailCount: 1, HasRetriedUnit: true, StartedAt: now.Add(-1 * time.Hour),
	})
	rid := insertReport(t, ctx, env.Pool, gid, "e2e-life", "")
	sid := insertSuite(t, ctx, env.Pool, rid, "Life")
	insertCase(t, ctx, env.Pool, sid, tPass, "pass", "passed", 0, "", 0)
	insertCaseWithSig(t, ctx, env.Pool, sid, tFail, sigID, "fail", "failed", 0, 1)

	// 1. Catch-up sweep (kpi_rollups_refreshed_at is NULL).
	if err := kpi.RefreshStaleGroupRollups(ctx, env.Pool, nil, 10); err != nil {
		t.Fatalf("RefreshStaleGroupRollups: %v", err)
	}
	var sweptSampleLen int
	if err := env.Pool.QueryRow(ctx, `
		SELECT COALESCE(cardinality(duration_samples_ms), 0)
		  FROM kpi_daily_lane_metrics
		 WHERE repository = 'mattermost/mattermost' AND stage = 'pr' AND lane = 'e2e-life' AND is_all_platforms = true
	`).Scan(&sweptSampleLen); err != nil {
		t.Fatalf("query duration_samples_ms after sweep: %v", err)
	}
	if sweptSampleLen != 1 {
		t.Fatalf("after catch-up sweep: duration_samples_ms cardinality = %d, want 1", sweptSampleLen)
	}

	// 2. Idempotency + -race concurrent refresh stress test.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- kpi.RefreshGroupRollups(ctx, env.Pool, gid)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent RefreshGroupRollups failed: %v", err)
		}
	}

	var sampleLen int
	if err := env.Pool.QueryRow(ctx, `
		SELECT COALESCE(cardinality(duration_samples_ms), 0)
		  FROM kpi_daily_lane_metrics
		 WHERE repository = 'mattermost/mattermost' AND stage = 'pr' AND lane = 'e2e-life' AND is_all_platforms = true
	`).Scan(&sampleLen); err != nil {
		t.Fatalf("query duration_samples_ms: %v", err)
	}
	if sampleLen != 1 {
		t.Fatalf("duration_samples_ms cardinality = %d, want 1 (idempotency violated)", sampleLen)
	}

	// 3. POST /api/v1/verdicts triggers KPI rollup refresh (verdicts_total, triaged_test_occurrences).
	// Also confirm secret error_context is never exposed via /kpi/*.
	verdictBody, _ := json.Marshal(map[string]any{
		"repository":      "mattermost/mattermost",
		"report_group_id": gid.String(),
		"verdicts": []any{
			map[string]any{
				"test_id":      tFail.String(),
				"signature_id": sigID.String(),
				"label":        "infra",
				"error_context": map[string]any{
					"token": "ghp_secret_must_never_leak_via_kpi",
				},
			},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/verdicts", bytes.NewReader(verdictBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post verdict: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post verdict status = %d, want 200", resp.StatusCode)
	}

	var verdictsTotal, triagedOcc int
	if err := env.Pool.QueryRow(ctx, `
		SELECT verdicts_total, triaged_test_occurrences
		  FROM kpi_group_rollups
		 WHERE report_group_id = $1 AND is_all_platforms = true
	`, gid).Scan(&verdictsTotal, &triagedOcc); err != nil {
		t.Fatalf("query verdict rollup: %v", err)
	}
	if verdictsTotal != 1 || triagedOcc != 1 {
		t.Fatalf("after POST /verdicts: verdicts_total=%d triaged=%d, want 1/1", verdictsTotal, triagedOcc)
	}

	// 4. Simulate `tsioctl orchestration prune`: delete orchestration_runs and refresh again!
	// Snapshot must preserve has_orchestration, orch_status, is_red, has_retried_unit, total_duration_ms
	// even if report_groups.orchestration_json still holds a stale non-terminal status.
	if _, err := env.Pool.Exec(ctx, `
		UPDATE report_groups
		   SET orchestration_json = '{"status":"in_progress","counts":{"completed_fail":0,"abandoned":0}}'::jsonb
		 WHERE id = $1
	`, gid); err != nil {
		t.Fatalf("set stale orchestration_json: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, `DELETE FROM orchestration_runs WHERE repository = 'mattermost/mattermost'`); err != nil {
		t.Fatalf("delete orchestration_runs: %v", err)
	}
	if err := kpi.RefreshGroupRollups(ctx, env.Pool, gid); err != nil {
		t.Fatalf("RefreshGroupRollups after prune: %v", err)
	}
	var hasOrch, isRed, hasRetried bool
	var orchStatus *string
	var totalDur *int64
	if err := env.Pool.QueryRow(ctx, `
		SELECT has_orchestration, orch_status, is_red, has_retried_unit, total_duration_ms
		  FROM kpi_group_rollups
		 WHERE report_group_id = $1 AND is_all_platforms = true
	`, gid).Scan(&hasOrch, &orchStatus, &isRed, &hasRetried, &totalDur); err != nil {
		t.Fatalf("query post-prune kpi_group_rollups: %v", err)
	}
	if !hasOrch || orchStatus == nil || *orchStatus != "completed" || !isRed || !hasRetried || totalDur == nil || *totalDur != 42000 {
		t.Fatalf("prune survival failed: hasOrch=%v orchStatus=%v isRed=%v hasRetried=%v totalDur=%v",
			hasOrch, orchStatus, isRed, hasRetried, totalDur)
	}

	// Verify /kpi/summary shows the 42000ms sample and never contains error_context.
	rawSum := getRaw(t, env.ServerURL+"/api/v1/kpi/summary?window=30d")
	if strings.Contains(rawSum, "ghp_secret_must_never_leak_via_kpi") || strings.Contains(rawSum, "error_context") {
		t.Fatal("KPI summary leaked error_context")
	}
	var sum kpi.SummaryResponse
	if err := json.Unmarshal([]byte(rawSum), &sum); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if sum.E2EDuration.SampleCount != 1 {
		t.Fatalf("post-prune sample_count = %d, want 1", sum.E2EDuration.SampleCount)
	}

	// 5. DELETE /api/v1/reports/{id} recomputes daily and commit rollups cleanly.
	delReq, _ := http.NewRequest(http.MethodDelete, env.ServerURL+"/api/v1/reports/"+gid.String(), nil)
	delReq.Header.Set("X-API-Key", apiKey)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("delete report group: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}

	var remDaily, remCommit, remExec int
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM kpi_daily_lane_metrics`).Scan(&remDaily); err != nil {
		t.Fatalf("count kpi_daily_lane_metrics: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM kpi_test_commit_outcomes`).Scan(&remCommit); err != nil {
		t.Fatalf("count kpi_test_commit_outcomes: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM kpi_daily_test_executions`).Scan(&remExec); err != nil {
		t.Fatalf("count kpi_daily_test_executions: %v", err)
	}
	if remDaily != 0 || remCommit != 0 || remExec != 0 {
		t.Fatalf("expected 0 remaining rollups after group delete, got daily=%d commit=%d exec=%d", remDaily, remCommit, remExec)
	}

	// Deleting a missing group returns 404.
	missingReq, _ := http.NewRequest(http.MethodDelete, env.ServerURL+"/api/v1/reports/"+uuid.NewString(), nil)
	missingReq.Header.Set("X-API-Key", apiKey)
	missingResp, err := http.DefaultClient.Do(missingReq)
	if err != nil {
		t.Fatalf("delete missing report group: %v", err)
	}
	missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want 404", missingResp.StatusCode)
	}
}

func TestKPI_ConcurrentDeleteVsVerdictRefreshLeavesZeroOrphans(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()
	client := &http.Client{Timeout: 30 * time.Second}
	now := time.Now().UTC()
	apiKey := env.IssueAPIKey(t, "kpi-delete-race")

	var sigID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO failure_signatures (fingerprint, algo_version, normalized_message, first_seen_at)
		VALUES ($1, $2, 'delete race signature', $3)
		RETURNING id
	`, strings.Repeat("b", 64), errorsig.AlgoVersion, now).Scan(&sigID); err != nil {
		t.Fatalf("insert signature: %v", err)
	}

	tHeavy := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:race-heavy", "Race > heavy")

	// Deterministic pre-delete supersession (200 with superseded_id != nil) and post-delete 400 checks.
	detGID := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-det", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-det", Commit: "det-sha",
		RunID: "det-1", Attempt: "1", Stage: "pr", CreatedAt: now,
	})
	detRID := insertReport(t, ctx, env.Pool, detGID, "e2e-det", "")
	detSID := insertSuite(t, ctx, env.Pool, detRID, "Det")
	insertCaseWithSig(t, ctx, env.Pool, detSID, tHeavy, sigID, "det", "failed", 0, 0)

	postVerdict := func(reqCtx context.Context, groupID uuid.UUID, label string) (*http.Response, []byte, error) {
		body, _ := json.Marshal(map[string]any{
			"repository":      "mattermost/mattermost",
			"report_group_id": groupID.String(),
			"verdicts": []any{
				map[string]any{
					"test_id":      tHeavy.String(),
					"signature_id": sigID.String(),
					"label":        label,
				},
			},
		})
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, env.ServerURL+"/api/v1/verdicts", bytes.NewReader(body))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", apiKey)
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		return resp, raw, err
	}

	if resp, _, err := postVerdict(ctx, detGID, "initial"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("deterministic seed verdict failed: err=%v resp=%v", err, resp)
	}
	supResp, supBody, err := postVerdict(ctx, detGID, "infra")
	if err != nil {
		t.Fatalf("deterministic supersede verdict: %v", err)
	}
	if supResp.StatusCode != http.StatusOK {
		t.Fatalf("deterministic supersede status = %d, want 200 (body=%s)", supResp.StatusCode, string(supBody))
	}
	var supParsed struct {
		Verdicts []struct {
			SupersededID *uuid.UUID `json:"superseded_id"`
		} `json:"verdicts"`
	}
	if err := json.Unmarshal(supBody, &supParsed); err != nil {
		t.Fatalf("unmarshal supersede response: %v", err)
	}
	if len(supParsed.Verdicts) != 1 || supParsed.Verdicts[0].SupersededID == nil {
		t.Fatalf("expected 1 superseded verdict with superseded_id != nil, got %s", string(supBody))
	}
	detDelReq, _ := http.NewRequestWithContext(ctx, http.MethodDelete, env.ServerURL+"/api/v1/reports/"+detGID.String(), nil)
	detDelReq.Header.Set("X-API-Key", apiKey)
	detDelResp, err := client.Do(detDelReq)
	if err != nil {
		t.Fatalf("deterministic delete: %v", err)
	}
	detDelResp.Body.Close()
	if detDelResp.StatusCode != http.StatusNoContent {
		t.Fatalf("deterministic delete status = %d, want 204", detDelResp.StatusCode)
	}
	afterDelResp, afterDelBody, err := postVerdict(ctx, detGID, "product-bug")
	if err != nil {
		t.Fatalf("post-delete verdict: %v", err)
	}
	if afterDelResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("post-delete verdict status = %d, want 400 (body=%s)", afterDelResp.StatusCode, string(afterDelBody))
	}

	const numGroups = 16
	gids := make([]uuid.UUID, 0, numGroups)
	for i := range numGroups {
		gid := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "playwright", Name: "e2e-race", Status: "completed",
			Repo: "mattermost/mattermost", Branch: "pr-race", Commit: fmt.Sprintf("race-sha-%d", i%4),
			RunID: fmt.Sprintf("race-%d", i), Attempt: "1", Stage: "pr", CreatedAt: now,
		})
		rid := insertReport(t, ctx, env.Pool, gid, "e2e-race", "")
		sid := insertSuite(t, ctx, env.Pool, rid, "Race")
		for j := range 100 {
			insertCaseWithSig(t, ctx, env.Pool, sid, tHeavy, sigID, "heavy", "failed", 0, j)
		}

		// Pre-seed an active verdict so concurrent POST /api/v1/verdicts calls
		// execute the supersede path (FOR UPDATE + UPDATE verdicts + INSERT verdicts)
		// while racing DELETE /api/v1/reports/{id}.
		seedResp, _, err := postVerdict(ctx, gid, "initial")
		if err != nil {
			t.Fatalf("seed verdict %s: %v", gid, err)
		}
		if seedResp.StatusCode != http.StatusOK {
			t.Fatalf("seed verdict %s status = %d, want 200", gid, seedResp.StatusCode)
		}

		gids = append(gids, gid)
	}

	raceCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	supersedeLabels := []string{"infra", "product-bug"}
	var wg sync.WaitGroup
	errCh := make(chan error, numGroups*4)
	for i, gid := range gids {
		for lIdx, label := range supersedeLabels {
			wg.Add(1)
			go func(idx, labelIdx int, groupID uuid.UUID, lbl string) {
				defer wg.Done()
				if (idx+labelIdx)%2 == 0 {
					time.Sleep(time.Duration((idx+labelIdx)%4) * time.Millisecond)
				}
				resp, _, err := postVerdict(raceCtx, groupID, lbl)
				if err != nil {
					errCh <- fmt.Errorf("post verdict %s (%s): %w", groupID, lbl, err)
					return
				}
				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
					errCh <- fmt.Errorf("post verdict %s (%s) status = %d", groupID, lbl, resp.StatusCode)
				}
			}(i, lIdx, gid, label)
		}

		wg.Add(2)
		go func(groupID uuid.UUID) {
			defer wg.Done()
			if err := kpi.RefreshGroupRollups(raceCtx, env.Pool, groupID); err != nil {
				errCh <- fmt.Errorf("refresh %s: %w", groupID, err)
			}
		}(gid)

		go func(idx int, groupID uuid.UUID) {
			defer wg.Done()
			if idx%2 == 1 {
				time.Sleep(time.Duration((idx%4)+1) * time.Millisecond)
			}
			req, _ := http.NewRequestWithContext(raceCtx, http.MethodDelete, env.ServerURL+"/api/v1/reports/"+groupID.String(), nil)
			req.Header.Set("X-API-Key", apiKey)
			resp, err := client.Do(req)
			if err != nil {
				errCh <- fmt.Errorf("delete %s: %w", groupID, err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				errCh <- fmt.Errorf("delete %s status = %d, want 204", groupID, resp.StatusCode)
			}
		}(i, gid)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	var execRows, execRuns, laneRows, commitRows, groupRows, sigRows, groupSigRows int
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(run_count), 0) FROM kpi_daily_test_executions`).Scan(&execRows, &execRuns); err != nil {
		t.Fatalf("count kpi_daily_test_executions: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM kpi_daily_lane_metrics`).Scan(&laneRows); err != nil {
		t.Fatalf("count kpi_daily_lane_metrics: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM kpi_test_commit_outcomes`).Scan(&commitRows); err != nil {
		t.Fatalf("count kpi_test_commit_outcomes: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM kpi_group_rollups`).Scan(&groupRows); err != nil {
		t.Fatalf("count kpi_group_rollups: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM kpi_daily_signature_metrics`).Scan(&sigRows); err != nil {
		t.Fatalf("count kpi_daily_signature_metrics: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM kpi_group_signature_rollups`).Scan(&groupSigRows); err != nil {
		t.Fatalf("count kpi_group_signature_rollups: %v", err)
	}
	if execRows != 0 || execRuns != 0 || laneRows != 0 || commitRows != 0 || groupRows != 0 || sigRows != 0 || groupSigRows != 0 {
		t.Fatalf("leaked rollup rows after concurrent delete/refresh: execRows=%d execRuns=%d lane=%d commit=%d group=%d sig=%d groupSig=%d",
			execRows, execRuns, laneRows, commitRows, groupRows, sigRows, groupSigRows)
	}
}

func TestKPI_TimedOutStrictPassRate14dFlakyCoveragePerAppPriorAndFirstAttemptSkipped(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	day1 := today.AddDate(0, 0, -1)
	day10 := today.AddDate(0, 0, -10)
	day9 := today.AddDate(0, 0, -9)
	day20 := today.AddDate(0, 0, -20)

	mmPass := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:reg-mm-pass", "Reg > mm pass")
	mmSkipPass := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:reg-mm-skip-pass", "Reg > mm skip+pass")
	mmFlaky := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:reg-mm-flaky", "Reg > mm flaky")
	deskFlaky := insertTest(t, ctx, env.Pool, "mattermost/desktop", "playwright", "h:reg-desk-flaky", "Reg > desktop flaky")

	// 1. Main attempt-1 passing orchestrated lane + synthetic (retry=0 skipped, retry=1 passed) test.
	gMainOK := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-main-ok", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "main", Commit: "reg-main-ok",
		RunID: "reg-main-1", Attempt: "1", Stage: "main", CreatedAt: day1,
		DurMs: int64Ptr(10000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "reg-main-ok", RunID: "reg-main-1",
		Name: "e2e-main-ok", Attempt: "1", Status: "completed", StartedAt: day1,
	})
	rOK := insertReport(t, ctx, env.Pool, gMainOK, "e2e-main-ok", "")
	sOK := insertSuite(t, ctx, env.Pool, rOK, "Suite")
	insertCase(t, ctx, env.Pool, sOK, mmPass, "mm pass", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sOK, mmSkipPass, "mm skip+pass", "skipped", 0, "", 1)
	insertCase(t, ctx, env.Pool, sOK, mmSkipPass, "mm skip+pass", "passed", 1, "", 2)
	mustRefresh(t, ctx, env.Pool, gMainOK)

	// Verify synthetic retry=0 skipped + retry=1 passed has verdict='passed' AND first_attempt_passed=false.
	var verdict string
	var firstAttemptPassed bool
	if err := env.Pool.QueryRow(ctx, `
		SELECT verdict, first_attempt_passed
		  FROM kpi_test_results
		 WHERE report_group_id = $1 AND test_id = $2
	`, gMainOK, mmSkipPass).Scan(&verdict, &firstAttemptPassed); err != nil {
		t.Fatalf("query kpi_test_results for skip+pass: %v", err)
	}
	if verdict != "passed" || firstAttemptPassed {
		t.Fatalf("skip+pass got verdict=%q first_attempt_passed=%v, want passed / false", verdict, firstAttemptPassed)
	}

	// 2. Main attempt-1 timed_out orchestrated lane (must count in strict_total_lanes as failure!).
	// Start as in_progress first, refresh, then transition orchestration_runs to timed_out to also
	// verify RefreshStaleGroupRollups catches completed report_groups whose orchestration transitioned afterward.
	gMainTO := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-main-to", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "main", Commit: "reg-main-to",
		RunID: "reg-main-2", Attempt: "1", Stage: "main", CreatedAt: day1,
	})
	orchTO := insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "reg-main-to", RunID: "reg-main-2",
		Name: "e2e-main-to", Attempt: "1", Status: "in_progress", StartedAt: day1,
	})
	mustRefresh(t, ctx, env.Pool, gMainTO)

	if _, err := env.Pool.Exec(ctx, `
		UPDATE orchestration_runs
		   SET status = 'timed_out',
		       abandoned_count = 1,
		       pending_count = 0,
		       terminal_at = now(),
		       updated_at = now() + interval '1 second'
		 WHERE id = $1
	`, orchTO); err != nil {
		t.Fatalf("update orchestration_runs to timed_out: %v", err)
	}
	if err := kpi.RefreshStaleGroupRollups(ctx, env.Pool, nil, 10); err != nil {
		t.Fatalf("RefreshStaleGroupRollups after orch timed_out: %v", err)
	}

	// 3. Mattermost prior-14d run (day20) + current flaky test across 2 commits (day1, day9).
	gMMPrior := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-mm-prior", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-p1", Commit: "mm-prior-sha",
		RunID: "mm-p1", Attempt: "1", Stage: "pr", CreatedAt: day20,
	})
	rMMPrior := insertReport(t, ctx, env.Pool, gMMPrior, "e2e-mm-prior", "")
	sMMPrior := insertSuite(t, ctx, env.Pool, rMMPrior, "Suite")
	insertCase(t, ctx, env.Pool, sMMPrior, mmPass, "mm pass", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gMMPrior)

	for idx, cday := range []time.Time{day1, day9} {
		gMMF := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "playwright", Name: "e2e-mm-flaky", Status: "completed",
			Repo: "mattermost/mattermost", Branch: "pr-f", Commit: fmt.Sprintf("mm-flaky-%d", idx),
			RunID: fmt.Sprintf("mm-f-%d", idx), Attempt: "1", Stage: "pr", CreatedAt: cday,
		})
		rMMF := insertReport(t, ctx, env.Pool, gMMF, "e2e-mm-flaky", "")
		sMMF := insertSuite(t, ctx, env.Pool, rMMF, "Suite")
		insertCase(t, ctx, env.Pool, sMMF, mmFlaky, "mm flaky", "failed", 0, "", 0)
		insertCase(t, ctx, env.Pool, sMMF, mmFlaky, "mm flaky", "passed", 1, "", 1)
		mustRefresh(t, ctx, env.Pool, gMMF)
	}

	// 4. Desktop flaky test across 2 commits 9-10 days ago (inside 14d, outside 7d) and ZERO runs in prior 14d!
	for idx, cday := range []time.Time{day10, day9} {
		gDeskF := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "playwright", Name: "desktop-10d-ago", Status: "completed",
			Repo: "mattermost/desktop", Branch: "pr-df", Commit: fmt.Sprintf("desk-flaky-%d", idx),
			RunID: fmt.Sprintf("desk-f-%d", idx), Attempt: "1", Stage: "pr", CreatedAt: cday,
		})
		rDeskF := insertReport(t, ctx, env.Pool, gDeskF, "desktop-10d-ago", "")
		sDeskF := insertSuite(t, ctx, env.Pool, rDeskF, "Desktop")
		insertCase(t, ctx, env.Pool, sDeskF, deskFlaky, "desk flaky", "failed", 0, "", 0)
		insertCase(t, ctx, env.Pool, sDeskF, deskFlaky, "desk flaky", "passed", 1, "", 1)
		mustRefresh(t, ctx, env.Pool, gDeskF)
	}

	summary := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?window=7d")

	// Assert KPI 1 test-level (skip+pass excluded from first_attempt_passed -> 1/2 = 50%).
	if summary.MainPassRate.TestLevel.FirstAttemptPassed != 1 || summary.MainPassRate.TestLevel.TotalTests != 2 {
		t.Fatalf("test_level = %d/%d, want 1/2",
			summary.MainPassRate.TestLevel.FirstAttemptPassed, summary.MainPassRate.TestLevel.TotalTests)
	}
	assertNearPtr(t, "test_level pass_rate", summary.MainPassRate.TestLevel.PassRate, 50.0, 1e-6)

	// Assert timed_out orchestrated run counted in strict_total_lanes -> 1/2 = 50%.
	if summary.MainPassRate.LaneLevel.PassedLanes != 1 || summary.MainPassRate.LaneLevel.TotalLanes != 2 {
		t.Fatalf("lane_level = %d/%d, want 1/2",
			summary.MainPassRate.LaneLevel.PassedLanes, summary.MainPassRate.LaneLevel.TotalLanes)
	}
	assertNearPtr(t, "lane_level pass_rate", summary.MainPassRate.LaneLevel.PassRate, 50.0, 1e-6)
	if summary.MainPassRate.LaneLevel.StrictPassedLanes != 1 || summary.MainPassRate.LaneLevel.StrictTotalLanes != 2 {
		t.Fatalf("strict_lane_level = %d/%d, want 1/2",
			summary.MainPassRate.LaneLevel.StrictPassedLanes, summary.MainPassRate.LaneLevel.StrictTotalLanes)
	}
	assertNearPtr(t, "strict_pass_rate", summary.MainPassRate.LaneLevel.StrictPassRate, 50.0, 1e-6)

	// Assert ?window=7d top-level coverage omits desktop-10d-ago, while flaky_tests.lane_coverage includes it!
	for _, ln := range summary.LaneCoverage.CoveredLanes {
		if ln == "desktop-10d-ago" {
			t.Fatalf("7d top-level lane_coverage unexpectedly included desktop-10d-ago: %v", summary.LaneCoverage.CoveredLanes)
		}
	}
	foundDesk10d := false
	for _, ln := range summary.FlakyTests.LaneCoverage.CoveredLanes {
		if ln == "desktop-10d-ago" {
			foundDesk10d = true
			break
		}
	}
	if !foundDesk10d {
		t.Fatalf("flaky_tests.lane_coverage (%v) missing 10d-old flaky lane 'desktop-10d-ago'",
			summary.FlakyTests.LaneCoverage.CoveredLanes)
	}

	// Assert per-app prior_count / delta: mattermost has numeric prior_count=0 (delta=1), desktop has nil prior_count & nil delta!
	assertAppFlaky := func(endpoint string, byApp []kpi.AppFlakyCount) {
		t.Helper()
		m := map[string]kpi.AppFlakyCount{}
		for _, item := range byApp {
			m[item.App] = item
		}
		mm, ok := m["mattermost"]
		if !ok || mm.Count != 1 || mm.PriorCount == nil || *mm.PriorCount != 0 || mm.Delta == nil || *mm.Delta != 1 {
			t.Fatalf("%s mattermost by_app = %+v (exists=%v), want count=1 prior=0 delta=1", endpoint, mm, ok)
		}
		desk, ok := m["desktop"]
		if !ok || desk.Count != 1 || desk.PriorCount != nil || desk.Delta != nil {
			t.Fatalf("%s desktop by_app = %+v (exists=%v), want count=1 prior=nil delta=nil", endpoint, desk, ok)
		}
	}
	assertAppFlaky("/kpi/summary?window=7d", summary.FlakyTests.ByApp)

	flaky := getJSON[kpi.FlakyResponse](t, env.ServerURL+"/api/v1/kpi/flaky?window=14d")
	assertAppFlaky("/kpi/flaky?window=14d", flaky.ByApp)
}

func TestKPI_SummaryVersionOutOfWindowFlakyMixedLaneOldestSweepAndForShare(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	day1 := today.AddDate(0, 0, -1)
	day20 := today.AddDate(0, 0, -20)

	// 1. Out-of-window failure (day20) + in-window pass (day1) on the SAME commit (across 2 commits):
	// Must NOT be counted as flaky in window=14d, MUST be counted as flaky in window=30d.
	tSplit := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:split-flaky", "Split > cross-window")
	for i, sha := range []string{"split-sha-1", "split-sha-2"} {
		gOld := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "playwright", Name: "e2e-split", Status: "completed",
			Repo: "mattermost/mattermost", Branch: "pr-split", Commit: sha,
			RunID: fmt.Sprintf("split-old-%d", i), Attempt: "1", Stage: "pr", CreatedAt: day20,
		})
		rOld := insertReport(t, ctx, env.Pool, gOld, "e2e-split", "")
		sOld := insertSuite(t, ctx, env.Pool, rOld, "Split")
		insertCase(t, ctx, env.Pool, sOld, tSplit, "cross-window", "failed", 0, "", 0)
		mustRefresh(t, ctx, env.Pool, gOld)

		gNew := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "playwright", Name: "e2e-split", Status: "completed",
			Repo: "mattermost/mattermost", Branch: "pr-split", Commit: sha,
			RunID: fmt.Sprintf("split-new-%d", i), Attempt: "1", Stage: "pr", CreatedAt: day1,
		})
		rNew := insertReport(t, ctx, env.Pool, gNew, "e2e-split", "")
		sNew := insertSuite(t, ctx, env.Pool, rNew, "Split")
		insertCase(t, ctx, env.Pool, sNew, tSplit, "cross-window", "passed", 0, "", 0)
		mustRefresh(t, ctx, env.Pool, gNew)
	}

	flaky14 := getJSON[kpi.FlakyResponse](t, env.ServerURL+"/api/v1/kpi/flaky?window=14d")
	if flaky14.TotalFlakyTests != 0 {
		t.Fatalf("14d flaky count = %d, want 0 (out-of-window fail must not leak into 14d)", flaky14.TotalFlakyTests)
	}
	flaky30 := getJSON[kpi.FlakyResponse](t, env.ServerURL+"/api/v1/kpi/flaky?window=30d")
	if flaky30.TotalFlakyTests != 1 {
		t.Fatalf("30d flaky count = %d, want 1", flaky30.TotalFlakyTests)
	}

	// 2. Mixed lane (1 orchestrated + 1 upload-only run on the same lane):
	// UploadOnlyLanes must be 0 and UploadOnlyLaneNames must be empty.
	gMixOrch := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-mixed", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "main", Commit: "mix-sha-1",
		RunID: "mix-1", Attempt: "1", Stage: "main", CreatedAt: day1, DurMs: int64Ptr(15000),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "mix-sha-1", RunID: "mix-1",
		Name: "e2e-mixed", Attempt: "1", Status: "completed", StartedAt: day1,
	})
	mustRefresh(t, ctx, env.Pool, gMixOrch)

	gMixUp := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-mixed", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "main", Commit: "mix-sha-2",
		RunID: "mix-2", Attempt: "1", Stage: "main", CreatedAt: day1,
	})
	mustRefresh(t, ctx, env.Pool, gMixUp)

	sum := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?window=7d&lane=e2e-mixed")
	if sum.LaneCoverage.TotalLanes != 1 ||
		sum.LaneCoverage.OrchestratedLanes != 1 ||
		sum.LaneCoverage.TotalRuns != 2 ||
		sum.LaneCoverage.OrchestratedRuns != 1 ||
		sum.LaneCoverage.UploadOnlyRuns != 1 ||
		sum.LaneCoverage.UploadOnlyLanes != 0 ||
		len(sum.LaneCoverage.UploadOnlyLaneNames) != 0 {
		t.Fatalf("mixed lane coverage = %+v, want total_lanes=1 orchestrated_lanes=1 total_runs=2 orchestrated_runs=1 upload_only_runs=1 upload_only_lanes=0 and empty upload_only_lane_names", sum.LaneCoverage)
	}

	// 3. kpi_summary_version > kpi_rollups_version triggers RefreshStaleGroupRollups even when
	// kpi_rollups_refreshed_at is already stamped ahead of updated_at.
	if _, err := env.Pool.Exec(ctx, `
		UPDATE report_groups
		   SET total_duration_ms = 25000,
		       kpi_summary_version = kpi_summary_version + 1,
		       kpi_rollups_refreshed_at = now() + interval '1 hour'
		 WHERE id = $1
	`, gMixOrch); err != nil {
		t.Fatalf("bump kpi_summary_version: %v", err)
	}
	if err := kpi.RefreshStaleGroupRollups(ctx, env.Pool, nil, 10); err != nil {
		t.Fatalf("RefreshStaleGroupRollups version catch-up: %v", err)
	}
	var gotDur *int64
	var sumVer, rollVer int64
	if err := env.Pool.QueryRow(ctx, `
		SELECT r.total_duration_ms, g.kpi_summary_version, g.kpi_rollups_version
		  FROM kpi_group_rollups r
		  JOIN report_groups g ON g.id = r.report_group_id
		 WHERE r.report_group_id = $1 AND r.is_all_platforms = true
	`, gMixOrch).Scan(&gotDur, &sumVer, &rollVer); err != nil {
		t.Fatalf("query version catch-up: %v", err)
	}
	if gotDur == nil || *gotDur != 25000 || rollVer < sumVer {
		t.Fatalf("version catch-up failed: dur=%v sumVer=%d rollVer=%d", gotDur, sumVer, rollVer)
	}

	// 4. Split newest+oldest stale sweep: with 4 stale groups and limit=2, both newest and oldest refresh.
	staleIDs := make([]uuid.UUID, 4)
	for i := range 4 {
		staleIDs[i] = insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "playwright", Name: "e2e-sweep", Status: "completed",
			Repo: "mattermost/mattermost", Branch: "main", Commit: fmt.Sprintf("sweep-%d", i),
			RunID: fmt.Sprintf("sweep-%d", i), Attempt: "1", Stage: "main",
			CreatedAt: today.Add(time.Duration(i) * time.Minute),
		})
	}
	if err := kpi.RefreshStaleGroupRollups(ctx, env.Pool, nil, 2); err != nil {
		t.Fatalf("RefreshStaleGroupRollups split batch: %v", err)
	}
	var refreshedOldest, refreshedNewest *time.Time
	if err := env.Pool.QueryRow(ctx, `SELECT kpi_rollups_refreshed_at FROM report_groups WHERE id = $1`, staleIDs[0]).Scan(&refreshedOldest); err != nil {
		t.Fatalf("query oldest: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT kpi_rollups_refreshed_at FROM report_groups WHERE id = $1`, staleIDs[3]).Scan(&refreshedNewest); err != nil {
		t.Fatalf("query newest: %v", err)
	}
	if refreshedOldest == nil || refreshedNewest == nil {
		t.Fatalf("split batch starved oldest or missed newest: oldest=%v newest=%v", refreshedOldest, refreshedNewest)
	}

	// 5. FOR SHARE OF o blocks RefreshGroupRollups until concurrent orchestration_runs update commits.
	gOrch := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-share", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "main", Commit: "share-sha",
		RunID: "share-1", Attempt: "1", Stage: "main", CreatedAt: day1,
	})
	orchID := insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "share-sha", RunID: "share-1",
		Name: "e2e-share", Attempt: "1", Status: "in_progress", StartedAt: day1,
	})

	tx, err := env.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin orch writer tx: %v", err)
	}
	var writerPID int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&writerPID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("query writer pg_backend_pid: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE orchestration_runs
		   SET status = 'completed', pending_count = 0, completed_pass_count = 1,
		       terminal_at = clock_timestamp(), updated_at = clock_timestamp()
		 WHERE id = $1
	`, orchID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("update orch in tx: %v", err)
	}

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- kpi.RefreshGroupRollups(ctx, env.Pool, gOrch)
	}()

	waitDeadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err := env.Pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_stat_activity
				 WHERE datname = current_database()
				   AND pid <> $1
				   AND wait_event_type = 'Lock'
				   AND $1 = ANY(pg_blocking_pids(pid))
			)
		`, writerPID).Scan(&blocked); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("poll pg_stat_activity for lock wait: %v", err)
		}
		if blocked {
			break
		}
		select {
		case err := <-refreshDone:
			_ = tx.Rollback(ctx)
			t.Fatalf("RefreshGroupRollups did not block behind uncommitted orchestration_runs lock (err=%v)", err)
		default:
		}
		if time.Now().After(waitDeadline) {
			_ = tx.Rollback(ctx)
			t.Fatalf("timed out waiting for RefreshGroupRollups to block on writer pid %d", writerPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit orch writer tx: %v", err)
	}
	if err := <-refreshDone; err != nil {
		t.Fatalf("RefreshGroupRollups after commit: %v", err)
	}
	var gotOrchStatus *string
	if err := env.Pool.QueryRow(ctx, `
		SELECT orch_status FROM kpi_group_rollups WHERE report_group_id = $1 AND is_all_platforms = true
	`, gOrch).Scan(&gotOrchStatus); err != nil {
		t.Fatalf("query orch_status: %v", err)
	}
	if gotOrchStatus == nil || *gotOrchStatus != "completed" {
		t.Fatalf("orch_status = %v, want 'completed'", gotOrchStatus)
	}
}

func TestKPI_RecurringFailuresStatesLineageTrendRateAndVerdictRedaction(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)

	apiKey := env.IssueAPIKey(t, "kpi-failures-e2e")

	// Tests for each signature scenario:
	// 1. Active + merged_into_id multi-hop lineage (sigOld1 -> sigOld2 -> sigCanon) + 14d vs prior 14d trend + verdict non-leakage.
	// 2. Quiet (>=500 runs in trailing 7d, 0 occurrences in trailing 7d).
	// 3. Not enough runs (<500 runs in trailing 7d, 0 occurrences in trailing 7d).
	// 4. Resurfaced (prior 7d >=500 runs with 0 occurrences, then recurred in trailing 7d) + empty prior 14d (null prior rate & delta).
	tActive := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:rf-active", "RF > active merged test")
	tQuiet := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:rf-quiet", "RF > quiet test")
	tLow := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:rf-low", "RF > low runs test")
	tResurf := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:rf-resurf", "RF > resurfaced test")

	// Seed initial report group for first_seen metadata on sigOld1.
	prFirst := 888
	gFirst := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-rf-pr", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-888", Commit: "first-seen-sha",
		RunID: "rf-first-1", Attempt: "1", PRNum: &prFirst, Stage: "pr", CreatedAt: today.AddDate(0, 0, -20),
	})

	insertSig := func(fp, msg string, firstSeen time.Time, firstGroup *uuid.UUID, mergedInto *uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO failure_signatures (
				fingerprint, algo_version, error_class, normalized_message, top_frame, sample_message,
				first_seen_at, first_seen_report_group_id, merged_into_id
			) VALUES ($1, $2, 'TimeoutError', $3, 'e2e/spec.ts:42', 'TimeoutError: '||$3, $4, $5, $6)
			RETURNING id
		`, fp, errorsig.AlgoVersion, msg, firstSeen, firstGroup, mergedInto).Scan(&id); err != nil {
			t.Fatalf("insert signature %s: %v", msg, err)
		}
		return id
	}

	sigCanon := insertSig(strings.Repeat("1", 64), "canonical selector timeout", today.AddDate(0, 0, -10), nil, nil)
	sigOld2 := insertSig(strings.Repeat("2", 64), "old selector timeout v2", today.AddDate(0, 0, -18), nil, &sigCanon)
	sigOld1 := insertSig(strings.Repeat("3", 64), "old selector timeout v1", today.AddDate(0, 0, -20), &gFirst, &sigOld2)
	sigQuiet := insertSig(strings.Repeat("4", 64), "quiet network reset", today.AddDate(0, 0, -12), nil, nil)
	sigLow := insertSig(strings.Repeat("5", 64), "low runs error", today.AddDate(0, 0, -12), nil, nil)
	sigResurf := insertSig(strings.Repeat("6", 64), "resurfaced deadlock", today.AddDate(0, 0, -25), nil, nil)

	insertDailySig := func(dayOffset int, lane string, sigID uuid.UUID, occ int, testID uuid.UUID, prNums []int) {
		t.Helper()
		d := today.AddDate(0, 0, dayOffset)
		if _, err := env.Pool.Exec(ctx, `
			INSERT INTO kpi_daily_signature_metrics (
				metric_date, repository, app, stage, lane, platform, signature_id,
				occurrence_count, raw_attempt_count, group_count, affected_test_ids, pr_numbers, last_seen_at
			) VALUES ($1, 'mattermost/mattermost', 'mattermost', 'pr', $2, '', $3, $4, $4, 1, $5, $6, $7)
		`, d, lane, sigID, occ, []uuid.UUID{testID}, prNums, d.Add(time.Hour)); err != nil {
			t.Fatalf("insert daily sig metric (%d, %s): %v", dayOffset, lane, err)
		}
	}

	insertDailyExec := func(dayOffset int, lane string, testID uuid.UUID, runs, fails int) {
		t.Helper()
		d := today.AddDate(0, 0, dayOffset)
		if _, err := env.Pool.Exec(ctx, `
			INSERT INTO kpi_daily_test_executions (
				metric_date, repository, app, stage, lane, platform, test_id,
				run_count, pass_count, fail_count, flaky_count, skip_count
			) VALUES ($1, 'mattermost/mattermost', 'mattermost', 'pr', $2, '', $3, $4, $5, $6, 0, 0)
		`, d, lane, testID, runs, runs-fails, fails); err != nil {
			t.Fatalf("insert daily exec (%d, %s): %v", dayOffset, lane, err)
		}
	}

	// 1. Active + merged lineage (sigOld1 -> sigOld2 -> sigCanon on tActive):
	//    - Prior 14d (-20d): sigOld1 occ=2, tActive runs=200 => prior_rate_per_1k_14d = 10.0
	//    - Current 14d (-2d): sigOld1 occ=1 + sigCanon occ=3 (=4 total!), tActive runs=100 (must NOT double count!) => rate_per_1k_14d = 40.0, delta = +30.0
	//    - 30d window: total occ = 6, total runs = 300 => rate_per_1k = 20.0
	insertDailySig(-20, "e2e-rf-pr", sigOld1, 2, tActive, []int{888})
	insertDailyExec(-20, "e2e-rf-pr", tActive, 200, 2)
	insertDailySig(-2, "e2e-rf-pr", sigOld1, 1, tActive, []int{889})
	insertDailySig(-2, "e2e-rf-pr", sigCanon, 3, tActive, []int{890})
	insertDailyExec(-2, "e2e-rf-pr", tActive, 100, 4)

	// 2. Quiet (sigQuiet on tQuiet):
	//    - Seen -10d (occ=2, runs=100), 0 occ in trailing 7d, 500 runs in trailing 7d (-3d: 300, -1d: 200) -> Quiet (10d)
	insertDailySig(-10, "e2e-rf-pr", sigQuiet, 2, tQuiet, []int{891})
	insertDailyExec(-10, "e2e-rf-pr", tQuiet, 100, 2)
	insertDailyExec(-3, "e2e-rf-pr", tQuiet, 300, 0)
	insertDailyExec(-1, "e2e-rf-pr", tQuiet, 200, 0)

	// 3. Not enough runs (sigLow on tLow):
	//    - Seen -9d (occ=1, runs=50), 0 occ in trailing 7d, 499 runs in trailing 7d (-2d: 499) -> Not enough runs (9d)
	insertDailySig(-9, "e2e-rf-pr", sigLow, 1, tLow, []int{892})
	insertDailyExec(-9, "e2e-rf-pr", tLow, 50, 1)
	insertDailyExec(-2, "e2e-rf-pr", tLow, 499, 0)

	// 4. Resurfaced (sigResurf on tResurf):
	//    - Seen -12d (occ=1, runs=10), quiet -11d..-5d (7 days, 0 occ, 550 runs at -8d), recurred -1d (occ=1, runs=50)
	//    - Notice prior 14d (-27d..-14d) has ZERO runs for tResurf => prior_rate_per_1k_14d = nil, delta_rate_per_1k = nil!
	insertDailySig(-12, "e2e-rf-pr", sigResurf, 1, tResurf, []int{893})
	insertDailyExec(-12, "e2e-rf-pr", tResurf, 10, 1)
	insertDailyExec(-8, "e2e-rf-pr", tResurf, 550, 0)
	insertDailySig(-1, "e2e-rf-pr", sigResurf, 1, tResurf, []int{894})
	insertDailyExec(-1, "e2e-rf-pr", tResurf, 50, 1)

	// Attach a verdict to sigOld1 (with secret error_context) via POST /api/v1/verdicts.
	verdictBody, _ := json.Marshal(map[string]any{
		"repository": "mattermost/mattermost",
		"verdicts": []any{
			map[string]any{
				"test_id":      tActive.String(),
				"signature_id": sigOld1.String(),
				"label":        "flaky-infra",
				"decided_by":   "llm",
				"summary":      "Redis connection dropped",
				"model":        "claude-sonnet",
				"triage_url":   "https://github.com/mattermost/mattermost/actions/runs/999",
				"error_context": map[string]any{
					"token": "ghp_super_secret_error_context_12345",
				},
			},
		},
	})
	vReq, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/verdicts", bytes.NewReader(verdictBody))
	vReq.Header.Set("Content-Type", "application/json")
	vReq.Header.Set("X-API-Key", apiKey)
	vResp, err := http.DefaultClient.Do(vReq)
	if err != nil {
		t.Fatalf("post verdict: %v", err)
	}
	vResp.Body.Close()
	if vResp.StatusCode != http.StatusOK {
		t.Fatalf("post verdict status = %d, want 200", vResp.StatusCode)
	}

	// ---- Assert /api/v1/kpi/failures?window=30d ----
	rawFailures := getRaw(t, env.ServerURL+"/api/v1/kpi/failures?window=30d&sort=occurrences_desc")
	if strings.Contains(rawFailures, "ghp_super_secret_error_context_12345") || strings.Contains(rawFailures, "error_context") {
		t.Fatal("/kpi/failures leaked error_context")
	}
	var failResp kpi.FailuresResponse
	if err := json.Unmarshal([]byte(rawFailures), &failResp); err != nil {
		t.Fatalf("unmarshal /kpi/failures: %v", err)
	}

	if failResp.Summary.TotalSignatures != 4 ||
		failResp.Summary.ActiveCount != 1 ||
		failResp.Summary.ResurfacedCount != 1 ||
		failResp.Summary.QuietCount != 1 ||
		failResp.Summary.NotEnoughRunsCount != 1 ||
		failResp.Summary.TotalOccurrences != 11 ||
		failResp.Summary.TriagedCount != 1 {
		t.Fatalf("unexpected failures summary: %+v", failResp.Summary)
	}

	bySig := map[string]kpi.RecurringFailureRow{}
	for _, row := range failResp.Failures {
		bySig[row.SignatureID] = row
		if row.State == "Fixed" {
			t.Fatalf("state must never be Fixed: %+v", row)
		}
	}

	activeRow, ok := bySig[sigCanon.String()]
	if !ok {
		t.Fatalf("canonical signature %s missing from /kpi/failures (keys=%v)", sigCanon, len(bySig))
	}
	if _, mergedPresent := bySig[sigOld1.String()]; mergedPresent {
		t.Fatal("merged alias sigOld1 must not appear as a separate row")
	}
	if activeRow.State != kpi.FailureStateActive || activeRow.QuietDays != nil {
		t.Fatalf("activeRow state = (%q, %v), want Active/nil", activeRow.State, activeRow.QuietDays)
	}
	if activeRow.OccurrenceCount != 6 || activeRow.AffectedTestCount != 1 || activeRow.AffectedTestRuns != 300 {
		t.Fatalf("activeRow counts = occ:%d tests:%d runs:%d, want 6/1/300",
			activeRow.OccurrenceCount, activeRow.AffectedTestCount, activeRow.AffectedTestRuns)
	}
	assertNearPtr(t, "active rate_per_1k (30d)", activeRow.RatePer1k, 20.0, 1e-6)
	assertNearPtr(t, "active rate_per_1k_14d", activeRow.RatePer1k14d, 40.0, 1e-6)
	assertNearPtr(t, "active prior_rate_per_1k_14d", activeRow.PriorRatePer1k14d, 10.0, 1e-6)
	assertNearPtr(t, "active delta_rate_per_1k", activeRow.DeltaRatePer1k, 30.0, 1e-6)
	if activeRow.LatestVerdictLabel == nil || *activeRow.LatestVerdictLabel != "flaky-infra" {
		t.Fatalf("activeRow latest_verdict_label = %v, want flaky-infra", activeRow.LatestVerdictLabel)
	}
	if len(activeRow.MergedSignatureIDs) != 2 {
		t.Fatalf("activeRow merged_signature_ids = %v, want 2 merged IDs", activeRow.MergedSignatureIDs)
	}

	quietRow, ok := bySig[sigQuiet.String()]
	if !ok || quietRow.State != kpi.FailureStateQuiet || quietRow.QuietDays == nil || *quietRow.QuietDays != 10 {
		t.Fatalf("quietRow = %+v, want Quiet (10d)", quietRow)
	}

	lowRow, ok := bySig[sigLow.String()]
	if !ok || lowRow.State != kpi.FailureStateNotEnoughRuns || lowRow.QuietDays == nil || *lowRow.QuietDays != 9 {
		t.Fatalf("lowRow = %+v, want Not enough runs (9d)", lowRow)
	}

	resurfRow, ok := bySig[sigResurf.String()]
	if !ok || resurfRow.State != kpi.FailureStateResurfaced || resurfRow.QuietDays != nil {
		t.Fatalf("resurfRow = %+v, want Resurfaced/nil", resurfRow)
	}
	if resurfRow.PriorRatePer1k14d != nil || resurfRow.DeltaRatePer1k != nil {
		t.Fatalf("resurfRow prior/delta = (%v, %v), want nil/nil due to 0 prior runs",
			resurfRow.PriorRatePer1k14d, resurfRow.DeltaRatePer1k)
	}

	// Verify Resurfaced persists under window=14d as well.
	fail14d := getJSON[kpi.FailuresResponse](t, env.ServerURL+"/api/v1/kpi/failures?window=14d")
	foundResurf14d := false
	for _, r := range fail14d.Failures {
		if r.SignatureID == sigResurf.String() {
			foundResurf14d = true
			if r.State != kpi.FailureStateResurfaced {
				t.Fatalf("under window=14d resurfRow state = %q, want Resurfaced", r.State)
			}
		}
	}
	if !foundResurf14d {
		t.Fatal("sigResurf missing under window=14d")
	}

	// ---- Assert GET /api/v1/kpi/failures/{oldMergedID} lineage + detail + redaction ----
	rawDetail := getRaw(t, env.ServerURL+"/api/v1/kpi/failures/"+sigOld1.String()+"?window=30d")
	if strings.Contains(rawDetail, "ghp_super_secret_error_context_12345") || strings.Contains(rawDetail, "error_context") {
		t.Fatal("/kpi/failures/{id} leaked error_context")
	}
	var detail kpi.FailureDetailResponse
	if err := json.Unmarshal([]byte(rawDetail), &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail.RequestedSignatureID != sigOld1.String() || detail.CanonicalSignatureID != sigCanon.String() {
		t.Fatalf("detail IDs = (%s -> %s), want (%s -> %s)",
			detail.RequestedSignatureID, detail.CanonicalSignatureID, sigOld1, sigCanon)
	}
	if detail.MergedIntoID == nil || *detail.MergedIntoID != sigOld2.String() {
		t.Fatalf("detail merged_into_id = %v, want %s", detail.MergedIntoID, sigOld2)
	}
	if detail.State != kpi.FailureStateActive || detail.OccurrenceCount != 6 || detail.AffectedTestRuns != 300 {
		t.Fatalf("detail metrics = state:%s occ:%d runs:%d", detail.State, detail.OccurrenceCount, detail.AffectedTestRuns)
	}
	if detail.FirstSeen.ReportGroupID == nil || *detail.FirstSeen.ReportGroupID != gFirst.String() ||
		detail.FirstSeen.CommitSHA == nil || *detail.FirstSeen.CommitSHA != "first-seen-sha" ||
		detail.FirstSeen.GHPRNumber == nil || *detail.FirstSeen.GHPRNumber != prFirst {
		t.Fatalf("detail first_seen = %+v", detail.FirstSeen)
	}
	if detail.LatestVerdict == nil || detail.LatestVerdict.Label != "flaky-infra" ||
		detail.LatestVerdict.Model == nil || *detail.LatestVerdict.Model != "claude-sonnet" ||
		detail.LatestVerdict.TriageURL == nil || detail.LatestVerdict.Summary == nil {
		t.Fatalf("detail latest_verdict = %+v", detail.LatestVerdict)
	}
	if len(detail.DailySeries) != 30 {
		t.Fatalf("detail daily_series len = %d, want 30", len(detail.DailySeries))
	}
	if len(detail.AffectedTests) != 1 || detail.AffectedTests[0].TestID != tActive.String() {
		t.Fatalf("detail affected_tests = %+v", detail.AffectedTests)
	}
	if len(detail.AffectedRepos) != 1 || detail.AffectedRepos[0].Repository != "mattermost/mattermost" {
		t.Fatalf("detail affected_repos = %+v", detail.AffectedRepos)
	}

	// ---- Assert 400 and 404 on /kpi/failures/{id} ----
	badResp, err := http.Get(env.ServerURL + "/api/v1/kpi/failures/invalid-uuid") //nolint:gosec
	if err != nil {
		t.Fatalf("GET invalid uuid: %v", err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid uuid status = %d, want 400", badResp.StatusCode)
	}

	notFoundResp, err := http.Get(env.ServerURL + "/api/v1/kpi/failures/" + uuid.NewString()) //nolint:gosec
	if err != nil {
		t.Fatalf("GET unknown uuid: %v", err)
	}
	notFoundResp.Body.Close()
	if notFoundResp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown uuid status = %d, want 404", notFoundResp.StatusCode)
	}

	// ---- Assert KPISummaryResponse.recurring_failures & weekly_series[].recurring_failures_count ----
	summary := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?window=30d")
	if summary.RecurringFailures == nil || *summary.RecurringFailures != failResp.Summary {
		t.Fatalf("summary.recurring_failures = %+v, want %+v", summary.RecurringFailures, failResp.Summary)
	}
	nonZeroWeek := false
	for _, wp := range summary.WeeklySeries {
		if wp.RecurringFailuresCount == nil {
			t.Fatalf("weekly_series[%s].recurring_failures_count is nil", wp.WeekStart)
		}
		if *wp.RecurringFailuresCount > 0 {
			nonZeroWeek = true
		}
	}
	if !nonZeroWeek {
		t.Fatal("expected at least one week with recurring_failures_count > 0")
	}
}

func TestKPI_RecurringFailuresCrossScopeFirstSeenAndLassoCycle(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)

	tCross := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:rf-cross", "RF > cross scope")
	tLasso := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:rf-lasso", "RF > lasso cycle")

	// 1. Cross-scope first_seen_at regression:
	//    sCross occurred on stage='main' at -25d (global first_seen_at = -25d).
	//    On stage='pr', tCross had 600 passing runs at -20d and sCross occurred for the
	//    very first time on stage='pr' at -1d.
	//    Querying ?stage=pr must classify sCross as Active (not Resurfaced) on both /failures and /failures/{id}.
	var sCross uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO failure_signatures (
			fingerprint, algo_version, error_class, normalized_message, top_frame, sample_message, first_seen_at
		) VALUES ($1, $2, 'TimeoutError', 'cross scope first seen', 'e2e/cross.spec.ts:10', 'TimeoutError: cross', $3)
		RETURNING id
	`, strings.Repeat("c", 64), errorsig.AlgoVersion, today.AddDate(0, 0, -25)).Scan(&sCross); err != nil {
		t.Fatalf("insert sCross: %v", err)
	}

	insertMetric := func(dayOffset int, stage, lane string, sigID uuid.UUID, occ int, testID uuid.UUID) {
		t.Helper()
		d := today.AddDate(0, 0, dayOffset)
		if _, err := env.Pool.Exec(ctx, `
			INSERT INTO kpi_daily_signature_metrics (
				metric_date, repository, app, stage, lane, platform, signature_id,
				occurrence_count, raw_attempt_count, group_count, affected_test_ids, pr_numbers, last_seen_at
			) VALUES ($1, 'mattermost/mattermost', 'mattermost', $2, $3, '', $4, $5, $5, 1, $6, '{}'::integer[], $7)
		`, d, stage, lane, sigID, occ, []uuid.UUID{testID}, d.Add(time.Hour)); err != nil {
			t.Fatalf("insert metric (%d, %s, %s): %v", dayOffset, stage, lane, err)
		}
	}
	insertExec := func(dayOffset int, stage, lane string, testID uuid.UUID, runs, fails int) {
		t.Helper()
		d := today.AddDate(0, 0, dayOffset)
		if _, err := env.Pool.Exec(ctx, `
			INSERT INTO kpi_daily_test_executions (
				metric_date, repository, app, stage, lane, platform, test_id,
				run_count, pass_count, fail_count, flaky_count, skip_count
			) VALUES ($1, 'mattermost/mattermost', 'mattermost', $2, $3, '', $4, $5, $6, $7, 0, 0)
		`, d, stage, lane, testID, runs, runs-fails, fails); err != nil {
			t.Fatalf("insert exec (%d, %s, %s): %v", dayOffset, stage, lane, err)
		}
	}

	insertMetric(-25, "main", "e2e-cross-main", sCross, 1, tCross)
	insertExec(-25, "main", "e2e-cross-main", tCross, 10, 1)
	insertExec(-20, "pr", "e2e-cross-pr", tCross, 600, 0)
	insertMetric(-1, "pr", "e2e-cross-pr", sCross, 1, tCross)
	insertExec(-1, "pr", "e2e-cross-pr", tCross, 10, 1)

	prList := getJSON[kpi.FailuresResponse](t, env.ServerURL+"/api/v1/kpi/failures?repo=mattermost/mattermost&stage=pr&window=30d")
	if len(prList.Failures) != 1 || prList.Failures[0].SignatureID != sCross.String() {
		t.Fatalf("prList.Failures = %+v, want 1 row for %s", prList.Failures, sCross)
	}
	if prList.Failures[0].State != kpi.FailureStateActive {
		t.Fatalf("GET /kpi/failures?stage=pr state = %q, want %q", prList.Failures[0].State, kpi.FailureStateActive)
	}
	prDetail := getJSON[kpi.FailureDetailResponse](t, env.ServerURL+"/api/v1/kpi/failures/"+sCross.String()+"?repo=mattermost/mattermost&stage=pr&window=30d")
	if prDetail.State != kpi.FailureStateActive {
		t.Fatalf("GET /kpi/failures/{id}?stage=pr state = %q, want %q", prDetail.State, kpi.FailureStateActive)
	}

	// Unscoped (global across main + pr): was seen at -25d on main, had 600 runs at -20d, recurred at -1d => Resurfaced.
	unscopedDetail := getJSON[kpi.FailureDetailResponse](t, env.ServerURL+"/api/v1/kpi/failures/"+sCross.String()+"?window=30d")
	if unscopedDetail.State != kpi.FailureStateResurfaced {
		t.Fatalf("unscoped GET /kpi/failures/{id} state = %q, want %q", unscopedDetail.State, kpi.FailureStateResurfaced)
	}

	// 2. Lasso cycle canonicalization regression:
	//    L1 -> L2 -> L3 -> L2 with L1 < L2 < L3.
	//    Minimum UUID among cycle members {L2, L3} is L2 (not L1).
	l1 := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	l2 := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	l3 := uuid.MustParse("00000000-0000-4000-8000-000000000003")

	for _, sig := range []struct {
		id uuid.UUID
		fp string
	}{
		{l1, strings.Repeat("d", 64)},
		{l2, strings.Repeat("e", 64)},
		{l3, strings.Repeat("f", 64)},
	} {
		if _, err := env.Pool.Exec(ctx, `
			INSERT INTO failure_signatures (
				id, fingerprint, algo_version, error_class, normalized_message, top_frame, sample_message, first_seen_at
			) VALUES ($1, $2, $3, 'LassoError', 'lasso cycle error', 'e2e/lasso.spec.ts:1', 'LassoError', $4)
		`, sig.id, sig.fp, errorsig.AlgoVersion, today.AddDate(0, 0, -5)); err != nil {
			t.Fatalf("insert lasso sig %s: %v", sig.id, err)
		}
	}
	for _, edge := range []struct {
		from, to uuid.UUID
	}{
		{l1, l2},
		{l2, l3},
		{l3, l2},
	} {
		if _, err := env.Pool.Exec(ctx, `UPDATE failure_signatures SET merged_into_id = $1 WHERE id = $2`, edge.to, edge.from); err != nil {
			t.Fatalf("update merged_into_id %s->%s: %v", edge.from, edge.to, err)
		}
	}

	insertMetric(-1, "pr", "e2e-lasso", l1, 1, tLasso)
	insertMetric(-1, "pr", "e2e-lasso", l2, 2, tLasso)
	insertExec(-1, "pr", "e2e-lasso", tLasso, 100, 3)

	lassoList := getJSON[kpi.FailuresResponse](t, env.ServerURL+"/api/v1/kpi/failures?lane=e2e-lasso&window=30d")
	if lassoList.Summary.TotalSignatures != 1 || lassoList.Summary.TotalOccurrences != 3 || len(lassoList.Failures) != 1 {
		t.Fatalf("lassoList summary/len = (%+v, %d), want 1 sig / 3 occ", lassoList.Summary, len(lassoList.Failures))
	}
	if lassoList.Failures[0].SignatureID != l2.String() || lassoList.Failures[0].OccurrenceCount != 3 || lassoList.Failures[0].AffectedTestRuns != 100 {
		t.Fatalf("lassoList row = %+v, want sig=%s occ=3 runs=100", lassoList.Failures[0], l2)
	}

	for _, reqID := range []uuid.UUID{l1, l2, l3} {
		d := getJSON[kpi.FailureDetailResponse](t, env.ServerURL+"/api/v1/kpi/failures/"+reqID.String()+"?lane=e2e-lasso&window=30d")
		if d.CanonicalSignatureID != l2.String() || d.OccurrenceCount != 3 || d.AffectedTestRuns != 100 {
			t.Fatalf("GET /kpi/failures/%s = canon:%s occ:%d runs:%d, want canon:%s occ:3 runs:100",
				reqID, d.CanonicalSignatureID, d.OccurrenceCount, d.AffectedTestRuns, l2)
		}
		if len(d.MergedSignatureIDs) != 2 || d.MergedSignatureIDs[0] != l1.String() || d.MergedSignatureIDs[1] != l3.String() {
			t.Fatalf("GET /kpi/failures/%s merged_signature_ids = %v, want [%s %s]", reqID, d.MergedSignatureIDs, l1, l3)
		}
	}

	lassoSummary := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?lane=e2e-lasso&window=30d")
	if lassoSummary.RecurringFailures == nil ||
		lassoSummary.RecurringFailures.TotalSignatures != 1 ||
		lassoSummary.RecurringFailures.TotalOccurrences != 3 {
		t.Fatalf("lassoSummary.RecurringFailures = %+v, want total_sig=1 total_occ=3", lassoSummary.RecurringFailures)
	}
	weeklyCountSum := 0
	for _, wp := range lassoSummary.WeeklySeries {
		if wp.RecurringFailuresCount != nil {
			weeklyCountSum += *wp.RecurringFailuresCount
		}
	}
	if weeklyCountSum != 1 {
		t.Fatalf("lassoSummary weekly recurring_failures_count sum = %d, want 1", weeklyCountSum)
	}
}

func TestKPI_TriageSummaryWeeklyCoverageAndTestState(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)

	apiKey := env.IssueAPIKey(t, "kpi-triage-e2e")

	tFlaky := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:ts-flaky", "TS > flaky")
	tNewRecent := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:ts-new-recent", "TS > new recent")
	tNewOneCommit := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:ts-new-one", "TS > new single commit")
	tKnown := insertTest(t, ctx, env.Pool, "mattermost/mattermost", "playwright", "h:ts-known", "TS > known")
	tMobile := insertTest(t, ctx, env.Pool, "mattermost/mattermost-mobile", "detox", "h:ts-mob", "TS > mobile split")

	// Force older first_seen_at on tNewOneCommit and tKnown so tests.first_seen_at is >14d ago.
	if _, err := env.Pool.Exec(ctx, `
		UPDATE tests SET first_seen_at = $1 WHERE id IN ($2, $3, $4)
	`, today.AddDate(0, 0, -25), tNewOneCommit, tKnown, tMobile); err != nil {
		t.Fatalf("backdate tests.first_seen_at: %v", err)
	}

	var sigA, sigB uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO failure_signatures (fingerprint, algo_version, normalized_message, first_seen_at)
		VALUES ($1, $2, 'triage sig A', $3) RETURNING id
	`, strings.Repeat("c", 64), errorsig.AlgoVersion, today).Scan(&sigA); err != nil {
		t.Fatalf("insert sigA: %v", err)
	}
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO failure_signatures (fingerprint, algo_version, normalized_message, first_seen_at)
		VALUES ($1, $2, 'triage sig B', $3) RETURNING id
	`, strings.Repeat("d", 64), errorsig.AlgoVersion, today).Scan(&sigB); err != nil {
		t.Fatalf("insert sigB: %v", err)
	}

	// Seed base verdict on another group so reused_from_id can reference it.
	gSeedVerdict := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-seed", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-seed", Commit: "seed-sha",
		RunID: "seed-run", Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, -40),
	})
	var baseVerdictID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO verdicts (report_group_id, repository, test_id, signature_id, label, error_context)
		VALUES ($1, 'mattermost/mattermost', $2, $3, 'infra', '{"secret":"base_secret_never_leak"}'::jsonb)
		RETURNING id
	`, gSeedVerdict, tFlaky, sigA).Scan(&baseVerdictID); err != nil {
		t.Fatalf("insert base verdict: %v", err)
	}
	mustRefresh(t, ctx, env.Pool, gSeedVerdict)

	// ---- Current 30d runs for mattermost/mattermost ----
	// 1. Failed completed run (triaged): 2 failed test occurrences (tFlaky with sigA, tNewRecent with sigB).
	//    We attach 2 verdicts to gFail1: sigA (reused=true, label="infra") and sigB (reused=false, label="flaky-infra").
	//    -> 1 failed run (triaged), 2 failed test occ (2 triaged), 2 verdicts (1 reused).
	gFail1 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-triage", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-1", Commit: "tr-sha-1",
		RunID: "tr-1", Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, -2),
	})
	rF1 := insertReport(t, ctx, env.Pool, gFail1, "e2e-triage", "")
	sF1 := insertSuite(t, ctx, env.Pool, rF1, "Suite")
	// tFlaky mixed on tr-sha-1 (failed + passed -> verdict='flaky', wait: failed_test_occurrences counts verdict='failed'!)
	// Keep tFlaky verdict='failed' here so failed_test_occurrences counts both tFlaky & tNewRecent, and make tFlaky flaky across 2 other commits!
	insertCaseWithSig(t, ctx, env.Pool, sF1, tFlaky, sigA, "flaky test", "failed", 0, 0)
	insertCaseWithSig(t, ctx, env.Pool, sF1, tNewRecent, sigB, "new recent", "failed", 0, 1)
	mustRefresh(t, ctx, env.Pool, gFail1)

	vBody, _ := json.Marshal(map[string]any{
		"repository":      "mattermost/mattermost",
		"report_group_id": gFail1.String(),
		"verdicts": []any{
			map[string]any{
				"test_id":        tFlaky.String(),
				"signature_id":   sigA.String(),
				"label":          "infra",
				"reused_from_id": baseVerdictID.String(),
				"error_context":  map[string]any{"token": "ghp_triage_secret_must_never_leak"},
			},
			map[string]any{
				"test_id":       tNewRecent.String(),
				"signature_id":  sigB.String(),
				"label":         "flaky-infra",
				"error_context": map[string]any{"password": "super_secret_password"},
			},
		},
	})
	vReq, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/verdicts", bytes.NewReader(vBody))
	vReq.Header.Set("Content-Type", "application/json")
	vReq.Header.Set("X-API-Key", apiKey)
	vResp, err := http.DefaultClient.Do(vReq)
	if err != nil {
		t.Fatalf("post verdicts: %v", err)
	}
	vResp.Body.Close()
	if vResp.StatusCode != http.StatusOK {
		t.Fatalf("post verdicts status = %d, want 200", vResp.StatusCode)
	}

	// 2. Failed completed run (untriaged) in same week: 2 failed test occurrences.
	gFail2 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-triage", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-2", Commit: "tr-sha-2",
		RunID: "tr-2", Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, -1),
	})
	rF2 := insertReport(t, ctx, env.Pool, gFail2, "e2e-triage", "")
	sF2 := insertSuite(t, ctx, env.Pool, rF2, "Suite")
	insertCaseWithSig(t, ctx, env.Pool, sF2, tNewRecent, sigA, "new recent", "failed", 0, 0)
	insertCaseWithSig(t, ctx, env.Pool, sF2, tKnown, sigB, "known", "failed", 0, 1)
	mustRefresh(t, ctx, env.Pool, gFail2)

	// 3. Cleanly completed passing run (group_status='completed', orch_status='completed').
	gClean := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-triage", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-3", Commit: "tr-sha-3",
		RunID: "tr-3", Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, -1),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "tr-sha-3", RunID: "tr-3",
		Name: "e2e-triage", Attempt: "1", Status: "completed", StartedAt: today.AddDate(0, 0, -1),
	})
	rClean := insertReport(t, ctx, env.Pool, gClean, "e2e-triage", "")
	sClean := insertSuite(t, ctx, env.Pool, rClean, "Suite")
	insertCase(t, ctx, env.Pool, sClean, tKnown, "known", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gClean)

	// 4. Timed-out run whose report_group finalized as 'completed' (must count as timed_out, NOT completed; is_red=true).
	gTOCompleted := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-triage", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-4", Commit: "tr-sha-4",
		RunID: "tr-4", Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, -1),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "tr-sha-4", RunID: "tr-4",
		Name: "e2e-triage", Attempt: "1", Status: "timed_out", StartedAt: today.AddDate(0, 0, -1),
	})
	mustRefresh(t, ctx, env.Pool, gTOCompleted)

	// 5. Timed-out run whose report_group is still 'in_progress' (must count as timed_out, NOT in_progress / dropped; is_red=true).
	gTOInProg := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-triage", Status: "in_progress",
		Repo: "mattermost/mattermost", Branch: "pr-5", Commit: "tr-sha-5",
		RunID: "tr-5", Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, -1),
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "tr-sha-5", RunID: "tr-5",
		Name: "e2e-triage", Attempt: "1", Status: "timed_out", StartedAt: today.AddDate(0, 0, -1),
	})
	mustRefresh(t, ctx, env.Pool, gTOInProg)

	// 6. Incomplete upload run (group_status='incomplete', no failed tests).
	gInc := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-triage", Status: "incomplete",
		Repo: "mattermost/mattermost", Branch: "pr-6", Commit: "tr-sha-6",
		RunID: "tr-6", Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, -1),
	})
	mustRefresh(t, ctx, env.Pool, gInc)

	// 7. Truly in-progress run (group_status='in_progress', orch_status='in_progress').
	gInProg := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-triage", Status: "in_progress",
		Repo: "mattermost/mattermost", Branch: "pr-7", Commit: "tr-sha-7",
		RunID: "tr-7", Attempt: "1", Stage: "pr", CreatedAt: today,
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: "mattermost/mattermost", Commit: "tr-sha-7", RunID: "tr-7",
		Name: "e2e-triage", Attempt: "1", Status: "in_progress", StartedAt: today,
	})
	mustRefresh(t, ctx, env.Pool, gInProg)

	// Seed additional test executions/commits for /tests/{id}/state assertions:
	// - tFlaky: 2 mixed commits in trailing 14d (tr-flk-1, tr-flk-2) -> state="flaky"
	// - tNewOneCommit: 1 commit at -20d -> state="new" (total_commits <= 1)
	// - tKnown: commit at -20d + commit at -1d (gFail2) -> 2 commits, first_seen=-25d -> state="known"
	// - tMobile: 2 commits at -20d, -18d with ios pass & android fail -> state="known", is_flaky=false!
	for idx := range 2 {
		gFlk := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "playwright", Name: "e2e-triage", Status: "completed",
			Repo: "mattermost/mattermost", Branch: "pr-flk", Commit: fmt.Sprintf("tr-flk-%d", idx),
			RunID: fmt.Sprintf("tr-flk-%d", idx), Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, -3-idx),
		})
		rFlk := insertReport(t, ctx, env.Pool, gFlk, "e2e-triage", "")
		sFlk := insertSuite(t, ctx, env.Pool, rFlk, "Suite")
		insertCase(t, ctx, env.Pool, sFlk, tFlaky, "flaky test", "failed", 0, "", 0)
		insertCase(t, ctx, env.Pool, sFlk, tFlaky, "flaky test", "passed", 1, "", 1)
		mustRefresh(t, ctx, env.Pool, gFlk)
	}
	// Update tFlaky's latest verdict to be "flaky-infra" (higher created_at than POST /verdicts at now) with secret error_context.
	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO verdicts (report_group_id, repository, test_id, signature_id, label, error_context, created_at)
		VALUES ($1, 'mattermost/mattermost', $2, $3, 'flaky-infra', '{"secret":"test_state_secret_never_leak"}'::jsonb, $4)
	`, gClean, tFlaky, sigA, now.Add(time.Minute)); err != nil {
		t.Fatalf("insert latest test verdict: %v", err)
	}

	gOld1 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "e2e-triage", Status: "completed",
		Repo: "mattermost/mattermost", Branch: "pr-old-1", Commit: "tr-old-sha-1",
		RunID: "tr-old-1", Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, -20),
	})
	rOld1 := insertReport(t, ctx, env.Pool, gOld1, "e2e-triage", "")
	sOld1 := insertSuite(t, ctx, env.Pool, rOld1, "Suite")
	insertCase(t, ctx, env.Pool, sOld1, tNewOneCommit, "new one", "passed", 0, "", 0)
	insertCase(t, ctx, env.Pool, sOld1, tKnown, "known", "passed", 0, "", 1)
	mustRefresh(t, ctx, env.Pool, gOld1)

	for idx, dayOff := range []int{-20, -18} {
		gMob := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "detox", Name: "detox-pr", Status: "completed",
			Repo: "mattermost/mattermost-mobile", Branch: "pr-mob", Commit: fmt.Sprintf("tr-mob-%d", idx),
			RunID: fmt.Sprintf("tr-mob-%d", idx), Attempt: "1", Stage: "pr", CreatedAt: today.AddDate(0, 0, dayOff),
		})
		rIOS := insertReport(t, ctx, env.Pool, gMob, "ios", "ios")
		sIOS := insertSuite(t, ctx, env.Pool, rIOS, "Mobile")
		insertCase(t, ctx, env.Pool, sIOS, tMobile, "mobile split", "passed", 0, "", 0)
		rAnd := insertReport(t, ctx, env.Pool, gMob, "android", "android")
		sAnd := insertSuite(t, ctx, env.Pool, rAnd, "Mobile")
		insertCase(t, ctx, env.Pool, sAnd, tMobile, "mobile split", "failed", 0, "", 0)
		mustRefresh(t, ctx, env.Pool, gMob)
	}

	// ---- Assert /api/v1/kpi/summary?app=mattermost&window=30d ----
	// Total runs in 30d for mattermost:
	// gFail1 (completed, red, triaged)
	// gFail2 (completed, red, untriaged)
	// gClean (completed, green)
	// gTOCompleted (completed+timed_out -> timed_out, red, untriaged)
	// gTOInProg (in_progress+timed_out -> timed_out, red, untriaged)
	// gInc (incomplete, green)
	// gInProg (in_progress, green)
	// gFlk[0], gFlk[1] (2 completed, green because verdict=flaky -> is_red=false)
	// gOld1 (completed, green)
	// Total = 10 runs:
	//   completed_runs = 5 (gFail1, gFail2, gClean, gFlk[0], gFlk[1], gOld1 -> wait: 6! Let's count: gFail1, gFail2, gClean, gFlk0, gFlk1, gOld1 = 6)
	//   timed_out_runs = 2 (gTOCompleted, gTOInProg)
	//   incomplete_runs = 1 (gInc)
	//   in_progress_runs = 1 (gInProg)
	//   total_runs = 10, denom = 10 - 1 = 9, completion_rate = 6 / 9 * 100 = 66.666667
	// Failed runs: gFail1, gFail2, gTOCompleted, gTOInProg = 4; triaged runs = 1 (gFail1) => run_coverage_rate = 25.0
	// Failed test occ: 2 (gFail1) + 2 (gFail2) = 4; triaged test occ = 2 (gFail1) => test_coverage_rate = 50.0
	// Verdicts: 2 total (on gFail1), 1 reused => verdict_reuse_rate = 50.0
	// Label mix: flaky-infra (1, 50%), infra (1, 50%), sorted alphabetically on tie.
	rawSum := getRaw(t, env.ServerURL+"/api/v1/kpi/summary?app=mattermost&window=30d")
	if strings.Contains(rawSum, "ghp_triage_secret_must_never_leak") ||
		strings.Contains(rawSum, "super_secret_password") ||
		strings.Contains(rawSum, "base_secret_never_leak") ||
		strings.Contains(rawSum, "error_context") {
		t.Fatal("/api/v1/kpi/summary leaked error_context")
	}
	var sum kpi.SummaryResponse
	if err := json.Unmarshal([]byte(rawSum), &sum); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if sum.Triage == nil {
		t.Fatal("expected non-nil summary.triage")
	}
	tr := sum.Triage
	assertNearPtr(t, "run_coverage_rate", tr.RunCoverageRate, 25.0, 1e-6)
	assertNearPtr(t, "test_coverage_rate", tr.TestCoverageRate, 50.0, 1e-6)
	assertNearPtr(t, "verdict_reuse_rate", tr.VerdictReuseRate, 50.0, 1e-6)
	if tr.FailedRunsCount != 4 || tr.TriagedRunsCount != 1 || tr.FailedTestOccurrences != 4 || tr.TriagedTestOccurrences != 2 {
		t.Fatalf("unexpected triage counts: %+v", tr)
	}
	if tr.VerdictsTotal != 2 || tr.VerdictsReused != 1 {
		t.Fatalf("unexpected verdict counts: %+v", tr)
	}
	if len(tr.LabelMix) != 2 || tr.LabelMix[0].Label != "flaky-infra" || tr.LabelMix[0].Count != 1 ||
		tr.LabelMix[1].Label != "infra" || tr.LabelMix[1].Count != 1 {
		t.Fatalf("unexpected label_mix: %+v", tr.LabelMix)
	}
	assertNearPtr(t, "label_mix[0].share", &tr.LabelMix[0].Share, 50.0, 1e-6)
	rc := tr.RunCompletion
	if rc.TotalRuns != 10 || rc.CompletedRuns != 6 || rc.IncompleteRuns != 1 || rc.InProgressRuns != 1 || rc.TimedOutRuns != 2 {
		t.Fatalf("unexpected run_completion: %+v", rc)
	}
	assertNearPtr(t, "completion_rate", rc.CompletionRate, 600.0/9.0, 1e-6)

	hasWeeklyTriage := false
	for _, wp := range sum.WeeklySeries {
		if wp.TriageCoverageRate != nil {
			hasWeeklyTriage = true
			if *wp.TriageCoverageRate < 0 || *wp.TriageCoverageRate > 100 {
				t.Fatalf("out-of-range weekly triage_coverage_rate: %v", *wp.TriageCoverageRate)
			}
		}
	}
	if !hasWeeklyTriage {
		t.Fatal("expected at least one non-nil weekly_series[].triage_coverage_rate")
	}

	// Zero-denominator guards on empty filter slice.
	emptySum := getJSON[kpi.SummaryResponse](t, env.ServerURL+"/api/v1/kpi/summary?lane=nonexistent-lane&window=30d")
	if emptySum.Triage == nil {
		t.Fatal("expected non-nil triage struct even on empty slice")
	}
	if emptySum.Triage.RunCoverageRate != nil ||
		emptySum.Triage.TestCoverageRate != nil ||
		emptySum.Triage.VerdictReuseRate != nil ||
		emptySum.Triage.RunCompletion.CompletionRate != nil ||
		len(emptySum.Triage.LabelMix) != 0 {
		t.Fatalf("expected nil rates and empty label_mix on empty slice, got %+v", emptySum.Triage)
	}

	// ---- Assert GET /api/v1/tests/{id}/state ----
	rawFlakyState := getRaw(t, env.ServerURL+"/api/v1/tests/"+tFlaky.String()+"/state")
	if strings.Contains(rawFlakyState, "test_state_secret_never_leak") ||
		strings.Contains(rawFlakyState, "ghp_triage_secret_must_never_leak") ||
		strings.Contains(rawFlakyState, "error_context") {
		t.Fatal("/api/v1/tests/{id}/state leaked error_context")
	}
	var stFlaky kpi.TestStateResponse
	if err := json.Unmarshal([]byte(rawFlakyState), &stFlaky); err != nil {
		t.Fatalf("unmarshal flaky test state: %v", err)
	}
	if stFlaky.State != kpi.TestStateFlaky || !stFlaky.IsFlaky || stFlaky.FlakyCommitCount14d != 2 {
		t.Fatalf("flaky test state = %+v, want state=flaky is_flaky=true flaky_commit_count_14d=2", stFlaky)
	}
	if stFlaky.App != "mattermost" || stFlaky.TotalCommits != 3 || stFlaky.TotalRuns != 3 {
		t.Fatalf("flaky test metadata = %+v, want app=mattermost commits=3 runs=3", stFlaky)
	}
	if stFlaky.LatestVerdictLabel == nil || *stFlaky.LatestVerdictLabel != "flaky-infra" {
		gotLbl := "<nil>"
		if stFlaky.LatestVerdictLabel != nil {
			gotLbl = *stFlaky.LatestVerdictLabel
		}
		t.Fatalf("flaky latest_verdict_label = %q, want flaky-infra", gotLbl)
	}

	stNewRecent := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tNewRecent.String()+"/state")
	if stNewRecent.State != kpi.TestStateNew || stNewRecent.IsFlaky || stNewRecent.TotalCommits != 2 {
		t.Fatalf("new recent test state = %+v, want state=new is_flaky=false total_commits=2", stNewRecent)
	}

	stNewOne := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tNewOneCommit.String()+"/state")
	if stNewOne.State != kpi.TestStateNew || stNewOne.IsFlaky || stNewOne.TotalCommits != 1 {
		t.Fatalf("new 1-commit test state = %+v, want state=new is_flaky=false total_commits=1", stNewOne)
	}

	stKnown := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tKnown.String()+"/state?window=7d")
	if stKnown.State != kpi.TestStateKnown || stKnown.IsFlaky || stKnown.TotalCommits != 3 || stKnown.TotalRuns != 3 {
		t.Fatalf("known test state = %+v, want state=known is_flaky=false total_commits=3 total_runs=3", stKnown)
	}

	stMob := getJSON[kpi.TestStateResponse](t, env.ServerURL+"/api/v1/tests/"+tMobile.String()+"/state")
	if stMob.State != kpi.TestStateKnown || stMob.IsFlaky || stMob.FlakyCommitCount14d != 0 || stMob.App != "mobile" {
		t.Fatalf("mobile test state = %+v, want state=known is_flaky=false flaky_14d=0 app=mobile", stMob)
	}

	// 400 and 404 checks.
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/v1/tests/not-a-uuid/state", http.StatusBadRequest},
		{"/api/v1/tests/" + uuid.NewString() + "/state", http.StatusNotFound},
		{"/api/v1/tests/" + tKnown.String() + "/state?window=99d", http.StatusBadRequest},
	} {
		resp, gerr := http.Get(env.ServerURL + tc.path) //nolint:gosec
		if gerr != nil {
			t.Fatalf("GET %s: %v", tc.path, gerr)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("GET %s status = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
}

// ---- test helpers ----

type groupSeed struct {
	Framework string
	Name      string
	Status    string
	Repo      string
	Branch    string
	Commit    string
	RunID     string
	Attempt   string
	PRNum     *int
	Stage     string
	CreatedAt time.Time
	DurMs     *int64
}

type orchSeed struct {
	Repo           string
	Commit         string
	RunID          string
	Name           string
	Attempt        string
	Status         string
	FailCount      int
	HasRetriedUnit bool
	StartedAt      time.Time
}

func int64Ptr(v int64) *int64 { return &v }

func insertTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repo, framework, testKey, fullTitle string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO tests (repository, framework, test_key, file, full_title)
		VALUES ($1, $2, $3, 'e2e/test.spec.ts', $4)
		RETURNING id
	`, repo, framework, testKey, fullTitle).Scan(&id); err != nil {
		t.Fatalf("insert test %s: %v", testKey, err)
	}
	return id
}

func insertGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, s groupSeed) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO report_groups (
			framework, name, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
			gh_pr_number, stage, total_duration_ms, created_at, updated_at, last_upload_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12, $12)
		RETURNING id
	`, s.Framework, s.Name, s.Status, s.Repo, s.Branch, s.Commit, s.RunID, s.Attempt,
		s.PRNum, s.Stage, s.DurMs, s.CreatedAt).Scan(&id); err != nil {
		t.Fatalf("insert report_group %s/%s: %v", s.Name, s.Commit, err)
	}
	return id
}

func insertOrchRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, s orchSeed) uuid.UUID {
	t.Helper()
	var runID uuid.UUID
	var terminalAt *time.Time
	if s.Status != "in_progress" {
		ts := s.StartedAt.Add(time.Minute)
		terminalAt = &ts
	}
	passCount := 1
	if s.FailCount > 0 {
		passCount = 0
	}
	totalUnits := passCount + s.FailCount
	pendingCount := 0
	if s.Status == "in_progress" {
		pendingCount = 1
		passCount = 0
		totalUnits = 1 + s.FailCount
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orchestration_runs (
			repository, commit_sha, gh_run_id, name, gh_run_attempt, framework,
			lease_timeout_ms, idle_timeout_ms, status,
			pending_count, completed_pass_count, completed_fail_count, total_units,
			dispatch_units_hash, started_at, last_activity_at, terminal_at, owner_oidc_subject
		) VALUES (
			$1, $2, $3, $4, $5, 'playwright',
			60000, 60000, $6,
			$7, $8, $9, $10,
			'\xdeadbeef'::bytea, $11, $11, $12, 'test-oidc'
		)
		RETURNING id
	`, s.Repo, s.Commit, s.RunID, s.Name, s.Attempt, s.Status,
		pendingCount, passCount, s.FailCount, totalUnits, s.StartedAt, terminalAt).Scan(&runID); err != nil {
		t.Fatalf("insert orchestration_run %s: %v", s.Name, err)
	}
	if s.HasRetriedUnit {
		if _, err := pool.Exec(ctx, `
			INSERT INTO dispatch_units (run_id, dispatch_seq, spec_path, state, lease_count, fail_count, outcome_set_at)
			VALUES ($1, 0, 'e2e/retried.spec.ts', 'completed_pass', 2, 1, $2)
		`, runID, s.StartedAt.Add(time.Minute)); err != nil {
			t.Fatalf("insert retried dispatch_unit: %v", err)
		}
	}
	return runID
}

func insertReport(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID, name, platform string) uuid.UUID {
	t.Helper()
	var plat *string
	if platform != "" {
		plat = &platform
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO reports (report_group_id, name, status, platform)
		VALUES ($1, $2, 'complete', $3)
		RETURNING id
	`, groupID, name, plat).Scan(&id); err != nil {
		t.Fatalf("insert report: %v", err)
	}
	return id
}

func insertSuite(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO suites (report_id, title, file, ordinal)
		VALUES ($1, $2, 'e2e/test.spec.ts', 0)
		RETURNING id
	`, reportID, title).Scan(&id); err != nil {
		t.Fatalf("insert suite: %v", err)
	}
	return id
}

func insertCase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suiteID, testID uuid.UUID, title, status string, retry int, project string, ordinal int) {
	t.Helper()
	var proj *string
	if project != "" {
		proj = &project
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO test_cases (suite_id, title, full_title, status, retry_count, project, ordinal, test_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, suiteID, title, "Suite > "+title, status, retry, proj, ordinal, testID); err != nil {
		t.Fatalf("insert test_case %s: %v", title, err)
	}
}

func insertCaseWithSig(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suiteID, testID, sigID uuid.UUID, title, status string, retry, ordinal int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO test_cases (suite_id, title, full_title, status, retry_count, ordinal, test_id, signature_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, suiteID, title, "Suite > "+title, status, retry, ordinal, testID, sigID); err != nil {
		t.Fatalf("insert test_case with sig %s: %v", title, err)
	}
}

func mustRefresh(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) {
	t.Helper()
	if err := kpi.RefreshGroupRollups(ctx, pool, groupID); err != nil {
		t.Fatalf("RefreshGroupRollups(%s): %v", groupID, err)
	}
}

func getRaw(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d (body=%s)", url, resp.StatusCode, string(body))
	}
	return string(body)
}

func getJSON[T any](t *testing.T, url string) T {
	t.Helper()
	raw := getRaw(t, url)
	var out T
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal %s: %v\nbody=%s", url, err, raw)
	}
	return out
}

func assertNearPtr(t *testing.T, label string, got *float64, want, tol float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %.4f", label, want)
	}
	if math.Abs(*got-want) > tol {
		t.Fatalf("%s = %.6f, want %.6f (tol %.6f)", label, *got, want, tol)
	}
}

var _ = fmt.Sprintf
