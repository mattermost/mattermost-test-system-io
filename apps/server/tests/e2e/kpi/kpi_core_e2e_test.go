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

	if _, err := env.Pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("exec 000036 down: %v", err)
	}
	assertRegclass(false)

	if _, err := env.Pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("re-exec 000036 up: %v", err)
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
	// Snapshot must preserve has_orchestration, orch_status, is_red, has_retried_unit, total_duration_ms.
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
		seedBody, _ := json.Marshal(map[string]any{
			"repository":      "mattermost/mattermost",
			"report_group_id": gid.String(),
			"verdicts": []any{
				map[string]any{
					"test_id":      tHeavy.String(),
					"signature_id": sigID.String(),
					"label":        "initial",
				},
			},
		})
		seedReq, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/verdicts", bytes.NewReader(seedBody))
		seedReq.Header.Set("Content-Type", "application/json")
		seedReq.Header.Set("X-API-Key", apiKey)
		seedResp, err := http.DefaultClient.Do(seedReq)
		if err != nil {
			t.Fatalf("seed verdict %s: %v", gid, err)
		}
		seedResp.Body.Close()
		if seedResp.StatusCode != http.StatusOK {
			t.Fatalf("seed verdict %s status = %d, want 200", gid, seedResp.StatusCode)
		}

		gids = append(gids, gid)
	}

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
				body, _ := json.Marshal(map[string]any{
					"repository":      "mattermost/mattermost",
					"report_group_id": groupID.String(),
					"verdicts": []any{
						map[string]any{
							"test_id":      tHeavy.String(),
							"signature_id": sigID.String(),
							"label":        lbl,
						},
					},
				})
				req, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/verdicts", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-API-Key", apiKey)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					errCh <- fmt.Errorf("post verdict %s (%s): %w", groupID, lbl, err)
					return
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
					errCh <- fmt.Errorf("post verdict %s (%s) status = %d", groupID, lbl, resp.StatusCode)
				}
			}(i, lIdx, gid, label)
		}

		wg.Add(2)
		go func(groupID uuid.UUID) {
			defer wg.Done()
			if err := kpi.RefreshGroupRollups(ctx, env.Pool, groupID); err != nil {
				errCh <- fmt.Errorf("refresh %s: %w", groupID, err)
			}
		}(gid)

		go func(idx int, groupID uuid.UUID) {
			defer wg.Done()
			if idx%2 == 1 {
				time.Sleep(time.Duration((idx%4)+1) * time.Millisecond)
			}
			req, _ := http.NewRequest(http.MethodDelete, env.ServerURL+"/api/v1/reports/"+groupID.String(), nil)
			req.Header.Set("X-API-Key", apiKey)
			resp, err := http.DefaultClient.Do(req)
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
