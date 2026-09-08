package ingest

import "testing"

func TestCountStatusesKeepsProjectsSeparate(t *testing.T) {
	chrome, firefox := "chrome", "firefox"
	cases := []ExtractedCase{
		{FullTitle: "same test", Project: &chrome, Status: StatusFailed},
		{FullTitle: "same test", Project: &firefox, Status: StatusPassed},
	}
	passed, failed, skipped, flaky, unique := countStatuses(cases)
	if passed != 1 || failed != 1 || skipped != 0 || flaky != 0 || unique != 2 {
		t.Fatalf("cross-browser results merged: passed=%d failed=%d skipped=%d flaky=%d unique=%d", passed, failed, skipped, flaky, unique)
	}
}

func TestCountStatusesInterruptedIsNotPassed(t *testing.T) {
	for _, terminal := range []string{StatusInterrupted, StatusPassed} {
		t.Run(terminal, func(t *testing.T) {
			cases := []ExtractedCase{{FullTitle: "test", Status: StatusInterrupted}, {FullTitle: "test", Status: terminal}}
			passed, failed, skipped, flaky, unique := countStatuses(cases)
			wantFailed, wantFlaky := 1, 0
			if terminal == StatusPassed {
				wantFailed, wantFlaky = 0, 1
			}
			if passed != 0 || failed != wantFailed || skipped != 0 || flaky != wantFlaky || unique != 1 {
				t.Fatalf("interrupted result lost: passed=%d failed=%d skipped=%d flaky=%d unique=%d", passed, failed, skipped, flaky, unique)
			}
		})
	}
}
