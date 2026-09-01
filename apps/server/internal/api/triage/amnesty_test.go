package triage

import "testing"

// W4 gate cases against the shipped amnesty semantics (PROPOSED-BUILD-DEFAULT:
// 3 waivers / 14d inclusive + failure-rate ≥10% / 30d inclusive, both
// query-parameter overridable — the drift report keeps the rate component the
// build plan's "5 in 60d" rule would have dropped).
//
// Bystander-PR behavior lives in the action's canWaive (bystander carve-out);
// this is the server-side decision the action consumes.
func TestApplyAmnestyLimits(t *testing.T) {
	tests := []struct {
		name    string
		waivers int
		runs    int
		rate    float64
		granted bool
	}{
		{"2 waivers in window — still waiving", 2, 10, 0.05, true},
		{"3 waivers in window — expired (inclusive limit)", 3, 10, 0.05, false},
		{"failure rate 9% — still waiving", 1, 10, 0.09, true},
		{"failure rate 10% — expired (inclusive limit)", 1, 10, 0.10, false},
		{"no runs in rate window — granted (no evidence to deny on)", 1, 0, 0, true},
		{"both limits hit — denied", 3, 10, 0.5, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &amnestyResponse{
				WaiversInWindow: tc.waivers,
				Runs:            tc.runs,
				FailureRate:     tc.rate,
			}
			applyAmnestyLimits(resp, "main", 3, 0.10, "14d", "30d")
			if resp.Granted != tc.granted {
				t.Fatalf("granted = %v, want %v (reason: %s)", resp.Granted, tc.granted, resp.Reason)
			}
		})
	}
}
