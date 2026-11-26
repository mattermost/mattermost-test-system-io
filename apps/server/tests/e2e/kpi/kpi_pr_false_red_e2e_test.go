//go:build e2e
// +build e2e

package kpie2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
	"github.com/mattermost/mattermost-test-system-io/apps/server/migrations"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestMigration000037_UpDownAndNoConcurrentOrTestCasesIndex(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()

	upSQL, err := migrations.FS.ReadFile("000037_kpi_e2e_lanes.up.sql")
	if err != nil {
		t.Fatalf("read 000037 up: %v", err)
	}
	downSQL, err := migrations.FS.ReadFile("000037_kpi_e2e_lanes.down.sql")
	if err != nil {
		t.Fatalf("read 000037 down: %v", err)
	}
	if strings.Contains(strings.ToUpper(string(upSQL)), "CONCURRENTLY") ||
		strings.Contains(strings.ToUpper(string(downSQL)), "CONCURRENTLY") {
		t.Fatal("000037 migration must not contain CREATE/DROP INDEX CONCURRENTLY")
	}

	// Verify zero indexes added to test_cases or suites.
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

	// Verify all 16 default E2E lanes are seeded.
	var seededCount int
	if err := env.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM kpi_e2e_lanes WHERE enabled = true`).Scan(&seededCount); err != nil {
		t.Fatalf("count kpi_e2e_lanes: %v", err)
	}
	if seededCount != 16 {
		t.Fatalf("seeded kpi_e2e_lanes count = %d, want 16", seededCount)
	}

	assertObjects := func(wantPresent bool) {
		t.Helper()
		for _, obj := range []string{
			"public.kpi_e2e_lanes",
			"public.kpi_e2e_lanes_repo_lower_idx",
			"public.kpi_group_rollups_rerun_idx",
			"public.kpi_group_rollups_pr_red_idx",
			"public.kpi_group_signature_rollups_sig_date_idx",
		} {
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

	assertObjects(true)

	if _, err := env.Pool.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("exec 000037 down: %v", err)
	}
	assertObjects(false)

	if _, err := env.Pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("re-exec 000037 up: %v", err)
	}
	assertObjects(true)
}

func insertFailureSig(t *testing.T, ctx context.Context, env *testenv.Env, _ string, fingerprint string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO failure_signatures (fingerprint, algo_version, error_class, normalized_message, first_seen_at)
		VALUES ($1, 2, 'Error', $2, now())
		RETURNING id
	`, fingerprint, "msg-"+fingerprint).Scan(&id); err != nil {
		t.Fatalf("insert failure_signature %s: %v", fingerprint, err)
	}
	return id
}

func TestKPI_PRFalseRedSettledAttributionAndSummaryFixtures(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	ctx := t.Context()

	// Disable cypress-full to verify disabled lanes are excluded alongside unconfigured lanes.
	if _, err := env.Pool.Exec(ctx, `
		UPDATE kpi_e2e_lanes SET enabled = false
		 WHERE repository = 'mattermost/mattermost' AND lane = 'cypress-full'
	`); err != nil {
		t.Fatalf("disable cypress-full: %v", err)
	}

	fixedNow := time.Now().UTC().Truncate(time.Second)
	store := &kpi.Store{
		Pool: env.Pool,
		Now:  func() time.Time { return fixedNow },
	}

	// 0. Empty DB / zero-settled-red division guard.
	emptyResp, err := store.QueryPRsRed(ctx, kpi.FilterParams{
		Window: window30dForTest(), Sort: kpi.SortCreatedDesc, Page: 1, Limit: 50, WindowDays: 30,
		From: fixedNow.AddDate(0, 0, -29), To: fixedNow,
		CurFromDate:   time.Date(fixedNow.Year(), fixedNow.Month(), fixedNow.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -29),
		CurToDate:     time.Date(fixedNow.Year(), fixedNow.Month(), fixedNow.Day(), 0, 0, 0, 0, time.UTC),
		PriorFromDate: time.Date(fixedNow.Year(), fixedNow.Month(), fixedNow.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -60),
		PriorToDate:   time.Date(fixedNow.Year(), fixedNow.Month(), fixedNow.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -30),
	})
	if err != nil {
		t.Fatalf("empty QueryPRsRed: %v", err)
	}
	if emptyResp.FalseRedRate != nil || emptyResp.PriorFalseRedRate != nil || emptyResp.DeltaPP != nil {
		t.Fatalf("expected nil rates on empty DB, got cur=%v prior=%v delta=%v",
			emptyResp.FalseRedRate, emptyResp.PriorFalseRedRate, emptyResp.DeltaPP)
	}
	if len(emptyResp.ConfiguredE2ELanes) != 15 {
		t.Fatalf("configured_e2e_lanes = %d, want 15 (16 seeded - 1 disabled)", len(emptyResp.ConfiguredE2ELanes))
	}

	// Seed tests & failure signatures.
	mmRepo := "mattermost/mattermost"
	mobRepo := "mattermost/mattermost-mobile"
	deskRepo := "mattermost/desktop"

	t1 := insertTest(t, ctx, env.Pool, mmRepo, "playwright", "h:pr-t1", "PR > t1")
	tFlaky := insertTest(t, ctx, env.Pool, mmRepo, "playwright", "h:pr-flaky", "PR > flaky")
	tMob := insertTest(t, ctx, env.Pool, mobRepo, "detox", "h:mob-t1", "Mob > t1")

	sigMain := insertFailureSig(t, ctx, env, mmRepo, "fp-main-outage")
	sig3PRs := insertFailureSig(t, ctx, env, mmRepo, "fp-3prs-outage")
	sig2PRs := insertFailureSig(t, ctx, env, mmRepo, "fp-2prs-only")
	sigUnique := insertFailureSig(t, ctx, env, mmRepo, "fp-unique")
	sigUnique108 := insertFailureSig(t, ctx, env, mmRepo, "fp-unique-108")
	sigUnique109 := insertFailureSig(t, ctx, env, mmRepo, "fp-unique-109")
	sigUnique201 := insertFailureSig(t, ctx, env, mmRepo, "fp-unique-201")
	sigUnique500 := insertFailureSig(t, ctx, env, mmRepo, "fp-unique-500")
	sigUnique501 := insertFailureSig(t, ctx, env, mmRepo, "fp-unique-501")
	sigOldMain := insertFailureSig(t, ctx, env, mmRepo, "fp-old-main")

	prPtr := func(n int) *int { return &n }

	// Anchor timestamps relative to fixedNow.
	t48h := fixedNow.Add(-48 * time.Hour)
	t47h := fixedNow.Add(-47 * time.Hour)
	t30h := fixedNow.Add(-30 * time.Hour)
	t25h := fixedNow.Add(-25 * time.Hour)
	t24hExact := fixedNow.Add(-24 * time.Hour)
	t23h := fixedNow.Add(-23 * time.Hour)
	tPrior35d := fixedNow.AddDate(0, 0, -35)

	// ---- Prior window (35d ago) ----
	// 2 settled red lanes: 1 went green on rerun (50% prior_false_red_rate).
	gPrior1 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-90", Commit: "sha-prior-1", RunID: "run-p1", Attempt: "1",
		PRNum: prPtr(90), Stage: "pr", CreatedAt: tPrior35d,
	})
	rP1 := insertReport(t, ctx, env.Pool, gPrior1, "playwright-full", "")
	sP1 := insertSuite(t, ctx, env.Pool, rP1, "S")
	insertCaseWithSig(t, ctx, env.Pool, sP1, t1, sigUnique, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gPrior1)

	gPrior1Rerun := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-90", Commit: "sha-prior-1", RunID: "run-p1", Attempt: "2",
		PRNum: prPtr(90), Stage: "pr", CreatedAt: tPrior35d.Add(time.Hour),
	})
	rP1R := insertReport(t, ctx, env.Pool, gPrior1Rerun, "playwright-full", "")
	sP1R := insertSuite(t, ctx, env.Pool, rP1R, "S")
	insertCase(t, ctx, env.Pool, sP1R, t1, "t1", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPrior1Rerun)

	gPrior2 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-91", Commit: "sha-prior-2", RunID: "run-p2", Attempt: "1",
		PRNum: prPtr(91), Stage: "pr", CreatedAt: tPrior35d,
	})
	rP2 := insertReport(t, ctx, env.Pool, gPrior2, "playwright-full", "")
	sP2 := insertSuite(t, ctx, env.Pool, rP2, "S")
	insertCaseWithSig(t, ctx, env.Pool, sP2, t1, sigUnique, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gPrior2)

	// ---- Main failures for 24h & >24h checks ----
	// sigMain failed on main 45h ago (within 24h of t48h and t30h, wait: 48-45=3h, 45-30=15h -> within 24h of BOTH t48h and t30h!).
	gMainRecent := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "main", Commit: "sha-main-1", RunID: "run-main-1", Attempt: "1",
		PRNum: nil, Stage: "main", CreatedAt: fixedNow.Add(-45 * time.Hour),
	})
	rMain := insertReport(t, ctx, env.Pool, gMainRecent, "playwright-full", "")
	sMain := insertSuite(t, ctx, env.Pool, rMain, "S")
	insertCaseWithSig(t, ctx, env.Pool, sMain, t1, sigMain, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gMainRecent)

	// sigOldMain failed on main 100h ago (outside ±24h of t30h).
	gMainOld := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "main", Commit: "sha-main-old", RunID: "run-main-old", Attempt: "1",
		PRNum: nil, Stage: "main", CreatedAt: fixedNow.Add(-100 * time.Hour),
	})
	rMainOld := insertReport(t, ctx, env.Pool, gMainOld, "playwright-full", "")
	sMainOld := insertSuite(t, ctx, env.Pool, rMainOld, "S")
	insertCaseWithSig(t, ctx, env.Pool, sMainOld, t1, sigOldMain, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gMainOld)

	// ---- Excluded lanes (unconfigured + disabled) ----
	gUnconfigured := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "unit-tests", Status: "completed",
		Repo: mmRepo, Branch: "pr-500", Commit: "sha-unconf", RunID: "run-unconf", Attempt: "1",
		PRNum: prPtr(500), Stage: "pr", CreatedAt: t30h,
	})
	rUnconf := insertReport(t, ctx, env.Pool, gUnconfigured, "unit-tests", "")
	sUnconf := insertSuite(t, ctx, env.Pool, rUnconf, "S")
	insertCaseWithSig(t, ctx, env.Pool, sUnconf, t1, sigUnique500, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gUnconfigured)

	gDisabled := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "cypress", Name: "cypress-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-501", Commit: "sha-dis", RunID: "run-dis", Attempt: "1",
		PRNum: prPtr(501), Stage: "pr", CreatedAt: t30h,
	})
	rDis := insertReport(t, ctx, env.Pool, gDisabled, "cypress-full", "")
	sDis := insertSuite(t, ctx, env.Pool, rDis, "S")
	insertCaseWithSig(t, ctx, env.Pool, sDis, t1, sigUnique501, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gDisabled)

	// ---- Current window Settled Red PR E2E Lanes (10 settled total) ----

	// [1] PR #101 (t48h): numeric attempt '2' red, later attempt '10' green, AND sigMain on main -> priority MUST pick went_green_on_rerun!
	gPR101 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "refs/heads/feat/pr-101", Commit: "sha-101", RunID: "run-101", Attempt: "2",
		PRNum: prPtr(101), Stage: "pr", CreatedAt: t48h,
	})
	r101 := insertReport(t, ctx, env.Pool, gPR101, "playwright-full", "")
	s101 := insertSuite(t, ctx, env.Pool, r101, "S")
	insertCaseWithSig(t, ctx, env.Pool, s101, t1, sigMain, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gPR101)

	gPR101Rerun := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "refs/heads/feat/pr-101", Commit: "sha-101", RunID: "run-101", Attempt: "10",
		PRNum: prPtr(101), Stage: "pr", CreatedAt: t47h,
	})
	r101R := insertReport(t, ctx, env.Pool, gPR101Rerun, "playwright-full", "")
	s101R := insertSuite(t, ctx, env.Pool, r101R, "S")
	insertCase(t, ctx, env.Pool, s101R, t1, "t1", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPR101Rerun)

	// [2] PR #102 (t48h): orchestrated completed_fail_count > 0 with status='incomplete' (red!), no rerun, sigMain on main -> failing_elsewhere.
	gPR102 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "incomplete",
		Repo: mmRepo, Branch: "pr-102", Commit: "sha-102", RunID: "run-102", Attempt: "1",
		PRNum: prPtr(102), Stage: "pr", CreatedAt: t48h,
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: mmRepo, Commit: "sha-102", RunID: "run-102", Name: "playwright-full",
		Attempt: "1", Status: "completed", FailCount: 1, StartedAt: t48h,
	})
	r102 := insertReport(t, ctx, env.Pool, gPR102, "playwright-full", "")
	s102 := insertSuite(t, ctx, env.Pool, r102, "S")
	insertCaseWithSig(t, ctx, env.Pool, s102, t1, sigMain, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gPR102)

	// [3] PR #103 (t30h): sig3PRs failed on 3 other PRs (#104, #105, #106) within 24h (with reports.created_at = now()!).
	// Also test new-commit fix exclusion: PR #103 later pushed sha-103-new which went green -> went_green_on_rerun MUST be false, still_red MUST be false!
	gPR103 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-103", Commit: "sha-103-old", RunID: "run-103", Attempt: "1",
		PRNum: prPtr(103), Stage: "pr", CreatedAt: t30h,
	})
	r103 := insertReport(t, ctx, env.Pool, gPR103, "playwright-full", "")
	s103 := insertSuite(t, ctx, env.Pool, r103, "S")
	insertCaseWithSig(t, ctx, env.Pool, s103, t1, sig3PRs, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gPR103)

	gPR103NewCommit := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-103", Commit: "sha-103-new", RunID: "run-103-new", Attempt: "1",
		PRNum: prPtr(103), Stage: "pr", CreatedAt: t25h,
	})
	r103New := insertReport(t, ctx, env.Pool, gPR103NewCommit, "playwright-full", "")
	s103New := insertSuite(t, ctx, env.Pool, r103New, "S")
	insertCase(t, ctx, env.Pool, s103New, t1, "t1", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gPR103NewCommit)

	// [4], [5], [6] PRs #104, #105, #106 (t30h): also have sig3PRs (reports.created_at defaults to now(), proving group_created_at 24h index works!).
	for _, prNum := range []int{104, 105, 106} {
		g := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "playwright", Name: "playwright-full", Status: "completed",
			Repo: mmRepo, Branch: "pr-x", Commit: "sha-x-" + uuid.NewString()[:6], RunID: "run-x-" + uuid.NewString()[:6], Attempt: "1",
			PRNum: prPtr(prNum), Stage: "pr", CreatedAt: t30h,
		})
		r := insertReport(t, ctx, env.Pool, g, "playwright-full", "")
		s := insertSuite(t, ctx, env.Pool, r, "S")
		insertCaseWithSig(t, ctx, env.Pool, s, t1, sig3PRs, "t1", "failed", 0, 0)
		mustRefresh(t, ctx, env.Pool, g)
	}

	// [7] PR #107 (t30h): orchestrated timed_out with 0 test_cases (0 signatures) -> MUST NOT vacuously match failing_elsewhere -> reason = "other"!
	gPR107 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full-fips", Status: "incomplete",
		Repo: mmRepo, Branch: "pr-107", Commit: "sha-107", RunID: "run-107", Attempt: "1",
		PRNum: prPtr(107), Stage: "pr", CreatedAt: t30h,
	})
	insertOrchRun(t, ctx, env.Pool, orchSeed{
		Repo: mmRepo, Commit: "sha-107", RunID: "run-107", Name: "playwright-full-fips",
		Attempt: "1", Status: "timed_out", FailCount: 0, StartedAt: t30h,
	})
	mustRefresh(t, ctx, env.Pool, gPR107)

	// [8] PR #108 (t30h): 2 failed signatures (sigMain + sigUnique108) -> NOT all failing elsewhere -> reason = "other", failing_on_main = true.
	gPR108 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-108", Commit: "sha-108", RunID: "run-108", Attempt: "1",
		PRNum: prPtr(108), Stage: "pr", CreatedAt: t30h,
	})
	r108 := insertReport(t, ctx, env.Pool, gPR108, "playwright-full", "")
	s108 := insertSuite(t, ctx, env.Pool, r108, "S")
	insertCaseWithSig(t, ctx, env.Pool, s108, t1, sigMain, "t1", "failed", 0, 0)
	insertCaseWithSig(t, ctx, env.Pool, s108, tFlaky, sigUnique108, "t2", "failed", 0, 1)
	mustRefresh(t, ctx, env.Pool, gPR108)

	// [9] PR #109 (t30h): sigMain occurred ONLY on a recovered flaky retry attempt (failed retry 0 + passed retry 1),
	// while t1 failed with sigUnique109 -> has_failed_test=false for sigMain -> reason = "other", failing_on_main = false!
	gPR109 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-109", Commit: "sha-109", RunID: "run-109", Attempt: "1",
		PRNum: prPtr(109), Stage: "pr", CreatedAt: t30h,
	})
	r109 := insertReport(t, ctx, env.Pool, gPR109, "playwright-full", "")
	s109 := insertSuite(t, ctx, env.Pool, r109, "S")
	insertCaseWithSig(t, ctx, env.Pool, s109, tFlaky, sigMain, "flaky", "failed", 0, 0)
	insertCase(t, ctx, env.Pool, s109, tFlaky, "flaky", "passed", 1, "", 1)
	insertCaseWithSig(t, ctx, env.Pool, s109, t1, sigUnique109, "t1", "failed", 0, 2)
	mustRefresh(t, ctx, env.Pool, gPR109)

	// [10] PR #110 (t25h): upload-only desktop-pr with test_stats_json.failed = 2 -> red!
	// Also seed an upload-only desktop-pr with status='incomplete' and test_stats_json.failed = 0 -> NOT red!
	var gDeskRed uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (
			framework, name, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
			gh_pr_number, stage, test_stats_json, created_at, updated_at, last_upload_at
		) VALUES ('playwright', 'desktop-pr', 'completed', $1, 'pr-110', 'sha-110', 'run-110', '1', 110, 'pr',
		          '{"total":10,"failed":2}'::jsonb, $2, $2, $2)
		RETURNING id
	`, deskRepo, t25h).Scan(&gDeskRed); err != nil {
		t.Fatalf("insert desktop red: %v", err)
	}
	mustRefresh(t, ctx, env.Pool, gDeskRed)

	var gDeskGreenIncomplete uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (
			framework, name, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
			gh_pr_number, stage, test_stats_json, created_at, updated_at, last_upload_at
		) VALUES ('playwright', 'desktop-pr', 'incomplete', $1, 'pr-111', 'sha-111', 'run-111', '1', 111, 'pr',
		          '{"total":10,"failed":0}'::jsonb, $2, $2, $2)
		RETURNING id
	`, deskRepo, t25h).Scan(&gDeskGreenIncomplete); err != nil {
		t.Fatalf("insert desktop incomplete green: %v", err)
	}
	mustRefresh(t, ctx, env.Pool, gDeskGreenIncomplete)

	// [11] PR #112 (t30h): sigOldMain failed on main 100h ago (>24h) -> reason = "other".
	gPR112 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-112", Commit: "sha-112", RunID: "run-112", Attempt: "1",
		PRNum: prPtr(112), Stage: "pr", CreatedAt: t30h,
	})
	r112 := insertReport(t, ctx, env.Pool, gPR112, "playwright-full", "")
	s112 := insertSuite(t, ctx, env.Pool, r112, "S")
	insertCaseWithSig(t, ctx, env.Pool, s112, t1, sigOldMain, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gPR112)

	// [12] PR #113 (t30h): sig2PRs failed on 2 other PRs (#114, #115) -> 2 < 3 -> reason = "other"!
	for _, prNum := range []int{113, 114, 115} {
		g := insertGroup(t, ctx, env.Pool, groupSeed{
			Framework: "playwright", Name: "playwright-full", Status: "completed",
			Repo: mmRepo, Branch: "pr-2p", Commit: "sha-2p-" + uuid.NewString()[:6], RunID: "run-2p-" + uuid.NewString()[:6], Attempt: "1",
			PRNum: prPtr(prNum), Stage: "pr", CreatedAt: t30h,
		})
		r := insertReport(t, ctx, env.Pool, g, "playwright-full", "")
		s := insertSuite(t, ctx, env.Pool, r, "S")
		insertCaseWithSig(t, ctx, env.Pool, s, t1, sig2PRs, "t1", "failed", 0, 0)
		mustRefresh(t, ctx, env.Pool, g)
	}

	// ---- Unsettled Red PR E2E Lanes (<= 24h) ----
	// [U1] PR #201 at exact boundary fixedNow - 24h -> MUST be unsettled (settled=false).
	gU24 := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-201", Commit: "sha-201", RunID: "run-201", Attempt: "1",
		PRNum: prPtr(201), Stage: "pr", CreatedAt: t24hExact,
	})
	rU24 := insertReport(t, ctx, env.Pool, gU24, "playwright-full", "")
	sU24 := insertSuite(t, ctx, env.Pool, rU24, "S")
	insertCaseWithSig(t, ctx, env.Pool, sU24, t1, sigUnique201, "t1", "failed", 0, 0)
	mustRefresh(t, ctx, env.Pool, gU24)

	// [U2] PR #202 at fixedNow - 23h -> unsettled multi-platform mobile-detox-pr (ios failed, android passed).
	gMob := insertGroup(t, ctx, env.Pool, groupSeed{
		Framework: "detox", Name: "mobile-detox-pr", Status: "completed",
		Repo: mobRepo, Branch: "pr-202", Commit: "sha-202", RunID: "run-202", Attempt: "1",
		PRNum: prPtr(202), Stage: "pr", CreatedAt: t23h,
	})
	rMobIOS := insertReport(t, ctx, env.Pool, gMob, "mobile-detox-pr-ios", "ios")
	sMobIOS := insertSuite(t, ctx, env.Pool, rMobIOS, "S")
	insertCase(t, ctx, env.Pool, sMobIOS, tMob, "mob", "failed", 0, "", 0)
	rMobAnd := insertReport(t, ctx, env.Pool, gMob, "mobile-detox-pr-android", "android")
	sMobAnd := insertSuite(t, ctx, env.Pool, rMobAnd, "S")
	insertCase(t, ctx, env.Pool, sMobAnd, tMob, "mob", "passed", 0, "", 0)
	mustRefresh(t, ctx, env.Pool, gMob)

	// Let's tally settled red PR E2E lanes:
	// PR #101 -> went_green_on_rerun (1)
	// PR #102 -> failing_elsewhere (sigMain)
	// PR #103, #104, #105, #106 -> failing_elsewhere (sig3PRs on 3 distinct other PRs each) (total failing_elsewhere = 5)
	// PR #107 (timed_out, 0 sigs) -> other
	// PR #108 (sigMain + sigUnique) -> other
	// PR #109 (flaky sigMain + failed sigUnique) -> other
	// PR #110 (desktop-pr upload-only) -> other
	// PR #112 (sigOldMain >24h) -> other
	// PR #113, #114, #115 (sig2PRs, only 2 other PRs each) -> other
	// Settled total = 1 (rerun) + 5 (failing_elsewhere) + 8 (other) = 14 settled red lanes!
	// Unsettled total = 2 (PR #201 at exact 24h + PR #202 at 23h).
	// False-red rate = 1 / 14 * 100 = 7.142857...%
	// Prior rate = 1 / 2 * 100 = 50.0%
	// DeltaPP = 7.142857 - 50.0 = -42.857143

	f30, err := kpi.ParsePRsRedParams(nil, fixedNow)
	if err != nil {
		t.Fatalf("ParsePRsRedParams: %v", err)
	}
	res, err := store.QueryPRsRed(ctx, f30)
	if err != nil {
		t.Fatalf("QueryPRsRed: %v", err)
	}

	if !res.IsLowerBound || res.RateQualifier != "at least" || res.SettledAfterHours != 24 {
		t.Fatalf("qualifier mismatch: lower=%v qual=%q hours=%d", res.IsLowerBound, res.RateQualifier, res.SettledAfterHours)
	}
	if res.SettledRedLanes != 14 || res.SettledFalseRedLanes != 1 || res.UnsettledRedLanes != 2 {
		t.Fatalf("counts = settled:%d falseRed:%d unsettled:%d, want 14/1/2",
			res.SettledRedLanes, res.SettledFalseRedLanes, res.UnsettledRedLanes)
	}
	if res.ReasonMix.WentGreenOnRerun != 1 || res.ReasonMix.FailingElsewhere != 5 || res.ReasonMix.Other != 8 || res.ReasonMix.Total != 14 {
		t.Fatalf("reason_mix = %+v, want 1/5/8/14", res.ReasonMix)
	}
	wantRate := 100.0 / 14.0
	assertNearPtr(t, "false_red_rate", res.FalseRedRate, wantRate, 1e-6)
	assertNearPtr(t, "prior_false_red_rate", res.PriorFalseRedRate, 50.0, 1e-6)
	assertNearPtr(t, "delta_pp", res.DeltaPP, wantRate-50.0, 1e-6)

	// Index rows by PR number for detailed per-row verification.
	byPR := map[int]kpi.RedPRLaneRow{}
	for _, row := range res.Lanes {
		byPR[row.GHPRNumber] = row
	}
	if _, excluded := byPR[500]; excluded {
		t.Fatal("unconfigured lane 'unit-tests' was not excluded")
	}
	if _, excluded := byPR[501]; excluded {
		t.Fatal("disabled lane 'cypress-full' was not excluded")
	}
	if _, excluded := byPR[111]; excluded {
		t.Fatal("upload-only incomplete with failed=0 was falsely treated as red")
	}

	assertRow := func(pr int, settled, stillRed, rerun, onMain bool, otherPRs int, reason string) {
		t.Helper()
		r, ok := byPR[pr]
		if !ok {
			t.Fatalf("missing PR #%d in response lanes", pr)
		}
		if r.Settled != settled || r.StillRed != stillRed || r.WentGreenOnRerun != rerun || r.FailingOnMain != onMain || r.OtherPRsCount != otherPRs || r.Reason != reason {
			t.Fatalf("PR #%d = {settled:%v stillRed:%v rerun:%v onMain:%v otherPRs:%d reason:%q}, want {%v %v %v %v %d %q}",
				pr, r.Settled, r.StillRed, r.WentGreenOnRerun, r.FailingOnMain, r.OtherPRsCount, r.Reason,
				settled, stillRed, rerun, onMain, otherPRs, reason)
		}
	}
	assertRow(101, true, false, true, true, 2, kpi.ReasonWentGreenOnRerun)
	assertRow(102, true, true, false, true, 2, kpi.ReasonFailingElsewhere)
	assertRow(103, true, false, false, false, 3, kpi.ReasonFailingElsewhere)
	assertRow(107, true, true, false, false, 0, kpi.ReasonOther)
	assertRow(108, true, true, false, true, 2, kpi.ReasonOther)
	assertRow(109, true, true, false, false, 0, kpi.ReasonOther)
	assertRow(110, true, true, false, false, 0, kpi.ReasonOther)
	assertRow(112, true, true, false, false, 0, kpi.ReasonOther)
	assertRow(113, true, true, false, false, 2, kpi.ReasonOther)
	assertRow(201, false, true, false, false, 0, kpi.ReasonOther)
	assertRow(202, false, true, false, false, 0, kpi.ReasonOther)

	if wantURL := "/reports/mattermost/feat~pr-101/sha-101/playwright-full?gh_run_id=run-101&gh_run_attempt=2"; byPR[101].RunURL != wantURL {
		t.Fatalf("PR #101 run_url = %q, want %q", byPR[101].RunURL, wantURL)
	}

	// Platform slicing: ?platform=ios includes PR #202; ?platform=android returns 0 rows.
	fIOS, _ := kpi.ParsePRsRedParams(map[string][]string{"platform": {"ios"}}, fixedNow)
	resIOS, err := store.QueryPRsRed(ctx, fIOS)
	if err != nil {
		t.Fatalf("QueryPRsRed(ios): %v", err)
	}
	if resIOS.Pagination.Total != 1 || resIOS.Lanes[0].GHPRNumber != 202 {
		t.Fatalf("ios filter got %+v, want PR #202 only", resIOS.Lanes)
	}
	fAnd, _ := kpi.ParsePRsRedParams(map[string][]string{"platform": {"android"}}, fixedNow)
	resAnd, err := store.QueryPRsRed(ctx, fAnd)
	if err != nil {
		t.Fatalf("QueryPRsRed(android): %v", err)
	}
	if resAnd.Pagination.Total != 0 || resAnd.FalseRedRate != nil {
		t.Fatalf("android filter got total=%d rate=%v, want 0/nil", resAnd.Pagination.Total, resAnd.FalseRedRate)
	}

	// TSIO_KPI_E2E_LANES override test: restrict to desktop-pr only.
	overrideStore := &kpi.Store{
		Pool:     env.Pool,
		Now:      func() time.Time { return fixedNow },
		E2ELanes: []string{"mattermost/desktop:desktop-pr"},
	}
	resOverride, err := overrideStore.QueryPRsRed(ctx, f30)
	if err != nil {
		t.Fatalf("override QueryPRsRed: %v", err)
	}
	if resOverride.Pagination.Total != 1 || resOverride.Lanes[0].GHPRNumber != 110 {
		t.Fatalf("override got %+v, want PR #110 only", resOverride.Lanes)
	}

	// Summary & WeeklySeries & HTTP endpoint checks (including stage=main ignored note + zero author names).
	rawHTTP := getRaw(t, env.ServerURL+"/api/v1/kpi/prs/red?window=30d&stage=main&sort=pr_desc&page=1&limit=3")
	if strings.Contains(strings.ToLower(rawHTTP), "author") {
		t.Fatalf("unexpected author field in /kpi/prs/red JSON: %s", rawHTTP)
	}
	httpPRs := getJSON[kpi.PRRedResponse](t, env.ServerURL+"/api/v1/kpi/prs/red?window=30d&stage=main&sort=pr_desc&page=1&limit=3")
	if len(httpPRs.Lanes) != 3 || httpPRs.Pagination.Total != 16 || httpPRs.Pagination.TotalPages != 6 {
		t.Fatalf("pagination = %+v len=%d, want 3 rows, total=16, totalPages=6", httpPRs.Pagination, len(httpPRs.Lanes))
	}
	if !(httpPRs.Lanes[0].GHPRNumber >= httpPRs.Lanes[1].GHPRNumber && httpPRs.Lanes[1].GHPRNumber >= httpPRs.Lanes[2].GHPRNumber) {
		t.Fatalf("sort=pr_desc order violated: %d, %d, %d",
			httpPRs.Lanes[0].GHPRNumber, httpPRs.Lanes[1].GHPRNumber, httpPRs.Lanes[2].GHPRNumber)
	}
	hasStageNote := false
	for _, n := range httpPRs.LaneCoverage.Notes {
		if strings.Contains(n, `stage="main"`) {
			hasStageNote = true
		}
	}
	if !hasStageNote {
		t.Fatalf("expected stage ignored note in lane_coverage.notes, got %v", httpPRs.LaneCoverage.Notes)
	}

	// Summary integration via Store (deterministic fixedNow).
	sumFilter, err := kpi.ParseSummaryParams(map[string][]string{"window": {"30d"}, "stage": {"main"}}, fixedNow)
	if err != nil {
		t.Fatalf("ParseSummaryParams: %v", err)
	}
	summary, err := store.QuerySummary(ctx, sumFilter)
	if err != nil {
		t.Fatalf("QuerySummary: %v", err)
	}
	if summary.PRFalseRed == nil {
		t.Fatal("expected summary.PRFalseRed to be populated")
	}
	if summary.PRFalseRed.SettledRedLanes != 14 || summary.PRFalseRed.SettledFalseRedLanes != 1 || summary.PRFalseRed.UnsettledRedLanes != 2 {
		t.Fatalf("summary.PRFalseRed counts = %+v", summary.PRFalseRed)
	}
	assertNearPtr(t, "summary.PRFalseRed.FalseRedRate", summary.PRFalseRed.FalseRedRate, wantRate, 1e-6)
	assertNearPtr(t, "summary.PRFalseRed.PriorFalseRedRate", summary.PRFalseRed.PriorFalseRedRate, 50.0, 1e-6)
	foundWeeklyRate := false
	for _, wp := range summary.WeeklySeries {
		if wp.PRFalseRedRate != nil {
			foundWeeklyRate = true
			assertNearPtr(t, "weekly pr_false_red_rate", wp.PRFalseRedRate, wantRate, 1e-6)
		}
	}
	if !foundWeeklyRate {
		t.Fatal("expected at least one weekly_series entry with non-nil pr_false_red_rate")
	}

	// Verify remaining sort modes return HTTP 200 cleanly.
	for _, sortVal := range []string{"created_desc", "age_desc", "lane_asc", "reason_asc"} {
		_ = getJSON[kpi.PRRedResponse](t, env.ServerURL+"/api/v1/kpi/prs/red?sort="+sortVal)
	}
}

func window30dForTest() string { return kpi.Window30d }
