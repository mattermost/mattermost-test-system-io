//go:build e2e
// +build e2e

package orchestratione2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// TestCheckoutEmptyPollDoesNotWriteLeases is a regression test: an empty
// checkout (nothing pending) must not touch the leases table at all. Before
// the fix, AtomicCheckout unconditionally inserted a lease row and then
// deleted it again the moment dispatch found nothing — real write
// amplification (4 indexes touched twice) on every single empty poll, which
// happens on a fixed interval for a run's entire lifetime regardless of
// upload activity.
//
// Asserting on leases row *count* alone can't catch this: the buggy path's
// insert and delete happen in the same transaction, so the net row count is
// unchanged either way. Postgres's own per-table write counters
// (pg_stat_user_tables.n_tup_ins/n_tup_del) can tell the difference between
// "no write happened" and "a write happened and was undone" — the fixed
// code contributes zero to either counter on an empty poll, the buggy code
// contributes +1 to both.
func TestCheckoutEmptyPollDoesNotWriteLeases(t *testing.T) {
	env, tok := startEnv(t)

	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{
			"tests/a.spec.ts",
			"tests/b.spec.ts",
		}, nil))
	expectStatus(t, beginResp, http.StatusCreated)

	// Worker A takes both units — pending drops to 0, run stays in_progress
	// (nothing has completed yet).
	checkA := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-A", 2))
	bodyA := expectStatus(t, checkA, http.StatusOK)
	if bodyA["queue_empty"].(bool) {
		t.Fatalf("worker A got queue_empty=true; expected both units")
	}
	if got := len(bodyA["units"].([]any)); got != 2 {
		t.Fatalf("worker A got %d units, want 2", got)
	}

	// pg_stat_user_tables lags the actual write by Postgres's stats-flush
	// interval; poll for it rather than guessing a fixed duration.
	pollUntil(t, 5*time.Second, 100*time.Millisecond,
		"worker A's lease insert to appear in pg_stat_user_tables", func() bool {
			ins, _ := leasesWriteCounters(t, env)
			return ins >= 1
		})
	insBefore, delBefore := leasesWriteCounters(t, env)

	// Worker B polls an empty queue: everything is leased to A, nothing
	// pending, retest is off by default so there's no fallback path either.
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-B", 1))
	bodyB := expectStatus(t, checkB, http.StatusOK)
	if !bodyB["queue_empty"].(bool) {
		t.Fatalf("worker B got queue_empty=false; expected empty queue")
	}

	// No positive condition to poll for here (we're confirming an absence,
	// not waiting for a state) — check repeatedly across the flush-latency
	// window instead, so a regression fails as soon as it appears.
	assertLeasesWriteCountersStable(t, env, insBefore, delBefore, 1500*time.Millisecond, 100*time.Millisecond)
}

// assertLeasesWriteCountersStable fails immediately if the leases table's
// write counters ever diverge from (wantIns, wantDel) within window.
func assertLeasesWriteCountersStable(t *testing.T, env *testenv.Env, wantIns, wantDel int64, window, interval time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for {
		ins, del := leasesWriteCounters(t, env)
		if ins != wantIns || del != wantDel {
			t.Fatalf("leases write counters changed: n_tup_ins %d->%d, n_tup_del %d->%d (want both unchanged)",
				wantIns, ins, wantDel, del)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(interval)
	}
}

// TestCheckoutEmptyPollStillDetectsActiveLease is a regression test for the
// empty fast path added alongside TestCheckoutEmptyPollDoesNotWriteLeases:
// that fast path skips insertLeaseTx, which is where WORKER_HAS_ACTIVE_LEASE
// was previously always detected via a unique-index conflict — regardless
// of whether any units were pending. A worker that leases the run's last
// unit (pending drops to 0) and calls checkout again without completing
// must still get 409 WORKER_HAS_ACTIVE_LEASE, not a silent queue_empty.
func TestCheckoutEmptyPollStillDetectsActiveLease(t *testing.T) {
	env, tok := startEnv(t)

	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{"tests/only.spec.ts"}, nil))
	expectStatus(t, beginResp, http.StatusCreated)

	// Worker A takes the only unit — pending drops to 0, run stays
	// in_progress, worker A's lease stays unreleased (no /complete call).
	first := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-active-empty-A", 1))
	firstBody := expectStatus(t, first, http.StatusOK)
	if len(firstBody["units"].([]any)) != 1 {
		t.Fatalf("first checkout: expected 1 unit")
	}

	// Same worker checks out again while still holding that lease, on a
	// queue that is now empty — must hit the active-lease guard, not the
	// empty fast path.
	second := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-active-empty-A", 1))
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second checkout on empty queue while leased: status = %d, want 409; body=%s",
			second.StatusCode, readBodyString(second))
	}
	if code := errorCode(t, second); code != "WORKER_HAS_ACTIVE_LEASE" {
		t.Fatalf("error code = %q, want WORKER_HAS_ACTIVE_LEASE", code)
	}
}

// TestCheckoutFastPathAccountsForReclaimedUnits is a regression test for the
// `run.Counts.Pending + reclaimed` arithmetic in the empty-queue fast path.
// The other tests in this file only exercise reclaimed == 0 (nothing expires
// during the call). TestLeaseTimeoutReclaim exercises reclaimed > 0, but
// begins with two units, so run.Counts.Pending is already 1 by itself
// before any reclaim — that test would still pass even if the "+ reclaimed"
// term were silently dropped from the fast path.
//
// This test begins with exactly ONE unit, so after worker A leases it,
// run.Counts.Pending reads 0 on its own — the reclaimed addend is the only
// thing that can make the queue read non-empty. Worker A's lease then
// expires; worker B's checkout must reclaim and dispatch that unit. A
// regression that reverted the gate to checking run.Counts.Pending alone
// would instead return queue_empty: true here.
//
// Runs with the background reaper disabled: it ticks every 500ms in tests
// and would otherwise independently reclaim worker A's lease on its own,
// racing ahead of (and masking a regression in) the inline lazy-expiration
// path inside AtomicCheckout that this test targets.
func TestCheckoutFastPathAccountsForReclaimedUnits(t *testing.T) {
	env, tok := startEnv(t, testenv.WithReaperDisabled())

	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{"tests/reclaim-only.spec.ts"}, map[string]any{
			"lease_timeout_ms": 300, // 300ms
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// Worker A takes the only unit — pending drops to 0 even before any
	// reclaim is in play.
	checkA := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-reclaim-A", 1))
	bodyA := expectStatus(t, checkA, http.StatusOK)
	if len(bodyA["units"].([]any)) != 1 {
		t.Fatalf("worker A: expected 1 unit")
	}

	// Wait past the deadline so worker B's checkout lazily reclaims it. The
	// reaper is disabled, so nothing else can do this reclaim first.
	time.Sleep(500 * time.Millisecond)

	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-reclaim-B", 1))
	bodyB := expectStatus(t, checkB, http.StatusOK)
	if bodyB["queue_empty"].(bool) {
		t.Fatalf("worker B got queue_empty=true; the fast path should have " +
			"accounted for the unit reclaimed from worker A's expired lease")
	}
	if got := len(bodyB["units"].([]any)); got != 1 {
		t.Fatalf("worker B got %d units, want 1 (the reclaimed unit)", got)
	}
}

func leasesWriteCounters(t *testing.T, env *testenv.Env) (inserts, deletes int64) {
	t.Helper()
	err := env.Pool.QueryRow(context.Background(), `
		SELECT n_tup_ins, n_tup_del FROM pg_stat_user_tables WHERE relname = 'leases'
	`).Scan(&inserts, &deletes)
	if err != nil {
		t.Fatalf("read leases write counters: %v", err)
	}
	return inserts, deletes
}
