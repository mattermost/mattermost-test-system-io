package reports

import (
	"testing"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

func TestRollupTitleStatus(t *testing.T) {
	ios := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f1")
	android := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f2")
	retest := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f3")

	t.Run("ios fail android pass is failed not flaky", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: android, ShardLabel: "detox-android", Status: teststatus.Passed},
			{ReportID: ios, ShardLabel: "detox-ios", Status: teststatus.Failed},
		}, teststatus.RuleShared)
		if got != teststatus.Failed {
			t.Fatalf("got %q, want failed", got)
		}
	})

	t.Run("primary fail then named retest pass is flaky", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: ios, ShardLabel: "detox-ios", Status: teststatus.Failed},
			{ReportID: retest, ShardLabel: "detox-ios-retest", Status: teststatus.Passed},
		}, teststatus.RuleShared)
		if got != teststatus.Flaky {
			t.Fatalf("got %q, want flaky", got)
		}
	})

	t.Run("primary fail then cypress run-failed-tests pass is flaky", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: ios, ShardLabel: "cypress-shard-A", Status: teststatus.Failed},
			{ReportID: retest, ShardLabel: "run-failed-tests", Status: teststatus.Passed},
		}, teststatus.RuleShared)
		if got != teststatus.Flaky {
			t.Fatalf("got %q, want flaky", got)
		}
	})

	t.Run("in-shard retries on one report is flaky", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: ios, ShardLabel: "detox-ios", Status: teststatus.Failed},
			{ReportID: ios, ShardLabel: "detox-ios", Status: teststatus.Passed},
		}, teststatus.RuleShared)
		if got != teststatus.Flaky {
			t.Fatalf("got %q, want flaky", got)
		}
	})

	t.Run("both platforms passed", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: android, ShardLabel: "detox-android", Status: teststatus.Passed},
			{ReportID: ios, ShardLabel: "detox-ios", Status: teststatus.Passed},
		}, teststatus.RuleShared)
		if got != teststatus.Passed {
			t.Fatalf("got %q, want passed", got)
		}
	})

	t.Run("empty shard labels treat each report as primary peer", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: android, ShardLabel: "", Status: teststatus.Passed},
			{ReportID: ios, ShardLabel: "", Status: teststatus.Failed},
		}, teststatus.RuleShared)
		if got != teststatus.Failed {
			t.Fatalf("got %q, want failed", got)
		}
	})

	t.Run("single primary failure stays failed", func(t *testing.T) {
		got := rollupTitleStatus([]titleCase{
			{ReportID: ios, ShardLabel: "detox-ios", Status: teststatus.Failed},
		}, teststatus.RuleShared)
		if got != teststatus.Failed {
			t.Fatalf("got %q, want failed", got)
		}
	})

	t.Run("interrupted and unknown single attempts fail under shared and pass under legacy", func(t *testing.T) {
		for _, s := range []string{teststatus.Interrupted, "unknown"} {
			if got := rollupTitleStatus([]titleCase{{ReportID: ios, ShardLabel: "pw", Status: s}}, teststatus.RuleShared); got != teststatus.Failed {
				t.Fatalf("shared status %q: got %q, want failed", s, got)
			}
			if got := rollupTitleStatus([]titleCase{{ReportID: ios, ShardLabel: "pw", Status: s}}, teststatus.RuleLegacy); got != teststatus.Passed {
				t.Fatalf("legacy status %q: got %q, want passed", s, got)
			}
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
		{"passed", []specAttempt{try(teststatus.Passed, 0)}, true},
		{"skipped", []specAttempt{try(teststatus.Skipped, 0)}, true},
		{"pending", []specAttempt{try(teststatus.Pending, 0)}, true},
		{"failed then passed on retry", []specAttempt{try(teststatus.Failed, 0), try(teststatus.Passed, 1)}, true},
		{"legacy rows all flaky", []specAttempt{try(teststatus.Flaky, 0), try(teststatus.Flaky, 1)}, true},
		// A Playwright test.fail() test that passed, then failed on retry: Playwright and countStatuses call it flaky.
		{"passed then failed on retry", []specAttempt{try(teststatus.Passed, 0), try(teststatus.Failed, 1)}, true},
		// A retest upload merged into the suite starts again at retry 0; the later pass decides, as before.
		{"failed, then passed in a retest", []specAttempt{try(teststatus.Failed, 0), try(teststatus.Failed, 1), try(teststatus.Passed, 0)}, true},
		// Two Playwright projects share a full title: a pass in one is not a retry of a failure in the next.
		{"one project passed, the next failed", []specAttempt{try(teststatus.Passed, 0), try(teststatus.Failed, 0)}, false},
		{"one project flaky, the next failed", []specAttempt{try(teststatus.Failed, 0), try(teststatus.Passed, 1), try(teststatus.Failed, 0), try(teststatus.Failed, 1)}, false},
		{"failed on every attempt", []specAttempt{try(teststatus.Failed, 0), try(teststatus.Failed, 1)}, false},
		{"timed out", []specAttempt{try(teststatus.TimedOut, 0)}, false},
		{"interrupted after a pass", []specAttempt{try(teststatus.Passed, 0), try(teststatus.Interrupted, 1)}, false},
		{"unknown", []specAttempt{try("unknown", 0)}, false},
		{"no attempts", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suiteSpecOK(tt.attempts, teststatus.RuleShared); got != tt.want {
				t.Fatalf("suiteSpecOK(%v, shared) = %v, want %v", tt.attempts, got, tt.want)
			}
		})
	}
	if !suiteSpecOK([]specAttempt{try(teststatus.Passed, 0), try(teststatus.TimedOut, 1)}, teststatus.RuleLegacy) {
		t.Fatal("legacy suiteSpecOK treated timedOut retry after pass as flaky")
	}
	if suiteSpecOK([]specAttempt{try(teststatus.Passed, 0), try(teststatus.TimedOut, 1)}, teststatus.RuleShared) {
		t.Fatal("shared suiteSpecOK must reject timedOut retry after pass")
	}
}
