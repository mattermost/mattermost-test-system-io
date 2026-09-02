package triage

import (
	"math"
	"testing"
)

func TestBinomTailAtLeast(t *testing.T) {
	tests := []struct {
		name string
		k, n int
		p    float64
		want float64
	}{
		// The ABAC case's arithmetic, spelled out: a 40%-flaky test going
		// 3-of-3 is 0.4^3. This is the weakest shift the gate must catch, and
		// it is why alpha is 0.10 rather than 0.05.
		{name: "abac 3 of 3 at 40 percent", k: 3, n: 3, p: 0.40, want: 0.064},
		{name: "1 of 3 at 40 percent", k: 1, n: 3, p: 0.40, want: 0.784},
		{name: "2 of 3 at 40 percent", k: 2, n: 3, p: 0.40, want: 0.352},
		{name: "3 of 3 at 90 percent explains itself", k: 3, n: 3, p: 0.90, want: 0.729},
		{name: "2 of 2 at 5 percent", k: 2, n: 2, p: 0.05, want: 0.0025},
		{name: "k of zero is certain", k: 0, n: 3, p: 0.4, want: 1},
		{name: "k above n is impossible", k: 4, n: 3, p: 0.4, want: 0},
		{name: "never failed on baseline explains nothing", k: 1, n: 3, p: 0, want: 0},
		{name: "always fails on baseline explains anything", k: 3, n: 3, p: 1, want: 1},
		{name: "no runs", k: 1, n: 0, p: 0.4, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := binomTailAtLeast(tc.k, tc.n, tc.p)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("binomTailAtLeast(%d, %d, %v) = %v, want %v", tc.k, tc.n, tc.p, got, tc.want)
			}
		})
	}
}

// The tail must be a valid probability and must decrease monotonically in k —
// if either breaks, every threshold comparison downstream is meaningless.
func TestBinomTailIsMonotonicProbability(t *testing.T) {
	for _, p := range []float64{0.01, 0.1, 0.25, 0.4, 0.5, 0.75, 0.99} {
		for n := 1; n <= 20; n++ {
			prev := math.Inf(1)
			for k := 0; k <= n; k++ {
				got := binomTailAtLeast(k, n, p)
				if got < 0 || got > 1 {
					t.Fatalf("p=%v n=%d k=%d: tail %v outside [0,1]", p, n, k, got)
				}
				if got > prev+1e-12 {
					t.Fatalf("p=%v n=%d k=%d: tail %v rose above previous %v", p, n, k, got, prev)
				}
				prev = got
			}
			// P(X >= 0) is the whole distribution.
			if got := binomTailAtLeast(0, n, p); math.Abs(got-1) > 1e-9 {
				t.Fatalf("p=%v n=%d: P(X>=0) = %v, want 1", p, n, got)
			}
		}
	}
}

func TestComputeRateShift(t *testing.T) {
	tests := []struct {
		name                                         string
		baselineRuns, baselineFailed, prRuns, prFail int
		wantOK, wantShifted                          bool
		why                                          string
	}{
		{
			name:         "ABAC MM-T5824 — 40 percent baseline, 3 of 3 on this PR",
			baselineRuns: 20, baselineFailed: 8, prRuns: 3, prFail: 3,
			wantOK: true, wantShifted: true,
			why: "p=0.064 <= alpha=0.10; this is the case the gate exists for",
		},
		{
			name:         "ABAC MM-T5820 — same cluster, same shape",
			baselineRuns: 20, baselineFailed: 8, prRuns: 3, prFail: 3,
			wantOK: true, wantShifted: true,
			why: "the second ABAC case must be refused identically",
		},
		{
			name:         "unshifted — 40 percent on both sides",
			baselineRuns: 20, baselineFailed: 8, prRuns: 5, prFail: 2,
			wantOK: true, wantShifted: false,
			why: "40% vs 40% is exactly what the baseline predicts",
		},
		{
			name:         "unshifted — flaked once on a flaky test",
			baselineRuns: 20, baselineFailed: 8, prRuns: 3, prFail: 1,
			wantOK: true, wantShifted: false,
			why: "p=0.784; one failure is ordinary for a 40% test",
		},
		{
			name:         "unshifted — 2 of 3 on a 40 percent test",
			baselineRuns: 20, baselineFailed: 8, prRuns: 3, prFail: 2,
			wantOK: true, wantShifted: false,
			why: "p=0.352; still well inside its usual behavior",
		},
		{
			name:         "shifted — clean baseline, failing here",
			baselineRuns: 20, baselineFailed: 0, prRuns: 2, prFail: 2,
			wantOK: true, wantShifted: true,
			why: "a never-failing test cannot explain any failure",
		},
		{
			name:         "unshifted — chronically broken baseline",
			baselineRuns: 20, baselineFailed: 18, prRuns: 3, prFail: 3,
			wantOK: true, wantShifted: false,
			why: "a 90% test going 3-of-3 is unremarkable (p=0.729)",
		},
		{
			name:         "unshifted — rate fell",
			baselineRuns: 20, baselineFailed: 16, prRuns: 4, prFail: 1,
			wantOK: true, wantShifted: false,
			why: "an improvement is never a shift worth refusing over",
		},
		{
			name:         "not computable — baseline too small",
			baselineRuns: 4, baselineFailed: 2, prRuns: 3, prFail: 3,
			wantOK: false, wantShifted: false,
			why: "below minBaselineRuns the null hypothesis is noise",
		},
		{
			name:         "not computable — single PR run",
			baselineRuns: 20, baselineFailed: 8, prRuns: 1, prFail: 1,
			wantOK: false, wantShifted: false,
			why: "one failure can never carry a shift",
		},
		{
			name:         "not computable — no failures on this PR",
			baselineRuns: 20, baselineFailed: 8, prRuns: 3, prFail: 0,
			wantOK: false, wantShifted: false,
			why: "nothing to explain",
		},
		{
			name:         "not computable — no history at all",
			baselineRuns: 0, baselineFailed: 0, prRuns: 3, prFail: 3,
			wantOK: false, wantShifted: false,
			why: "no baseline means no null hypothesis",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeRateShift(tc.baselineRuns, tc.baselineFailed, tc.prRuns, tc.prFail)
			if got.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v (%s)", got.OK, tc.wantOK, tc.why)
			}
			if got.Shifted != tc.wantShifted {
				t.Fatalf("Shifted = %v, want %v — p=%v alpha=%v (%s)",
					got.Shifted, tc.wantShifted, got.PValue, got.Alpha, tc.why)
			}
			if got.Alpha != rateShiftAlpha {
				t.Fatalf("Alpha = %v, want the package threshold %v", got.Alpha, rateShiftAlpha)
			}
		})
	}
}

// An unavailable signal must never look like a shift. This is the fail-open
// direction that keeps the gate safe: it may only ever refuse a waiver, so a
// missing comparison has to reduce to the pre-existing behavior.
func TestComputeRateShiftZeroValueIsNotShifted(t *testing.T) {
	var zero RateShift
	if zero.OK || zero.Shifted {
		t.Fatalf("zero RateShift must be neither OK nor Shifted, got %+v", zero)
	}
}

// The gate's threshold is load-bearing and was chosen against the bars, not
// fitted to a sample. Pin it so a future edit has to justify itself.
func TestRateShiftThresholdsArePinned(t *testing.T) {
	if rateShiftAlpha != 0.10 {
		t.Fatalf("rateShiftAlpha = %v; alpha is also the false-red rate this gate adds "+
			"(bar: <= 20%%) and 0.10 is the level required to catch a 40%%-baseline test at 3-of-3",
			rateShiftAlpha)
	}
	if minBaselineRuns != 5 {
		t.Fatalf("minBaselineRuns = %d, want 5", minBaselineRuns)
	}
	if minPRRunsForShift != 2 {
		t.Fatalf("minPRRunsForShift = %d, want 2", minPRRunsForShift)
	}
	// alpha = 0.05 would let the ABAC case through; that is the whole reason
	// the looser level was chosen, so assert the boundary directly.
	abac := binomTailAtLeast(3, 3, 0.40)
	if abac <= 0.05 {
		t.Fatalf("ABAC p-value %v is below 0.05 — the alpha rationale in rateshift.go is stale", abac)
	}
	if abac > rateShiftAlpha {
		t.Fatalf("ABAC p-value %v exceeds alpha %v — the gate would not catch the case it exists for",
			abac, rateShiftAlpha)
	}
}

// The citation must reach the evidence pack, because the gate's refusal reason
// has to be traceable to a signal a human can audit in the ledger.
func TestSuggestCitesRateShift(t *testing.T) {
	shifted := ComputeRateShift(20, 8, 3, 3)
	if !shifted.Shifted {
		t.Fatalf("fixture is not shifted: %+v", shifted)
	}

	t.Run("historically unstable branch cites the shift", func(t *testing.T) {
		s := Signals{
			Status: "failed", HasStableID: true, HistoryOK: true,
			Runs: 20, Failed: 8, Flips: 6, FailureRate: 0.40,
			ElsewhereOK: true, RateShift: shifted,
		}
		got := Suggest(s)
		if got.Verdict != slaVerdictFlakyTest {
			t.Fatalf("verdict = %q, want FLAKY_TEST (the verdict is deliberately unchanged)", got.Verdict)
		}
		if !hasCite(got.Citations, citeRateShift) {
			t.Fatalf("citations %v missing %q", got.Citations, citeRateShift)
		}
	})

	t.Run("retry-recovered branch cites the shift", func(t *testing.T) {
		s := Signals{
			Status: statusFlaky, HasStableID: true, HistoryOK: true,
			Runs: 20, Failed: 8, Flips: 6, RateShift: shifted,
		}
		got := Suggest(s)
		if !hasCite(got.Citations, citeRateShift) {
			t.Fatalf("citations %v missing %q", got.Citations, citeRateShift)
		}
	})

	t.Run("unshifted does not cite", func(t *testing.T) {
		s := Signals{
			Status: "failed", HasStableID: true, HistoryOK: true,
			Runs: 20, Failed: 8, Flips: 6, FailureRate: 0.40,
			ElsewhereOK: true, RateShift: ComputeRateShift(20, 8, 3, 1),
		}
		got := Suggest(s)
		if hasCite(got.Citations, citeRateShift) {
			t.Fatalf("citations %v must not include %q when unshifted", got.Citations, citeRateShift)
		}
	})
}

func hasCite(cites []string, want string) bool {
	for _, c := range cites {
		if c == want {
			return true
		}
	}
	return false
}
