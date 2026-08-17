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
