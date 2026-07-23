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

	// Give Postgres's stats system time to flush worker A's writes before
	// taking the baseline reading.
	time.Sleep(1500 * time.Millisecond)
	insBefore, delBefore := leasesWriteCounters(t, env)
	if insBefore < 1 {
		t.Fatalf("n_tup_ins for leases = %d before worker B's poll, want >= 1 (worker A's insert)", insBefore)
	}

	// Worker B polls an empty queue: everything is leased to A, nothing
	// pending, retest is off by default so there's no fallback path either.
	checkB := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-B", "job-B", 1))
	bodyB := expectStatus(t, checkB, http.StatusOK)
	if !bodyB["queue_empty"].(bool) {
		t.Fatalf("worker B got queue_empty=false; expected empty queue")
	}

	time.Sleep(1500 * time.Millisecond)
	insAfter, delAfter := leasesWriteCounters(t, env)
	if insAfter != insBefore || delAfter != delBefore {
		t.Fatalf("leases write counters changed on an empty poll: "+
			"n_tup_ins %d->%d, n_tup_del %d->%d (want both unchanged)",
			insBefore, insAfter, delBefore, delAfter)
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
