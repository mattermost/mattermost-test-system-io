package triage

import (
	"testing"
	"time"
)

func TestTrialsCountDistinctRunsAndPreserveFlakes(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	p := DefaultThresholds()
	p.MaxTrunkRuns = 2
	all := []Observation{
		{ReportGroupID: "old", Status: StatusFailed, ObservedAt: now.Add(-3 * time.Hour)},
		{ReportGroupID: "first", Status: StatusFailed, ObservedAt: now.Add(-2 * time.Hour), GroupCreatedAt: now.Add(-2 * time.Hour)},
		{ReportGroupID: "first", Status: StatusPassed, AttemptIndex: 1, ObservedAt: now.Add(-time.Minute), GroupCreatedAt: now.Add(-2 * time.Hour)},
		{ReportGroupID: "last", Status: StatusPassed, ObservedAt: now.Add(-time.Hour), GroupCreatedAt: now.Add(-time.Hour)},
	}
	got := Trials(all, now, p)
	if len(got) != 2 || got[0].ReportGroupID != "first" || got[0].Status != StatusFlaky || got[1].ReportGroupID != "last" {
		t.Fatalf("%+v", got)
	}
	stats := Summarize(got)
	if stats.Runs != 2 || stats.Flaky != 1 || stats.Passes != 1 || stats.ConsecutiveFails != 0 {
		t.Fatalf("%+v", stats)
	}
}

func TestTrialsExcludeInvalidEvidence(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	p := DefaultThresholds()
	all := []Observation{
		{ReportGroupID: "infra", Status: StatusFailed, ObservedAt: now, IsInfraStub: true},
		{ReportGroupID: "old", Status: StatusFailed, ObservedAt: now.Add(-15 * 24 * time.Hour)},
		{ReportGroupID: "future", Status: StatusFailed, ObservedAt: now.Add(time.Minute)},
		{ReportGroupID: "skipped", Status: StatusSkipped, ObservedAt: now},
		{ReportGroupID: "ok", Status: StatusPassed, ObservedAt: now},
	}
	got := Trials(all, now, p)
	if len(got) != 1 || got[0].ReportGroupID != "ok" {
		t.Fatalf("%+v", got)
	}
}

func TestSkippedRetestDoesNotEraseFailure(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	got := Trials([]Observation{{ReportGroupID: "run", Status: StatusFailed, ObservedAt: now}, {ReportGroupID: "run", Status: StatusSkipped, AttemptIndex: 1, ObservedAt: now}}, now, DefaultThresholds())
	if len(got) != 1 || got[0].Status != StatusFailed {
		t.Fatalf("%+v", got)
	}
}
