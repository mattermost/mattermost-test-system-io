package reports

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestCompareStatusRules_Validation(t *testing.T) {
	if _, err := CompareStatusRules(context.Background(), nil, StatusShadowOpts{}); err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestBuildOrchRunShadowDelta(t *testing.T) {
	id := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f1")

	t.Run("flaky pending unknown and skipped shifts", func(t *testing.T) {
		// Legacy: 2 passed (1 passed + 1 flaky-only), 1 failed, 0 flaky, 1 skipped, 6 total (pending+unknown in total only)
		// Shared: 1 passed, 2 failed (failed+unknown), 1 flaky, 2 skipped (skipped+pending), 6 total
		got := buildOrchRunShadowDelta(
			id, "mattermost/mattermost", "playwright", "100", "1",
			StatusCounts{Passed: 2, Failed: 1, Flaky: 0, Skipped: 1, Total: 6},
			StatusCounts{Passed: 1, Failed: 2, Flaky: 1, Skipped: 2, Total: 6},
		)
		if !got.Changed {
			t.Fatal("expected Changed = true")
		}
		if got.Delta.Passed != -1 || got.Delta.Failed != 1 || got.Delta.Flaky != 1 ||
			got.Delta.Skipped != 1 || got.Delta.Total != 0 {
			t.Fatalf("unexpected count deltas: %+v", got.Delta)
		}
		// Legacy rate: (2+0)*100/6 = 33; Shared rate: (1+1)*100/(1+1+2) = 50; Delta = +17
		if got.Legacy.PassRate == nil || *got.Legacy.PassRate != 33 {
			t.Fatalf("legacy PassRate = %v, want 33", got.Legacy.PassRate)
		}
		if got.Shared.PassRate == nil || *got.Shared.PassRate != 50 {
			t.Fatalf("shared PassRate = %v, want 50", got.Shared.PassRate)
		}
		if got.Delta.PassRate == nil || *got.Delta.PassRate != 17 {
			t.Fatalf("delta PassRate = %v, want 17", got.Delta.PassRate)
		}
	})

	t.Run("all skipped marks changed with nil shared rate", func(t *testing.T) {
		got := buildOrchRunShadowDelta(
			id, "mattermost/mattermost", "playwright", "101", "1",
			StatusCounts{Skipped: 4, Total: 4},
			StatusCounts{Skipped: 4, Total: 4},
		)
		if !got.Changed {
			t.Fatal("expected Changed = true when pass rate becomes unavailable")
		}
		if got.Legacy.PassRate == nil || *got.Legacy.PassRate != 0 {
			t.Fatalf("legacy PassRate = %v, want 0", got.Legacy.PassRate)
		}
		if got.Shared.PassRate != nil || got.Delta.PassRate != nil {
			t.Fatalf("expected nil shared/delta pass rate, got shared=%v delta=%v", got.Shared.PassRate, got.Delta.PassRate)
		}
	})

	t.Run("identical counts without skipped are unchanged", func(t *testing.T) {
		got := buildOrchRunShadowDelta(
			id, "mattermost/mattermost", "playwright", "102", "1",
			StatusCounts{Passed: 9, Failed: 1, Total: 10},
			StatusCounts{Passed: 9, Failed: 1, Total: 10},
		)
		if got.Changed {
			t.Fatalf("expected Changed = false, got %+v", got)
		}
		if got.Delta.PassRate == nil || *got.Delta.PassRate != 0 {
			t.Fatalf("delta PassRate = %v, want 0", got.Delta.PassRate)
		}
	})
}

func TestBuildReportGroupShadowDelta(t *testing.T) {
	id := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f2")

	got := buildReportGroupShadowDelta(id, "mattermost/mattermost", "cypress", "200", "1", &testStats{
		Passed: 8, Flaky: 1, Failed: 1, Skipped: 5, Total: 15,
	})
	if !got.Changed {
		t.Fatal("expected Changed = true")
	}
	if got.LegacyPassRate == nil || *got.LegacyPassRate != 60 {
		t.Fatalf("legacy rate = %v, want 60", got.LegacyPassRate)
	}
	if got.SharedPassRate == nil || *got.SharedPassRate != 90 {
		t.Fatalf("shared rate = %v, want 90", got.SharedPassRate)
	}
	if got.DeltaPassRate == nil || *got.DeltaPassRate != 30 {
		t.Fatalf("delta rate = %v, want 30", got.DeltaPassRate)
	}

	unchanged := buildReportGroupShadowDelta(id, "mattermost/mattermost", "cypress", "201", "1", &testStats{
		Passed: 9, Failed: 1, Total: 10,
	})
	if unchanged.Changed {
		t.Fatalf("expected Changed = false, got %+v", unchanged)
	}
}
