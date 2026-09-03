//go:build e2e
// +build e2e

package orchestratione2e

import (
	"net/http"
	"testing"
)

// TestCypressFrameworkBeginAcceptsCypress confirms that the orchestration
// begin endpoint accepts framework: "cypress" end to end against a real
// HTTP stack and a real Postgres testcontainer. Mirrors the Playwright
// happy-path begin but submits a Cypress-shaped composite identity.
func TestCypressFrameworkBeginAcceptsCypress(t *testing.T) {
	env, tok := startEnv(t)

	id := identity(map[string]any{
		"name":      "cypress-default",
		"framework": "cypress",
	})
	dispatch := []map[string]any{
		{"spec_path": "tests/integration/auth_spec.ts"},
		{"spec_path": "tests/integration/multi_test_spec.ts"},
	}
	body := merge(id, map[string]any{
		"total_reports_expected": 1,
		"dispatch_units":         dispatch,
	})

	resp := postJSON(t, env, tok, "/api/v1/orchestration/begin", body)
	begin := expectStatus(t, resp, http.StatusCreated)
	if begin["status"] != "in_progress" {
		t.Fatalf("status = %v, want in_progress", begin["status"])
	}
	if jsonNumberInt(t, begin["total_units"]) != len(dispatch) {
		t.Fatalf("total_units = %v, want %d", begin["total_units"], len(dispatch))
	}
}

// TestCypressAndPlaywrightCoexistByName confirms that a Cypress run and a
// Playwright run can share every component of the composite identity
// EXCEPT name. The orchestration_runs uniqueness key
// (repository, commit_sha, gh_run_id, name, gh_run_attempt) excludes
// framework, so consumers running both runners under the same CI run
// disambiguate via name (e.g. "cypress-full" vs "playwright-full").
func TestCypressAndPlaywrightCoexistByName(t *testing.T) {
	env, tok := startEnv(t)

	cypressBody := merge(
		identity(map[string]any{
			"name":      "cypress-coexist",
			"framework": "cypress",
		}),
		map[string]any{
			"total_reports_expected": 1,
			"dispatch_units":         []map[string]any{{"spec_path": "tests/integration/basics_spec.ts"}},
		},
	)
	cresp := postJSON(t, env, tok, "/api/v1/orchestration/begin", cypressBody)
	expectStatus(t, cresp, http.StatusCreated)

	playwrightBody := merge(
		identity(map[string]any{
			"name":      "playwright-coexist",
			"framework": "playwright",
		}),
		map[string]any{
			"playwright_project":     defaultPlaywrightPrj,
			"total_reports_expected": 1,
			"dispatch_units":         []map[string]any{{"spec_path": "tests/login.spec.ts"}},
		},
	)
	presp := postJSON(t, env, tok, "/api/v1/orchestration/begin", playwrightBody)
	expectStatus(t, presp, http.StatusCreated)

	// Status lookups by composite identity hit each run independently.
	cstatus := getJSON(t, env, tok, statusURL(identity(map[string]any{
		"name": "cypress-coexist",
	})))
	cs := expectStatus(t, cstatus, http.StatusOK)
	if cs["framework"] != "cypress" {
		t.Fatalf("cypress run framework = %v, want cypress", cs["framework"])
	}

	pstatus := getJSON(t, env, tok, statusURL(identity(map[string]any{
		"name": "playwright-coexist",
	})))
	ps := expectStatus(t, pstatus, http.StatusOK)
	if ps["framework"] != "playwright" {
		t.Fatalf("playwright run framework = %v, want playwright", ps["framework"])
	}
}

// TestCypressCompleteAcceptsMochawesomeTestCases confirms a /complete
// payload carrying a Mochawesome-derived test_cases array is accepted
// and the per-attempt JSONB stores the values in the same shape the
// dispatcher will populate from the Cypress reporter output. The
// per-test-case fields exercised here mirror the mapping described in
// the data-model entry for this feature: status, retry_count,
// duration_ms, error_message, error_stack, ordinal.
func TestCypressCompleteAcceptsMochawesomeTestCases(t *testing.T) {
	env, tok := startEnv(t)

	id := identity(map[string]any{
		"name":      "cypress-tc-roundtrip",
		"framework": "cypress",
	})
	specPath := "tests/integration/multi_test_spec.ts"
	body := merge(id, map[string]any{
		"total_reports_expected": 1,
		"dispatch_units":         []map[string]any{{"spec_path": specPath}},
	})
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin", body), http.StatusCreated)

	checkout := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "cypress-shard-1",
			"gh_job_id":   "j-cyp-1",
			"batch_size":  1,
		}))
	expectStatus(t, checkout, http.StatusOK)

	// Mochawesome-shape test cases as produced by the dispatcher's
	// parser. Covers: a passing case, a failing case with err detail,
	// a flaky-passed case (retry_count > 0), and a skipped case.
	mochawesomeTestCases := []map[string]any{
		{
			"title":         "addition",
			"full_title":    "multi-test addition",
			"status":        "passed",
			"retry_count":   0,
			"duration_ms":   42,
			"error_message": nil,
			"error_stack":   nil,
			"ordinal":       0,
		},
		{
			"title":         "subtraction",
			"full_title":    "multi-test subtraction",
			"status":        "failed",
			"retry_count":   0,
			"duration_ms":   58,
			"error_message": "expected 5 - 3 to equal 1",
			"error_stack":   "AssertionError: expected 5 - 3 to equal 1\n    at Context.<anonymous> (multi_test_spec.ts:6:15)",
			"ordinal":       1,
		},
		{
			"title":         "multiplication",
			"full_title":    "multi-test multiplication",
			"status":        "flaky",
			"retry_count":   1,
			"duration_ms":   71,
			"error_message": nil,
			"error_stack":   nil,
			"ordinal":       2,
		},
		{
			"title":         "division",
			"full_title":    "multi-test division",
			"status":        "skipped",
			"retry_count":   0,
			"duration_ms":   0,
			"error_message": nil,
			"error_stack":   nil,
			"ordinal":       3,
		},
	}

	completeResults := []map[string]any{
		{
			"spec_path":          specPath,
			"status":             "failed", // aggregate: any failed → failed
			"actual_duration_ms": 171,
			"test_cases":         mochawesomeTestCases,
		},
	}

	complete := postJSON(t, env, tok, "/api/v1/orchestration/complete", merge(id, map[string]any{
		"gh_job_name": "cypress-shard-1",
		"gh_job_id":   "j-cyp-1",
		"results":     completeResults,
	}))
	resBody := expectStatus(t, complete, http.StatusOK)
	if !resBody["accepted"].(bool) {
		t.Fatalf("complete: accepted=false; body=%v", resBody)
	}

	// Status lookup confirms the run advanced to a terminal state and
	// the per-attempt rollup sees the completed_fail outcome.
	statusResp := getJSON(t, env, tok, statusURL(id))
	statusBody := expectStatus(t, statusResp, http.StatusOK)
	c := counts(t, statusBody)
	if c["completed_fail"] != 1 {
		t.Fatalf("counts.completed_fail = %d, want 1; body=%v", c["completed_fail"], statusBody)
	}
}

// TestCypressRetestExercisesFrameworkAgnosticPath confirms that the
// existing retest engine (begin run with retest_on_fail=true; lazy
// dispatch gated on first-pass completion; worker exclusion) operates
// identically over Cypress dispatch units. No new code path; the test
// exists to surface a regression if the orchestration retest engine
// ever grows a framework-specific branch.
func TestCypressRetestExercisesFrameworkAgnosticPath(t *testing.T) {
	env, tok := startEnv(t)

	id := identity(map[string]any{
		"name":      "cypress-retest",
		"framework": "cypress",
	})
	specs := []string{
		"tests/integration/auth_spec.ts",
		"tests/integration/basics_spec.ts",
		"tests/integration/multi_test_spec.ts",
	}
	dispatch := make([]map[string]any, 0, len(specs))
	for _, sp := range specs {
		dispatch = append(dispatch, map[string]any{"spec_path": sp})
	}
	body := merge(id, map[string]any{
		"retest_on_fail":         true,
		"retest_budget":          1,
		"lease_timeout_ms":       60_000,
		"idle_timeout_ms":        60_000,
		"total_reports_expected": 1,
		"dispatch_units":         dispatch,
	})
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin", body), http.StatusCreated)

	// Worker A picks up unit 0 first and fails it. Worker B passes
	// units 1 and 2. Then either worker re-leases the failed unit and
	// passes it on the second attempt — completed_pass via retest.
	checkA1 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "cypress-shard-A",
			"gh_job_id":   "job-cyp-retest-A",
			"batch_size":  1,
		}))
	bodyA1 := expectStatus(t, checkA1, http.StatusOK)
	unitsA1 := bodyA1["units"].([]any)
	if len(unitsA1) != 1 {
		t.Fatalf("worker A: want 1 unit, got %d", len(unitsA1))
	}
	failedSpec := unitsA1[0].(map[string]any)["spec_path"].(string)

	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
		merge(id, map[string]any{
			"gh_job_name": "cypress-shard-A",
			"gh_job_id":   "job-cyp-retest-A",
			"results": []map[string]any{
				{"spec_path": failedSpec, "status": "failed"},
			},
		})), http.StatusOK)

	// Worker B drains the rest of the pending pool with a passing batch.
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "cypress-shard-B",
			"gh_job_id":   "job-cyp-retest-B",
			"batch_size":  10,
		}))
	bodyB := expectStatus(t, checkB, http.StatusOK)
	unitsB := bodyB["units"].([]any)
	if len(unitsB) == 0 {
		t.Fatalf("worker B: expected pending units, got 0")
	}
	bResults := make([]map[string]any, 0, len(unitsB))
	for _, u := range unitsB {
		um := u.(map[string]any)
		bResults = append(bResults, map[string]any{
			"spec_path": um["spec_path"].(string),
			"status":    "passed",
		})
	}
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
		merge(id, map[string]any{
			"gh_job_name": "cypress-shard-B",
			"gh_job_id":   "job-cyp-retest-B",
			"results":     bResults,
		})), http.StatusOK)

	// First-pass complete. Worker B (a worker that didn't fail it) now
	// asks for retest work and should be handed the failed unit.
	checkB2 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "cypress-shard-B",
			"gh_job_id":   "job-cyp-retest-B",
			"batch_size":  1,
		}))
	bodyB2 := expectStatus(t, checkB2, http.StatusOK)
	unitsB2 := bodyB2["units"].([]any)
	if len(unitsB2) != 1 {
		t.Fatalf("worker B retest: want 1 unit, got %d", len(unitsB2))
	}
	if !bodyB2["is_retest"].(bool) {
		t.Fatalf("worker B retest: is_retest=false; expected retest dispatch")
	}
	if got := unitsB2[0].(map[string]any)["spec_path"].(string); got != failedSpec {
		t.Fatalf("retest spec_path = %q, want %q", got, failedSpec)
	}

	// Pass on retest → unit transitions to completed_pass.
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
		merge(id, map[string]any{
			"gh_job_name": "cypress-shard-B",
			"gh_job_id":   "job-cyp-retest-B",
			"results": []map[string]any{
				{"spec_path": failedSpec, "status": "passed"},
			},
		})), http.StatusOK)

	// Final status confirms 0 fails, 3 passes, run completed.
	statusResp := getJSON(t, env, tok, statusURL(id))
	statusBody := expectStatus(t, statusResp, http.StatusOK)
	if statusBody["status"] != "completed" {
		t.Fatalf("run status = %v, want completed", statusBody["status"])
	}
	c := counts(t, statusBody)
	if c["completed_pass"] != 3 || c["completed_fail"] != 0 {
		t.Fatalf("counts = %+v, want pass=3 fail=0", c)
	}
}

// TestUnknownFrameworkRejected confirms the begin handler rejects an
// unsupported framework value. The OpenAPI middleware enforces the enum
// at the wire level; the orchestration handler's IsSupportedFramework
// check provides defense in depth for direct callers.
func TestUnknownFrameworkRejected(t *testing.T) {
	env, tok := startEnv(t)

	body := merge(
		identity(map[string]any{
			"name":      "webdriverio-rejected",
			"framework": "webdriverio",
		}),
		map[string]any{
			"total_reports_expected": 1,
			"dispatch_units":         []map[string]any{{"spec_path": "tests/foo.wdio.ts"}},
		},
	)
	resp := postJSON(t, env, tok, "/api/v1/orchestration/begin", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, readBodyString(resp))
	}
}
