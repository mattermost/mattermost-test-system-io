package reports

import (
	"testing"

	"github.com/google/uuid"
)

func TestRollupTitleStatus(t *testing.T) {
	ios := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f1")
	android := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f2")
	retest := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f3")

	t.Run("ios fail android pass is failed not flaky", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: android, ShardLabel: "detox-android", Status: statusPassed},
			{ReportID: ios, ShardLabel: "detox-ios", Status: statusFailed},
		})
		if got != statusFailed {
			t.Fatalf("got %q, want failed", got)
		}
	})

	t.Run("primary fail then named retest pass is flaky", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: ios, ShardLabel: "detox-ios", Status: statusFailed},
			{ReportID: retest, ShardLabel: "detox-ios-retest", Status: statusPassed},
		})
		if got != statusFlaky {
			t.Fatalf("got %q, want flaky", got)
		}
	})

	t.Run("primary fail then cypress run-failed-tests pass is flaky", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: ios, ShardLabel: "cypress-shard-A", Status: statusFailed},
			{ReportID: retest, ShardLabel: "run-failed-tests", Status: statusPassed},
		})
		if got != statusFlaky {
			t.Fatalf("got %q, want flaky", got)
		}
	})

	t.Run("in-shard retries on one report is flaky", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: ios, ShardLabel: "detox-ios", Status: statusFailed},
			{ReportID: ios, ShardLabel: "detox-ios", Status: statusPassed},
		})
		if got != statusFlaky {
			t.Fatalf("got %q, want flaky", got)
		}
	})

	t.Run("both platforms passed", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: android, ShardLabel: "detox-android", Status: statusPassed},
			{ReportID: ios, ShardLabel: "detox-ios", Status: statusPassed},
		})
		if got != statusPassed {
			t.Fatalf("got %q, want passed", got)
		}
	})

	t.Run("empty shard labels treat each report as primary peer", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: android, ShardLabel: "", Status: statusPassed},
			{ReportID: ios, ShardLabel: "", Status: statusFailed},
		})
		if got != statusFailed {
			t.Fatalf("got %q, want failed", got)
		}
	})

	t.Run("single primary failure stays failed", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: ios, ShardLabel: "detox-ios", Status: statusFailed},
		})
		if got != statusFailed {
			t.Fatalf("got %q, want failed", got)
		}
	})
}

func TestSuiteSpecOK(t *testing.T) {
	try := func(status string, retry int) specAttempt { return specAttempt{Status: status, Retry: retry} }
	tests := []struct {
		name     string
		attempts []specAttempt
		want     bool
	}{
		{"passed", []specAttempt{try(statusPassed, 0)}, true},
		{"skipped", []specAttempt{try(statusSkipped, 0)}, true},
		{"failed then passed on retry", []specAttempt{try(statusFailed, 0), try(statusPassed, 1)}, true},
		{"legacy rows all flaky", []specAttempt{try(statusFlaky, 0), try(statusFlaky, 1)}, true},
		// A Playwright test.fail() test that passed, then failed on retry: Playwright and countStatuses call it flaky.
		{"passed then failed on retry", []specAttempt{try(statusPassed, 0), try(statusFailed, 1)}, true},
		// A retest upload merged into the suite starts again at retry 0; the later pass decides, as before.
		{"failed, then passed in a retest", []specAttempt{try(statusFailed, 0), try(statusFailed, 1), try(statusPassed, 0)}, true},
		// Two Playwright projects share a full title: a pass in one is not a retry of a failure in the next.
		{"one project passed, the next failed", []specAttempt{try(statusPassed, 0), try(statusFailed, 0)}, false},
		{"one project flaky, the next failed", []specAttempt{try(statusFailed, 0), try(statusPassed, 1), try(statusFailed, 0), try(statusFailed, 1)}, false},
		{"failed on every attempt", []specAttempt{try(statusFailed, 0), try(statusFailed, 1)}, false},
		{"timed out", []specAttempt{try(statusTimedOut, 0)}, false},
		{"interrupted after a pass", []specAttempt{try(statusPassed, 0), try("interrupted", 1)}, false},
		{"no attempts", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suiteSpecOK(tt.attempts); got != tt.want {
				t.Fatalf("suiteSpecOK(%v) = %v, want %v", tt.attempts, got, tt.want)
			}
		})
	}
}
