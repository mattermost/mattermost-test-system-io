//go:build e2e

// The same run, reported by two frameworks, must produce the same history.
//
// It used not to. A test that failed once and passed on retry stored, under
// Playwright, two rows both stamped 'flaky' (the test's rolled-up status
// applied to every attempt) and rolled up as flaky; under Cypress it stored a
// single final-state row with retry_count hardcoded to 0 and rolled up as a
// CLEAN PASS. Both CI configs retry once, so that divergence applied to every
// retried test in production, and a failure rate computed across both
// frameworks was averaging a per-run number with a per-attempt one.
//
// This is the before/after record for the group rollup: same input, and the
// Cypress half of the answer changes from "passed" to "flaky".
package testhistory

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	parityTitle = "MM-T7311 flakes once then passes"
	// stable_key as stored for each framework. Playwright reports a project
	// and is prefixed by it; Cypress has no project concept and is not.
	parityPlaywrightKey = "chrome :: MM-T7311"
	parityCypressKey    = "MM-T7311"
)

// oneFailOnePassPlaywrightReport is what the Playwright JSON reporter emits
// for a test that failed on attempt 0 and passed on attempt 1: the test is
// labeled "flaky" and each attempt appears as its own result.
func oneFailOnePassPlaywrightReport() string {
	return fmt.Sprintf(`{
  "config": {"projects": [{"name": "chrome"}]},
  "suites": [{
    "title": "channels/parity.spec.ts", "file": "channels/parity.spec.ts",
    "specs": [{"title": %q, "tests": [{"projectName": "chrome", "status": "flaky", "results": [
      {"status": "failed", "duration": 900, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z",
       "errors": [{"message": "Error: expect(locator).toBeVisible() failed", "stack": "at parity.spec.ts:12:3"}]},
      {"status": "passed", "duration": 400, "retry": 1, "startTime": "2026-01-01T00:00:02.000Z"}
    ]}]}]
  }]
}`, parityTitle)
}

// oneFailOnePassCypressReport is the mochawesome equivalent: the test object
// carries the final passing state and "attempts" carries both attempts. The
// parser used to read only the former.
func oneFailOnePassCypressReport() string {
	return fmt.Sprintf(`{
  "stats": {"start": "2026-01-01T00:00:00.000Z"},
  "results": [{
    "title": "root",
    "file": "cypress/e2e/parity.cy.js",
    "fullFile": "cypress/e2e/parity.cy.js",
    "suites": [],
    "tests": [{
      "title": %q, "fullTitle": %q, "duration": 400, "state": "passed", "pass": true,
      "attempts": [
        {"state": "failed", "fail": true, "duration": 900,
         "err": {"message": "AssertionError: expected sidebar to be visible",
                 "estack": "at Context.<anonymous> (parity.cy.js:12:3)"}},
        {"state": "passed", "pass": true, "duration": 400}
      ]
    }]
  }]
}`, parityTitle, parityTitle)
}

// rollupOf reads the stored run-level rollup off the first attempt row.
func storedRollup(t *testing.T, env *testenv.Env, key string) (attempts, failed int, runFailed bool) {
	t.Helper()
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT attempts, attempts_failed, run_failed FROM test_cases
		 WHERE stable_key = $1 ORDER BY ordinal LIMIT 1`, key).
		Scan(&attempts, &failed, &runFailed); err != nil {
		t.Fatalf("rollup for %q: %v", key, err)
	}
	return
}

func storedRowCount(t *testing.T, env *testenv.Env, key string) int {
	t.Helper()
	var n int
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM test_cases WHERE stable_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("row count for %q: %v", key, err)
	}
	return n
}

func historyEntryFor(t *testing.T, env *testenv.Env, key string) map[string]any {
	t.Helper()
	q := url.Values{}
	q.Set("repo", "mattermost")
	q.Set("test_id", key)
	q.Set("branch", "master")
	got := getJSON(t, env, "/api/v1/tests/history?"+q.Encode())
	entries := got["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries for %q = %d, want 1", key, len(entries))
	}
	return entries[0].(map[string]any)
}

// TestRetryParity_PlaywrightAndCypressStoreTheSameRun is the acceptance case.
func TestRetryParity_PlaywrightAndCypressStoreTheSameRun(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	ingestReport(t, env, tok, "playwright", "playwright-parity",
		sha("p7", 0), "job-pw", oneFailOnePassPlaywrightReport())
	ingestReport(t, env, tok, "cypress", "cypress-parity",
		sha("c7", 0), "job-cy", oneFailOnePassCypressReport())

	// Both frameworks store one row per attempt.
	if n := storedRowCount(t, env, parityPlaywrightKey); n != 2 {
		t.Errorf("playwright rows = %d, want 2 (one per attempt)", n)
	}
	if n := storedRowCount(t, env, parityCypressKey); n != 2 {
		t.Errorf("cypress rows = %d, want 2 (one per attempt) — a single final-state "+
			"row is the shape that made a retry-survivor read as a clean pass", n)
	}

	// And the identical run-level rollup.
	pwA, pwF, pwRun := storedRollup(t, env, parityPlaywrightKey)
	cyA, cyF, cyRun := storedRollup(t, env, parityCypressKey)
	if pwA != 2 || pwF != 1 || pwRun {
		t.Errorf("playwright rollup = (%d, %d, %v), want (2, 1, false)", pwA, pwF, pwRun)
	}
	if cyA != 2 || cyF != 1 || cyRun {
		t.Errorf("cypress rollup = (%d, %d, %v), want (2, 1, false)", cyA, cyF, cyRun)
	}
	if pwA != cyA || pwF != cyF || pwRun != cyRun {
		t.Fatalf("frameworks disagree: playwright (%d, %d, %v) vs cypress (%d, %d, %v) — "+
			"no rate can be computed across both while this differs",
			pwA, pwF, pwRun, cyA, cyF, cyRun)
	}
}

// TestRetryParity_BothRollUpAsFlakyNotPassed is the before/after difference in
// the group rollup itself. The Playwright half is unchanged behavior, kept
// here as the control; the Cypress half is what changed — it used to be
// "passed".
func TestRetryParity_BothRollUpAsFlakyNotPassed(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	ingestReport(t, env, tok, "playwright", "playwright-parity",
		sha("p8", 0), "job-pw", oneFailOnePassPlaywrightReport())
	ingestReport(t, env, tok, "cypress", "cypress-parity",
		sha("c8", 0), "job-cy", oneFailOnePassCypressReport())

	pw := historyEntryFor(t, env, parityPlaywrightKey)
	cy := historyEntryFor(t, env, parityCypressKey)

	if pw["outcome"] != "flaky" {
		t.Errorf("playwright outcome = %v, want flaky", pw["outcome"])
	}
	if cy["outcome"] != "flaky" {
		t.Errorf("cypress outcome = %v, want flaky — BEFORE this change it was "+
			"\"passed\", so the same run got opposite verdicts by framework", cy["outcome"])
	}

	// The rollup fields the API now reports, and the boolean a consumer
	// should read rather than inferring failure from the counts.
	for name, e := range map[string]map[string]any{"playwright": pw, "cypress": cy} {
		if e["attempts"].(float64) != 2 {
			t.Errorf("%s attempts = %v, want 2", name, e["attempts"])
		}
		if e["attempts_failed"].(float64) != 1 {
			t.Errorf("%s attempts_failed = %v, want 1", name, e["attempts_failed"])
		}
		if e["run_failed"] != false {
			t.Errorf("%s run_failed = %v, want false — one attempt survived", name, e["run_failed"])
		}
	}
}

// TestRetryParity_ARunWhereEveryAttemptFailedIsAFailure is the other side of
// run_failed: no attempt survived, so the run failed outright and the outcome
// is "failed", not "flaky".
func TestRetryParity_ARunWhereEveryAttemptFailedIsAFailure(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	bothFailed := fmt.Sprintf(`{
  "config": {"projects": [{"name": "chrome"}]},
  "suites": [{
    "title": "channels/parity.spec.ts", "file": "channels/parity.spec.ts",
    "specs": [{"title": %q, "tests": [{"projectName": "chrome", "status": "unexpected", "results": [
      {"status": "failed", "duration": 900, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z",
       "errors": [{"message": "Error: still broken", "stack": "at parity.spec.ts:12:3"}]},
      {"status": "failed", "duration": 900, "retry": 1, "startTime": "2026-01-01T00:00:02.000Z",
       "errors": [{"message": "Error: still broken", "stack": "at parity.spec.ts:12:3"}]}
    ]}]}]
  }]
}`, parityTitle)

	ingestReport(t, env, tok, "playwright", "playwright-parity",
		sha("p9", 0), "job-pw", bothFailed)

	a, f, runFailed := storedRollup(t, env, parityPlaywrightKey)
	if a != 2 || f != 2 || !runFailed {
		t.Fatalf("rollup = (%d, %d, %v), want (2, 2, true)", a, f, runFailed)
	}
	e := historyEntryFor(t, env, parityPlaywrightKey)
	if e["outcome"] != "failed" {
		t.Errorf("outcome = %v, want failed — no attempt survived", e["outcome"])
	}
	if e["run_failed"] != true {
		t.Errorf("run_failed = %v, want true", e["run_failed"])
	}
}
