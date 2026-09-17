package triage

import (
	"sort"
	"time"
)

// Trials collapses executions to one observation per group, oldest first.
// Retries that eventually pass count as flaky, never multiple independent runs.
func Trials(observations []Observation, now time.Time, thresholds Thresholds) []Observation {
	ordered := append([]Observation(nil), observations...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.ReportGroupID != b.ReportGroupID {
			return a.ReportGroupID < b.ReportGroupID
		}
		// Chronological within a run; attempt_index only breaks ties (see terminal).
		if !a.ObservedAt.Equal(b.ObservedAt) {
			return a.ObservedAt.Before(b.ObservedAt)
		}
		if a.AttemptIndex != b.AttemptIndex {
			return a.AttemptIndex < b.AttemptIndex
		}
		return a.ID < b.ID
	})
	byGroup := map[string]Observation{}
	failed := map[string]bool{}
	cutoff := time.Time{}
	if thresholds.WindowDays > 0 {
		cutoff = now.Add(-time.Duration(thresholds.WindowDays) * 24 * time.Hour)
	}
	for _, o := range ordered {
		if o.IsInfraStub || o.Status == StatusSkipped || o.ObservedAt.Before(cutoff) || o.ObservedAt.After(now) {
			continue
		}
		failed[o.ReportGroupID] = failed[o.ReportGroupID] || IsFailure(o.Status) || o.Status == StatusFlaky
		if o.Status == StatusPassed && failed[o.ReportGroupID] {
			o.Status = StatusFlaky
		}
		byGroup[o.ReportGroupID] = o
	}
	out := make([]Observation, 0, len(byGroup))
	for _, o := range byGroup {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if RunTime(out[i]).Equal(RunTime(out[j])) {
			return out[i].ReportGroupID < out[j].ReportGroupID
		}
		return RunTime(out[i]).Before(RunTime(out[j]))
	})
	if len(out) > thresholds.MaxTrunkRuns {
		out = out[len(out)-thresholds.MaxTrunkRuns:]
	}
	return out
}

// Summarize consumes chronological trials for exactly one identity and lane.
func Summarize(trials []Observation) Stats {
	s := Stats{Runs: len(trials)}
	sigs := map[string]int{}
	loci := map[string]int{}
	excerpts := map[string]string{}
	for i, o := range trials {
		s.LatestAt = o.ObservedAt
		switch {
		case IsFailure(o.Status):
			s.Fails++
			s.ConsecutiveFails++
			s.ConsecutivePasses = 0
			s.LastFailAt = &o.ObservedAt
			s.LastFailSHA = o.CommitSHA
			if o.ErrorSignature != "" {
				sigs[o.ErrorSignature]++
				excerpts[o.ErrorSignature] = o.ErrorExcerpt
			}
			if o.FailureLocus != "" {
				loci[o.FailureLocus]++
			}
		case o.Status == StatusFlaky:
			s.Flaky++
			s.ConsecutiveFails = 0
			s.ConsecutivePasses = 0
		case o.Status == StatusPassed:
			s.Passes++
			s.ConsecutiveFails = 0
			s.ConsecutivePasses++
			s.LastPassAt = &o.ObservedAt
			s.LastPassSHA = o.CommitSHA
			if i >= len(trials)-10 {
				s.RecentPass = true
			}
		}
	}
	s.InstabilityRate = float64(s.Fails+s.Flaky+1) / float64(s.Runs+2)
	s.DominantSignature = dominant(sigs)
	s.DominantLocus = dominant(loci)
	s.DominantExcerpt = excerpts[s.DominantSignature]
	return s
}

func dominant(counts map[string]int) string {
	best := ""
	n := 0
	for value, count := range counts {
		if count > n || (count == n && value < best) {
			best = value
			n = count
		}
	}
	return best
}

// RunTime orders trunk commits by report creation, unaffected by upload/retest delay.
func RunTime(o Observation) time.Time {
	if !o.GroupCreatedAt.IsZero() {
		return o.GroupCreatedAt
	}
	return o.ObservedAt
}
