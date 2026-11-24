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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
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
// run timeout (Store.MarkRunTimedOut) and worker completion (RecordCompletion)
// synchronized at the leases FOR UPDATE lock boundary to verify lock ordering
// (leases -> dispatch_units -> orchestration_runs) produces zero 40P01 deadlocks,
// exercises both terminal outcomes (completed and timed_out), and verifies that
// activity refreshed after the reaper's initial scan aborts the timeout under lock.
func TestConcurrentMarkRunTimedOutAndRecordCompletionNoDeadlock(t *testing.T) {
	env, tok := startEnv(t, testenv.WithReaperDisabled())
	ctx := context.Background()

	countLeaseLockWaiters := func() int {
		var waiters int
		_ = env.Pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database()
			   AND wait_event_type = 'Lock'
			   AND query LIKE '%FROM leases%'
		`).Scan(&waiters)
		return waiters
	}

	// 1. Verify idle-deadline recheck under lock: MarkRunTimedOut enters its tx
	// and blocks on leases FOR UPDATE while the run is idle; activity is then
	// refreshed before holdTx releases the lock, and MarkRunTimedOut must abort
	// without timing out the run.
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
		irRunID := runUUID(t, env, idMap)
		if _, err := env.Pool.Exec(ctx, `
			UPDATE orchestration_runs
			   SET last_activity_at = now() - interval '10 minutes'
			 WHERE id = $1
		`, irRunID); err != nil {
			t.Fatalf("backdate idleRecheckRun: %v", err)
		}

		holdTx, err := env.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			t.Fatalf("begin holdTx: %v", err)
		}
		var lockedLeaseID uuid.UUID
		if err := holdTx.QueryRow(ctx, `
			SELECT id FROM leases
			 WHERE run_id = $1 AND released_at IS NULL
			 ORDER BY id LIMIT 1
			 FOR UPDATE
		`, irRunID).Scan(&lockedLeaseID); err != nil {
			_ = holdTx.Rollback(ctx)
			t.Fatalf("lock lease in holdTx: %v", err)
		}

		type timeoutRes struct {
			timedOut bool
			err      error
		}
		resCh := make(chan timeoutRes, 1)
		go func() {
			to, err := env.Orchestrator.MarkRunTimedOut(ctx, irRunID)
			resCh <- timeoutRes{timedOut: to, err: err}
		}()

		// Wait until MarkRunTimedOut is blocked inside its transaction on leases FOR UPDATE.
		pollUntil(t, 3*time.Second, 20*time.Millisecond, "MarkRunTimedOut to block on leases FOR UPDATE", func() bool {
			return countLeaseLockWaiters() >= 1
		})

		// Refresh last_activity_at inside holdTx (leases -> orchestration_runs lock order) and commit.
		if _, err := holdTx.Exec(ctx, `
			UPDATE orchestration_runs SET last_activity_at = now() WHERE id = $1
		`, irRunID); err != nil {
			_ = holdTx.Rollback(ctx)
			t.Fatalf("refresh last_activity_at in holdTx: %v", err)
		}
		if err := holdTx.Commit(ctx); err != nil {
			t.Fatalf("commit holdTx: %v", err)
		}

		tr := <-resCh
		if tr.err != nil || tr.timedOut {
			t.Fatalf("MarkRunTimedOut after activity refresh = (timedOut=%v, err=%v), want (false, nil)", tr.timedOut, tr.err)
		}

		midBody := expectStatus(t, getJSON(t, env, tok, statusURL(idMap)), http.StatusOK)
		if midBody["status"] != "in_progress" {
			t.Fatalf("run status after activity refresh = %v, want in_progress", midBody["status"])
		}
		for w := range len(units) {
			cmp := merge(completeBody(fmt.Sprintf("ir-w-%d", w), fmt.Sprintf("job-ir-%d", w), passResults([]string{units[w]})), idMap)
			expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete", cmp), http.StatusOK)
		}
		doneBody := expectStatus(t, getJSON(t, env, tok, statusURL(idMap)), http.StatusOK)
		if doneBody["status"] != "completed" {
			t.Fatalf("idleRecheckRun final status = %v, want completed", doneBody["status"])
		}
	}

	// 2. Synchronize MarkRunTimedOut and RecordCompletion at the leases row-lock
	// boundary and verify both terminal outcomes ("completed" when completion
	// queues first, and "timed_out" when MarkRunTimedOut queues first) with zero
	// 40P01 deadlocks and direct error checking on MarkRunTimedOut.
	var sawCompleted, sawTimedOut bool
	for iter := range 6 {
		runIDStr := fmt.Sprintf("ci-run-deadlock-%d", iter)
		idMap := identity(map[string]any{"gh_run_id": runIDStr})
		units := []string{"tests/dl-0.spec.ts", "tests/dl-1.spec.ts"}

		beginBody := merge(beginRunBody(units, map[string]any{
			"lease_timeout_ms": 60_000,
			"idle_timeout_ms":  60_000,
		}), idMap)
		expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin", beginBody), http.StatusCreated)

		for w := range len(units) {
			chk := merge(checkoutBody(fmt.Sprintf("worker-%d", w), fmt.Sprintf("job-dl-%d-%d", iter, w), 1), idMap)
			expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout", chk), http.StatusOK)
		}
		curRunID := runUUID(t, env, idMap)

		if _, err := env.Pool.Exec(ctx, `
			UPDATE orchestration_runs
			   SET last_activity_at = now() - interval '10 minutes',
			       idle_timeout_ms = 1000
			 WHERE id = $1
		`, curRunID); err != nil {
			t.Fatalf("backdate last_activity_at: %v", err)
		}

		// Hold FOR UPDATE on the lowest-id active lease of this run so both
		// RecordCompletion (worker 0) and MarkRunTimedOut block on the same row lock.
		holdTx, err := env.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			t.Fatalf("begin holdTx: %v", err)
		}
		var firstLeaseJobID string
		if err := holdTx.QueryRow(ctx, `
			SELECT gh_job_id FROM leases
			 WHERE run_id = $1 AND released_at IS NULL
			 ORDER BY id LIMIT 1
			 FOR UPDATE
		`, curRunID).Scan(&firstLeaseJobID); err != nil {
			_ = holdTx.Rollback(ctx)
			t.Fatalf("lock lease in holdTx: %v", err)
		}
		firstWorker := 0
		if strings.HasSuffix(firstLeaseJobID, "-1") {
			firstWorker = 1
		}
		otherWorker := 1 - firstWorker

		// On even iterations, complete the non-blocked worker first so that when
		// firstWorker wins the lease lock ahead of MarkRunTimedOut, the run
		// transitions immediately to "completed".
		if iter%2 == 0 {
			cmpOther := merge(completeBody(
				fmt.Sprintf("worker-%d", otherWorker),
				fmt.Sprintf("job-dl-%d-%d", iter, otherWorker),
				passResults([]string{units[otherWorker]}),
			), idMap)
			expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete", cmpOther), http.StatusOK)
			// Re-backdate last_activity_at so the run is idle when MarkRunTimedOut starts.
			if _, err := env.Pool.Exec(ctx, `
				UPDATE orchestration_runs
				   SET last_activity_at = now() - interval '10 minutes',
				       idle_timeout_ms = 1000
				 WHERE id = $1
			`, curRunID); err != nil {
				_ = holdTx.Rollback(ctx)
				t.Fatalf("re-backdate last_activity_at: %v", err)
			}
		}

		var wg sync.WaitGroup
		errCh := make(chan string, 3)

		startWorker := func(w int) {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
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

		startTimeout := func() {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := env.Orchestrator.MarkRunTimedOut(ctx, curRunID)
				if err != nil {
					errCh <- fmt.Sprintf("iter=%d MarkRunTimedOut err=%v", iter, err)
				}
			}()
		}

		if iter%2 == 0 {
			// Queue firstWorker on the lease lock first, then queue MarkRunTimedOut second.
			startWorker(firstWorker)
			pollUntil(t, 3*time.Second, 20*time.Millisecond, "worker to queue on lease lock", func() bool {
				return countLeaseLockWaiters() >= 1
			})
			startTimeout()
			pollUntil(t, 3*time.Second, 20*time.Millisecond, "MarkRunTimedOut to queue behind worker on lease lock", func() bool {
				return countLeaseLockWaiters() >= 2
			})
		} else {
			// Queue MarkRunTimedOut on the lease lock first, then queue firstWorker second.
			startTimeout()
			pollUntil(t, 3*time.Second, 20*time.Millisecond, "MarkRunTimedOut to queue on lease lock", func() bool {
				return countLeaseLockWaiters() >= 1
			})
			startWorker(firstWorker)
			pollUntil(t, 3*time.Second, 20*time.Millisecond, "worker to queue behind MarkRunTimedOut on lease lock", func() bool {
				return countLeaseLockWaiters() >= 2
			})
			startWorker(otherWorker)
		}

		if err := holdTx.Rollback(ctx); err != nil {
			t.Fatalf("rollback holdTx: %v", err)
		}

		wg.Wait()
		close(errCh)
		for e := range errCh {
			t.Fatalf("unexpected error/deadlock during concurrent timeout+complete: %s", e)
		}

		statBody := expectStatus(t, getJSON(t, env, tok, statusURL(idMap)), http.StatusOK)
		st, _ := statBody["status"].(string)
		switch st {
		case "completed":
			sawCompleted = true
		case "timed_out":
			sawTimedOut = true
		default:
			t.Fatalf("iter=%d terminal status = %v, want completed or timed_out", iter, st)
		}
		c := counts(t, statBody)
		if c["pending"] != 0 || c["leased"] != 0 || c["completed_pass"]+c["abandoned"] != len(units) {
			t.Fatalf("iter=%d inconsistent counts after concurrent timeout+complete: %s", iter, fmtCounts(c))
		}
	}
	if !sawCompleted || !sawTimedOut {
		t.Fatalf("expected both terminal outcomes across iterations, got sawCompleted=%v sawTimedOut=%v", sawCompleted, sawTimedOut)
	}
}

// TestLegacyGitSuffixBackwardCompatibility verifies that pre-existing
// orchestration_runs and report_groups rows stored with a trailing ".git" or ".GIT"
// repository suffix are matched by normalized HTTP requests (/orchestration/begin
// idempotent replay without creating a duplicate row, /checkout, /complete,
// /status), ListRunsByDisplayIdentity, and reports.LoadHomePageData RepositoryFilter,
// while owner/repository comparison stays case-sensitive.
func TestLegacyGitSuffixBackwardCompatibility(t *testing.T) {
	env, tok := startEnv(t)
	ctx := context.Background()

	for idx, suffix := range []string{".git", ".GIT"} {
		legacyRunID := fmt.Sprintf("ci-run-legacy-git-%d", idx+1)
		legacyRepo := defaultRepository + suffix
		units := []string{"tests/legacy-git.spec.ts"}

		// Seed a pre-existing run directly via Store.BeginRun with the un-trimmed suffix
		// (simulating a row created before input normalization was added).
		sub := defaultOIDCSubject
		legacyCI := orchestration.CompositeIdentity{
			Repository:   legacyRepo,
			CommitSHA:    defaultCommit,
			GHRunID:      legacyRunID,
			Name:         defaultName,
			GHRunAttempt: defaultRunAttempt,
			Branch:       defaultBranch,
			Framework:    "playwright",
		}
		seededRun, created, seededGroup, err := env.Orchestrator.BeginRun(ctx, legacyCI, orchestration.BeginRunOptions{
			LeaseTimeoutMs:       60_000,
			IdleTimeoutMs:        60_000,
			TotalReportsExpected: 1,
			PlaywrightProject:    defaultPlaywrightPrj,
			Branch:               defaultBranch,
		}, units, orchestration.OwnerInfo{OIDCSubject: &sub})
		if err != nil || !created || seededRun == nil || seededGroup == nil {
			t.Fatalf("seed legacy %s run: created=%v err=%v", suffix, created, err)
		}

		// 1. Calling POST /api/v1/orchestration/begin with normalized repository
		// must hit the existing legacy row (200 OK idempotent replay, NOT 201 Created duplicate).
		idMap := identity(map[string]any{"gh_run_id": legacyRunID, "repository": defaultRepository})
		beginBody := merge(beginRunBody(units, nil), idMap)
		replayResp := postJSON(t, env, tok, "/api/v1/orchestration/begin", beginBody)
		expectStatus(t, replayResp, http.StatusOK)

		var runCount int
		if err := env.Pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_runs WHERE gh_run_id = $1`, legacyRunID).Scan(&runCount); err != nil {
			t.Fatalf("count runs: %v", err)
		}
		if runCount != 1 {
			t.Fatalf("orchestration_runs count for %s = %d, want 1 (no duplicate)", legacyRunID, runCount)
		}

		// 2. /checkout, /complete, and /status with normalized repository find and complete the legacy run.
		jobID := fmt.Sprintf("job-legacy-git-%d", idx+1)
		chk := merge(checkoutBody("legacy-worker", jobID, 1), idMap)
		chkBody := expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout", chk), http.StatusOK)
		if len(chkBody["units"].([]any)) != 1 {
			t.Fatalf("checkout units = %v, want 1", chkBody["units"])
		}

		cmp := merge(completeBody("legacy-worker", jobID, passResults(units)), idMap)
		expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete", cmp), http.StatusOK)

		statBody := expectStatus(t, getJSON(t, env, tok, statusURL(idMap)), http.StatusOK)
		if statBody["status"] != "completed" {
			t.Fatalf("legacy run status = %v, want completed", statBody["status"])
		}

		// 3. ListRunsByDisplayIdentity matches both full slug and trailing segment,
		// while different owner/repository casing does NOT falsely match.
		for _, lookupRepo := range []string{defaultRepository, "orch-e2e"} {
			listed, err := env.Orchestrator.ListRunsByDisplayIdentity(ctx, lookupRepo, defaultCommit, defaultName, defaultBranch)
			if err != nil {
				t.Fatalf("ListRunsByDisplayIdentity(%q): %v", lookupRepo, err)
			}
			var foundRun bool
			for _, r := range listed {
				if r.Identity.GHRunID == legacyRunID {
					foundRun = true
					break
				}
			}
			if !foundRun {
				t.Fatalf("ListRunsByDisplayIdentity(%q) did not find %s", lookupRepo, legacyRunID)
			}
		}
		upperRuns, err := env.Orchestrator.ListRunsByDisplayIdentity(ctx, strings.ToUpper(defaultRepository), defaultCommit, defaultName, defaultBranch)
		if err != nil {
			t.Fatalf("ListRunsByDisplayIdentity(upper): %v", err)
		}
		if len(upperRuns) != 0 {
			t.Fatalf("expected case-sensitive owner/repo mismatch for %q, got %d runs", strings.ToUpper(defaultRepository), len(upperRuns))
		}

		// 4. reports.LoadHomePageData with normalized RepositoryFilter(defaultRepository),
		// .git/.GIT suffix, and trailing-slug filter all match the legacy report_groups row,
		// while upper-cased owner/repo does not.
		if err := reports.RefreshGroupSummary(ctx, env.Pool, seededGroup.ID); err != nil {
			t.Fatalf("RefreshGroupSummary: %v", err)
		}
		for _, filter := range []string{defaultRepository, defaultRepository + ".git", defaultRepository + ".GIT", "orch-e2e"} {
			home, err := reports.LoadHomePageData(ctx, env.Pool, 50, 0, filter, "")
			if err != nil {
				t.Fatalf("LoadHomePageData(%q): %v", filter, err)
			}
			var found bool
			for _, r := range home.Static {
				if r.GHRunID == legacyRunID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("LoadHomePageData(%q) did not match legacy %s report_group %s", filter, suffix, legacyRunID)
			}
		}
		upperHome, err := reports.LoadHomePageData(ctx, env.Pool, 50, 0, strings.ToUpper(defaultRepository), "")
		if err != nil {
			t.Fatalf("LoadHomePageData(upper): %v", err)
		}
		if len(upperHome.Static) != 0 {
			t.Fatalf("expected 0 rows for case-mismatched repo filter %q, got %d", strings.ToUpper(defaultRepository), len(upperHome.Static))
		}
	}

	// 5. Verify B-tree index usage (Index Scan / Bitmap Index Scan, zero Seq Scan)
	// for ANY($N::text[]) repository lookup predicates on orchestration_runs and report_groups.
	planTx, err := env.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin planTx: %v", err)
	}
	defer func() { _ = planTx.Rollback(ctx) }()
	if _, err := planTx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	variants := []string{defaultRepository, defaultRepository + ".git", defaultRepository + ".GIT"}
	for _, q := range []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "orchestration_runs lookup",
			sql: `EXPLAIN (COSTS OFF) SELECT id FROM orchestration_runs
				WHERE (repository = ANY($1::text[]) OR split_part(repository, '/', 2) = ANY($1::text[]))
				  AND commit_sha = $2 AND gh_run_id = $3 AND name = $4 AND gh_run_attempt = $5`,
			args: []any{variants, defaultCommit, "ci-run-legacy-git-1", defaultName, defaultRunAttempt},
		},
		{
			name: "report_groups repository filter",
			sql: `EXPLAIN (COSTS OFF) SELECT id FROM report_groups
				WHERE (repository = ANY($1::text[]) OR split_part(repository, '/', 2) = ANY($1::text[]))`,
			args: []any{variants},
		},
	} {
		rows, err := planTx.Query(ctx, q.sql, q.args...)
		if err != nil {
			t.Fatalf("explain %s: %v", q.name, err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				t.Fatalf("scan explain %s: %v", q.name, err)
			}
			plan.WriteString(line)
			plan.WriteByte('\n')
		}
		rows.Close()
		planText := plan.String()
		if !strings.Contains(planText, "Index Scan") && !strings.Contains(planText, "Bitmap Index Scan") {
			t.Fatalf("%s did not use B-tree index scan:\n%s", q.name, planText)
		}
		if strings.Contains(planText, "Seq Scan") {
			t.Fatalf("%s fell back to Seq Scan:\n%s", q.name, planText)
		}
	}
}
