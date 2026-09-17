package health

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

func TestClassification(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	p := triage.DefaultThresholds()
	for _, tc := range []struct {
		name  string
		stats triage.Stats
		last  time.Time
		want  string
	}{
		{"unknown", triage.Stats{Runs: 4}, now, "unknown"},
		{"healthy", triage.Stats{Runs: 60, InstabilityRate: .016, ConsecutivePasses: 60, RecentPass: true}, now, "healthy"},
		{"healthy at the 30-run cap", triage.Stats{Runs: 30, InstabilityRate: 1.0 / 32, ConsecutivePasses: 30, RecentPass: true}, now, "healthy"},
		{"one flake in 30 is flaky, not healthy", triage.Stats{Runs: 30, Flaky: 1, InstabilityRate: 2.0 / 32, RecentPass: true}, now, triage.StatusFlaky},
		{triage.StatusFlaky, triage.Stats{Runs: 10, Fails: 1, Flaky: 1, InstabilityRate: .25, RecentPass: true}, now, triage.StatusFlaky},
		{classificationBroken, triage.Stats{Runs: 10, Fails: 2, InstabilityRate: .25, ConsecutiveFails: 2, RecentPass: true}, now, classificationBroken},
		{"unstable", triage.Stats{Runs: 10, Fails: 7, InstabilityRate: 8.0 / 12, RecentPass: true}, now, "unstable"},
		{"uncovered range", triage.Stats{Runs: 100, Fails: 3, InstabilityRate: 4.0 / 102, RecentPass: true}, now, "unknown"},
		{classificationRetired, triage.Stats{Runs: 10}, now.Add(-31 * 24 * time.Hour), classificationRetired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.stats, tc.last, now, p); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestClassificationHonorsLoweredFlakyMinRate(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	strict := triage.DefaultThresholds()
	strict.FlakyMinRate = .01
	// 3 flakes in 200 runs: raw rate 1.5% is under the 2% healthy cutoff, but
	// the policy says 1% is already flaky, so the policy wins.
	s := triage.Stats{Runs: 200, Flaky: 3, InstabilityRate: 4.0 / 202, RecentPass: true}
	if got := Classify(s, now, now, strict); got != triage.StatusFlaky {
		t.Fatalf("got %s want %s", got, triage.StatusFlaky)
	}
	if got := Classify(s, now, now, triage.DefaultThresholds()); got != classificationHealthy {
		t.Fatalf("default policy: got %s want healthy", got)
	}
}
