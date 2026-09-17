package health

import (
	"strconv"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

func TestSustainedHealthUsesEachHistoricalWindow(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	p := triage.DefaultThresholds()
	p.MaxTrunkRuns = 5
	var all []triage.Observation
	for i := 0; i < 7; i++ {
		all = append(all, triage.Observation{IdentityID: "test", ReportGroupID: strconv.Itoa(i), Status: triage.StatusFailed, ObservedAt: now.Add(time.Duration(i-7) * time.Hour)})
	}
	s, class, since, refreshes := project(all, now, p)
	if class != classificationBroken || s.Runs != 5 || refreshes != 3 || !since.Equal(all[4].ObservedAt) {
		t.Fatalf("%+v class=%s since=%s refreshes=%d", s, class, since, refreshes)
	}
	_, again, againSince, againCount := project(all, now.Add(time.Minute), p)
	if again != class || !since.Equal(againSince) || againCount != refreshes {
		t.Fatal("repeated refresh changed the sustained run count")
	}
	// An execution retry in one group cannot advance the counter.
	retry := all[6]
	retry.AttemptIndex = 1
	retry.ObservedAt = retry.ObservedAt.Add(time.Minute)
	all = append(all, retry)
	_, _, _, retryCount := project(all, now, p)
	if retryCount != refreshes {
		t.Fatalf("retry advanced count to %d", retryCount)
	}
}

func TestProjectionRetiresWithoutReadingEveryOldFact(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	p := triage.DefaultThresholds()
	last := now.Add(-31 * 24 * time.Hour)
	s, class, since, count := project([]triage.Observation{{ReportGroupID: "old", Status: triage.StatusFailed, ObservedAt: last}}, now, p)
	if class != classificationRetired || s.Runs != 0 || count != 1 || !since.Equal(last.Add(30*24*time.Hour)) {
		t.Fatalf("%+v %s %s %d", s, class, since, count)
	}
}

func TestProjectionUsesWindowAtEachRefresh(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	p := triage.DefaultThresholds()
	p.WindowDays = 1
	p.MinTrunkRuns = 2
	all := []triage.Observation{
		{ReportGroupID: "a", Status: triage.StatusFailed, ObservedAt: now.Add(-48 * time.Hour)},
		{ReportGroupID: "b", Status: triage.StatusFailed, ObservedAt: now.Add(-47 * time.Hour)},
		{ReportGroupID: "c", Status: triage.StatusFailed, ObservedAt: now.Add(-3 * time.Hour)},
		{ReportGroupID: "d", Status: triage.StatusFailed, ObservedAt: now.Add(-2 * time.Hour)},
	}
	_, class, _, count := project(all, now, p)
	if class != classificationBroken || count != 1 {
		t.Fatalf("class=%s count=%d; old runs crossed window boundary", class, count)
	}
}
