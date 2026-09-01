package triage

import "testing"

func TestSuggest_thisRunRecoveredIsMeasuredFlake(t *testing.T) {
	got := Suggest(Signals{Status: "flaky", HistoryOK: true, Flips: 2})
	if got.Verdict != "FLAKY_TEST" || got.Confidence != 1 || got.NeedsAI {
		t.Fatalf("got %+v", got)
	}
	if !contains(got.Citations, citeThisRunRecovered) || !contains(got.Citations, citeFlipCount) {
		t.Fatalf("citations = %v", got.Citations)
	}
}

func TestSuggest_preexistingOnBaselineSkipsAI(t *testing.T) {
	got := Suggest(Signals{
		Status:             "failed",
		HasStableID:        true,
		HistoryOK:          true,
		Failed:             4,
		FailingSinceCommit: true,
		ElsewhereOK:        true,
		DistinctPRs:        3,
	})
	if got.Verdict != "MAIN_REGRESSION" || got.NeedsAI || got.Confidence < 0.9 {
		t.Fatalf("got %+v", got)
	}
	if !contains(got.Citations, citeFailingOnBaseline) || !contains(got.Citations, citeFailingElsewhere) {
		t.Fatalf("citations = %v", got.Citations)
	}
}

func TestSuggest_historicalFlipsNeedScreenshots(t *testing.T) {
	got := Suggest(Signals{
		Status:      "failed",
		HasStableID: true,
		HistoryOK:   true,
		Runs:        12,
		Failed:      4,
		Flips:       5,
		FailureRate: 0.4,
	})
	if got.Verdict != "FLAKY_TEST" || !got.NeedsAI || got.Confidence != 0.8 {
		t.Fatalf("got %+v", got)
	}
}

func TestSuggest_isolatedNewFailureIsRegressionCandidate(t *testing.T) {
	got := Suggest(Signals{
		Status:      "failed",
		HasStableID: true,
		HistoryOK:   true,
		Runs:        8,
		Failed:      0,
		Flaky:       0,
		ElsewhereOK: true,
		DistinctPRs: 0,
	})
	if got.Verdict != "PR_REGRESSION" || !got.NeedsAI {
		t.Fatalf("got %+v", got)
	}
}

func TestSuggest_noStableIDFailsClosed(t *testing.T) {
	got := Suggest(Signals{Status: "failed"})
	if got.Verdict != "INCONCLUSIVE" || !got.NeedsAI || got.Confidence != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestSuggest_historyUnavailableFailsClosed(t *testing.T) {
	got := Suggest(Signals{Status: "failed", HasStableID: true, HistoryOK: false})
	if got.Verdict != "INCONCLUSIVE" || !got.NeedsAI {
		t.Fatalf("got %+v", got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// W9 — config-delta pre-tag: clean history + captured config that differs
// from the last passing run → deterministic FLAKY_INFRA, never a model call.
func TestSuggestConfigDeltaPreTag(t *testing.T) {
	t.Run("clean history plus config delta is FLAKY_INFRA without AI", func(t *testing.T) {
		s := Suggest(Signals{
			Status: "failed", HasStableID: true, HistoryOK: true,
			Runs: 6, Failed: 1, Flaky: 0, // Failed == 1: the current run is the only failure
			ConfigDeltaKeys: []string{"E2E_FEATURE_FLAG_X"},
		})
		if s.Verdict != configDeltaVerdict {
			t.Fatalf("verdict = %q, want FLAKY_INFRA", s.Verdict)
		}
		if s.NeedsAI {
			t.Fatal("config-delta pre-tag must not need AI — zero model calls is the gate")
		}
		if !containsStr(s.Citations, "config_delta_only") {
			t.Fatalf("citations = %v, want config_delta_only", s.Citations)
		}
	})

	t.Run("dirty history keeps the delta from pre-tagging", func(t *testing.T) {
		s := Suggest(Signals{
			Status: "failed", HasStableID: true, HistoryOK: true,
			Runs: 6, Failed: 3, Flaky: 1,
			ConfigDeltaKeys: []string{"E2E_FEATURE_FLAG_X"},
		})
		if s.Verdict == configDeltaVerdict {
			t.Fatal("delta must not pre-tag when the test has failures in history")
		}
	})

	t.Run("two failures in history keep the delta from pre-tagging", func(t *testing.T) {
		s := Suggest(Signals{
			Status: "failed", HasStableID: true, HistoryOK: true,
			Runs: 6, Failed: 2, Flaky: 0,
			ConfigDeltaKeys: []string{"E2E_FEATURE_FLAG_X"},
		})
		if s.Verdict == configDeltaVerdict {
			t.Fatal("Failed >= 2 means the failure predates this run — not a config delta")
		}
	})

	t.Run("no delta keys = no pre-tag", func(t *testing.T) {
		s := Suggest(Signals{Status: "failed", HasStableID: true, HistoryOK: true, Runs: 6})
		if s.Verdict == configDeltaVerdict {
			t.Fatal("clean history alone must not pre-tag FLAKY_INFRA")
		}
	})
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
