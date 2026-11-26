//go:build e2e
// +build e2e

package htmlpagee2e

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestKPISSRPages_DisabledWhenHTMLViewFalse(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())

	for _, path := range []string{
		"/kpi",
		"/kpi/prs",
		"/kpi/failures/019a1234-5678-7abc-8def-0123456789ab",
	} {
		resp, err := http.Get(env.ServerURL + path) //nolint:gosec
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		s := string(body)
		for _, ssrMarker := range []string{
			`Test Health KPIs`,
			`PR E2E Signal Reliability`,
			`kpi-failure-hero`,
			`kpi-cards-grid`,
		} {
			if strings.Contains(s, ssrMarker) {
				t.Fatalf("GET %s with HTMLViewEnabled=false unexpectedly served KPI SSR HTML (%q)", path, ssrMarker)
			}
		}
	}
}

func TestKPISSRPages_EmptyZeroDenominatorStates(t *testing.T) {
	env := testenv.Start(t, testenv.WithHTMLView(), testenv.WithReaperDisabled())

	overviewHTML := getHTML(t, env.ServerURL+"/kpi", http.StatusOK)
	for _, want := range []string{
		`Test Health KPIs`,
		`N/A`,
		`No data`,
		`No triage or run completion data in this window`,
		`No weekly trend data in this window`,
		`No recurring failures`,
		`Lane coverage:`,
	} {
		if !strings.Contains(overviewHTML, want) {
			t.Fatalf("empty /kpi missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "/kpi (empty)", overviewHTML, nil)

	prsHTML := getHTML(t, env.ServerURL+"/kpi/prs", http.StatusOK)
	for _, want := range []string{
		`PR E2E Signal Reliability`,
		`N/A`,
		`No settled red PR lanes to classify in this window`,
		`No E2E red PR lanes`,
		`Configured E2E lanes:`,
		`Lane coverage:`,
	} {
		if !strings.Contains(prsHTML, want) {
			t.Fatalf("empty /kpi/prs missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "/kpi/prs (empty)", prsHTML, nil)
}

func TestKPISSRPages_SeededDataFiltersDetailsAndNoSecrets(t *testing.T) {
	env := testenv.Start(t, testenv.WithHTMLView(), testenv.WithReaperDisabled())
	ctx := t.Context()

	now := time.Now().UTC().Truncate(time.Second)
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	if today.After(now) {
		today = today.Add(-24 * time.Hour)
	}

	const (
		mmRepo  = "mattermost/mattermost"
		mobRepo = "mattermost/mattermost-mobile"

		sentinelSecret1 = "TSIO_SENTINEL_SECRET_DO_NOT_LEAK_94827"
		sentinelSecret2 = "SECRET_ENV_VALUE_7731"
	)

	// Seed tests across repos.
	tMainPass := seedTest(t, ctx, env.Pool, mmRepo, "playwright", "k:main-pass", "Main > pass test")
	tActive := seedTest(t, ctx, env.Pool, mmRepo, "playwright", "k:active-fail", "Auth > active failure")
	tFlaky := seedTest(t, ctx, env.Pool, mmRepo, "playwright", "k:flaky-test", "Channel > flaky test")
	tQuiet := seedTest(t, ctx, env.Pool, mmRepo, "playwright", "k:quiet-test", "Search > quiet failure")
	tLowRuns := seedTest(t, ctx, env.Pool, mmRepo, "playwright", "k:low-runs", "Admin > low runs failure")
	tResurfaced := seedTest(t, ctx, env.Pool, mmRepo, "playwright", "k:resurfaced", "Files > resurfaced failure")
	tMobile := seedTest(t, ctx, env.Pool, mobRepo, "detox", "k:mobile-fail", "Mobile > login fail")

	// Seed failure signatures covering Active, Quiet, Not enough runs, and Resurfaced.
	sigActive := seedFailureSignature(t, ctx, env.Pool, "TimeoutError", "waiting for #login-submit button", "e2e/login.spec.ts:42", today.AddDate(0, 0, -20))
	sigQuiet := seedFailureSignature(t, ctx, env.Pool, "NetworkError", "ECONNRESET on search index", "e2e/search.spec.ts:19", today.AddDate(0, 0, -15))
	sigNotEnough := seedFailureSignature(t, ctx, env.Pool, "AssertionError", "expected badge count to equal 2", "e2e/admin.spec.ts:88", today.AddDate(0, 0, -12))
	sigResurfaced := seedFailureSignature(t, ctx, env.Pool, "ReferenceError", "window.__MM_STATE__ is undefined", "e2e/files.spec.ts:15", today.AddDate(0, 0, -24))

	prPtr := func(n int) *int { return &n }
	durPtr := func(ms int64) *int64 { return &ms }

	// 1. Main stage run (2d ago): attempt 1 with pass + flaky + active failure (sigActive on main!).
	gMain := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "e2e-pw-main", Status: "completed",
		Repo: mmRepo, Branch: "main", Commit: "main-sha-1", RunID: "run-main-1", Attempt: "1",
		Stage: "main", CreatedAt: now.Add(-46 * time.Hour), DurMs: durPtr(600000),
	})
	seedOrchRun(t, ctx, env.Pool, mmRepo, "main-sha-1", "run-main-1", "e2e-pw-main", "1", "completed", 1, true, now.Add(-46*time.Hour))
	rMain := seedReport(t, ctx, env.Pool, gMain, "e2e-pw-main", "")
	sMain := seedSuite(t, ctx, env.Pool, rMain, "MainSuite")
	seedCase(t, ctx, env.Pool, sMain, tMainPass, nil, "pass test", "passed", 0, 0)
	seedCase(t, ctx, env.Pool, sMain, tFlaky, nil, "flaky test", "failed", 0, 1)
	seedCase(t, ctx, env.Pool, sMain, tFlaky, nil, "flaky test", "passed", 1, 2)
	seedCase(t, ctx, env.Pool, sMain, tActive, &sigActive, "active failure", "failed", 0, 3)
	mustRefreshRollups(t, ctx, env.Pool, gMain)

	// Second commit for tFlaky so KPI 3 (>=2 flaky commits in 14d) counts it.
	gMain2 := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "e2e-pw-main", Status: "completed",
		Repo: mmRepo, Branch: "main", Commit: "main-sha-2", RunID: "run-main-2", Attempt: "1",
		Stage: "main", CreatedAt: now.Add(-30 * time.Hour), DurMs: durPtr(550000),
	})
	seedOrchRun(t, ctx, env.Pool, mmRepo, "main-sha-2", "run-main-2", "e2e-pw-main", "1", "completed", 0, true, now.Add(-30*time.Hour))
	rMain2 := seedReport(t, ctx, env.Pool, gMain2, "e2e-pw-main", "")
	sMain2 := seedSuite(t, ctx, env.Pool, rMain2, "MainSuite")
	seedCase(t, ctx, env.Pool, sMain2, tFlaky, nil, "flaky test", "failed", 0, 0)
	seedCase(t, ctx, env.Pool, sMain2, tFlaky, nil, "flaky test", "passed", 1, 1)
	mustRefreshRollups(t, ctx, env.Pool, gMain2)

	// 2. Settled PR #201 (-48h): playwright-full red attempt 1 -> went green on rerun attempt 2 (false red!).
	gPR201 := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-201", Commit: "sha-pr-201", RunID: "run-pr-201", Attempt: "1",
		PRNum: prPtr(201), Stage: "pr", CreatedAt: now.Add(-48 * time.Hour), DurMs: durPtr(900000),
	})
	seedOrchRun(t, ctx, env.Pool, mmRepo, "sha-pr-201", "run-pr-201", "playwright-full", "1", "completed", 1, false, now.Add(-48*time.Hour))
	r201 := seedReport(t, ctx, env.Pool, gPR201, "playwright-full", "")
	s201 := seedSuite(t, ctx, env.Pool, r201, "PR201")
	seedCase(t, ctx, env.Pool, s201, tActive, &sigActive, "active failure", "failed", 0, 0)
	mustRefreshRollups(t, ctx, env.Pool, gPR201)

	gPR201Rerun := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-201", Commit: "sha-pr-201", RunID: "run-pr-201", Attempt: "2",
		PRNum: prPtr(201), Stage: "pr", CreatedAt: now.Add(-47 * time.Hour), DurMs: durPtr(840000),
	})
	seedOrchRun(t, ctx, env.Pool, mmRepo, "sha-pr-201", "run-pr-201", "playwright-full", "2", "completed", 0, false, now.Add(-47*time.Hour))
	r201R := seedReport(t, ctx, env.Pool, gPR201Rerun, "playwright-full", "")
	s201R := seedSuite(t, ctx, env.Pool, r201R, "PR201")
	seedCase(t, ctx, env.Pool, s201R, tActive, nil, "active failure", "passed", 0, 0)
	mustRefreshRollups(t, ctx, env.Pool, gPR201Rerun)

	// 3. Settled PR #202 (-45h): playwright-full red with sigActive (also failing on main within ±24h) -> failing_elsewhere.
	gPR202 := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-202", Commit: "sha-pr-202", RunID: "run-pr-202", Attempt: "1",
		PRNum: prPtr(202), Stage: "pr", CreatedAt: now.Add(-45 * time.Hour), DurMs: durPtr(1200000),
	})
	seedOrchRun(t, ctx, env.Pool, mmRepo, "sha-pr-202", "run-pr-202", "playwright-full", "1", "completed", 1, false, now.Add(-45*time.Hour))
	r202 := seedReport(t, ctx, env.Pool, gPR202, "playwright-full", "")
	s202 := seedSuite(t, ctx, env.Pool, r202, "PR202")
	seedCase(t, ctx, env.Pool, s202, tActive, &sigActive, "active failure", "failed", 0, 0)
	mustRefreshRollups(t, ctx, env.Pool, gPR202)

	// 4. Unsettled PR #203 (-3h): playwright-full still red now with sigActive + sigResurfaced -> "E2E red" + "Unsettled (<24h)".
	gPR203 := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "playwright-full", Status: "completed",
		Repo: mmRepo, Branch: "pr-203", Commit: "sha-pr-203", RunID: "run-pr-203", Attempt: "1",
		PRNum: prPtr(203), Stage: "pr", CreatedAt: now.Add(-3 * time.Hour), DurMs: durPtr(720000),
	})
	seedOrchRun(t, ctx, env.Pool, mmRepo, "sha-pr-203", "run-pr-203", "playwright-full", "1", "completed", 2, false, now.Add(-3*time.Hour))
	r203 := seedReport(t, ctx, env.Pool, gPR203, "playwright-full", "")
	s203 := seedSuite(t, ctx, env.Pool, r203, "PR203")
	seedCase(t, ctx, env.Pool, s203, tActive, &sigActive, "active failure", "failed", 0, 0)
	seedCase(t, ctx, env.Pool, s203, tResurfaced, &sigResurfaced, "resurfaced failure", "failed", 0, 1)
	mustRefreshRollups(t, ctx, env.Pool, gPR203)

	// 5. Prior-14d occurrence for sigActive (-20d) on non-E2E lane so 14d vs prior 14d comparison has real prior data without altering PR E2E false-red denominator.
	gPriorSig := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "e2e-unit-pr", Status: "completed",
		Repo: mmRepo, Branch: "pr-190", Commit: "sha-pr-190", RunID: "run-pr-190", Attempt: "1",
		PRNum: prPtr(190), Stage: "pr", CreatedAt: now.AddDate(0, 0, -20), DurMs: durPtr(650000),
	})
	rPriorSig := seedReport(t, ctx, env.Pool, gPriorSig, "e2e-unit-pr", "")
	sPriorSig := seedSuite(t, ctx, env.Pool, rPriorSig, "Prior")
	seedCase(t, ctx, env.Pool, sPriorSig, tActive, &sigActive, "active failure", "failed", 0, 0)
	mustRefreshRollups(t, ctx, env.Pool, gPriorSig)

	// 6. Quiet signature (-10d) + >=500 runs of tQuiet in trailing 7d.
	gQuiet := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "e2e-unit-pr", Status: "completed",
		Repo: mmRepo, Branch: "pr-195", Commit: "sha-pr-195", RunID: "run-pr-195", Attempt: "1",
		PRNum: prPtr(195), Stage: "pr", CreatedAt: now.AddDate(0, 0, -10),
	})
	rQuiet := seedReport(t, ctx, env.Pool, gQuiet, "e2e-unit-pr", "")
	sQuiet := seedSuite(t, ctx, env.Pool, rQuiet, "Quiet")
	seedCase(t, ctx, env.Pool, sQuiet, tQuiet, &sigQuiet, "quiet failure", "failed", 0, 0)
	mustRefreshRollups(t, ctx, env.Pool, gQuiet)
	seedTestExecutions(t, ctx, env.Pool, today.AddDate(0, 0, -2), mmRepo, "mattermost", "pr", "e2e-unit-pr", tQuiet, 600)

	// 7. Not-enough-runs signature (-10d) with <500 runs in trailing 7d.
	gLow := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "e2e-unit-pr", Status: "completed",
		Repo: mmRepo, Branch: "pr-196", Commit: "sha-pr-196", RunID: "run-pr-196", Attempt: "1",
		PRNum: prPtr(196), Stage: "pr", CreatedAt: now.AddDate(0, 0, -10),
	})
	rLow := seedReport(t, ctx, env.Pool, gLow, "e2e-unit-pr", "")
	sLow := seedSuite(t, ctx, env.Pool, rLow, "LowRuns")
	seedCase(t, ctx, env.Pool, sLow, tLowRuns, &sigNotEnough, "low runs failure", "failed", 0, 0)
	mustRefreshRollups(t, ctx, env.Pool, gLow)

	// 8. Resurfaced signature: initial occurrence -24d, >=500 quiet runs in [-20d..-14d], resurfaced today in gPR203.
	gResurfOld := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "playwright", Name: "e2e-unit-pr", Status: "completed",
		Repo: mmRepo, Branch: "pr-180", Commit: "sha-pr-180", RunID: "run-pr-180", Attempt: "1",
		PRNum: prPtr(180), Stage: "pr", CreatedAt: now.AddDate(0, 0, -24),
	})
	rResurf := seedReport(t, ctx, env.Pool, gResurfOld, "e2e-unit-pr", "")
	sResurf := seedSuite(t, ctx, env.Pool, rResurf, "ResurfacedOld")
	seedCase(t, ctx, env.Pool, sResurf, tResurfaced, &sigResurfaced, "resurfaced failure", "failed", 0, 0)
	mustRefreshRollups(t, ctx, env.Pool, gResurfOld)
	seedTestExecutions(t, ctx, env.Pool, today.AddDate(0, 0, -16), mmRepo, "mattermost", "pr", "e2e-unit-pr", tResurfaced, 550)

	// 9. Mobile failure on mattermost/mattermost-mobile so app filter dropdowns have 2 distinct apps.
	gMob := seedGroup(t, ctx, env.Pool, seedGroupSpec{
		Framework: "detox", Name: "mobile-pr", Status: "completed",
		Repo: mobRepo, Branch: "pr-55", Commit: "sha-mob-55", RunID: "run-mob-55", Attempt: "1",
		PRNum: prPtr(55), Stage: "pr", CreatedAt: now.Add(-2 * time.Hour),
	})
	rMob := seedReport(t, ctx, env.Pool, gMob, "mobile-pr", "ios")
	sMob := seedSuite(t, ctx, env.Pool, rMob, "Mobile")
	seedCase(t, ctx, env.Pool, sMob, tMobile, &sigActive, "mobile active failure", "failed", 0, 0)
	mustRefreshRollups(t, ctx, env.Pool, gMob)

	// 10. Seed verdicts (including sensitive error_context sentinel that MUST NEVER appear in any SSR page).
	vOriginID := seedVerdict(t, ctx, env.Pool, gPR201, mmRepo, tActive, sigActive, "flaky-infra",
		"Known infra timeout in login", "https://github.com/mattermost/mattermost/actions/runs/999",
		nil, sentinelSecret1, now.Add(-47*time.Hour))
	_ = seedVerdict(t, ctx, env.Pool, gPR203, mmRepo, tActive, sigActive, "flaky-infra",
		"Known infra timeout in login", "https://github.com/mattermost/mattermost/actions/runs/999",
		&vOriginID, sentinelSecret2, now.Add(-2*time.Hour))

	// Refresh groups that received verdicts afterwards so triage rollups reflect verdicts.
	mustRefreshRollups(t, ctx, env.Pool, gPR201)
	mustRefreshRollups(t, ctx, env.Pool, gPR203)

	secrets := []string{sentinelSecret1, sentinelSecret2}

	// ---- Verify GET /kpi (200) ----
	ovHTML := getHTML(t, env.ServerURL+"/kpi", http.StatusOK)
	for _, want := range []string{
		`Test Health KPIs`,
		`Main first-attempt pass rate`,
		`PR false-red rate`,
		`Flaky tests`,
		`Recurring failures`,
		`E2E PR run time`,
		`at least 50.0%`,
		`AI Triage &amp; Run Completion`,
		`flaky-infra: 2 (100.0%)`,
		`Weekly Trends`,
		`Lane coverage:`,
		`Active`,
		`Quiet (`,
		`Not enough runs`,
		`Resurfaced`,
		`/kpi/failures/` + sigActive.String(),
		`href="/kpi/prs"`,
	} {
		if !strings.Contains(ovHTML, want) {
			t.Fatalf("GET /kpi missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi", ovHTML, secrets)

	// ---- Verify GET /kpi/prs (200) ----
	prsHTML := getHTML(t, env.ServerURL+"/kpi/prs", http.StatusOK)
	for _, want := range []string{
		`PR E2E Signal Reliability`,
		`at least 50.0%`,
		`Went green on rerun`,
		`Also failing elsewhere`,
		`Other`,
		`Configured E2E lanes:`,
		`mattermost/mattermost:playwright-full`,
		`Lane coverage:`,
		`Red now`,
		`E2E red`,
		`Failing on main`,
		`#203`,
		`Unsettled (&lt;24h)`,
		`/reports/mattermost/pr-203/`,
	} {
		if !strings.Contains(prsHTML, want) {
			t.Fatalf("GET /kpi/prs missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi/prs", prsHTML, secrets)

	// ---- Verify GET /kpi/failures/{id} (200, 400, 404) ----
	detailHTML := getHTML(t, env.ServerURL+"/kpi/failures/"+sigActive.String(), http.StatusOK)
	for _, want := range []string{
		`TimeoutError`,
		`waiting for #login-submit button`,
		`e2e/login.spec.ts:42`,
		`Daily Occurrences &amp; Rate`,
		`14d vs prior 14d`,
		`Affected Tests`,
		`Auth &gt; active failure`,
		`Affected Repositories`,
		`mattermost/mattermost`,
		`mattermost/mattermost-mobile`,
		`First seen`,
		`Latest AI triage verdict`,
		`flaky-infra`,
		`Known infra timeout in login`,
		`91%`,
		`https://github.com/mattermost/mattermost/actions/runs/999`,
		`Active`,
	} {
		if !strings.Contains(detailHTML, want) {
			t.Fatalf("GET /kpi/failures/{id} missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi/failures/{id}", detailHTML, secrets)

	// 400 invalid UUID & 404 unknown UUID.
	getHTML(t, env.ServerURL+"/kpi/failures/not-a-uuid", http.StatusBadRequest)
	getHTML(t, env.ServerURL+"/kpi/failures/019a0000-0000-7000-8000-000000000999", http.StatusNotFound)

	// ---- Verify filter query-param round-tripping ----
	ovFiltered := getHTML(t, env.ServerURL+"/kpi?app=mattermost&window=14d&sort=rate_desc&page=1", http.StatusOK)
	for _, want := range []string{
		`<option value="mattermost" selected>`,
		`<option value="14d" selected>`,
		`<option value="rate_desc" selected>`,
		`href="/kpi/prs?app=mattermost&amp;window=14d"`,
		`/kpi/failures/` + sigActive.String() + `?app=mattermost&amp;window=14d`,
	} {
		if !strings.Contains(ovFiltered, want) {
			t.Fatalf("filtered /kpi missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi filtered", ovFiltered, secrets)

	prsFiltered := getHTML(t, env.ServerURL+"/kpi/prs?app=mattermost&window=14d&sort=age_desc&page=1", http.StatusOK)
	for _, want := range []string{
		`<option value="mattermost" selected>`,
		`<option value="14d" selected>`,
		`<option value="age_desc" selected>`,
		`href="/kpi?app=mattermost&amp;window=14d"`,
	} {
		if !strings.Contains(prsFiltered, want) {
			t.Fatalf("filtered /kpi/prs missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi/prs filtered", prsFiltered, secrets)

	// ---- Verify custom limit preservation in pagination links and filter form ----
	prsLimitP1 := getHTML(t, env.ServerURL+"/kpi/prs?limit=1", http.StatusOK)
	for _, want := range []string{
		`<input type="hidden" name="limit" value="1">`,
		`href="/kpi?limit=1"`,
		`href="/kpi/prs?limit=1"`,
		`href="?limit=1&amp;page=2">Next</a>`,
		`href="/kpi/prs" class="kpi-btn kpi-btn-ghost">Reset</a>`,
	} {
		if !strings.Contains(prsLimitP1, want) {
			t.Fatalf("GET /kpi/prs?limit=1 missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi/prs?limit=1", prsLimitP1, secrets)

	prsLimitP2 := getHTML(t, env.ServerURL+"/kpi/prs?limit=1&page=2", http.StatusOK)
	for _, want := range []string{
		`<input type="hidden" name="limit" value="1">`,
		`href="?limit=1">Previous</a>`,
	} {
		if !strings.Contains(prsLimitP2, want) {
			t.Fatalf("GET /kpi/prs?limit=1&page=2 missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi/prs?limit=1&page=2", prsLimitP2, secrets)

	prsDefaultLimit := getHTML(t, env.ServerURL+"/kpi/prs?limit=50", http.StatusOK)
	if strings.Contains(prsDefaultLimit, `name="limit"`) {
		t.Fatal("GET /kpi/prs?limit=50 must not render hidden limit input")
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi/prs?limit=50", prsDefaultLimit, secrets)

	ovLimitP1 := getHTML(t, env.ServerURL+"/kpi?limit=2", http.StatusOK)
	for _, want := range []string{
		`<input type="hidden" name="limit" value="2">`,
		`href="?limit=2&amp;page=2">Next</a>`,
		`href="/kpi/prs?limit=2"`,
	} {
		if !strings.Contains(ovLimitP1, want) {
			t.Fatalf("GET /kpi?limit=2 missing %q", want)
		}
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi?limit=2", ovLimitP1, secrets)

	// ---- Verify 400 on invalid filter/sort/page (including int64 overflow) ----
	for _, badPath := range []string{
		"/kpi?window=3d",
		"/kpi?stage=bogus",
		"/kpi?sort=unknown_sort",
		"/kpi?page=0",
		"/kpi?page=abc",
		"/kpi?page=999999999999999999",
		"/kpi/prs?window=3d",
		"/kpi/prs?sort=rate_desc",
		"/kpi/prs?page=-1",
		"/kpi/prs?page=999999999999999999",
		"/api/v1/kpi/failures?page=999999999999999999",
		"/api/v1/kpi/prs/red?page=999999999999999999",
		"/kpi/failures/" + sigActive.String() + "?window=3d",
	} {
		getHTML(t, env.ServerURL+badPath, http.StatusBadRequest)
	}

	// ---- Verify page > total_pages preserves pagination recovery and avoids misleading empty copy ----
	ovOutOfRange := getHTML(t, env.ServerURL+"/kpi?page=5", http.StatusOK)
	for _, want := range []string{
		`Page 5 out of range`,
		`kpi-pagination`,
		`Previous</a>`,
	} {
		if !strings.Contains(ovOutOfRange, want) {
			t.Fatalf("GET /kpi?page=5 missing %q", want)
		}
	}
	if strings.Contains(ovOutOfRange, "No recurring failures") {
		t.Fatal("GET /kpi?page=5 with seeded failures must NOT say 'No recurring failures'")
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi?page=5", ovOutOfRange, secrets)

	prsOutOfRange := getHTML(t, env.ServerURL+"/kpi/prs?page=5", http.StatusOK)
	for _, want := range []string{
		`Page 5 out of range`,
		`kpi-pagination`,
		`Previous</a>`,
	} {
		if !strings.Contains(prsOutOfRange, want) {
			t.Fatalf("GET /kpi/prs?page=5 missing %q", want)
		}
	}
	if strings.Contains(prsOutOfRange, "No E2E red PR lanes") {
		t.Fatal("GET /kpi/prs?page=5 with seeded red PR lanes must NOT say 'No E2E red PR lanes'")
	}
	assertNoKPIGuardrailViolations(t, "GET /kpi/prs?page=5", prsOutOfRange, secrets)
}

func assertNoKPIGuardrailViolations(t *testing.T, label, htmlBody string, secrets []string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(htmlBody, s) {
			t.Fatalf("%s leaked verdicts.error_context sentinel %q", label, s)
		}
	}
	for _, forbid := range []string{
		`error_context`,
		`Fixed`,
		`id="connection-status"`,
	} {
		if strings.Contains(htmlBody, forbid) {
			t.Fatalf("%s must not contain %q", label, forbid)
		}
	}
	if strings.Contains(strings.ToLower(htmlBody), "blocked") {
		t.Fatalf("%s must NEVER contain 'blocked'", label)
	}
}

func getHTML(t *testing.T, rawURL string, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(rawURL) //nolint:gosec
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d (body=%s)", rawURL, resp.StatusCode, wantStatus, string(body))
	}
	return string(body)
}

type seedGroupSpec struct {
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

func seedTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repo, framework, testKey, fullTitle string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO tests (repository, framework, test_key, file, full_title)
		VALUES ($1, $2, $3, 'e2e/test.spec.ts', $4)
		RETURNING id
	`, repo, framework, testKey, fullTitle).Scan(&id); err != nil {
		t.Fatalf("seed test %s: %v", testKey, err)
	}
	return id
}

func seedFailureSignature(t *testing.T, ctx context.Context, pool *pgxpool.Pool, errClass, normMsg, topFrame string, firstSeenAt time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO failure_signatures (fingerprint, algo_version, error_class, normalized_message, top_frame, first_seen_at)
		VALUES ($1, 2, $2, $3, $4, $5)
		RETURNING id
	`, uuid.NewString(), errClass, normMsg, topFrame, firstSeenAt).Scan(&id); err != nil {
		t.Fatalf("seed failure_signature: %v", err)
	}
	return id
}

func seedGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, s seedGroupSpec) uuid.UUID {
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
		t.Fatalf("seed report_group %s/%s: %v", s.Name, s.Commit, err)
	}
	return id
}

func seedOrchRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repo, commit, runID, name, attempt, status string, failCount int, hasRetried bool, startedAt time.Time) {
	t.Helper()
	var orchID uuid.UUID
	termAt := startedAt.Add(time.Minute)
	passCount := 1
	if failCount > 0 {
		passCount = 0
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
			0, $7, $8, $9,
			'\xdeadbeef'::bytea, $10, $10, $11, 'test-oidc'
		)
		RETURNING id
	`, repo, commit, runID, name, attempt, status, passCount, failCount, passCount+failCount, startedAt, termAt).Scan(&orchID); err != nil {
		t.Fatalf("seed orchestration_run %s: %v", name, err)
	}
	if hasRetried {
		if _, err := pool.Exec(ctx, `
			INSERT INTO dispatch_units (run_id, dispatch_seq, spec_path, state, lease_count, fail_count, outcome_set_at)
			VALUES ($1, 0, 'e2e/retried.spec.ts', 'completed_pass', 2, 1, $2)
		`, orchID, termAt); err != nil {
			t.Fatalf("seed dispatch_unit: %v", err)
		}
	}
}

func seedReport(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID, name, platform string) uuid.UUID {
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
		t.Fatalf("seed report: %v", err)
	}
	return id
}

func seedSuite(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO suites (report_id, title, file, ordinal)
		VALUES ($1, $2, 'e2e/test.spec.ts', 0)
		RETURNING id
	`, reportID, title).Scan(&id); err != nil {
		t.Fatalf("seed suite: %v", err)
	}
	return id
}

func seedCase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suiteID, testID uuid.UUID, sigID *uuid.UUID, title, status string, retry, ordinal int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO test_cases (suite_id, title, full_title, status, retry_count, ordinal, test_id, signature_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, suiteID, title, "Suite > "+title, status, retry, ordinal, testID, sigID); err != nil {
		t.Fatalf("seed test_case %s: %v", title, err)
	}
}

func seedTestExecutions(t *testing.T, ctx context.Context, pool *pgxpool.Pool, metricDate time.Time, repo, app, stage, lane string, testID uuid.UUID, runs int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO kpi_daily_test_executions (
			metric_date, repository, app, stage, lane, platform, test_id, run_count, pass_count
		) VALUES ($1, $2, $3, $4, $5, '', $6, $7, $7)
		ON CONFLICT (metric_date, repository, stage, lane, platform, test_id)
		DO UPDATE SET run_count = kpi_daily_test_executions.run_count + EXCLUDED.run_count,
		              pass_count = kpi_daily_test_executions.pass_count + EXCLUDED.pass_count
	`, metricDate.UTC().Format("2006-01-02"), repo, app, stage, lane, testID, runs); err != nil {
		t.Fatalf("seed kpi_daily_test_executions: %v", err)
	}
}

func seedVerdict(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID, repo string, testID, sigID uuid.UUID, label, summary, triageURL string, reusedFrom *uuid.UUID, secretVal string, createdAt time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	errCtx := `{"secret_token":"` + secretVal + `"}`
	if err := pool.QueryRow(ctx, `
		INSERT INTO verdicts (
			report_group_id, repository, test_id, signature_id,
			label, decided_by, rule, confidence, summary, model, triage_url,
			error_context, reused_from_id, created_at
		) VALUES ($1, $2, $3, $4, $5, 'llm', 'sig-match', 0.91, $6, 'claude-sonnet', $7, $8::jsonb, $9, $10)
		RETURNING id
	`, groupID, repo, testID, sigID, label, summary, triageURL, errCtx, reusedFrom, createdAt).Scan(&id); err != nil {
		t.Fatalf("seed verdict: %v", err)
	}
	return id
}

func mustRefreshRollups(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) {
	t.Helper()
	if err := kpi.RefreshGroupRollups(ctx, pool, groupID); err != nil {
		t.Fatalf("RefreshGroupRollups(%s): %v", groupID, err)
	}
}
