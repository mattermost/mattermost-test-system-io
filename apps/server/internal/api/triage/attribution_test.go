package triage

import (
	"math"
	"strings"
	"testing"
)

// base builds a baseline with a coherent Runs/Failed/FailureRate triple, so a
// test never accidentally asserts against a rate that its own counts contradict.
func base(runs, failed int) AttributionBaseline {
	b := AttributionBaseline{Branch: "master", Window: "30d", Runs: runs, Failed: failed}
	if runs > 0 {
		b.FailureRate = float64(failed) / float64(runs)
	}
	return b
}

func obs(attempts, failed int) AttributionObserved {
	return AttributionObserved{Attempts: attempts, Failed: failed}
}

func TestDecide_PRSuspectIsDecidedFirstAndNeverGreens(t *testing.T) {
	// Spotless on master, failing here. The one message the system exists to
	// deliver, and the one that must survive every other rule.
	got := Decide(base(20, 0), obs(3, 3))

	if got.Outcome != AttrPRSuspect {
		t.Fatalf("outcome = %q, want %q", got.Outcome, AttrPRSuspect)
	}
	if got.CanGreen {
		t.Fatal("PR_SUSPECT granted can_green — a suspected regression must never green a check")
	}
	if got.NeedsReproduction {
		t.Error("PR_SUSPECT asked for a reproduction: the answer is already red, so the run is wasted")
	}
}

func TestDecide_PRSuspectEvenOnASingleFailure(t *testing.T) {
	// One failure is enough when the baseline is spotless: a test that has never
	// failed in 20 master runs and failed once here is not explained by chance.
	got := Decide(base(20, 0), obs(1, 1))
	if got.Outcome != AttrPRSuspect {
		t.Fatalf("outcome = %q, want %q", got.Outcome, AttrPRSuspect)
	}
}

func TestDecide_MasterBrokenGreensTheBystander(t *testing.T) {
	b := base(20, 6)
	b.FailingNow = true
	// Realistic 40-char SHAs: the reason abbreviates to 8, as git does.
	since, pass := "d14ab9c3e5f1079a2b4c6d8e0f1a2b3c4d5e6f70", "d13ff02a1b3c5d7e9f0a1b2c3d4e5f6a7b8c9d01"
	b.FailingSince, b.LastPass = &since, &pass

	got := Decide(b, obs(1, 1))

	if got.Outcome != AttrMasterBroken {
		t.Fatalf("outcome = %q, want %q", got.Outcome, AttrMasterBroken)
	}
	if !got.CanGreen {
		t.Fatal("a run that merely executed an already-broken test stayed red")
	}
	// The commit range is what attribution consumes, so the reason has to name
	// it — a green with no pointer to the owner is the old bucket list.
	if !strings.Contains(got.Reason, since[:8]) || !strings.Contains(got.Reason, pass[:8]) {
		t.Fatalf("reason does not name the failing_since/last_pass range: %q", got.Reason)
	}
}

func TestDecide_MasterBrokenNeverOverridesPRSuspect(t *testing.T) {
	// Contradictory input: nothing has ever failed on the baseline, yet the
	// baseline claims to be failing now. PR_SUSPECT must still win, because it
	// is evaluated first and nothing below may reach past it.
	b := base(20, 0)
	b.FailingNow = true

	if got := Decide(b, obs(1, 1)); got.Outcome != AttrPRSuspect || got.CanGreen {
		t.Fatalf("outcome = %q can_green = %v, want PR_SUSPECT and no green", got.Outcome, got.CanGreen)
	}
}

func TestDecide_KnownFlakeGreensWhenTheBaselineExplainsIt(t *testing.T) {
	// MM-T2001: 40% flaky on master, failed 1 of 3 here. p = 0.784 — nothing
	// unusual happened.
	got := Decide(base(20, 8), obs(3, 1))

	if got.Outcome != AttrKnownFlake {
		t.Fatalf("outcome = %q, want %q (reason: %s)", got.Outcome, AttrKnownFlake, got.Reason)
	}
	if !got.CanGreen {
		t.Fatal("a 40% flake failing 1 of 3 did not green — this is the primary promise")
	}
	if got.Observed.PValue == nil {
		t.Fatal("no p-value recorded; the green is unexplainable after the fact")
	}
	if p := *got.Observed.PValue; p < 0.7 || p > 0.85 {
		t.Errorf("p = %v, want ~0.784 for 1-of-3 at 40%%", p)
	}
}

func TestDecide_TheHardCase_ChronicFlakeThatActuallyBroke(t *testing.T) {
	// MM-T5824: 40% flaky on master AND failed 3 of 3 here. The case the old
	// classifier could not reach, because PR_REGRESSION required a spotless
	// history that a flaky test never has.
	//
	// p = 0.064 < 0.10, so "it flaked again" does not explain the run.
	got := Decide(base(20, 8), obs(3, 3))

	if got.CanGreen {
		t.Fatal("3-of-3 on a 40 percent test greened the check — this is the expensive failure")
	}
	if got.Outcome != AttrNeedsReproduction {
		t.Fatalf("outcome = %q, want %q", got.Outcome, AttrNeedsReproduction)
	}
	if !got.NeedsReproduction {
		t.Fatal("did not ask for a reproduction, so nothing would ever resolve it")
	}
	if p := *got.Observed.PValue; p >= unremarkableP {
		t.Errorf("p = %v, want below the %v threshold", p, unremarkableP)
	}
}

func TestDecide_NoBaselineNeverGreens(t *testing.T) {
	// A brand-new test, a renamed one, or a repo not using MM-T ids — all
	// indistinguishable here, and none of them a reason to green.
	got := Decide(base(0, 0), obs(3, 3))

	if got.Outcome != AttrNeedsReproduction || got.CanGreen {
		t.Fatalf("outcome = %q can_green = %v, want NEEDS_REPRODUCTION and no green", got.Outcome, got.CanGreen)
	}
	if got.Observed.PValue != nil {
		t.Error("p-value computed against a baseline that does not exist")
	}
}

func TestDecide_ThinHistoryNeverGreens(t *testing.T) {
	// Four runs, one failure: a 25% rate on paper, but four runs can produce
	// any rate at all. This is precisely what reproduction is for.
	got := Decide(base(4, 1), obs(3, 3))

	if got.Outcome != AttrNeedsReproduction || got.CanGreen {
		t.Fatalf("outcome = %q can_green = %v, want NEEDS_REPRODUCTION", got.Outcome, got.CanGreen)
	}
	if !strings.Contains(got.Reason, "4 baseline run") {
		t.Errorf("reason does not state the run count: %q", got.Reason)
	}
}

func TestDecide_ReliableTestIsNotDismissedAsAFlake(t *testing.T) {
	// 2% failure rate over 100 runs. The arithmetic alone would green a single
	// failure here (p = 0.02 is not below the threshold... it is 1-0.98 = 0.02,
	// which IS below, so it would ask for reproduction) — but the floor makes
	// the intent explicit rather than incidental: a test that works 98% of the
	// time is not a flake anyone should be told to ignore.
	got := Decide(base(100, 2), obs(1, 1))

	if got.CanGreen {
		t.Fatal("a 2 percent-failing test greened on one failure")
	}
	if !strings.Contains(got.Reason, "flake floor") {
		t.Errorf("reason should name the floor rather than the arithmetic: %q", got.Reason)
	}
}

func TestDecide_APassingObservationIsSettled(t *testing.T) {
	// Asked about a run where the test did not fail. Nothing to attribute, and
	// above all no reason to spend a server build re-establishing that a
	// passing test passes.
	got := Decide(base(20, 0), obs(3, 0))

	if got.Outcome != AttrNoFailure {
		t.Fatalf("outcome = %q, want %q", got.Outcome, AttrNoFailure)
	}
	if !got.CanGreen {
		t.Error("a run with no failures did not green")
	}
	if got.NeedsReproduction {
		t.Error("asked for a reproduction of a passing run")
	}
	if got.Outcome == AttrKnownFlake {
		t.Error("a passing run was recorded as a flake — that verdict reaches the ledger")
	}
}

func TestDecide_APassingObservationNeedsNoBaseline(t *testing.T) {
	// An unknown test that did not fail is still settled: the no-baseline guard
	// must not send it to a reproduction.
	got := Decide(base(0, 0), obs(1, 0))
	if got.Outcome != AttrNoFailure || got.NeedsReproduction {
		t.Fatalf("outcome = %q needs_reproduction = %v, want NO_FAILURE and no reproduction",
			got.Outcome, got.NeedsReproduction)
	}
}

func TestDecide_ZeroFailuresIsNeverASuspect(t *testing.T) {
	got := Decide(base(20, 0), obs(3, 0))
	if got.Outcome == AttrPRSuspect {
		t.Fatal("a passing run was called a PR suspect")
	}
}

func TestDecide_AlwaysEchoesTheThreshold(t *testing.T) {
	// The caller must never need the threshold out of band to explain a result.
	for _, tc := range []struct {
		name string
		b    AttributionBaseline
		o    AttributionObserved
	}{
		{"no baseline", base(0, 0), obs(1, 1)},
		{"suspect", base(20, 0), obs(1, 1)},
		{"flake", base(20, 8), obs(3, 1)},
		{"hard case", base(20, 8), obs(3, 3)},
		{"no failure", base(20, 8), obs(3, 0)},
	} {
		if got := Decide(tc.b, tc.o); got.Observed.Threshold != unremarkableP {
			t.Errorf("%s: threshold = %v, want %v", tc.name, got.Observed.Threshold, unremarkableP)
		}
	}
}

func TestDecide_EveryOutcomeCarriesAReason(t *testing.T) {
	// A verdict with no reason cannot be argued with, and every one of these
	// ends up in front of a developer.
	for _, tc := range []struct {
		name string
		b    AttributionBaseline
		o    AttributionObserved
	}{
		{"no baseline", base(0, 0), obs(1, 1)},
		{"suspect", base(20, 0), obs(1, 1)},
		{"thin", base(3, 1), obs(1, 1)},
		{"reliable", base(100, 2), obs(1, 1)},
		{"flake", base(20, 8), obs(3, 1)},
		{"hard case", base(20, 8), obs(3, 3)},
		{"no failure", base(20, 8), obs(3, 0)},
	} {
		got := Decide(tc.b, tc.o)
		if got.Reason == "" {
			t.Errorf("%s: empty reason", tc.name)
		}
		if got.Outcome == "" {
			t.Errorf("%s: empty outcome", tc.name)
		}
		// can_green and needs_reproduction are opposite halves of "settled";
		// both true would tell a caller to green it and also go check.
		if got.CanGreen && got.NeedsReproduction {
			t.Errorf("%s: both can_green and needs_reproduction", tc.name)
		}
	}
}

// --- the arithmetic itself ---

func TestAtLeast_KnownValues(t *testing.T) {
	cases := []struct {
		n, k int
		rate float64
		want float64
	}{
		// 1 of 3 at 40%: 1 - 0.6^3 = 0.784
		{3, 1, 0.40, 0.784},
		// 3 of 3 at 40%: 0.4^3 = 0.064 — the ABAC case
		{3, 3, 0.40, 0.064},
		// 2 of 3 at 40%: 3(0.16)(0.6) + 0.064 = 0.352
		{3, 2, 0.40, 0.352},
		// 1 of 1 at 40%
		{1, 1, 0.40, 0.400},
		// 1 of 3 at 10%: 1 - 0.9^3 = 0.271
		{3, 1, 0.10, 0.271},
		// 5 of 5 at 90%: 0.9^5 = 0.59049
		{5, 5, 0.90, 0.59049},
	}
	for _, c := range cases {
		got := atLeast(c.n, c.k, c.rate)
		if math.Abs(got-c.want) > 1e-6 {
			t.Errorf("atLeast(%d,%d,%.2f) = %.6f, want %.6f", c.n, c.k, c.rate, got, c.want)
		}
	}
}

func TestAtLeast_Boundaries(t *testing.T) {
	// Zero failures is certain, whatever the rate.
	if got := atLeast(3, 0, 0.4); got != 1 {
		t.Errorf("atLeast(3,0,0.4) = %v, want 1", got)
	}
	// A test that never fails cannot produce a failure.
	if got := atLeast(3, 1, 0); got != 0 {
		t.Errorf("atLeast(3,1,0) = %v, want 0", got)
	}
	// A test that always fails certainly does.
	if got := atLeast(3, 3, 1); got != 1 {
		t.Errorf("atLeast(3,3,1) = %v, want 1", got)
	}
	// Negative k is nonsense input; treat it as "at least none".
	if got := atLeast(3, -1, 0.4); got != 1 {
		t.Errorf("atLeast(3,-1,0.4) = %v, want 1", got)
	}
}

func TestAtLeast_StaysInRangeOverLargeRuns(t *testing.T) {
	// The iterative term must not drift out of [0,1] on the run counts a real
	// baseline produces — a naive factorial implementation overflows here.
	for _, n := range []int{50, 200, 1000} {
		for _, k := range []int{1, n / 2, n} {
			got := atLeast(n, k, 0.4)
			if got < 0 || got > 1 || math.IsNaN(got) {
				t.Fatalf("atLeast(%d,%d,0.4) = %v, out of range", n, k, got)
			}
		}
	}
}

func TestAtLeast_IsMonotonicInK(t *testing.T) {
	// More failures can never be more likely than fewer.
	prev := 1.0
	for k := 0; k <= 10; k++ {
		got := atLeast(10, k, 0.4)
		if got > prev+1e-12 {
			t.Fatalf("atLeast(10,%d,0.4) = %v rose above the previous %v", k, got, prev)
		}
		prev = got
	}
}
