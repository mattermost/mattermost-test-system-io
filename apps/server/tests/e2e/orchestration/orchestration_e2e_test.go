//go:build e2e
// +build e2e

package orchestratione2e

import (
	"net/http"
	"sort"
	"testing"
)

// TestOrchestrationHappyPath drives the entire User-Story-1 flow against the
// real HTTP stack and a real Postgres testcontainer.
//
// Steps mirror the contract narrative: begin run, three workers, multi-unit
// batch dispatches, intermediate /status snapshot, final completion flips the
// run to `completed`.
func TestOrchestrationHappyPath(t *testing.T) {
	env, tok := startEnv(t)

	// 6 dispatch units, each a single spec. Each entry becomes its own unit.
	units := []string{
		"tests/login.spec.ts",
		"tests/profile/a.spec.ts",
		"tests/profile/b.spec.ts",
		"tests/profile/c.spec.ts",
		"tests/teams.spec.ts",
		"tests/dms.spec.ts",
	}
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin", beginRunBody(units, nil))
	begin := expectStatus(t, beginResp, http.StatusCreated)
	if begin["status"] != "in_progress" {
		t.Fatalf("status = %v, want in_progress", begin["status"])
	}
	if jsonNumberInt(t, begin["total_units"]) != len(units) {
		t.Fatalf("total_units = %v, want %d", begin["total_units"], len(units))
	}

	// --- Worker A: batch_size=1 → unit 0 (single-spec login) ---
	checkA := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "42198765", 1))
	respA := expectStatus(t, checkA, http.StatusOK)
	if respA["queue_empty"].(bool) {
		t.Fatalf("worker A got queue_empty=true; expected work")
	}
	unitsA := respA["units"].([]any)
	if len(unitsA) != 1 {
		t.Fatalf("worker A got %d units, want 1", len(unitsA))
	}
	unitA := unitsA[0].(map[string]any)
	if got := dispatchSeqOf(t, unitA); got != 0 {
		t.Fatalf("worker A dispatch_seq = %d, want 0", got)
	}
	unitAID := unitIDOf(t, unitA)
	if got := unitA["spec_path"].(string); got != units[0] {
		t.Fatalf("worker A spec_path = %q, want %q", got, units[0])
	}

	// --- Worker B: batch_size=4 → units 1, 2, 3, 4 ---
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "42198766", 4))
	respB := expectStatus(t, checkB, http.StatusOK)
	unitsB := respB["units"].([]any)
	if len(unitsB) != 4 {
		t.Fatalf("worker B got %d units, want 4", len(unitsB))
	}
	bySeq := func(arr []any) []map[string]any {
		out := make([]map[string]any, len(arr))
		for i, u := range arr {
			out[i] = u.(map[string]any)
		}
		sort.Slice(out, func(i, j int) bool {
			return dispatchSeqOf(t, out[i]) < dispatchSeqOf(t, out[j])
		})
		return out
	}
	uB := bySeq(unitsB)
	for i, want := range []int{1, 2, 3, 4} {
		if got := dispatchSeqOf(t, uB[i]); got != want {
			t.Fatalf("worker B unit[%d] dispatch_seq = %d, want %d", i, got, want)
		}
		if got := uB[i]["spec_path"].(string); got != units[want] {
			t.Fatalf("worker B unit[%d] spec_path = %q, want %q", i, got, units[want])
		}
	}

	// --- Worker A reports passed for the login spec ---
	completeA := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "42198765", passResults([]string{units[0]})))
	resA := expectStatus(t, completeA, http.StatusOK)
	if !resA["accepted"].(bool) {
		t.Fatalf("worker A complete: accepted=false")
	}
	if resA["late_report"].(bool) {
		t.Fatalf("worker A complete: late_report=true; expected on-time")
	}
	changes := resA["unit_states_changed"].([]any)
	if len(changes) != 1 {
		t.Fatalf("worker A unit_states_changed = %d entries, want 1", len(changes))
	}
	chg := changes[0].(map[string]any)
	if chg["unit_id"] != unitAID {
		t.Fatalf("unit_states_changed[0].unit_id = %v, want %s", chg["unit_id"], unitAID)
	}
	if chg["new_state"] != "completed_pass" {
		t.Fatalf("unit_states_changed[0].new_state = %v, want completed_pass", chg["new_state"])
	}

	// --- /status: pending=1, leased=4, completed_pass=1 ---
	stat := getJSON(t, env, tok, statusURL(identity(nil)))
	statBody := expectStatus(t, stat, http.StatusOK)
	if statBody["status"] != "in_progress" {
		t.Fatalf("status snapshot = %v, want in_progress", statBody["status"])
	}
	c := counts(t, statBody)
	if c["pending"] != 1 || c["leased"] != 4 || c["completed_pass"] != 1 {
		t.Fatalf("counts = %s, want pending=1 leased=4 pass=1", fmtCounts(c))
	}

	// --- Worker C: batch_size=4 → unit 5 (the only one left) ---
	checkC := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-C", "42198767", 4))
	respC := expectStatus(t, checkC, http.StatusOK)
	unitsC := respC["units"].([]any)
	if len(unitsC) != 1 {
		t.Fatalf("worker C got %d units, want 1", len(unitsC))
	}
	unitC := unitsC[0].(map[string]any)
	if got := dispatchSeqOf(t, unitC); got != 5 {
		t.Fatalf("worker C dispatch_seq = %d, want 5", got)
	}

	// --- Worker B reports passed for all 4 of its leased units. ---
	allBSpecs := []string{units[1], units[2], units[3], units[4]}
	completeB := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-B", "42198766", passResults(allBSpecs)))
	resB := expectStatus(t, completeB, http.StatusOK)
	if !resB["accepted"].(bool) {
		t.Fatalf("worker B complete: accepted=false")
	}
	if len(resB["unit_states_changed"].([]any)) != 4 {
		t.Fatalf("worker B unit_states_changed = %d, want 4", len(resB["unit_states_changed"].([]any)))
	}

	// --- Worker C reports passed; should flip the run to completed. ---
	completeC := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-C", "42198767", passResults([]string{units[5]})))
	resC := expectStatus(t, completeC, http.StatusOK)
	if !resC["accepted"].(bool) {
		t.Fatalf("worker C complete: accepted=false")
	}
	if len(resC["unit_states_changed"].([]any)) != 1 {
		t.Fatalf("worker C unit_states_changed = %d, want 1", len(resC["unit_states_changed"].([]any)))
	}

	// --- Final /status: completed, terminal_at set, all 6 passed ---
	finalStat := getJSON(t, env, tok, statusURL(identity(nil)))
	finalBody := expectStatus(t, finalStat, http.StatusOK)
	if finalBody["status"] != "completed" {
		t.Fatalf("final status = %v, want completed", finalBody["status"])
	}
	if finalBody["terminal_at"] == nil {
		t.Fatalf("final terminal_at = nil; expected timestamp")
	}
	fc := counts(t, finalBody)
	if fc["pending"] != 0 || fc["leased"] != 0 ||
		fc["completed_pass"] != 6 || fc["completed_fail"] != 0 ||
		fc["completed_skipped"] != 0 || fc["abandoned"] != 0 {
		t.Fatalf("final counts = %s, want pass=6, all others 0", fmtCounts(fc))
	}
}
