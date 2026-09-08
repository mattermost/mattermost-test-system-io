package testhistory

import "testing"

// summarize computes every number /tests/history reports about a test — the
// rates a consumer decides on and the two commits it would bisect between —
// and it was reachable only through e2e-tagged tests, which need a database
// and are not run on every change. These are the cases that would silently
// produce a wrong answer.

// series builds entries newest-first from outcomes given newest-first, so a
// case reads in the same order the API returns.
func series(outcomes ...string) []historyEntry {
	entries := make([]historyEntry, len(outcomes))
	for i, o := range outcomes {
		entries[i] = historyEntry{
			Outcome: o,
			// Commits descend with age so a wrong pick is obvious.
			Commit: []string{"c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7"}[i],
		}
	}
	return entries
}

func TestSummarize_NoRuns(t *testing.T) {
	s := summarize(nil)
	if s.Runs != 0 {
		t.Errorf("runs = %d, want 0", s.Runs)
	}
	// A rate over zero runs is undefined, and reporting 0 for it would read as
	// "never fails" — the most dangerous wrong answer this function can give.
	if s.FailureRate != 0 || s.FlakeRate != 0 {
		t.Errorf("rates = (%v, %v), want (0, 0) for an empty series", s.FailureRate, s.FlakeRate)
	}
	if s.Flips != 0 {
		t.Errorf("flips = %d, want 0", s.Flips)
	}
	if s.LastPassCommit != nil {
		t.Errorf("last_pass_commit = %q, want nil", *s.LastPassCommit)
	}
	if s.FailingSinceCommit != nil {
		t.Errorf("failing_since_commit = %q, want nil", *s.FailingSinceCommit)
	}
	// Series must marshal as [] rather than null: a consumer iterating it
	// should not have to special-case the empty history.
	if s.Series == nil {
		t.Error("series = nil, want an empty slice")
	}
	if len(s.Series) != 0 {
		t.Errorf("series = %v, want empty", s.Series)
	}
}

func TestSummarize_AllPasses(t *testing.T) {
	s := summarize(series(outcomePassed, outcomePassed, outcomePassed))
	if s.Runs != 3 || s.Passed != 3 {
		t.Errorf("runs/passed = %d/%d, want 3/3", s.Runs, s.Passed)
	}
	if s.FailureRate != 0 || s.FlakeRate != 0 {
		t.Errorf("rates = (%v, %v), want (0, 0)", s.FailureRate, s.FlakeRate)
	}
	if s.Flips != 0 {
		t.Errorf("flips = %d, want 0", s.Flips)
	}
	if s.LastPassCommit == nil || *s.LastPassCommit != "c0" {
		t.Errorf("last_pass_commit = %v, want c0 (the newest run)", s.LastPassCommit)
	}
	// Nothing is failing, so there is no streak to name.
	if s.FailingSinceCommit != nil {
		t.Errorf("failing_since_commit = %q, want nil when the newest run passed", *s.FailingSinceCommit)
	}
}

func TestSummarize_AllFailures(t *testing.T) {
	s := summarize(series(outcomeFailed, outcomeFailed, outcomeFailed))
	if s.Runs != 3 || s.Failed != 3 {
		t.Errorf("runs/failed = %d/%d, want 3/3", s.Runs, s.Failed)
	}
	if s.FailureRate != 1 {
		t.Errorf("failure_rate = %v, want 1", s.FailureRate)
	}
	if s.FlakeRate != 0 {
		t.Errorf("flake_rate = %v, want 0 — never passing is not flaky", s.FlakeRate)
	}
	if s.Flips != 0 {
		t.Errorf("flips = %d, want 0 — an unbroken streak has no transitions", s.Flips)
	}
	// This is the case the whole function exists to get right. The test has
	// never passed inside the window, so there is no commit where it broke:
	// the honest answer is "no range", and naming the oldest run in the
	// window would point a bisect at a commit that was already failing.
	if s.LastPassCommit != nil {
		t.Errorf("last_pass_commit = %q, want nil — no pass in the window", *s.LastPassCommit)
	}
	if s.FailingSinceCommit == nil {
		t.Fatal("failing_since_commit = nil, want the oldest run in the streak")
	}
	if *s.FailingSinceCommit != "c2" {
		t.Errorf("failing_since_commit = %q, want c2", *s.FailingSinceCommit)
	}
}

// TestSummarize_StreakReachingTheStartOfTheWindow is the trap in the case
// above, stated on its own: when the streak runs off the oldest edge of the
// window, failing_since_commit names the oldest commit the window HAS, which
// is a floor on the answer and not the answer. A consumer that treats it as
// the breaking commit bisects the wrong range. It is only a real breaking
// point when a pass precedes it — and then last_pass_commit is non-nil.
func TestSummarize_StreakReachingTheStartOfTheWindow(t *testing.T) {
	unbounded := summarize(series(outcomeFailed, outcomeFailed, outcomeFailed))
	if unbounded.LastPassCommit != nil {
		t.Fatalf("last_pass_commit = %q, want nil", *unbounded.LastPassCommit)
	}

	bounded := summarize(series(outcomeFailed, outcomeFailed, outcomePassed))
	if bounded.LastPassCommit == nil || *bounded.LastPassCommit != "c2" {
		t.Errorf("last_pass_commit = %v, want c2", bounded.LastPassCommit)
	}
	if bounded.FailingSinceCommit == nil || *bounded.FailingSinceCommit != "c1" {
		t.Errorf("failing_since_commit = %v, want c1 — the first run after the last pass",
			bounded.FailingSinceCommit)
	}
	// The pair is only a bisectable range in the bounded case. Callers can
	// tell the two apart by last_pass_commit being present.
	if unbounded.FailingSinceCommit == nil {
		t.Error("failing_since_commit should still be reported as a floor")
	}
}

func TestSummarize_AlternatingFlips(t *testing.T) {
	// newest → oldest: pass, fail, pass, fail. Three adjacent transitions.
	s := summarize(series(outcomePassed, outcomeFailed, outcomePassed, outcomeFailed))
	if s.Runs != 4 {
		t.Errorf("runs = %d, want 4", s.Runs)
	}
	if s.Flips != 3 {
		t.Errorf("flips = %d, want 3", s.Flips)
	}
	if s.Passed != 2 || s.Failed != 2 {
		t.Errorf("passed/failed = %d/%d, want 2/2", s.Passed, s.Failed)
	}
	if s.FailureRate != 0.5 {
		t.Errorf("failure_rate = %v, want 0.5", s.FailureRate)
	}
	if s.LastPassCommit == nil || *s.LastPassCommit != "c0" {
		t.Errorf("last_pass_commit = %v, want c0", s.LastPassCommit)
	}
	if s.FailingSinceCommit != nil {
		t.Errorf("failing_since_commit = %q, want nil — newest run passed", *s.FailingSinceCommit)
	}
}

// TestSummarize_FlakyCountsAsFailureButNotAsAFlip pins the two places a flaky
// run is treated asymmetrically, both deliberate: it did not cleanly pass, so
// it counts against failure_rate; and it is folded to "failed" for flip
// counting so a flake does not read as two transitions.
func TestSummarize_FlakyCountsAsFailureButNotAsAFlip(t *testing.T) {
	s := summarize(series(outcomeFlaky, outcomeFlaky, outcomePassed))
	if s.Runs != 3 || s.Flaky != 2 || s.Passed != 1 {
		t.Errorf("runs/flaky/passed = %d/%d/%d, want 3/2/1", s.Runs, s.Flaky, s.Passed)
	}
	wantFailure := 2.0 / 3.0
	if s.FailureRate != wantFailure {
		t.Errorf("failure_rate = %v, want %v", s.FailureRate, wantFailure)
	}
	if s.FlakeRate != wantFailure {
		t.Errorf("flake_rate = %v, want %v", s.FlakeRate, wantFailure)
	}
	// oldest→newest is pass, flaky, flaky → one transition.
	if s.Flips != 1 {
		t.Errorf("flips = %d, want 1", s.Flips)
	}
	// A flaky run did pass, eventually, so it bounds the failing streak.
	if s.LastPassCommit == nil || *s.LastPassCommit != "c0" {
		t.Errorf("last_pass_commit = %v, want c0 — a flaky run passed on retry", s.LastPassCommit)
	}
}

// TestSummarize_SkippedRunsAreExcludedFromRates guards the denominator. A
// skipped run says nothing about stability, so counting it would dilute every
// rate toward zero and make a consistently broken test look intermittent.
func TestSummarize_SkippedRunsAreExcludedFromRates(t *testing.T) {
	s := summarize(series(outcomeFailed, outcomeSkipped, outcomeSkipped, outcomeFailed))
	if s.Skipped != 2 {
		t.Errorf("skipped = %d, want 2", s.Skipped)
	}
	if s.Runs != 2 {
		t.Errorf("runs = %d, want 2 — skipped runs are excluded", s.Runs)
	}
	if s.FailureRate != 1 {
		t.Errorf("failure_rate = %v, want 1 — both executed runs failed", s.FailureRate)
	}
	// The skips sit between two failures and must not read as transitions.
	if s.Flips != 0 {
		t.Errorf("flips = %d, want 0 — a skip is carried over, not counted", s.Flips)
	}
	// Nor may a skip break the failing streak: the test has been failing since
	// the oldest run, and the skips in between change nothing about that.
	if s.FailingSinceCommit == nil || *s.FailingSinceCommit != "c3" {
		t.Errorf("failing_since_commit = %v, want c3", s.FailingSinceCommit)
	}
	if s.LastPassCommit != nil {
		t.Errorf("last_pass_commit = %q, want nil", *s.LastPassCommit)
	}
}

// TestSummarize_SeriesIsNewestFirst pins the order, since every other field is
// documented relative to it.
func TestSummarize_SeriesIsNewestFirst(t *testing.T) {
	s := summarize(series(outcomePassed, outcomeFailed, outcomeFlaky))
	want := []string{outcomePassed, outcomeFailed, outcomeFlaky}
	if len(s.Series) != len(want) {
		t.Fatalf("series = %v, want %v", s.Series, want)
	}
	for i := range want {
		if s.Series[i] != want[i] {
			t.Errorf("series[%d] = %q, want %q", i, s.Series[i], want[i])
		}
	}
}

// TestSummarize_SingleRun is the thin-history case a decision rule has to
// recognize rather than act on: one observation makes failure_rate 1.0, which
// is arithmetically correct and evidentially worthless.
func TestSummarize_SingleRun(t *testing.T) {
	s := summarize(series(outcomeFailed))
	if s.Runs != 1 {
		t.Errorf("runs = %d, want 1", s.Runs)
	}
	if s.FailureRate != 1 {
		t.Errorf("failure_rate = %v, want 1", s.FailureRate)
	}
	if s.Flips != 0 {
		t.Errorf("flips = %d, want 0 — one run has no adjacent pair", s.Flips)
	}
	if s.FailingSinceCommit == nil || *s.FailingSinceCommit != "c0" {
		t.Errorf("failing_since_commit = %v, want c0", s.FailingSinceCommit)
	}
}
