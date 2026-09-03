//go:build e2e
// +build e2e

package orchestratione2e

import (
	"net/http"
	"testing"
)

// TestDetoxFrameworkBeginAcceptsDetox confirms that the orchestration begin
// endpoint accepts framework: "detox" end to end against a real HTTP stack
// and a real Postgres testcontainer. Mirrors the Cypress/Playwright
// happy-path begin but submits a Detox-shaped composite identity
// (spec_path values are *.e2e.ts, matching mattermost-mobile's convention).
func TestDetoxFrameworkBeginAcceptsDetox(t *testing.T) {
	env, tok := startEnv(t)

	id := identity(map[string]any{
		"name":      "detox-ios",
		"framework": "detox",
	})
	dispatch := []map[string]any{
		{"spec_path": "e2e/test/products/channels/messaging/message_post.e2e.ts"},
		{"spec_path": "e2e/test/products/channels/channels/browse_channels.e2e.ts"},
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

// TestDetoxAndCypressCoexistByName confirms that a Detox run and a Cypress
// run can share every component of the composite identity EXCEPT name,
// same guarantee TestCypressAndPlaywrightCoexistByName exercises for
// Cypress/Playwright.
func TestDetoxAndCypressCoexistByName(t *testing.T) {
	env, tok := startEnv(t)

	detoxBody := merge(
		identity(map[string]any{
			"name":      "detox-coexist",
			"framework": "detox",
		}),
		map[string]any{
			"total_reports_expected": 1,
			"dispatch_units":         []map[string]any{{"spec_path": "e2e/test/products/channels/account/settings.e2e.ts"}},
		},
	)
	dresp := postJSON(t, env, tok, "/api/v1/orchestration/begin", detoxBody)
	expectStatus(t, dresp, http.StatusCreated)

	cypressBody := merge(
		identity(map[string]any{
			"name":      "cypress-coexist-detox",
			"framework": "cypress",
		}),
		map[string]any{
			"total_reports_expected": 1,
			"dispatch_units":         []map[string]any{{"spec_path": "tests/integration/basics_spec.ts"}},
		},
	)
	cresp := postJSON(t, env, tok, "/api/v1/orchestration/begin", cypressBody)
	expectStatus(t, cresp, http.StatusCreated)

	dstatus := getJSON(t, env, tok, statusURL(identity(map[string]any{
		"name": "detox-coexist",
	})))
	ds := expectStatus(t, dstatus, http.StatusOK)
	if ds["framework"] != "detox" {
		t.Fatalf("detox run framework = %v, want detox", ds["framework"])
	}

	cstatus := getJSON(t, env, tok, statusURL(identity(map[string]any{
		"name": "cypress-coexist-detox",
	})))
	cs := expectStatus(t, cstatus, http.StatusOK)
	if cs["framework"] != "cypress" {
		t.Fatalf("cypress run framework = %v, want cypress", cs["framework"])
	}
}

// TestDetoxCompleteAcceptsJestDerivedTestCases confirms a /complete payload
// carrying Jest/Detox-derived test_cases (the shape dispatch-run's detox.ts
// adapter will populate from `--json --outputFile` output, bucketed by
// ancestorTitles the same way ingest/detox.go's extractDetox does for the
// no-queue report-upload path) is accepted and stored.
func TestDetoxCompleteAcceptsJestDerivedTestCases(t *testing.T) {
	env, tok := startEnv(t)

	id := identity(map[string]any{
		"name":      "detox-tc-roundtrip",
		"framework": "detox",
	})
	specPath := "e2e/test/products/channels/messaging/message_reply.e2e.ts"
	body := merge(id, map[string]any{
		"total_reports_expected": 1,
		"dispatch_units":         []map[string]any{{"spec_path": specPath}},
	})
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin", body), http.StatusCreated)

	checkout := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "detox-shard-1",
			"gh_job_id":   "j-detox-1",
			"batch_size":  1,
		}))
	expectStatus(t, checkout, http.StatusOK)

	// Jest-derived test cases: a passing case, a failing case with a
	// failureMessages-joined error, and a skipped (todo/pending) case.
	jestTestCases := []map[string]any{
		{
			"title":         "replies to a message",
			"full_title":    "Message Reply > replies to a message",
			"status":        "passed",
			"retry_count":   0,
			"duration_ms":   4210,
			"error_message": nil,
			"error_stack":   nil,
			"ordinal":       0,
		},
		{
			"title":         "shows the reply count",
			"full_title":    "Message Reply > shows the reply count",
			"status":        "failed",
			"retry_count":   0,
			"duration_ms":   3980,
			"error_message": "expected element to be visible",
			"error_stack":   "Error: expected element to be visible\n    at message_reply.e2e.ts:42:11",
			"ordinal":       1,
		},
		{
			"title":         "reply thread deep link",
			"full_title":    "Message Reply > reply thread deep link",
			"status":        "skipped",
			"retry_count":   0,
			"duration_ms":   0,
			"error_message": nil,
			"error_stack":   nil,
			"ordinal":       2,
		},
	}

	completeResults := []map[string]any{
		{
			"spec_path":          specPath,
			"status":             "failed", // aggregate: any failed → failed
			"actual_duration_ms": 8190,
			"test_cases":         jestTestCases,
		},
	}

	complete := postJSON(t, env, tok, "/api/v1/orchestration/complete", merge(id, map[string]any{
		"gh_job_name": "detox-shard-1",
		"gh_job_id":   "j-detox-1",
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
	if !ok || len(tcs) != len(jestTestCases) {
		t.Fatalf("test_cases = %v, want %d entries", attempts[0].(map[string]any)["test_cases"], len(jestTestCases))
	}
	for i, want := range jestTestCases {
		got, ok := tcs[i].(map[string]any)
		if !ok {
			t.Fatalf("test_cases[%d] = %v, want object", i, tcs[i])
		}
		if got["title"] != want["title"] {
			t.Fatalf("test_cases[%d].title = %v, want %v", i, got["title"], want["title"])
		}
		if got["status"] != want["status"] {
			t.Fatalf("test_cases[%d].status = %v, want %v", i, got["status"], want["status"])
		}
	}
}

// TestDetoxRetestExercisesFrameworkAgnosticPath confirms the retest engine
// (begin run with retest_on_fail=true; lazy dispatch gated on first-pass
// completion; worker exclusion) operates identically over Detox dispatch
// units — mirrors TestCypressRetestExercisesFrameworkAgnosticPath.
func TestDetoxRetestExercisesFrameworkAgnosticPath(t *testing.T) {
	env, tok := startEnv(t)

	id := identity(map[string]any{
		"name":      "detox-retest",
		"framework": "detox",
	})
	specs := []string{
		"e2e/test/products/channels/account/settings.e2e.ts",
		"e2e/test/products/channels/channels/browse_channels.e2e.ts",
		"e2e/test/products/channels/messaging/message_post.e2e.ts",
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
			"gh_job_name": "detox-shard-A",
			"gh_job_id":   "job-detox-retest-A",
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
			"gh_job_name": "detox-shard-A",
			"gh_job_id":   "job-detox-retest-A",
			"results": []map[string]any{
				{"spec_path": failedSpec, "status": "failed"},
			},
		})), http.StatusOK)

	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "detox-shard-B",
			"gh_job_id":   "job-detox-retest-B",
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
			"gh_job_name": "detox-shard-B",
			"gh_job_id":   "job-detox-retest-B",
			"results":     bResults,
		})), http.StatusOK)

	checkB2 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		merge(id, map[string]any{
			"gh_job_name": "detox-shard-B",
			"gh_job_id":   "job-detox-retest-B",
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
			"gh_job_name": "detox-shard-B",
			"gh_job_id":   "job-detox-retest-B",
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
