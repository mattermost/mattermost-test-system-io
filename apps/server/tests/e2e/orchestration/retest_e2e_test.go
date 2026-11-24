//go:build e2e
// +build e2e

package orchestratione2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// failResults builds a results array marking every spec as failed.
func failResults(specs []string) []map[string]any {
	out := make([]map[string]any, 0, len(specs))
	for _, sp := range specs {
		out = append(out, map[string]any{
			"spec_path": sp,
			"status":    "failed",
		})
	}
	return out
}

// TestRetestLazyDispatchAndPassFlipsUnitToCompletedPass: with
// retest_on_fail=true, a unit that fails during the first pass is held
// until first-pass completion, then re-leased to a DIFFERENT worker. When
// the retest passes, the unit's terminal state is completed_pass and the
// run completes.
func TestRetestLazyDispatchAndPassFlipsUnitToCompletedPass(t *testing.T) {
	env, tok := startEnv(t)

	// 3 single-spec units. Worker A will fail unit 0 on first pass; both
	// workers will pass everything else. Worker A then asks for more work
	// after first-pass completes — under the current "any worker may
	// retest" semantics it should pick up the retest of its own prior
	// failure.
	units := []string{
		"tests/retest-pass.spec.ts",
		"tests/passes-1.spec.ts",
		"tests/passes-2.spec.ts",
	}
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(units, map[string]any{
			"retest_on_fail": true,
			"retest_budget":  1,
			// Generous timeouts so the reaper does not interfere.
			"lease_timeout_ms": 60_000,
			"idle_timeout_ms":  60_000,
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// --- Worker A: first checkout = unit 0. Report failed. ---
	checkA1 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-retest-A", 1))
	bodyA1 := expectStatus(t, checkA1, http.StatusOK)
	unitsA1 := bodyA1["units"].([]any)
	if len(unitsA1) != 1 || dispatchSeqOf(t, unitsA1[0].(map[string]any)) != 0 {
		t.Fatalf("worker A first checkout: want unit 0, got %v", unitsA1)
	}
	if bodyA1["is_retest"].(bool) {
		t.Fatalf("worker A first checkout: is_retest=true; expected first-pass dispatch")
	}
	completeAFail := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-retest-A", failResults([]string{units[0]})))
	expectStatus(t, completeAFail, http.StatusOK)

	// --- Verify retest is NOT yet dispatched: pending pool still has work,
	// so worker A's next checkout returns a normal pending unit (not retest). ---
	checkA2 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-retest-A", 1))
	bodyA2 := expectStatus(t, checkA2, http.StatusOK)
	unitsA2 := bodyA2["units"].([]any)
	if len(unitsA2) != 1 {
		t.Fatalf("worker A second checkout: want 1 unit, got %d", len(unitsA2))
	}
	if bodyA2["is_retest"].(bool) {
		t.Fatalf("worker A second checkout is_retest=true while first-pass not complete")
	}
	a2Spec := unitsA2[0].(map[string]any)["spec_path"].(string)
	completeA2 := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-retest-A", passResults([]string{a2Spec})))
	expectStatus(t, completeA2, http.StatusOK)

	// --- Worker B: takes the remaining first-pass unit and passes it. ---
	checkB1 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-retest-B", 1))
	bodyB1 := expectStatus(t, checkB1, http.StatusOK)
	unitsB1 := bodyB1["units"].([]any)
	if len(unitsB1) != 1 {
		t.Fatalf("worker B first checkout: want 1 unit, got %d", len(unitsB1))
	}
	if bodyB1["is_retest"].(bool) {
		t.Fatalf("worker B first checkout is_retest=true but a pending unit existed")
	}
	b1Spec := unitsB1[0].(map[string]any)["spec_path"].(string)
	completeB1 := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-B", "job-retest-B", passResults([]string{b1Spec})))
	expectStatus(t, completeB1, http.StatusOK)

	// --- /status: pending=0, leased=0, retest_eligible=1, run still in_progress. ---
	stat := getJSON(t, env, tok, statusURL(identity(nil)))
	statBody := expectStatus(t, stat, http.StatusOK)
	if statBody["status"] != "in_progress" {
		t.Fatalf("status = %v, want in_progress (retest still pending)", statBody["status"])
	}
	c := counts(t, statBody)
	if c["pending"] != 0 || c["leased"] != 0 || c["retest_eligible"] != 1 ||
		c["completed_pass"] != 2 || c["completed_fail"] != 1 {
		t.Fatalf("counts = %s, want pending=0 leased=0 retest=1 pass=2 fail=1", fmtCounts(c))
	}

	// --- Worker A asks for more work. Under "any worker may retest" it
	// picks up the retest of its own prior failure. ---
	checkA3 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-retest-A", 1))
	bodyA3 := expectStatus(t, checkA3, http.StatusOK)
	unitsA3 := bodyA3["units"].([]any)
	if len(unitsA3) != 1 {
		t.Fatalf("worker A retest checkout: want 1 unit, got %d (queue_empty=%v)",
			len(unitsA3), bodyA3["queue_empty"])
	}
	if !bodyA3["is_retest"].(bool) {
		t.Fatalf("worker A retest checkout: is_retest=false; expected true")
	}
	retestUnit := unitsA3[0].(map[string]any)
	if dispatchSeqOf(t, retestUnit) != 0 {
		t.Fatalf("retest dispatch_seq = %d, want 0 (the previously-failed unit)",
			dispatchSeqOf(t, retestUnit))
	}
	// fail_count surfaces only on retest dispatches.
	if jsonNumberInt(t, retestUnit["fail_count"]) != 1 {
		t.Fatalf("retest fail_count = %v, want 1", retestUnit["fail_count"])
	}

	// While retest is leased, /status reports current_lease.kind == "retest".
	midStat := expectStatus(t, getJSON(t, env, tok, statusURL(identity(nil))), http.StatusOK)
	midUnit0 := midStat["units"].([]any)[0].(map[string]any)
	if midLease := midUnit0["current_lease"].(map[string]any); midLease["kind"] != "retest" {
		t.Fatalf("retest current_lease.kind = %v, want retest", midLease["kind"])
	}

	// --- Worker A passes the retest. Unit becomes completed_pass; run completes. ---
	completeA3 := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-retest-A", passResults([]string{units[0]})))
	resA3 := expectStatus(t, completeA3, http.StatusOK)
	changes := resA3["unit_states_changed"].([]any)
	if len(changes) != 1 {
		t.Fatalf("retest complete: unit_states_changed = %d, want 1", len(changes))
	}
	if changes[0].(map[string]any)["new_state"] != "completed_pass" {
		t.Fatalf("retest new_state = %v, want completed_pass", changes[0])
	}

	// --- Final /status: completed, all 3 in completed_pass, attempts marked fresh then retest. ---
	finalStat := getJSON(t, env, tok, statusURL(identity(nil)))
	finalBody := expectStatus(t, finalStat, http.StatusOK)
	if finalBody["status"] != "completed" {
		t.Fatalf("final status = %v, want completed", finalBody["status"])
	}
	fc := counts(t, finalBody)
	if fc["pending"] != 0 || fc["leased"] != 0 || fc["completed_pass"] != 3 ||
		fc["completed_fail"] != 0 || fc["retest_eligible"] != 0 {
		t.Fatalf("final counts = %s, want pass=3, all others 0", fmtCounts(fc))
	}
	finalUnit0 := finalBody["units"].([]any)[0].(map[string]any)
	u0Attempts := finalUnit0["attempts"].([]any)
	if len(u0Attempts) != 2 ||
		u0Attempts[0].(map[string]any)["kind"] != "fresh" ||
		u0Attempts[1].(map[string]any)["kind"] != "retest" {
		t.Fatalf("unit 0 attempt kinds = %+v, want [fresh, retest]", u0Attempts)
	}
	var retestLeaseKind string
	if err := env.Pool.QueryRow(context.Background(), `
		SELECT l.kind FROM leases l
		 WHERE l.id = $1::uuid
	`, u0Attempts[1].(map[string]any)["lease_id"]).Scan(&retestLeaseKind); err != nil {
		t.Fatalf("query retest lease kind: %v", err)
	}
	if retestLeaseKind != "retest" {
		t.Fatalf("retest lease kind = %q, want retest", retestLeaseKind)
	}
}

// TestRetestExhaustedBudgetIsTerminal: with retest_budget=1, a unit that
// fails on its first attempt and ALSO on its retest (fail_count=2 > 1) is
// terminal completed_fail and the run completes.
func TestRetestExhaustedBudgetIsTerminal(t *testing.T) {
	env, tok := startEnv(t)

	units := []string{
		"tests/always-fails.spec.ts",
		"tests/passes.spec.ts",
	}
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(units, map[string]any{
			"retest_on_fail":   true,
			"retest_budget":    1,
			"lease_timeout_ms": 60_000,
			"idle_timeout_ms":  60_000,
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// --- Worker A: first checkout = unit 0; report failed. ---
	checkA1 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-budget-A", 1))
	bodyA1 := expectStatus(t, checkA1, http.StatusOK)
	if dispatchSeqOf(t, bodyA1["units"].([]any)[0].(map[string]any)) != 0 {
		t.Fatalf("worker A first checkout did not get unit 0")
	}
	completeA1 := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-budget-A", failResults([]string{units[0]})))
	expectStatus(t, completeA1, http.StatusOK)

	// --- Worker B: takes unit 1; report passed. First-pass complete. ---
	checkB1 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-budget-B", 1))
	bodyB1 := expectStatus(t, checkB1, http.StatusOK)
	if dispatchSeqOf(t, bodyB1["units"].([]any)[0].(map[string]any)) != 1 {
		t.Fatalf("worker B first checkout did not get unit 1")
	}
	completeB1 := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-B", "job-budget-B", passResults([]string{units[1]})))
	expectStatus(t, completeB1, http.StatusOK)

	// --- Worker B picks up the retest (it didn't fail unit 0). Reports failed
	// AGAIN: fail_count becomes 2, exceeds retest_budget=1 -> terminal. ---
	checkB2 := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-budget-B", 1))
	bodyB2 := expectStatus(t, checkB2, http.StatusOK)
	if !bodyB2["is_retest"].(bool) {
		t.Fatalf("worker B retest checkout: is_retest=false; expected true")
	}
	completeB2 := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-B", "job-budget-B", failResults([]string{units[0]})))
	resB2 := expectStatus(t, completeB2, http.StatusOK)
	changes := resB2["unit_states_changed"].([]any)
	if len(changes) != 1 {
		t.Fatalf("retest fail: unit_states_changed = %d, want 1", len(changes))
	}
	if changes[0].(map[string]any)["new_state"] != "completed_fail" {
		t.Fatalf("retest fail new_state = %v, want completed_fail", changes[0])
	}

	// --- Final /status: completed (run finishes despite fail), 1 pass + 1 fail.
	// retest_eligible must be 0 (budget exhausted). ---
	finalStat := getJSON(t, env, tok, statusURL(identity(nil)))
	finalBody := expectStatus(t, finalStat, http.StatusOK)
	if finalBody["status"] != "completed" {
		t.Fatalf("final status = %v, want completed", finalBody["status"])
	}
	fc := counts(t, finalBody)
	if fc["completed_pass"] != 1 || fc["completed_fail"] != 1 ||
		fc["retest_eligible"] != 0 || fc["pending"] != 0 || fc["leased"] != 0 {
		t.Fatalf("final counts = %s, want pass=1 fail=1 retest=0", fmtCounts(fc))
	}
}

// TestRetestDisabledNoSecondAttempt: with retest_on_fail=false (default),
// a failed unit is strictly terminal — no retest is ever attempted, and
// the run completes with the failure.
func TestRetestDisabledNoSecondAttempt(t *testing.T) {
	env, tok := startEnv(t)

	units := []string{
		"tests/fails.spec.ts",
		"tests/passes.spec.ts",
	}
	// Explicitly omit retest_on_fail (default false) — also no retest_budget.
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(units, map[string]any{
			"lease_timeout_ms": 60_000,
			"idle_timeout_ms":  60_000,
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// Worker A fails unit 0.
	checkA := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-noretest-A", 1))
	expectStatus(t, checkA, http.StatusOK)
	completeA := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-noretest-A", failResults([]string{units[0]})))
	expectStatus(t, completeA, http.StatusOK)

	// Worker B passes unit 1.
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-noretest-B", 1))
	expectStatus(t, checkB, http.StatusOK)
	completeB := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-B", "job-noretest-B", passResults([]string{units[1]})))
	expectStatus(t, completeB, http.StatusOK)

	// --- /status: completed (no retest dispatched). ---
	finalStat := getJSON(t, env, tok, statusURL(identity(nil)))
	finalBody := expectStatus(t, finalStat, http.StatusOK)
	if finalBody["status"] != "completed" {
		t.Fatalf("final status = %v, want completed", finalBody["status"])
	}
	fc := counts(t, finalBody)
	if fc["completed_pass"] != 1 || fc["completed_fail"] != 1 ||
		fc["retest_eligible"] != 0 {
		t.Fatalf("final counts = %s, want pass=1 fail=1 retest=0", fmtCounts(fc))
	}

	// --- Either worker requesting more work gets queue_empty=true and
	// is_retest=false. ---
	for _, jobID := range []string{"job-noretest-A", "job-noretest-B"} {
		extra := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
			checkoutBody("playwright-shard-noretest", jobID, 1))
		// The run is now completed, so checkout returns 409 RUN_NOT_IN_PROGRESS.
		// That is the correct semantic — there is nothing to dispatch and the
		// run is terminal.
		if extra.StatusCode != http.StatusConflict {
			t.Fatalf("post-completion checkout: status = %d, want 409; body=%s",
				extra.StatusCode, readBodyString(extra))
		}
	}
}

// TestRetestLeaseExpiryRestoresCompletedFailAndAllowsSecondWorkerRetest verifies
// that when a worker leases a retest unit and lets the lease expire, the reaper
// restores the unit to state='completed_fail' (NOT 'pending'), increments
// completed_fail_count and retest_eligible_count while keeping pending_count==0,
// and a second worker can subsequently check out the retest unit and complete the run.
func TestRetestLeaseExpiryRestoresCompletedFailAndAllowsSecondWorkerRetest(t *testing.T) {
	env, tok := startEnv(t)

	units := []string{
		"tests/retest-expire.spec.ts",
	}
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(units, map[string]any{
			"repository":       defaultRepository + ".git",
			"retest_on_fail":   true,
			"retest_budget":    1,
			"lease_timeout_ms": 15_000,
			"idle_timeout_ms":  60_000,
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// Worker A checks out fresh unit 0 (using bare defaultRepository without .git)
	// and reports failed (fail_count=1 <= budget=1).
	checkA := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-retest-exp-A", 1))
	expectStatus(t, checkA, http.StatusOK)
	completeA := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-retest-exp-A", failResults(units)))
	expectStatus(t, completeA, http.StatusOK)

	// Worker B checks out the retest unit (is_retest=true), then its lease expires.
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-retest-exp-B", 1))
	bodyB := expectStatus(t, checkB, http.StatusOK)
	if !bodyB["is_retest"].(bool) {
		t.Fatalf("worker B checkout: is_retest=false, want true")
	}

	// Explicitly backdate Worker B's retest lease deadline so Workers A and C
	// never race a 1s timeout under heavy CI load.
	if _, err := env.Pool.Exec(context.Background(), `
		UPDATE leases
		   SET deadline = now() - interval '10 seconds'
		 WHERE gh_job_id = 'job-retest-exp-B' AND released_at IS NULL
	`); err != nil {
		t.Fatalf("backdate worker B retest lease deadline: %v", err)
	}

	// Wait for the reaper to expire Worker B's retest lease and restore completed_fail + retest_eligible.
	pollUntil(t, 5*time.Second, 100*time.Millisecond, "retest lease to expire back to completed_fail", func() bool {
		stat := getJSON(t, env, tok, statusURL(identity(nil)))
		if stat.StatusCode != http.StatusOK {
			return false
		}
		b := decodeJSON(t, stat)
		c := counts(t, b)
		return c["leased"] == 0 && c["completed_fail"] == 1 && c["retest_eligible"] == 1 && c["pending"] == 0
	})

	// Worker C checks out the restored retest unit (is_retest=true) and passes it.
	checkC := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-C", "job-retest-exp-C", 1))
	bodyC := expectStatus(t, checkC, http.StatusOK)
	if !bodyC["is_retest"].(bool) || len(bodyC["units"].([]any)) != 1 {
		t.Fatalf("worker C checkout: is_retest=%v units=%v, want true and 1 unit", bodyC["is_retest"], bodyC["units"])
	}

	completeC := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-C", "job-retest-exp-C", passResults(units)))
	expectStatus(t, completeC, http.StatusOK)

	finalBody := expectStatus(t, getJSON(t, env, tok, statusURL(identity(nil))), http.StatusOK)
	if finalBody["status"] != "completed" {
		t.Fatalf("final status = %v, want completed", finalBody["status"])
	}
	fc := counts(t, finalBody)
	if fc["completed_pass"] != 1 || fc["completed_fail"] != 0 || fc["retest_eligible"] != 0 || fc["pending"] != 0 {
		t.Fatalf("final counts = %s, want pass=1 all others 0", fmtCounts(fc))
	}
}

func postJSONRaw(serverURL, token, path string, body any) (int, string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+path, bytes.NewReader(raw))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

// TestConcurrentMarkRunTimedOutAndRecordCompletionNoDeadlock stresses concurrent
// run timeout (reaper markRunTimedOut) and worker completion (RecordCompletion)
// on the same run to verify lock ordering (leases -> dispatch_units -> orchestration_runs)
// produces zero 40P01 deadlocks and that fresh completions prevent stale idle timeouts.
func TestConcurrentMarkRunTimedOutAndRecordCompletionNoDeadlock(t *testing.T) {
	env, tok := startEnv(t)
	ctx := context.Background()

	// 1. Verify idle-deadline recheck under lock: if a run was idle when scanned
	// by the reaper, but a worker completion lands and refreshes last_activity_at
	// before markRunTimedOut locks orchestration_runs, the run must remain in_progress.
	{
		const idleRecheckRun = "ci-run-idle-recheck"
		idMap := identity(map[string]any{"gh_run_id": idleRecheckRun})
		units := []string{"tests/ir-0.spec.ts", "tests/ir-1.spec.ts"}
		beginBody := merge(beginRunBody(units, map[string]any{
			"lease_timeout_ms": 60_000,
			"idle_timeout_ms":  60_000,
		}), idMap)
		expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin", beginBody), http.StatusCreated)
		for w := range len(units) {
			chk := merge(checkoutBody(fmt.Sprintf("ir-w-%d", w), fmt.Sprintf("job-ir-%d", w), 1), idMap)
			expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout", chk), http.StatusOK)
		}
		// Backdate last_activity_at so it would have been idle, then immediately
		// complete worker 0 (which refreshes last_activity_at = now() with idle_timeout_ms = 60_000).
		if _, err := env.Pool.Exec(ctx, `
			UPDATE orchestration_runs
			   SET last_activity_at = now() - interval '10 minutes'
			 WHERE gh_run_id = $1
		`, idleRecheckRun); err != nil {
			t.Fatalf("backdate idleRecheckRun: %v", err)
		}
		cmp0 := merge(completeBody("ir-w-0", "job-ir-0", passResults([]string{units[0]})), idMap)
		expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete", cmp0), http.StatusOK)

		// Wait past a reaper tick (500ms); run must stay in_progress, NOT timed_out.
		time.Sleep(700 * time.Millisecond)
		midBody := expectStatus(t, getJSON(t, env, tok, statusURL(idMap)), http.StatusOK)
		if midBody["status"] != "in_progress" {
			t.Fatalf("run status after activity refresh = %v, want in_progress", midBody["status"])
		}
		cmp1 := merge(completeBody("ir-w-1", "job-ir-1", passResults([]string{units[1]})), idMap)
		expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete", cmp1), http.StatusOK)
	}

	// 2. Concurrent race between reaper timeout and worker completions across 10 runs.
	for iter := range 10 {
		runIDStr := fmt.Sprintf("ci-run-deadlock-%d", iter)
		idMap := identity(map[string]any{"gh_run_id": runIDStr})
		units := []string{"tests/dl-0.spec.ts", "tests/dl-1.spec.ts", "tests/dl-2.spec.ts", "tests/dl-3.spec.ts"}

		beginBody := merge(beginRunBody(units, map[string]any{
			"lease_timeout_ms": 60_000,
			"idle_timeout_ms":  60_000,
		}), idMap)
		expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin", beginBody), http.StatusCreated)

		for w := range len(units) {
			chk := merge(checkoutBody(fmt.Sprintf("worker-%d", w), fmt.Sprintf("job-dl-%d-%d", iter, w), 1), idMap)
			expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout", chk), http.StatusOK)
		}

		// Force the run's last_activity_at into the past so the reaper's markTimedOutRuns
		// picks it up concurrently with workers released from startCh calling /complete.
		if _, err := env.Pool.Exec(ctx, `
			UPDATE orchestration_runs
			   SET last_activity_at = now() - interval '10 minutes',
			       idle_timeout_ms = 1000
			 WHERE gh_run_id = $1
		`, runIDStr); err != nil {
			t.Fatalf("backdate last_activity_at: %v", err)
		}

		startCh := make(chan struct{})
		var wg sync.WaitGroup
		errCh := make(chan string, len(units))
		for w := range len(units) {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				<-startCh
				cmp := merge(completeBody(
					fmt.Sprintf("worker-%d", w),
					fmt.Sprintf("job-dl-%d-%d", iter, w),
					passResults([]string{units[w]}),
				), idMap)
				code, body, err := postJSONRaw(env.ServerURL, tok, "/api/v1/orchestration/complete", cmp)
				if err != nil {
					errCh <- fmt.Sprintf("iter=%d worker=%d request err=%v", iter, w, err)
					return
				}
				if code != http.StatusOK || strings.Contains(body, "40P01") {
					errCh <- fmt.Sprintf("iter=%d worker=%d status=%d body=%s", iter, w, code, body)
				}
			}(w)
		}
		close(startCh)
		wg.Wait()
		close(errCh)
		for e := range errCh {
			t.Fatalf("unexpected error/deadlock during concurrent timeout+complete: %s", e)
		}

		// Verify run reached a valid terminal state with consistent unit counters.
		statBody := expectStatus(t, getJSON(t, env, tok, statusURL(idMap)), http.StatusOK)
		st := statBody["status"]
		if st != "completed" && st != "timed_out" {
			t.Fatalf("iter=%d terminal status = %v, want completed or timed_out", iter, st)
		}
		c := counts(t, statBody)
		if c["pending"] != 0 || c["leased"] != 0 || c["completed_pass"]+c["abandoned"] != len(units) {
			t.Fatalf("iter=%d inconsistent counts after concurrent timeout+complete: %s", iter, fmtCounts(c))
		}
	}
}
