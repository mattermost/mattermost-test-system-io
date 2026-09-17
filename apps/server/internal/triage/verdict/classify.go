package verdict

import (
	"fmt"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

// Classify evaluates the ten ordered rules and then the cross-test guards.
func Classify(in Inputs) Verdict {
	v := Verdict{ReportGroupID: in.Group.ID, Mode: in.Policy.Mode, Verdict: resultSuccess, Confidence: 1, ComputedAt: in.Now, EngineVersion: triage.EngineVersion, ThresholdsUsed: in.Policy.Thresholds, Findings: []Finding{}}
	trunk := map[string][]triage.Observation{}
	for _, o := range in.Trunk {
		if o.BranchKind == triage.BranchTrunk && o.Branch == in.BaseRef && o.Lane == in.Lane && !o.IsInfraStub {
			trunk[o.IdentityID] = append(trunk[o.IdentityID], o)
		}
	}
	pr := map[string][]triage.Observation{}
	for _, o := range in.PR {
		if o.Lane == in.Lane {
			pr[o.IdentityID] = append(pr[o.IdentityID], o)
		}
	}
	ids := make([]string, 0, len(pr))
	for id := range pr {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o, m := terminal(pr[id])
		switch o.Status {
		case triage.StatusPassed:
			v.Counts.Passed++
			continue
		case triage.StatusFlaky:
			v.Counts.Flaky++
			continue
		case triage.StatusSkipped:
			v.Counts.Skipped++
			continue
		}
		trials := triage.Trials(trunk[id], in.Now, in.Policy.Thresholds)
		v.Findings = append(v.Findings, classifyTest(in, o, m, trials))
	}
	for _, file := range in.UnrepresentedFailures {
		v.Findings = append(v.Findings, Finding{File: file, FullTitle: "Failed orchestration unit without test evidence", Lane: in.Lane, Class: classInfra, Blocking: true, Reason: "The unit failed without a terminal failing test observation; inspect the file-level error and rerun.", PR: PRStats{FailCount: 1}})
	}
	for _, file := range in.Abandoned {
		v.Findings = append(v.Findings, Finding{File: file, FullTitle: "Abandoned orchestration unit", Lane: in.Lane, Class: classInfra, Blocking: true, Reason: "Worker did not complete this unit; rerun the job.", PR: PRStats{FailCount: 1}})
	}
	applyClusters(&v, in, trunk)
	applyAreas(&v, in, trunk)
	finish(&v, in)
	v.Markdown = Render(v)
	return v
}

// terminal orders a test's executions within one run chronologically (a
// retest that passed after the first failure makes the test flaky, not
// failed). attempt_index only breaks ties: it follows insertion order, which
// is not guaranteed to be chronological for imported or late-uploaded shards.
func terminal(observations []triage.Observation) (triage.Observation, int) {
	ordered := append([]triage.Observation(nil), observations...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if !a.ObservedAt.Equal(b.ObservedAt) {
			return a.ObservedAt.Before(b.ObservedAt)
		}
		if a.AttemptIndex != b.AttemptIndex {
			return a.AttemptIndex < b.AttemptIndex
		}
		return a.ID < b.ID
	})
	executions := map[string]int{}
	everFailed := false
	for _, o := range ordered {
		if !triage.IsFailure(o.Status) {
			continue
		}
		everFailed = true
		key := o.AttemptID
		if key == "" {
			key = strconv.Itoa(o.AttemptIndex)
		}
		executions[key] = max(executions[key], o.RetryCount+1)
	}
	m := 0
	for _, n := range executions {
		m += n
	}
	last := ordered[len(ordered)-1]
	for _, o := range ordered {
		// A skipped retest is not proof that an earlier execution recovered.
		if o.Status != triage.StatusSkipped {
			last = o
		}
	}
	for _, o := range ordered {
		// Infrastructure evidence cannot be erased by a subsequent test pass.
		if o.IsInfraStub {
			o.Status = triage.StatusFailed
			return o, max(1, m)
		}
	}
	if last.Status == triage.StatusPassed && everFailed {
		last.Status = triage.StatusFlaky
	}
	return last, max(1, m)
}

func classifyTest(in Inputs, o triage.Observation, m int, trials []triage.Observation) Finding {
	t := in.Policy.Thresholds
	s := triage.Summarize(trials)
	match := SignatureMatch(o.ErrorSignature, o.ErrorExcerpt, o.FailureLocus, s.DominantSignature, s.DominantExcerpt, s.DominantLocus)
	f := Finding{IdentityID: o.IdentityID, MMTID: o.MMTID, File: o.File, FullTitle: o.FullTitle, Lane: o.Lane, Blocking: true, Trunk: s, PR: PRStats{FailCount: m, ErrorExcerpt: o.ErrorExcerpt, FailureLocus: o.FailureLocus, SignatureMatch: match}, Signature: o.ErrorSignature, Confidence: 1, Links: Links{TSIOCaseURL: "/reports/g/" + in.Group.ID, TSIOHistoryURL: "/triage/tests/" + o.IdentityID}}
	switch {
	case o.IsInfraStub:
		f.Class = classInfra
		f.Reason = "CI infrastructure failure; rerun the job."
	case owned(o, in.ChangedFiles):
		f.Class = "OWNED_BY_PR"
		f.Reason = "The PR changes this test or a file named in its failure stack."
	default:
		return classifyEvidence(in, o, f, trials, t, match)
	}
	return f
}

func classifyEvidence(in Inputs, o triage.Observation, f Finding, trials []triage.Observation, t triage.Thresholds, match bool) Finding {
	for _, q := range in.Quarantine {
		if q.IdentityID == o.IdentityID && q.BaseRef == in.BaseRef && q.Status == "active" && (q.Lane == nil || *q.Lane == in.Lane) && (q.ExpiresAt == nil || q.ExpiresAt.After(in.Now)) {
			f.Class = "QUARANTINED"
			f.Blocking = false
			f.Reason = "Active " + q.Source + " quarantine: " + q.Reason
			f.Links.QuarantineID = q.ID
			f.Links.IssueURL = q.IssueURL
			return f
		}
	}
	s := f.Trunk
	switch {
	case s.Runs == 0 && !seenOnTrunk(in, o.IdentityID):
		f.Class = "NEW_TEST"
		f.Reason = "First seen in this PR with no trunk history; a renamed test must pass too."
	case s.Runs < t.MinTrunkRuns || (!s.LatestAt.IsZero() && in.Now.Sub(s.LatestAt) > time.Duration(t.RetireDays)*24*time.Hour):
		f.Class = classInsufficient
		f.Blocking = t.InsufficientDataPolicy != "neutral"
		f.Reason = fmt.Sprintf("Only %d trunk runs; need %d.", s.Runs, t.MinTrunkRuns)
	case s.ConsecutiveFails >= t.BrokenStreak && match:
		f.Class = classBrokenOnTrunk
		f.Blocking = false
		f.Confidence = float64(s.ConsecutiveFails) / float64(s.ConsecutiveFails+1)
		f.Reason = fmt.Sprintf("Matching failure on %d consecutive trunk runs.", s.ConsecutiveFails)
	case s.ConsecutiveFails >= t.BrokenStreak && !match:
		f.Class = "DIVERGENT"
		f.Reason = "Trunk is broken here, but this failure has a different signature and locus."
	case fixedAfterBase(trials, baseTime(in), t.BrokenStreak):
		f.Class = classRegression
		f.Reason = "Trunk passed at " + s.LastPassSHA + " after your base; rebase to include the fix."
	case float64(s.Fails+s.Flaky)/float64(s.Runs) >= t.FlakyMinRate && s.RecentPass:
		probability := math.Pow(s.InstabilityRate, float64(f.PR.FailCount))
		// Confidence is evidence sufficiency (more trunk runs → a better rate
		// estimate), mirroring the broken-streak c/(c+1). It is deliberately not
		// 1-p: a highly unstable test failing on a PR is *more* explainable.
		f.Confidence = float64(s.Runs) / float64(s.Runs+1)
		if probability+1e-12 >= t.PMin {
			f.Class = "FLAKY_CONFIRMED"
			f.Blocking = false
		} else {
			f.Class = "FLAKY_SUSPICIOUS"
		}
		f.Reason = fmt.Sprintf("%d failing executions have probability %.4f at trunk instability %.4f; threshold %.4f.", f.PR.FailCount, probability, s.InstabilityRate, t.PMin)
	case crossPRFlaky(in, o.IdentityID, s, t):
		c := in.CrossPR[o.IdentityID]
		f.Class = classFlakyCrossPR
		f.Blocking = false
		f.Confidence = float64(c.DistinctPRs) / float64(c.DistinctPRs+1)
		f.Reason = fmt.Sprintf("Failed on %d other PRs in the window while passing on trunk (%d runs, %d fails); the failure is environmental, not this PR's.", c.DistinctPRs, s.Runs, s.Fails)
	default:
		f.Class = classRegression
		f.Reason = "Trunk evidence does not explain this failure."
	}
	return f
}

// crossPRFlaky is the evidence trunk cannot provide: a test that passes on
// trunk but fails on several unrelated PRs is flaky in the PR environment.
// It requires at least one pass in the window (trunk or another PR) so a test
// that is simply broken everywhere is never exonerated by this rule.
func crossPRFlaky(in Inputs, id string, s triage.Stats, t triage.Thresholds) bool {
	if t.CrossPRMinPRs <= 0 {
		return false
	}
	c, ok := in.CrossPR[id]
	return ok && c.DistinctPRs >= t.CrossPRMinPRs && (s.Passes > 0 || c.Passes > 0)
}

func seenOnTrunk(in Inputs, id string) bool {
	if in.TrunkSeen[id] {
		return true
	}
	for _, o := range in.Trunk {
		if o.IdentityID == id && o.BranchKind == triage.BranchTrunk && !o.IsInfraStub {
			return true
		}
	}
	return false
}

func baseTime(in Inputs) time.Time {
	if !in.BaseTime.IsZero() {
		return in.BaseTime
	}
	var exact, older time.Time
	for _, o := range in.Trunk {
		at := triage.RunTime(o)
		if o.CommitSHA == in.BaseSHA && at.After(exact) {
			exact = at
		}
		if !at.After(in.Group.CreatedAt) && at.After(older) {
			older = at
		}
	}
	if !exact.IsZero() {
		return exact
	}
	// ASSUMPTION: when the merge-base has no run, PR creation is the only
	// supplied time anchor; choose the nearest older completed trunk run.
	return older
}

func fixedAfterBase(trials []triage.Observation, pivot time.Time, brokenStreak int) bool {
	if pivot.IsZero() || len(trials) == 0 || trials[len(trials)-1].Status != triage.StatusPassed {
		return false
	}
	streak, brokenAtBase := 0, false
	for _, o := range trials {
		if triage.RunTime(o).After(pivot) {
			if brokenAtBase && o.Status == triage.StatusPassed {
				return true
			}
			continue
		}
		if triage.IsFailure(o.Status) {
			streak++
		} else {
			streak = 0
		}
		brokenAtBase = streak >= brokenStreak
	}
	return false
}

func owned(o triage.Observation, changed []string) bool {
	locus := o.FailureLocus
	if i := strings.LastIndex(locus, ":"); i >= 0 {
		locus = locus[:i]
	}
	locus = path.Clean(strings.ReplaceAll(locus, `\`, "/"))
	for _, file := range changed {
		file = path.Clean(strings.ReplaceAll(file, `\`, "/"))
		// Full-path match only (either side may carry a workspace prefix); a
		// basename match would let any changed index.ts "own" unrelated failures.
		if samePath(file, o.File) || (locus != "." && samePath(file, locus)) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	if a == "" || b == "" || a == "." || b == "." {
		return false
	}
	return a == b || strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}

// SignatureMatch tolerates wording drift but never matches absent evidence.
func SignatureMatch(sig, excerpt, locus, otherSig, otherExcerpt, otherLocus string) bool {
	if sig != "" && sig == otherSig {
		return true
	}
	if locus != "" && locus == otherLocus {
		return true
	}
	tokens := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, word := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
			out[word] = true
		}
		return out
	}
	a, b := tokens(excerpt), tokens(otherExcerpt)
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	overlap := 0
	for word := range a {
		if b[word] {
			overlap++
		}
	}
	return float64(overlap)/float64(len(a)+len(b)-overlap) >= .7
}

func finish(v *Verdict, in Inputs) {
	for _, f := range v.Findings {
		v.Counts.Failed++
		if f.Blocking {
			v.Counts.Blocking++
		}
		if f.Class == classInfra {
			v.Counts.Infra++
		}
		if !f.Blocking && f.Class != classInsufficient {
			v.Counts.Exonerated++
			v.Confidence = math.Min(v.Confidence, f.Confidence)
		}
		if f.Class == classInsufficient && !f.Blocking {
			v.Verdict = "NEUTRAL"
		}
	}
	newest := in.LatestTrunkAt
	for _, o := range in.Trunk {
		if o.BranchKind == triage.BranchTrunk && !o.IsInfraStub && o.Lane == in.Lane && o.Branch == in.BaseRef && !o.ObservedAt.After(in.Now) && o.ObservedAt.After(newest) {
			newest = o.ObservedAt
		}
	}
	// Precedence, weakest first: stale/infra → ACTION_REQUIRED, then any
	// blocking finding → FAILURE (a real regression must never hide behind an
	// infra stub), then INCOMPLETE. Freshness is enforced only by the stale
	// guard; it no longer erodes confidence, so a broken-trunk exoneration is
	// valid right up to stale_trunk_hours (e.g. across a weekend).
	if v.Counts.Exonerated > 0 {
		age := in.Now.Sub(newest)
		if newest.IsZero() || age > time.Duration(in.Policy.Thresholds.StaleTrunkHours)*time.Hour {
			v.Verdict = resultActionRequired
			v.Reason = "Trunk evidence is stale; rerun trunk before exonerating PR failures."
		}
	}
	if v.Counts.Infra > 0 {
		v.Verdict = resultActionRequired
		v.Reason = "Infrastructure failures require a rerun."
	}
	if v.Counts.Exonerated > 0 && v.Confidence < in.Policy.Thresholds.ConfidenceFloor {
		v.Verdict = resultFailure
		v.Reason = "LOW_CONFIDENCE: the evidence is below the configured confidence floor."
	}
	// Infra findings are always blocking; only *non-infra* blocking findings
	// turn ACTION_REQUIRED into FAILURE.
	if v.Counts.Blocking-v.Counts.Infra > 0 {
		v.Verdict = resultFailure
		if v.Counts.Infra > 0 {
			v.Reason = "Blocking failures plus infrastructure failures; fix the regressions and rerun."
		}
	}
	if in.Group.Status != "completed" {
		v.Verdict = resultIncomplete
		v.Reason = "The report group did not finish; rerun missing work."
	}
	sort.SliceStable(v.Findings, func(i, j int) bool {
		if v.Findings[i].Blocking != v.Findings[j].Blocking {
			return v.Findings[i].Blocking
		}
		return v.Findings[i].IdentityID < v.Findings[j].IdentityID
	})
}
