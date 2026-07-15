//go:build e2e
// +build e2e

package orchestratione2e

import (
	"net/http"
	"testing"
)

// TestCheckoutWhileActiveLeaseReturns409 asserts the WORKER_HAS_ACTIVE_LEASE
// guard: a worker that already holds an unreleased lease cannot check out
// again until they /complete. After completion, the next checkout succeeds.
func TestCheckoutWhileActiveLeaseReturns409(t *testing.T) {
	env, tok := startEnv(t)

	units := []string{
		"tests/a.spec.ts",
		"tests/b.spec.ts",
	}
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(units, nil))
	expectStatus(t, beginResp, http.StatusCreated)

	// --- First checkout: 200, one unit. ---
	first := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-active-A", 1))
	firstBody := expectStatus(t, first, http.StatusOK)
	if len(firstBody["units"].([]any)) != 1 {
		t.Fatalf("first checkout: expected 1 unit")
	}
	firstUnit := firstBody["units"].([]any)[0].(map[string]any)
	firstSeq := dispatchSeqOf(t, firstUnit)

	// --- Second checkout WITHOUT complete: 409 WORKER_HAS_ACTIVE_LEASE. ---
	second := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-active-A", 1))
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second checkout while leased: status = %d, want 409; body=%s",
			second.StatusCode, readBodyString(second))
	}
	if code := errorCode(t, second); code != "WORKER_HAS_ACTIVE_LEASE" {
		t.Fatalf("error code = %q, want WORKER_HAS_ACTIVE_LEASE", code)
	}

	// --- Complete the leased unit: 200, accepted, one state change. ---
	completeResp := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-active-A", passResults([]string{units[firstSeq]})))
	resBody := expectStatus(t, completeResp, http.StatusOK)
	if !resBody["accepted"].(bool) {
		t.Fatalf("complete: accepted=false")
	}
	if len(resBody["unit_states_changed"].([]any)) != 1 {
		t.Fatalf("complete: unit_states_changed = %d, want 1",
			len(resBody["unit_states_changed"].([]any)))
	}

	// --- Now checkout again: 200, the next unit. ---
	third := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-active-A", 1))
	thirdBody := expectStatus(t, third, http.StatusOK)
	thirdUnits := thirdBody["units"].([]any)
	if len(thirdUnits) != 1 {
		t.Fatalf("third checkout: expected 1 unit, got %d", len(thirdUnits))
	}
	if dispatchSeqOf(t, thirdUnits[0].(map[string]any)) == firstSeq {
		t.Fatalf("third checkout returned the SAME unit seq=%d", firstSeq)
	}
}
