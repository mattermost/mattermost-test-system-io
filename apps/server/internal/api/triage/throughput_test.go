package triage

import (
	"math"
	"strings"
	"testing"
)

func approx(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.02 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

// The measured production numbers, end to end. This is the calculation that
// decides whether the master-side loop can ever replace PR-side waivers, so it
// is pinned rather than left to a spreadsheet.
func TestThroughputOnMeasuredProductionNumbers(t *testing.T) {
	// 1,552 master groups over the 44-day backtest window ≈ 35/day, and 44
	// newly-failing tests in 30 days = 1.47/day.
	resp := throughputResponse{
		MasterRunsPerDay:  35.3,
		ArrivalRate:       1.47,
		ReviewLatencyDays: defaultReviewLatencyDays,
		AttemptsPerFix:    defaultAttemptsPerFix,
		Concurrency:       defaultConcurrency,
		MaxConcurrency:    maxConcurrency,
	}
	computeThroughput(&resp)

	// 20/35.3 = 0.57, so the 7-day floor governs. Getting this backwards would
	// understate cycle time and therefore the concurrency needed.
	approx(t, "window_days", resp.WindowDays, 7)
	approx(t, "cycle_days", resp.CycleDays, 14)
	// 2 / (14 * 1.5)
	approx(t, "modeled_drain", resp.ModeledDrainRate, 0.10)
	// 1.47 * 14 * 1.5
	approx(t, "required_concurrency", resp.RequiredConcurrency, 30.87)

	if resp.KeepingUp {
		t.Fatal("keeping_up must be false: drain 0.10/day cannot meet arrival 1.47/day")
	}
	if resp.CoveragePct > 10 {
		t.Fatalf("coverage %v%% — expected single digits at the pilot concurrency", resp.CoveragePct)
	}
	// Raising concurrency cannot close a 31-vs-5 gap, so the advice is the
	// bigger lever: stop waiting for master to re-measure.
	if resp.BindingLever != leverWindow {
		t.Fatalf("binding_lever = %q, want %q — required concurrency %v exceeds the cap %d",
			resp.BindingLever, leverWindow, resp.RequiredConcurrency, resp.MaxConcurrency)
	}
	if !strings.Contains(resp.Recommendation, "--grep") {
		t.Fatalf("recommendation must name the mechanism, got %q", resp.Recommendation)
	}
	// The deficit is taken from the UNROUNDED drain (2/(14*1.5) = 0.09524), not
	// from the 0.10 that is reported, so the weekly figure is
	// (1.47 - 0.09524) * 7 = 9.62. Rounding only ever touches the output.
	approx(t, "backlog_growth_per_week", resp.BacklogGrowthPerWeek, 9.62)
}

// The lever, quantified: cutting review latency from a weekly rotation to 48h
// at the concurrency cap is the largest available improvement.
func TestThroughputReviewLatencyIsTheLever(t *testing.T) {
	base := func(latency float64, concurrency int) throughputResponse {
		r := throughputResponse{
			MasterRunsPerDay:  35.3,
			ArrivalRate:       1.47,
			ReviewLatencyDays: latency,
			AttemptsPerFix:    defaultAttemptsPerFix,
			Concurrency:       concurrency,
			MaxConcurrency:    maxConcurrency,
		}
		computeThroughput(&r)
		return r
	}

	weekly := base(7, maxConcurrency)
	fast := base(2, maxConcurrency)
	_ = weekly.BindingLever // advice ordering is asserted in its own tests

	// 5/(14*1.5) = 0.238 -> 5/(9*1.5) = 0.370, about +55%.
	approx(t, "drain at 7d latency", weekly.ModeledDrainRate, 0.24)
	approx(t, "drain at 2d latency", fast.ModeledDrainRate, 0.37)
	gain := (fast.ModeledDrainRate - weekly.ModeledDrainRate) / weekly.ModeledDrainRate
	if gain < 0.5 {
		t.Fatalf("cutting review latency 7d->2d gained only %.0f%%; the stated lever is ~55%%", gain*100)
	}

	// Even so, neither keeps up — which is why quarantine exists as the third
	// state rather than as a nice-to-have.
	if fast.KeepingUp {
		t.Fatal("even at 2d latency and the concurrency cap, drain cannot meet 1.47/day arrival")
	}
}

// Below the cap the advice should be the ordinary one: raise concurrency.
func TestThroughputRecommendsConcurrencyWhenItFits(t *testing.T) {
	resp := throughputResponse{
		MasterRunsPerDay:  35.3,
		ArrivalRate:       0.1, // a calm repo
		ReviewLatencyDays: 2,
		AttemptsPerFix:    defaultAttemptsPerFix,
		Concurrency:       1,
		MaxConcurrency:    maxConcurrency,
	}
	computeThroughput(&resp)
	// 0.1 * 9 * 1.5 = 1.35, which fits under the cap of 5.
	approx(t, "required_concurrency", resp.RequiredConcurrency, 1.35)
	if resp.BindingLever != leverConcurrency {
		t.Fatalf("binding_lever = %q, want concurrency", resp.BindingLever)
	}
	if !strings.Contains(resp.Recommendation, "raise concurrency to 2") {
		t.Fatalf("recommendation should round up to 2, got %q", resp.Recommendation)
	}
}

func TestThroughputKeepingUpAndEmptyCases(t *testing.T) {
	t.Run("drain meets arrival", func(t *testing.T) {
		resp := throughputResponse{
			MasterRunsPerDay:  35.3,
			ArrivalRate:       0.05,
			ReviewLatencyDays: 2,
			AttemptsPerFix:    defaultAttemptsPerFix,
			Concurrency:       maxConcurrency,
			MaxConcurrency:    maxConcurrency,
		}
		computeThroughput(&resp)
		if !resp.KeepingUp {
			t.Fatalf("drain %v should meet arrival %v", resp.ModeledDrainRate, resp.ArrivalRate)
		}
		if resp.BindingLever != slaStateNone {
			t.Fatalf("binding_lever = %q, want %q", resp.BindingLever, slaStateNone)
		}
		if resp.DeficitPerDay != 0 {
			t.Fatalf("deficit = %v, want 0 when keeping up", resp.DeficitPerDay)
		}
	})

	t.Run("nothing arriving is not a 0% coverage bug", func(t *testing.T) {
		resp := throughputResponse{
			MasterRunsPerDay:  10,
			ArrivalRate:       0,
			ReviewLatencyDays: 7,
			AttemptsPerFix:    defaultAttemptsPerFix,
			Concurrency:       2,
			MaxConcurrency:    maxConcurrency,
		}
		computeThroughput(&resp)
		if !resp.KeepingUp || resp.CoveragePct != 100 {
			t.Fatalf("no arrivals must read as keeping up at 100%%, got keeping_up=%v coverage=%v",
				resp.KeepingUp, resp.CoveragePct)
		}
		if len(resp.Notes) == 0 {
			t.Fatal("a 100% from a zero arrival rate must be explained in notes, not left bare")
		}
	})

	t.Run("a quiet master lifts window_days above the floor", func(t *testing.T) {
		// 20/2 = 10 days of runs > the 7-day floor, so the division governs.
		resp := throughputResponse{
			MasterRunsPerDay:  2,
			ArrivalRate:       0.5,
			ReviewLatencyDays: 3,
			AttemptsPerFix:    defaultAttemptsPerFix,
			Concurrency:       2,
			MaxConcurrency:    maxConcurrency,
		}
		computeThroughput(&resp)
		approx(t, "window_days", resp.WindowDays, 10)
		approx(t, "cycle_days", resp.CycleDays, 13)
	})

	t.Run("no master runs falls back to the floor without dividing by zero", func(t *testing.T) {
		resp := throughputResponse{
			MasterRunsPerDay:  0,
			ArrivalRate:       0.5,
			ReviewLatencyDays: 7,
			AttemptsPerFix:    defaultAttemptsPerFix,
			Concurrency:       2,
			MaxConcurrency:    maxConcurrency,
		}
		computeThroughput(&resp)
		approx(t, "window_days", resp.WindowDays, windowFloorDays)
		if math.IsNaN(resp.ModeledDrainRate) || math.IsInf(resp.ModeledDrainRate, 0) {
			t.Fatalf("drain must stay finite with no master runs, got %v", resp.ModeledDrainRate)
		}
	})
}

// A modeled drain with nothing actually resolved is an upper bound, and the
// response has to say so rather than implying the loop is working.
func TestThroughputFlagsUnmeasuredDrain(t *testing.T) {
	resp := throughputResponse{
		MasterRunsPerDay:  35.3,
		ArrivalRate:       1.47,
		ResolvedInWindow:  0,
		ObservedDrainRate: 0,
		ReviewLatencyDays: 7,
		AttemptsPerFix:    defaultAttemptsPerFix,
		Concurrency:       2,
		MaxConcurrency:    maxConcurrency,
	}
	computeThroughput(&resp)
	joined := strings.Join(resp.Notes, " | ")
	if !strings.Contains(joined, "upper bound") {
		t.Fatalf("notes must flag modeled drain as an upper bound when nothing resolved, got %q", joined)
	}
}

// Regression: the what-if knobs must actually take effect.
//
// They were first wired through the package's parseFloat, whose contract is "a
// fraction in [0,1]" — so review_latency_days=2 silently became 7 and
// concurrency=5 silently became 2. The demo showed identical numbers for two
// different scenarios and looked plausible. A knob that silently does nothing
// is worse than no knob, so out-of-range is now an error, not a default.
func TestParseRangeAppliesValuesAndRejectsJunk(t *testing.T) {
	t.Run("a value above 1 is honored, not clamped", func(t *testing.T) {
		got, err := parseRange("2", defaultReviewLatencyDays, 0, 60, "review_latency_days")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 2 {
			t.Fatalf("got %v, want 2 — this is the exact bug parseFloat caused", got)
		}
	})
	t.Run("empty uses the default", func(t *testing.T) {
		got, err := parseRange("", 7, 0, 60, "review_latency_days")
		if err != nil || got != 7 {
			t.Fatalf("got %v, %v; want 7, nil", got, err)
		}
	})
	for _, bad := range []string{"abc", "-1", "999"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			if _, err := parseRange(bad, 7, 0, 60, "review_latency_days"); err == nil {
				t.Fatalf("%q must be rejected, not silently defaulted", bad)
			}
		})
	}
}

// THE ANSWER TO "use a tag to run just the specific tests".
//
// The 7-day window was never a property of the fix — it was a property of how
// we chose to sample. Waiting for 20 natural master runs takes a week; running
// the one test 20 times with `--grep MM-Txxxx` takes one job. That single
// change is worth more than every other lever combined, and it is what makes
// the loop able to keep master clean at the measured arrival rate.
func TestTargetedRemeasurementMakesTheLoopKeepUp(t *testing.T) {
	newResp := func(latency float64, concurrency int, targeted bool) throughputResponse {
		r := throughputResponse{
			MasterRunsPerDay:  35.3,
			ArrivalRate:       1.47, // measured: 44 newly-failing tests in 30d
			ReviewLatencyDays: latency,
			AttemptsPerFix:    defaultAttemptsPerFix,
			Concurrency:       concurrency,
			MaxConcurrency:    maxConcurrency,
			Targeted:          targeted,
		}
		computeThroughput(&r)
		return r
	}

	// Where we started: wait for master, weekly reviewer, pilot concurrency.
	base := newResp(defaultReviewLatencyDays, defaultConcurrency, false)
	approx(t, "base window", base.WindowDays, 7)
	approx(t, "base drain", base.ModeledDrainRate, 0.10)
	if base.KeepingUp {
		t.Fatal("baseline must not keep up — that is the whole problem")
	}

	// Tag-based re-measurement alone, everything else unchanged.
	tagged := newResp(defaultReviewLatencyDays, defaultConcurrency, true)
	approx(t, "targeted window", tagged.WindowDays, targetedWindowDays)
	// 2 / ((7 + 0.25) * 1.5) = 0.184
	approx(t, "targeted drain", tagged.ModeledDrainRate, 0.18)
	if tagged.ModeledDrainRate <= base.ModeledDrainRate {
		t.Fatal("targeted re-measurement must raise drain")
	}

	// Tag-based re-measurement + the 48h review SLA + the concurrency cap.
	// cycle = 2 + 0.25 = 2.25d; drain = 5 / (2.25 * 1.5) = 1.48/day.
	full := newResp(2, maxConcurrency, true)
	approx(t, "full cycle", full.CycleDays, 2.25)
	approx(t, "full drain", full.ModeledDrainRate, 1.48)
	if !full.KeepingUp {
		t.Fatalf("targeted re-measurement + 48h review at the cap must MEET arrival: "+
			"drain %v vs arrival %v", full.ModeledDrainRate, full.ArrivalRate)
	}
	if full.RequiredConcurrency > float64(full.MaxConcurrency) {
		t.Fatalf("required concurrency %v must now fit under the cap %d",
			full.RequiredConcurrency, full.MaxConcurrency)
	}

	// For the record: how much each lever is worth on its own.
	t.Logf("drain/day — baseline %.2f | +tag %.2f | +tag+48h@cap %.2f | arrival %.2f",
		base.ModeledDrainRate, tagged.ModeledDrainRate, full.ModeledDrainRate, base.ArrivalRate)
}

// When the window is still master-bound, the advice must reach for the window
// first — it is the biggest lever — rather than review latency.
func TestThroughputAdvisesTargetedRemeasurementFirst(t *testing.T) {
	resp := throughputResponse{
		MasterRunsPerDay:  35.3,
		ArrivalRate:       1.47,
		ReviewLatencyDays: defaultReviewLatencyDays,
		AttemptsPerFix:    defaultAttemptsPerFix,
		Concurrency:       maxConcurrency,
		MaxConcurrency:    maxConcurrency,
	}
	computeThroughput(&resp)
	if resp.BindingLever != leverWindow {
		t.Fatalf("binding_lever = %q, want %q", resp.BindingLever, leverWindow)
	}
	if !strings.Contains(resp.Recommendation, "--grep") {
		t.Fatalf("recommendation must name the mechanism, got %q", resp.Recommendation)
	}
	// At a 7-day review latency, tags alone are NOT sufficient: drain reaches
	// 0.46/day against 1.47 arrival, and required concurrency is still ~16 vs a
	// cap of 5. The advice must quantify the gain WITHOUT claiming it closes
	// the gap — over-claiming here would send someone away thinking the problem
	// was solved by one change.
	if strings.Contains(resp.Recommendation, "KEEPS UP") {
		t.Fatalf("must not claim sufficiency at 7d review latency, got %q", resp.Recommendation)
	}
	if !strings.Contains(resp.Recommendation, "0.46") {
		t.Fatalf("advice must quantify the new drain rate, got %q", resp.Recommendation)
	}

	// Add the 48-hour SLA and it does close: cycle 2.25d, drain 1.48/day.
	// BOTH levers are required — that is the finding.
	withSLA := throughputResponse{
		MasterRunsPerDay:  35.3,
		ArrivalRate:       1.47,
		ReviewLatencyDays: 2,
		AttemptsPerFix:    defaultAttemptsPerFix,
		Concurrency:       maxConcurrency,
		MaxConcurrency:    maxConcurrency,
	}
	computeThroughput(&withSLA)
	if withSLA.BindingLever != leverWindow {
		t.Fatalf("binding_lever = %q, want %q", withSLA.BindingLever, leverWindow)
	}
	if !strings.Contains(withSLA.Recommendation, "KEEPS UP") {
		t.Fatalf("with a 48h SLA, targeted re-measurement closes the gap and the advice "+
			"should say so, got %q", withSLA.Recommendation)
	}
}
