package reports

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func summaryQuery(overrides map[string]string) url.Values {
	q := url.Values{
		"repository":     {"mattermost"},
		"branch":         {"master"},
		"name":           {"playwright-full-enterprise-master"},
		"commit":         {"e8d166d8fcc6fdce43e7009dd1b1281bc288475e"},
		"gh_run_id":      {"41049591902"},
		"gh_run_attempt": {"1"},
	}
	for k, v := range overrides {
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
	}
	return q
}

func TestParseSummaryRequestDefaults(t *testing.T) {
	q := summaryQuery(nil)
	q.Add("compare_names", "playwright-full-fips-master")
	req, bad := parseSummaryRequest(q)
	if bad != "" {
		t.Fatalf("bad request: %s", bad)
	}
	if req.streakRuns != summaryDefaultStreakRuns || req.rateDays != summaryDefaultRateDays || req.page != 1 || req.perPage != summaryPerPage {
		t.Errorf("defaults = %+v", req)
	}
	if len(req.compareNames) != 1 || req.compareNames[0] != "playwright-full-fips-master" {
		t.Errorf("compare_names = %v", req.compareNames)
	}
}

func TestParseSummaryRequestAcceptsCommaSeparatedCompareNames(t *testing.T) {
	req, bad := parseSummaryRequest(summaryQuery(map[string]string{"compare_names": "a, b,a"}))
	if bad != "" || strings.Join(req.compareNames, ",") != "a,b" {
		t.Errorf("compare_names = %v (%s); want [a b]", req.compareNames, bad)
	}
}

func TestParseSummaryRequestRejects(t *testing.T) {
	for name, q := range map[string]url.Values{
		"missing repository": summaryQuery(map[string]string{"repository": ""}),
		"missing branch":     summaryQuery(map[string]string{"branch": ""}),
		"missing name":       summaryQuery(map[string]string{"name": ""}),
		"missing run id":     summaryQuery(map[string]string{"gh_run_id": ""}),
		"missing attempt":    summaryQuery(map[string]string{"gh_run_attempt": ""}),
		"short commit":       summaryQuery(map[string]string{"commit": "e8d16"}),
		"non-hex commit":     summaryQuery(map[string]string{"commit": "zzzzzzz"}),
		"attempt zero":       summaryQuery(map[string]string{"gh_run_attempt": "0"}),
		"streak too long":    summaryQuery(map[string]string{"streak_runs": "201"}),
		"window too long":    summaryQuery(map[string]string{"rate_days": "31"}),
		"page zero":          summaryQuery(map[string]string{"page": "0"}),
		"per_page zero":      summaryQuery(map[string]string{"per_page": "0"}),
		"per_page too large": summaryQuery(map[string]string{"per_page": "501"}),
		"too many compares":  summaryQuery(map[string]string{"compare_names": "a,b,c,d,e,f"}),
	} {
		if _, bad := parseSummaryRequest(q); bad == "" {
			t.Errorf("%s: accepted", name)
		}
	}
}

var (
	t0      = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	fileA   = "specs/functional/channels/classification.spec.ts"
	titleA1 = "Channel Classification › dropdown shows levels"
	titleA2 = "Channel Classification › banner text"
)

type fixture struct {
	rows []caseRow
}

func group(name, commit, runID string, at time.Time) runGroup {
	return runGroup{ID: uuid.New(), Name: name, CommitSHA: commit, GHRunID: runID, GHRunAttempt: "1", CreatedAt: at}
}

// add records one try of a test in a group's primary shard.
func (f *fixture) add(g runGroup, title, status string) {
	f.addShard(g, "playwright-chrome-1", title, status)
}

func (f *fixture) addShard(g runGroup, shard, title, status string) {
	// One report per group and shard label, so tries in the same shard are in-shard retries.
	reportID := uuid.NewSHA1(g.ID, []byte(shard))
	f.rows = append(f.rows, caseRow{GroupID: g.ID, ReportID: reportID, ShardLabel: shard, File: fileA, FullTitle: title, Status: status})
}

func TestSummarizeReportsOnlyTheRunsFailures(t *testing.T) {
	var f fixture
	run := group("lane", "c9", "r9", t0)
	f.add(run, titleA1, "failed")
	f.add(run, titleA1, "failed")
	f.add(run, titleA2, "passed")

	got := summarize([]runGroup{run}, nil, t0.Add(-7*24*time.Hour), nil, f.rows)
	if len(got) != 1 || got[0].FullTitle != titleA1 || got[0].StatusInRun != "failed" || got[0].File != fileA {
		t.Fatalf("summaries = %+v; want only the failed test", got)
	}
}

func TestSummarizeFlakyInRunIsIncluded(t *testing.T) {
	var f fixture
	run := group("lane", "c9", "r9", t0)
	f.add(run, titleA1, "failed")
	f.add(run, titleA1, "passed") // in-shard retry passed

	got := summarize([]runGroup{run}, nil, t0.Add(-7*24*time.Hour), nil, f.rows)
	if len(got) != 1 || got[0].StatusInRun != "flaky" {
		t.Fatalf("summaries = %+v; want one flaky test", got)
	}
}

func TestSummarizeStreakAndLastNotFailed(t *testing.T) {
	var f fixture
	run := group("lane", "c9", "r9", t0)
	f.add(run, titleA1, "failed")
	// Newest first: c8 failed, c7 didn't run the test, c6 failed, c5 passed, c4 failed.
	c8 := group("lane", "c8", "r8", t0.Add(-1*time.Hour))
	c7 := group("lane", "c7", "r7", t0.Add(-2*time.Hour))
	c6 := group("lane", "c6", "r6", t0.Add(-3*time.Hour))
	c5 := group("lane", "c5", "r5", t0.Add(-4*time.Hour))
	c4 := group("lane", "c4", "r4", t0.Add(-5*time.Hour))
	f.add(c8, titleA1, "failed")
	f.add(c6, titleA1, "timedOut")
	f.add(c5, titleA1, "passed")
	f.add(c4, titleA1, "failed")

	got := summarize([]runGroup{run}, []runGroup{c8, c7, c6, c5, c4}, t0.Add(-7*24*time.Hour), nil, f.rows)
	s := got[0]
	if s.EarlierConsecutiveFailures != 2 || s.FirstFailedCommit != "c6" {
		t.Errorf("streak = %d from %s; want 2 earlier failures from c6 (absent runs neither break nor extend it)",
			s.EarlierConsecutiveFailures, s.FirstFailedCommit)
	}
	if s.LastNotFailed == nil || s.LastNotFailed.CommitSHA != "c5" || s.LastNotFailed.Status != "passed" {
		t.Errorf("last not failed = %+v; want c5 passed", s.LastNotFailed)
	}
	if s.Window.Runs != 5 || s.Window.Failed != 3 || s.Window.Passed != 1 || s.Window.Absent != 1 {
		t.Errorf("window = %+v; want 5 runs: 3 failed, 1 passed, 1 absent", s.Window)
	}
}

func TestSummarizeFirstFailureHasNoEarlierStreak(t *testing.T) {
	var f fixture
	run := group("lane", "c9", "r9", t0)
	f.add(run, titleA1, "failed")
	prev := group("lane", "c8", "r8", t0.Add(-time.Hour))
	f.add(prev, titleA1, "flaky")

	s := summarize([]runGroup{run}, []runGroup{prev}, t0.Add(-7*24*time.Hour), nil, f.rows)[0]
	if s.EarlierConsecutiveFailures != 0 || s.FirstFailedCommit != "c9" {
		t.Errorf("streak = %d from %s; want 0 from the run's own commit", s.EarlierConsecutiveFailures, s.FirstFailedCommit)
	}
	if s.LastNotFailed == nil || s.LastNotFailed.Status != "flaky" {
		t.Errorf("last not failed = %+v; a flaky run didn't fail", s.LastNotFailed)
	}
}

func TestSummarizeWindowExcludesOlderRuns(t *testing.T) {
	var f fixture
	run := group("lane", "c9", "r9", t0)
	f.add(run, titleA1, "failed")
	recent := group("lane", "c8", "r8", t0.Add(-24*time.Hour))
	old := group("lane", "c1", "r1", t0.Add(-10*24*time.Hour))
	f.add(recent, titleA1, "passed")
	f.add(old, titleA1, "failed")

	s := summarize([]runGroup{run}, []runGroup{recent, old}, t0.Add(-7*24*time.Hour), nil, f.rows)[0]
	if s.Window.Runs != 1 || s.Window.Passed != 1 {
		t.Errorf("window = %+v; want only the run inside 7 days", s.Window)
	}
}

func TestSummarizeSameCommitResults(t *testing.T) {
	var f fixture
	run := group("playwright-full-enterprise-master", "c9", "r9", t0)
	f.add(run, titleA1, "failed")
	rerun := group("playwright-full-enterprise-master", "c9", "r10", t0.Add(time.Hour))
	fips := group("playwright-full-fips-master", "c9", "r9", t0)
	f.add(rerun, titleA1, "passed")

	s := summarize([]runGroup{run}, nil, t0.Add(-7*24*time.Hour), []runGroup{rerun, fips}, f.rows)[0]
	if len(s.SameCommit) != 2 {
		t.Fatalf("same commit = %+v", s.SameCommit)
	}
	want := map[string]string{"r10": "passed", "r9": "absent"}
	for _, r := range s.SameCommit {
		if r.Status != want[r.GHRunID] {
			t.Errorf("same commit %s (%s) = %s; want %s", r.Name, r.GHRunID, r.Status, want[r.GHRunID])
		}
	}
}

func TestSummarizePeerPlatformDivergenceIsAFailure(t *testing.T) {
	// The same rule as the consolidated report: pass on one primary shard and fail on another is failed.
	var f fixture
	run := group("lane", "c9", "r9", t0)
	f.addShard(run, "ios", titleA1, "failed")
	f.addShard(run, "android", titleA1, "passed")

	got := summarize([]runGroup{run}, nil, t0.Add(-7*24*time.Hour), nil, f.rows)
	if len(got) != 1 || got[0].StatusInRun != "failed" {
		t.Fatalf("summaries = %+v; want failed", got)
	}
}

func TestParseSummaryRequestPerPage(t *testing.T) {
	req, bad := parseSummaryRequest(summaryQuery(map[string]string{"per_page": "100"}))
	if bad != "" || req.perPage != 100 {
		t.Errorf("per_page = %d (%s); want 100", req.perPage, bad)
	}
}
