package triage

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Validate rejects policies that could disable evidence or make rates meaningless.
func (t Thresholds) Validate() error {
	if t.WindowDays < 1 || t.WindowDays > 3650 || t.MaxTrunkRuns < 1 || t.MaxTrunkRuns > 10000 || t.MinTrunkRuns < 1 || t.MinTrunkRuns > t.MaxTrunkRuns || t.BrokenStreak < 1 || t.ClusterMin < 2 || t.AreaSlack < 0 || t.StaleTrunkHours < 1 || t.SkipAfterDays < 0 || t.AutoQuarantineAfterRuns < 1 || t.ReleaseAfterPasses < 1 || t.RetireDays < 1 || t.CrossPRMinPRs < 0 {
		return errors.New("invalid triage count or duration threshold")
	}
	for _, n := range []float64{t.FlakyMinRate, t.PMin, t.ConfidenceFloor, t.MaxExoneratedRatio} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1 {
			return errors.New("triage probability thresholds must be between 0 and 1")
		}
	}
	if t.PMin == 0 {
		return errors.New("p_min must be greater than zero")
	}
	if t.InsufficientDataPolicy != "block" && t.InsufficientDataPolicy != "neutral" {
		return errors.New("insufficient_data_policy must be block or neutral")
	}
	return nil
}

// MergeThresholds overlays persisted values on the configured defaults.
func MergeThresholds(defaults Thresholds, raw json.RawMessage) (Thresholds, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Thresholds{}, err
	}
	allowedRaw, _ := json.Marshal(defaults)
	var allowed map[string]json.RawMessage
	_ = json.Unmarshal(allowedRaw, &allowed)
	for key, value := range fields {
		if _, ok := allowed[key]; !ok {
			return Thresholds{}, fmt.Errorf("unknown threshold %q", key)
		}
		if string(value) == "null" {
			return Thresholds{}, fmt.Errorf("threshold %q cannot be null", key)
		}
	}
	if err := json.Unmarshal(raw, &defaults); err != nil {
		return Thresholds{}, err
	}
	return defaults, defaults.Validate()
}
