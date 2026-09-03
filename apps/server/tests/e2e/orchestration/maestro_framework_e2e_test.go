//go:build e2e
// +build e2e

package orchestratione2e

import (
	"net/http"
	"testing"
)

// TestMaestroFrameworkBeginAcceptsMaestro confirms that the orchestration
// begin endpoint accepts framework: "maestro" end to end against a real
// HTTP stack and a real Postgres testcontainer. Mirrors the Detox
// happy-path begin but submits Maestro-shaped dispatch units (spec_path
// values are flow *.yml files, matching mattermost-mobile's convention).
func TestMaestroFrameworkBeginAcceptsMaestro(t *testing.T) {
	env, tok := startEnv(t)

	id := identity(map[string]any{
		"name":      "maestro-ios",
		"framework": "maestro",
	})
	dispatch := []map[string]any{
		{"spec_path": "detox/maestro/flows/timezone/clock_display.yml"},
		{"spec_path": "detox/maestro/flows/channels/channel_bookmark_file.yml"},
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

// TestMaestroAndCypressCoexistByName confirms that a Maestro run and a
// Cypress run can share every component of the composite identity EXCEPT
// name, same guarantee TestCypressAndPlaywrightCoexistByName exercises for
// Cypress/Playwright.
func TestMaestroAndCypressCoexistByName(t *testing.T) {
	env, tok := startEnv(t)

	maestroBody := merge(
		identity(map[string]any{
			"name":      "maestro-coexist",
			"framework": "maestro",
		}),
		map[string]any{
			"total_reports_expected": 1,
			"dispatch_units":         []map[string]any{{"spec_path": "detox/maestro/flows/account/settings.yml"}},
		},
	)
	mresp := postJSON(t, env, tok, "/api/v1/orchestration/begin", maestroBody)
	expectStatus(t, mresp, http.StatusCreated)

	cypressBody := merge(
		identity(map[string]any{
			"name":      "cypress-coexist-maestro",
			"framework": "cypress",
		}),
		map[string]any{
			"total_reports_expected": 1,
			"dispatch_units":         []map[string]any{{"spec_path": "tests/integration/basics_spec.ts"}},
		},
	)
	cresp := postJSON(t, env, tok, "/api/v1/orchestration/begin", cypressBody)
	expectStatus(t, cresp, http.StatusCreated)

	mstatus := getJSON(t, env, tok, statusURL(identity(map[string]any{
		"name": "maestro-coexist",
	})))
	ms := expectStatus(t, mstatus, http.StatusOK)
	if ms["framework"] != "maestro" {
		t.Fatalf("maestro run framework = %v, want maestro", ms["framework"])
	}

	cstatus := getJSON(t, env, tok, statusURL(identity(map[string]any{
		"name": "cypress-coexist-maestro",
	})))
	cs := expectStatus(t, cstatus, http.StatusOK)
	if cs["framework"] != "cypress" {
		t.Fatalf("cypress run framework = %v, want cypress", cs["framework"])
	}
}

// TestMaestroCompleteAcceptsJUnitDerivedTestCases confirms a /complete
// payload carrying JUnit-derived test_cases (the shape dispatch-run's
// maestro.ts adapter populates from `maestro test --format junit` output,
// one test_case per flow since each flow file is exactly one JUnit
// testcase — unlike Detox's many-it()-per-spec-file shape) is accepted
// and stored.
func TestMaestroCompleteAcceptsJUnitDerivedTestCases(t *testing.T) {
	env, tok := startEnv(t)

	id := identity(map[string]any{
		"name":      "maestro-tc-roundtrip",
		"framework": "maestro",
	})
	specPath := "detox/maestro/flows/calls/join_call.yml"
	body := merge(id, map[string]any{
		"total_reports_expected": 1,
		"dispatch_units":         []map[string]any{{"spec_path": specPath}},
	})
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin", body), http.StatusCreated)

	checkout := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "maestro-shard-1",
			"gh_job_id":   "j-maestro-1",
			"batch_size":  1,
		}))
	expectStatus(t, checkout, http.StatusOK)

	// JUnit-derived test case: exactly one, matching Maestro's one-flow-one-
	// testcase convention (see maestro.ts's aggregateMaestroReport).
	junitTestCase := map[string]any{
		"title":         "join_call",
		"full_title":    "detox/maestro/flows/calls/join_call.yml",
		"status":        "failed",
		"retry_count":   0,
		"duration_ms":   42500,
		"error_message": "element not found: Join Call button",
		"error_stack":   "element not found: Join Call button",
		"ordinal":       0,
	}

	completeResults := []map[string]any{
		{
			"spec_path":          specPath,
			"status":             "failed",
			"actual_duration_ms": 42500,
			"test_cases":         []map[string]any{junitTestCase},
		},
	}

	complete := postJSON(t, env, tok, "/api/v1/orchestration/complete", merge(id, map[string]any{
		"gh_job_name": "maestro-shard-1",
		"gh_job_id":   "j-maestro-1",
		"results":     completeResults,
	}))
	resBody := expectStatus(t, complete, http.StatusOK)
	if !resBody["accepted"].(bool) {
		t.Fatalf("complete: accepted=false; body=%v", resBody)
	}

	statusResp := getJSON(t, env, tok, statusURL(id))
	statusBody := expectStatus(t, statusResp, http.StatusOK)
	c := counts(t, statusBody)
	if c["completed_fail"] != 1 {
		t.Fatalf("counts.completed_fail = %d, want 1; body=%v", c["completed_fail"], statusBody)
	}

	units, ok := statusBody["units"].([]any)
	if !ok || len(units) != 1 {
		t.Fatalf("units = %v, want 1 unit", statusBody["units"])
	}
	attempts, ok := units[0].(map[string]any)["attempts"].([]any)
	if !ok || len(attempts) != 1 {
		t.Fatalf("attempts = %v, want 1 attempt", units[0].(map[string]any)["attempts"])
	}
	tcs, ok := attempts[0].(map[string]any)["test_cases"].([]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("test_cases = %v, want 1 entry", attempts[0].(map[string]any)["test_cases"])
	}
	got, ok := tcs[0].(map[string]any)
	if !ok {
		t.Fatalf("test_cases[0] = %v, want object", tcs[0])
	}
	if got["title"] != junitTestCase["title"] {
		t.Fatalf("test_cases[0].title = %v, want %v", got["title"], junitTestCase["title"])
	}
	if got["status"] != junitTestCase["status"] {
		t.Fatalf("test_cases[0].status = %v, want %v", got["status"], junitTestCase["status"])
	}
}

// TestMaestroRetestExercisesFrameworkAgnosticPath confirms the retest
// engine (begin run with retest_on_fail=true; lazy dispatch gated on
// first-pass completion; worker exclusion) operates identically over
// Maestro dispatch units — mirrors TestDetoxRetestExercisesFrameworkAgnosticPath.
func TestMaestroRetestExercisesFrameworkAgnosticPath(t *testing.T) {
	env, tok := startEnv(t)

	id := identity(map[string]any{
		"name":      "maestro-retest",
		"framework": "maestro",
	})
	specs := []string{
		"detox/maestro/flows/account/settings.yml",
		"detox/maestro/flows/channels/channel_bookmark_file.yml",
		"detox/maestro/flows/timezone/clock_display.yml",
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

	checkA1 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "maestro-shard-A",
			"gh_job_id":   "job-maestro-retest-A",
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
			"gh_job_name": "maestro-shard-A",
			"gh_job_id":   "job-maestro-retest-A",
			"results": []map[string]any{
				{"spec_path": failedSpec, "status": "failed"},
			},
		})), http.StatusOK)

	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "maestro-shard-B",
			"gh_job_id":   "job-maestro-retest-B",
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
			"gh_job_name": "maestro-shard-B",
			"gh_job_id":   "job-maestro-retest-B",
			"results":     bResults,
		})), http.StatusOK)

	checkB2 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "maestro-shard-B",
			"gh_job_id":   "job-maestro-retest-B",
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

	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
		merge(id, map[string]any{
			"gh_job_name": "maestro-shard-B",
			"gh_job_id":   "job-maestro-retest-B",
			"results": []map[string]any{
				{"spec_path": failedSpec, "status": "passed"},
			},
		})), http.StatusOK)

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
