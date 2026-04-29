//go:build e2e
// +build e2e

package orchestratione2e

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestLateReportAcceptedAfterReassign drives the late-report acceptance path:
// worker A's lease times out before reporting, worker B inherits the unit and
// reports it `passed`, then worker A finally reports for its (now-expired)
// lease. Per spec, A's report is accepted with `late_report=true`, the unit's
// state is preserved, and a separate attempts row records A's late result.
func TestLateReportAcceptedAfterReassign(t *testing.T) {
	env, tok := startEnv(t)

	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{
			"tests/late.spec.ts",
			"tests/spare.spec.ts",
		}, map[string]any{
			"lease_timeout_ms": 1000, // 1s
			"idle_timeout_ms":  60_000,
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// --- Worker A leases the first unit. ---
	checkA := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-late-A", 1))
	bodyA := expectStatus(t, checkA, http.StatusOK)
	unitsA := bodyA["units"].([]any)
	if len(unitsA) != 1 {
		t.Fatalf("worker A got %d units, want 1", len(unitsA))
	}
	leasedUnitID := unitIDOf(t, unitsA[0].(map[string]any))

	// --- Wait until A's lease has timed out (rely on reaper or lazy on next checkout).
	time.Sleep(1500 * time.Millisecond)

	// --- Worker B picks up the reclaimed unit. ---
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-late-B", 1))
	bodyB := expectStatus(t, checkB, http.StatusOK)
	unitsB := bodyB["units"].([]any)
	if len(unitsB) != 1 {
		t.Fatalf("worker B got %d units, want 1", len(unitsB))
	}
	if unitIDOf(t, unitsB[0].(map[string]any)) != leasedUnitID {
		t.Fatalf("worker B unit_id = %s, want reclaimed %s",
			unitIDOf(t, unitsB[0].(map[string]any)), leasedUnitID)
	}

	// --- Worker B reports passed: unit transitions to completed_pass. ---
	completeB := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-B", "job-late-B", passResults([]string{"tests/late.spec.ts"})))
	resB := expectStatus(t, completeB, http.StatusOK)
	if resB["late_report"].(bool) {
		t.Fatalf("worker B should be on-time, got late_report=true")
	}
	if len(resB["unit_states_changed"].([]any)) != 1 {
		t.Fatalf("worker B unit_states_changed = %d, want 1", len(resB["unit_states_changed"].([]any)))
	}

	// --- Worker A finally reports the SAME passed result. Late, accepted, no state change. ---
	completeA := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-late-A", passResults([]string{"tests/late.spec.ts"})))
	resA := expectStatus(t, completeA, http.StatusOK)
	if !resA["accepted"].(bool) {
		t.Fatalf("worker A late report rejected: accepted=false")
	}
	if !resA["late_report"].(bool) {
		t.Fatalf("worker A late_report = false, want true")
	}
	if len(resA["unit_states_changed"].([]any)) != 0 {
		t.Fatalf("worker A unit_states_changed = %d, want 0 (unit already finalized)",
			len(resA["unit_states_changed"].([]any)))
	}

	// --- DB invariants: 2 attempts rows for tests/late.spec.ts ---
	ctx := context.Background()
	rows, err := env.Pool.Query(ctx, `
		SELECT a.expired, a.late_report, a.status
		  FROM attempts a
		  JOIN dispatch_units du ON du.id = a.dispatch_unit_id
		 WHERE du.id = $1 AND a.spec_path = 'tests/late.spec.ts'
		 ORDER BY a.created_at
	`, leasedUnitID)
	if err != nil {
		t.Fatalf("query attempts: %v", err)
	}
	type attemptRow struct {
		expired bool
		late    bool
		status  *string
	}
	var attempts []attemptRow
	for rows.Next() {
		var a attemptRow
		if err := rows.Scan(&a.expired, &a.late, &a.status); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		attempts = append(attempts, a)
	}
	rows.Close()
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts rows for the spec, got %d (%+v)", len(attempts), attempts)
	}

	// One attempt is the on-time pass (expired=false, late_report=false, passed),
	// the other is the late report (expired=true, late_report=true, passed).
	var sawOnTime, sawLate bool
	for _, a := range attempts {
		if a.status == nil || *a.status != "passed" {
			t.Fatalf("attempt status = %v, want passed", a.status)
		}
		if !a.expired && !a.late {
			sawOnTime = true
		}
		if a.expired && a.late {
			sawLate = true
		}
	}
	if !sawOnTime || !sawLate {
		t.Fatalf("missing attempt variants; got attempts=%+v", attempts)
	}

	// --- Unit row: lease_count==2, state=completed_pass. ---
	var (
		leaseCount int
		state      string
	)
	err = env.Pool.QueryRow(ctx, `SELECT lease_count, state FROM dispatch_units WHERE id = $1`,
		leasedUnitID).Scan(&leaseCount, &state)
	if err != nil {
		t.Fatalf("query dispatch_units: %v", err)
	}
	if leaseCount != 2 {
		t.Fatalf("lease_count = %d, want 2", leaseCount)
	}
	if state != "completed_pass" {
		t.Fatalf("state = %q, want completed_pass", state)
	}
}
