// Package health maintains the recomputable trunk-health projection.
package health

import (
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

// Classify follows the health table, with the specific broken streak before flaky.
func Classify(s triage.Stats, last, now time.Time, p triage.Thresholds) string {
	switch {
	case !last.IsZero() && now.Sub(last) > time.Duration(p.RetireDays)*24*time.Hour:
		return classificationRetired
	case s.Runs < p.MinTrunkRuns:
		return "unknown"
	case s.ConsecutiveFails >= p.BrokenStreak:
		return classificationBroken
	case rawRate(s) < .02:
		// Raw rate: the Laplace-smoothed rate is >= 1/(runs+2) and can never
		// reach 0.02 at the default 30-run cap, which would make auto-release
		// unattainable.
		return "healthy"
	case s.InstabilityRate >= p.FlakyMinRate && s.InstabilityRate < .5 && s.RecentPass:
		return triage.StatusFlaky
	case s.InstabilityRate >= .5:
		return "unstable"
	default:
		// ASSUMPTION: uncovered ranges in the blueprint are unknown, never healthy.
		return "unknown"
	}
}

func rawRate(s triage.Stats) float64 {
	if s.Runs == 0 {
		return 1
	}
	return float64(s.Fails+s.Flaky) / float64(s.Runs)
}

// Projection is the API representation of one identity/lane/base-ref health row.
type Projection struct {
	IdentityID           string     `json:"identity_id"`
	Lane                 string     `json:"lane"`
	BaseRef              string     `json:"base_ref"`
	WindowRuns           int        `json:"window_runs"`
	PassCount            int        `json:"pass_count"`
	FailCount            int        `json:"fail_count"`
	FlakyCount           int        `json:"flaky_count"`
	InstabilityRate      float64    `json:"instability_rate"`
	ConsecutiveFails     int        `json:"consecutive_fails"`
	LastPassAt           *time.Time `json:"last_pass_at"`
	LastFailAt           *time.Time `json:"last_fail_at"`
	DominantSignature    string     `json:"dominant_signature,omitempty"`
	DominantLocus        string     `json:"dominant_locus,omitempty"`
	Classification       string     `json:"classification"`
	ClassificationSince  time.Time  `json:"classification_since"`
	ConsecutiveRefreshes int        `json:"consecutive_refreshes"`
	ComputedAt           time.Time  `json:"computed_at"`
	EngineVersion        string     `json:"engine_version"`
}

const (
	classificationBroken  = "broken"
	classificationRetired = "retired"
)
