package triage

import "testing"

// W1 gate: a window with one waived and one real failure — raw counts both as
// failures, effective counts only the real one.
func TestRollupRatesWaivedVsReal(t *testing.T) {
	raw, effective := RollupRates(2, 2, 1)
	if raw != 0 {
		t.Fatalf("raw pass rate = %v, want 0 (both failures counted)", raw)
	}
	if effective != 50 {
		t.Fatalf("effective pass rate = %v, want 50 (only the real failure counted)", effective)
	}
}

func TestRollupRatesNoWaivers(t *testing.T) {
	raw, effective := RollupRates(10, 3, 0)
	if raw != 70 || effective != 70 {
		t.Fatalf("raw=%v effective=%v, want 70/70 with zero waivers", raw, effective)
	}
}

// Flaky outcomes count as raw failures (not a clean pass) and stay raw
// failures until a ledger waiver says otherwise.
func TestRollupRatesFlakyIsRawFailure(t *testing.T) {
	raw, effective := RollupRates(4, 1, 0)
	if raw != 75 || effective != 75 {
		t.Fatalf("raw=%v effective=%v, want 75/75", raw, effective)
	}
}

// A waiver can age out of the outcome window but not the verdict window;
// effective failures clamp at zero rather than going negative.
func TestRollupRatesClampZero(t *testing.T) {
	raw, effective := RollupRates(5, 1, 3)
	if raw != 80 {
		t.Fatalf("raw = %v, want 80", raw)
	}
	if effective != 100 {
		t.Fatalf("effective = %v, want 100 (clamped, never >100)", effective)
	}
}

func TestRollupRatesEmptyWindow(t *testing.T) {
	raw, effective := RollupRates(0, 0, 0)
	if raw != 0 || effective != 0 {
		t.Fatalf("raw=%v effective=%v, want 0/0 on empty window", raw, effective)
	}
}
