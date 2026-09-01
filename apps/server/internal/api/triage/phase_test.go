package triage

import "testing"

// W13 gate: simulate each demotion trigger → demote; the bar met + two clean
// weeks → promotion offered; one clean week → refused.

func TestEvaluatePhaseGateDemoteTriggers(t *testing.T) {
	cases := []struct {
		name string
		in   PhaseInputs
		want string
	}{
		{
			"agreement below demote floor",
			PhaseInputs{Reviews: 10, AgreementPooled: 0.89, FalseGreens30d: 0, ReleaseFalseGreens: 0},
			"pooled audit agreement below demote floor",
		},
		{
			"false-greens over 30d limit",
			PhaseInputs{Reviews: 10, AgreementPooled: 0.96, FalseGreens30d: 3, ReleaseFalseGreens: 0},
			"false-greens over 30d limit",
		},
		{
			"release-branch false-green",
			PhaseInputs{Reviews: 10, AgreementPooled: 0.99, FalseGreens30d: 0, ReleaseFalseGreens: 1},
			"release-branch false-green detected",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluatePhaseGate(tc.in)
			if !d.Demote {
				t.Fatalf("expected demotion, got %s", d.Report)
			}
			if d.DemoteReason != tc.want {
				t.Fatalf("reason = %q, want %q", d.DemoteReason, tc.want)
			}
			if d.PromoteEligible {
				t.Fatal("a demoting state must never be promote-eligible")
			}
		})
	}
}

func TestEvaluatePhaseGatePromotion(t *testing.T) {
	t.Run("bar met plus two clean weeks offers promotion", func(t *testing.T) {
		d := EvaluatePhaseGate(PhaseInputs{
			Reviews:            10,
			AgreementPooled:    0.96,
			FalseGreens30d:     2,
			ReleaseFalseGreens: 0,
			WeeklyAgreement:    []float64{0.97, 0.96},
		})
		if !d.PromoteEligible {
			t.Fatalf("expected promotion offered, got %s", d.Report)
		}
		if d.Demote {
			t.Fatal("no demotion trigger present")
		}
	})

	t.Run("one clean week refuses promotion", func(t *testing.T) {
		d := EvaluatePhaseGate(PhaseInputs{
			Reviews:         10,
			AgreementPooled: 0.96,
			WeeklyAgreement: []float64{0.97},
		})
		if d.PromoteEligible {
			t.Fatal("promotion offered after a single clean week")
		}
		if d.CleanWeeks != 1 {
			t.Fatalf("clean weeks = %d, want 1", d.CleanWeeks)
		}
	})

	t.Run("bar unmet refuses promotion regardless of streak", func(t *testing.T) {
		d := EvaluatePhaseGate(PhaseInputs{
			Reviews:         10,
			AgreementPooled: 0.93, // between floors: no demote, no promote
			WeeklyAgreement: []float64{0.97, 0.96},
		})
		if d.Demote {
			t.Fatal("0.93 is above the demote floor")
		}
		if d.PromoteEligible {
			t.Fatal("promotion offered below the promote bar")
		}
	})

	t.Run("a dirty week breaks the streak", func(t *testing.T) {
		d := EvaluatePhaseGate(PhaseInputs{
			Reviews:         10,
			AgreementPooled: 0.96,
			WeeklyAgreement: []float64{0.97, 0.90, 0.99},
		})
		if d.CleanWeeks != 1 {
			t.Fatalf("clean weeks = %d, want 1 (streak broken by 0.90)", d.CleanWeeks)
		}
		if d.PromoteEligible {
			t.Fatal("promotion offered with a broken streak")
		}
	})

	t.Run("no reviews yet — nothing offered, nothing demoted", func(t *testing.T) {
		d := EvaluatePhaseGate(PhaseInputs{})
		if d.Demote {
			t.Fatal("empty state must not demote")
		}
		if d.PromoteEligible || d.BarMet {
			t.Fatal("empty state must not offer promotion")
		}
	})
}
