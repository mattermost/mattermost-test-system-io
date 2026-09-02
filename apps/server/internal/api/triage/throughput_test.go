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
	// The whole point: raising concurrency cannot close a 31-vs-5 gap.
	if resp.BindingLever != "review_latency" {
		t.Fatalf("binding_lever = %q, want review_latency — required concurrency %v exceeds the cap %d",
			resp.BindingLever, resp.RequiredConcurrency, resp.MaxConcurrency)
	}
	if !strings.Contains(resp.Recommendation, "cannot close this") {
		t.Fatalf("recommendation must say concurrency cannot close the gap, got %q", resp.Recommendation)
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
	if resp.BindingLever != "concurrency" {
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
