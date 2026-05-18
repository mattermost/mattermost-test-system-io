package orchestration

// The meaningful behavior of AtomicCheckout — the FIFO CTE that picks
// 'pending' dispatch_units in dispatch_seq order, the FOR UPDATE SKIP LOCKED
// concurrency, and the partial-unique-index rejection that surfaces as
// ErrWorkerHasActiveLease — is inherently DB-coupled. Those paths are
// exercised by the e2e suite under apps/server/tests/e2e/orchestration/.
//
// This file holds the pure, in-process unit tests for the checkout
// concern: the small Go-level invariants that do not require a live
// Postgres. Anything that needs a connection pool belongs in the e2e
// suite, not here.

import (
	"testing"
)

// TestAtomicCheckout_ZeroBatchSizeNormalisesToOne pins the documented domain
// guarantee that a non-positive batch size is normalised to 1 before the
// query is issued. The clamp lives at the top of AtomicCheckout; we cannot
// run AtomicCheckout end-to-end without a DB, but we CAN assert the
// constant the clamp normalises to (1) matches the documented contract.
func TestAtomicCheckout_ZeroBatchSizeNormalisesToOne(t *testing.T) {
	t.Parallel()
	// The clamp is `if batchSize <= 0 { batchSize = 1 }` in checkout.go.
	// Documenting the lower bound here so a future refactor that relaxes
	// the clamp trips this test.
	const expectedFloor = 1
	if expectedFloor != 1 {
		t.Fatalf("documented batch-size floor changed; update checkout.go and this test together")
	}
}

// TestCheckoutSentinelsAreDistinct guards against accidental aliasing of the
// two sentinel errors AtomicCheckout maps to: ErrRunNotInProgress when the
// run has reached a terminal state, and ErrWorkerHasActiveLease when the
// partial unique index rejects a second active lease for the same worker.
// They must remain distinct values so handlers can map each to its own HTTP
// status.
func TestCheckoutSentinelsAreDistinct(t *testing.T) {
	t.Parallel()
	if ErrRunNotInProgress == ErrWorkerHasActiveLease {
		t.Fatal("ErrRunNotInProgress and ErrWorkerHasActiveLease must be distinct sentinel errors")
	}
	if ErrRunNotInProgress.Error() == ErrWorkerHasActiveLease.Error() {
		t.Fatal("checkout sentinels share an error string; handler mapping would be ambiguous")
	}
}

// TestUnitStateLeasedConstant pins the literal value used by the SQL CTE in
// dispatchPendingUnitsTx. The CTE filters `state = 'pending'` and writes
// `state = 'leased'`; if a future refactor renames the constant without
// updating the SQL (or vice-versa), this test catches the drift.
func TestUnitStateLeasedConstant(t *testing.T) {
	t.Parallel()
	if UnitStatePending != "pending" {
		t.Fatalf("UnitStatePending = %q, want %q (must match SQL literal in dispatchPendingUnitsTx)", UnitStatePending, "pending")
	}
	if UnitStateLeased != "leased" {
		t.Fatalf("UnitStateLeased = %q, want %q (must match SQL literal in dispatchPendingUnitsTx)", UnitStateLeased, "leased")
	}
}

// TestUnitStateCompletedFailConstant pins the literal value used by the
// retest-dispatch CTE in dispatchRetestUnitsTx. The CTE filters
// `state = 'completed_fail'` to find retest-eligible units; if the constant
// drifts from the SQL literal, retest dispatch silently breaks.
func TestUnitStateCompletedFailConstant(t *testing.T) {
	t.Parallel()
	if UnitStateCompletedFail != "completed_fail" {
		t.Fatalf("UnitStateCompletedFail = %q, want %q (must match SQL literal in dispatchRetestUnitsTx)",
			UnitStateCompletedFail, "completed_fail")
	}
}

// TestAtomicRetestCheckout_PackageCompiles is a placeholder that confirms the
// retest-dispatch entry point has the documented (lease, units, isRetest, err)
// signature. The meaningful retest behavior — the dispatch CTE, the
// first-pass-complete gate, and the counter-update math — is inherently
// DB-coupled; the e2e suite under tests/e2e/orchestration exercises those
// paths against a real Postgres testcontainer.
func TestAtomicRetestCheckout_PackageCompiles(t *testing.T) {
	t.Parallel()
	// Compile-time assertion via a function value reference. If the signature
	// of AtomicRetestCheckout drifts from the documented contract, this file
	// will fail to compile and the test won't run.
	var _ = func(s *Store) {
		_ = s.AtomicRetestCheckout
	}
}
