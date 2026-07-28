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
