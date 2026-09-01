package triage

import "strings"

// Signals is the subset of a failure the deterministic classifier needs.
// History fields are ignored when HistoryOK is false.
type Signals struct {
	Status             string
	HasStableID        bool
	HistoryOK          bool
	Runs               int
	Failed             int
	Flaky              int
	Flips              int
	FailureRate        float64
	FailingSinceCommit bool
	ElsewhereOK        bool
	DistinctPRs        int

	// ConfigDeltaKeys lists run-configuration keys whose captured values
	// differ from the last passing run for this test (W9). Empty/nil = no
	// baseline comparison available — never a signal on its own.
	ConfigDeltaKeys []string
}

// Suggestion is the deterministic (no-model, no-rerun) reading of a failure.
type Suggestion struct {
	Verdict    string   `json:"verdict"`
	Confidence float64  `json:"confidence"`
	NeedsAI    bool     `json:"needs_ai"`
	Reason     string   `json:"reason"`
	Citations  []string `json:"citations"`
}

const (
	statusFlaky = "flaky"

	citeThisRunRecovered   = "this_run_recovered"
	citeFailingOnBaseline  = "failing_on_baseline"
	citeFailingElsewhere   = "failing_elsewhere"
	citeFlipCount          = "flip_count"
	citeFailureRate        = "historical_failure_rate"
	citeNeverFailed        = "never_failed_on_baseline"
	citeIsolated           = "isolated_to_this_pr"
	citeNoStableID         = "no_stable_id"
	citeHistoryUnavailable = "history_unavailable"
	citeConfigDelta        = "config_delta_only"
)

// W9 — a run-configuration delta is deterministic flake evidence: the test
// passed on this branch under a different captured config, its history is
// otherwise clean, and the ONLY thing that changed is the environment. No
// model call needed; the deterministic layer pre-tags it.
const configDeltaVerdict = "FLAKY_INFRA"
const configDeltaConfidence = 0.9

// Suggest classifies a failure from TSIO history. It never asks for a rerun:
// in-run recovery is measurement, a failing streak on the baseline branch is
// an indexed query, and everything else is handed to a model with screenshots.
func Suggest(s Signals) Suggestion {
	if s.Status == statusFlaky {
		cites := []string{citeThisRunRecovered}
		if s.HistoryOK && s.Flips > 0 {
			cites = append(cites, citeFlipCount)
		}
		return Suggestion{
			Verdict:    "FLAKY_TEST",
			Confidence: 1,
			NeedsAI:    false,
			Reason:     "this run recovered after a retry — flakiness was measured, not inferred",
			Citations:  cites,
		}
	}

	if !s.HasStableID {
		return Suggestion{
			Verdict:    "INCONCLUSIVE",
			Confidence: 0,
			NeedsAI:    true,
			Reason:     "no stable test id, so history cannot be consulted",
			Citations:  []string{citeNoStableID},
		}
	}

	if !s.HistoryOK {
		return Suggestion{
			Verdict:    "INCONCLUSIVE",
			Confidence: 0,
			NeedsAI:    true,
			Reason:     "history lookup failed; fail closed",
			Citations:  []string{citeHistoryUnavailable},
		}
	}

	// W9 config-delta pre-tag: history where the ONLY failure is this run
	// (Failed == 1 — the current one), never flaky, plus a captured config
	// that differs from the last passing run for this test. The sole
	// difference is configuration — infra-owned, no AI.
	if len(s.ConfigDeltaKeys) > 0 && s.Failed == 1 && s.Flaky == 0 {
		return Suggestion{
			Verdict:    configDeltaVerdict,
			Confidence: configDeltaConfidence,
			NeedsAI:    false,
			Reason: "clean history; the only difference from the last passing run is configuration: " +
				strings.Join(s.ConfigDeltaKeys, ", "),
			Citations: []string{citeConfigDelta, citeNeverFailed},
		}
	}

	if s.FailingSinceCommit && s.Failed >= 2 {
		cites := []string{citeFailingOnBaseline}
		conf := 0.9
		reason := "already failing on the baseline branch — not introduced by this PR"
		if s.ElsewhereOK && s.DistinctPRs > 0 {
			cites = append(cites, citeFailingElsewhere)
			conf = 0.95
			reason = "already failing on the baseline branch and on other open PRs"
		}
		return Suggestion{
			Verdict:    "MAIN_REGRESSION",
			Confidence: conf,
			NeedsAI:    false,
			Reason:     reason,
			Citations:  cites,
		}
	}

	if s.FailingSinceCommit && s.ElsewhereOK && s.DistinctPRs > 0 {
		return Suggestion{
			Verdict:    "MAIN_REGRESSION",
			Confidence: 0.85,
			NeedsAI:    false,
			Reason:     "failing on the baseline branch and on other open PRs",
			Citations:  []string{citeFailingOnBaseline, citeFailingElsewhere},
		}
	}

	if s.Flips >= 3 && s.Runs >= 6 && s.FailureRate >= 0.1 && s.FailureRate <= 0.7 {
		return Suggestion{
			Verdict:    "FLAKY_TEST",
			Confidence: 0.8,
			NeedsAI:    true,
			Reason:     "historically unstable; screenshots can confirm the same flake versus a new UI break",
			Citations:  []string{citeFlipCount, citeFailureRate},
		}
	}

	if s.Runs >= 5 && s.Failed == 0 && s.Flaky == 0 && s.ElsewhereOK && s.DistinctPRs == 0 {
		return Suggestion{
			Verdict:    "PR_REGRESSION",
			Confidence: 0.7,
			NeedsAI:    true,
			Reason:     "clean on the baseline branch and isolated to this PR; screenshots and the diff should confirm",
			Citations:  []string{citeNeverFailed, citeIsolated},
		}
	}

	return Suggestion{
		Verdict:    "INCONCLUSIVE",
		Confidence: 0,
		NeedsAI:    true,
		Reason:     "history does not decide this failure; needs screenshots, logs, and the PR diff",
		Citations:  []string{},
	}
}
