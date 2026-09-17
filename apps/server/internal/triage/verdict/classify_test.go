package verdict

import (
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

const (
	otherExcerpt = "database unavailable"
	otherLocus   = "test.spec.ts:99"
)

func fixture() Inputs {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	pr := triage.Observation{IdentityID: "test", ReportGroupID: "pr", File: "test.spec.ts", FullTitle: "MM-T1 test", Status: triage.StatusFailed, ErrorSignature: "same", ErrorExcerpt: "assertion failed", FailureLocus: "test.spec.ts:42", Lane: "enterprise", ObservedAt: now, FirstSeenAt: now.Add(-30 * 24 * time.Hour)}
	in := Inputs{Now: now, Group: triage.Group{ID: "pr", Status: "completed", CreatedAt: now}, Policy: triage.Policy{Mode: "shadow", Thresholds: triage.DefaultThresholds()}, Lane: "enterprise", BaseRef: "master", BaseSHA: "base", PR: []triage.Observation{pr}, ChangedFiles: []string{}}
	for i := 0; i < 10; i++ {
		o := pr
		o.ID = string(rune('a' + i))
		o.ReportGroupID = o.ID
		o.BranchKind = triage.BranchTrunk
		o.Branch = "master"
		o.Status = triage.StatusPassed
		o.RetryCount = 0
		o.CommitSHA = o.ID
		o.ObservedAt = now.Add(-time.Duration(10-i) * time.Hour)
		o.GroupCreatedAt = o.ObservedAt
		in.Trunk = append(in.Trunk, o)
	}
	in.Trunk[5].CommitSHA = "base"
	return in
}

func TestOrderedRules(t *testing.T) {
	for _, tc := range []struct {
		name, class, overall string
		setup                func(*Inputs)
	}{
		{"infra always blocks", classInfra, resultActionRequired, func(in *Inputs) { in.PR[0].IsInfraStub = true; in.ChangedFiles = []string{"test.spec.ts"} }},
		{"owned file", "OWNED_BY_PR", resultFailure, func(in *Inputs) { in.ChangedFiles = []string{"test.spec.ts"} }},
		{"owned stack path", "OWNED_BY_PR", resultFailure, func(in *Inputs) {
			in.ChangedFiles = []string{"other/test.spec.ts"}
			in.PR[0].File = "unrelated.spec.ts"
		}},
		{"same basename elsewhere is not owned", classRegression, resultFailure, func(in *Inputs) {
			in.ChangedFiles = []string{"app/index.ts"}
			in.PR[0].File = "e2e-tests/playwright/specs/index.ts"
			in.PR[0].FailureLocus = "e2e-tests/playwright/specs/index.ts:7"
		}},
		{"quarantine", "QUARANTINED", resultSuccess, func(in *Inputs) {
			in.Trunk[0].Status = triage.StatusFailed
			in.Quarantine = []triage.Quarantine{{IdentityID: "test", BaseRef: "master", Status: "active"}}
		}},
		{"new test", "NEW_TEST", resultFailure, func(in *Inputs) { in.Trunk = nil; in.PR[0].FirstSeenAt = in.Group.CreatedAt }},
		{"insufficient", classInsufficient, resultFailure, func(in *Inputs) { in.Trunk = in.Trunk[:3] }},
		{"insufficient neutral", classInsufficient, "NEUTRAL", func(in *Inputs) { in.Trunk = in.Trunk[:3]; in.Policy.Thresholds.InsufficientDataPolicy = "neutral" }},
		{"broken after branching", classBrokenOnTrunk, resultSuccess, func(in *Inputs) { in.Trunk[8].Status = triage.StatusFailed; in.Trunk[9].Status = triage.StatusFailed }},
		{"broken before branching", classBrokenOnTrunk, resultSuccess, func(in *Inputs) {
			for i := 4; i < 10; i++ {
				in.Trunk[i].Status = triage.StatusFailed
			}
		}},
		{"different failure", "DIVERGENT", resultFailure, func(in *Inputs) {
			in.Trunk[8].Status = triage.StatusFailed
			in.Trunk[9].Status = triage.StatusFailed
			in.PR[0].ErrorSignature = "different"
			in.PR[0].ErrorExcerpt = otherExcerpt
			in.PR[0].FailureLocus = otherLocus
		}},
		{"flake", "FLAKY_CONFIRMED", resultSuccess, func(in *Inputs) { in.Trunk[0].Status = triage.StatusFailed }},
		{"suspicious flake", "FLAKY_SUSPICIOUS", resultFailure, func(in *Inputs) { in.Trunk[0].Status = triage.StatusFailed; in.PR[0].RetryCount = 1 }},
		{"regression", classRegression, resultFailure, func(*Inputs) {}},
		{"fix after base", classRegression, resultFailure, func(in *Inputs) { in.Trunk[4].Status = triage.StatusFailed; in.Trunk[5].Status = triage.StatusFailed }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fixture()
			tc.setup(&in)
			got := Classify(in)
			if got.Verdict != tc.overall || len(got.Findings) != 1 || got.Findings[0].Class != tc.class {
				t.Fatalf("got %+v", got)
			}
			if tc.name == "fix after base" && !strings.Contains(got.Findings[0].Reason, "rebase") {
				t.Fatal("missing rebase hint")
			}
		})
	}
}

func TestProbabilityBoundary(t *testing.T) {
	for _, tc := range []struct {
		fails, runs, m int
		class          string
	}{{0, 8, 1, "FLAKY_CONFIRMED"}, {0, 8, 2, "FLAKY_SUSPICIOUS"}, {2, 8, 2, "FLAKY_CONFIRMED"}, {2, 8, 3, "FLAKY_SUSPICIOUS"}} {
		in := fixture()
		in.BaseSHA = ""
		in.Trunk = in.Trunk[:tc.runs]
		in.Policy.Thresholds.FlakyMinRate = 0
		in.PR[0].RetryCount = tc.m - 1
		for i := 0; i < tc.fails; i++ {
			in.Trunk[i].Status = triage.StatusFailed
		}
		got := classifyTest(in, in.PR[0], tc.m, triage.Trials(in.Trunk, in.Now, in.Policy.Thresholds))
		if got.Class != tc.class {
			t.Errorf("%+v got %s", tc, got.Class)
		}
	}
}

func TestGuardsAndClusters(t *testing.T) {
	t.Run("stale cannot exonerate quarantine", func(t *testing.T) {
		in := fixture()
		in.Trunk[0].Status = triage.StatusFailed
		in.Quarantine = []triage.Quarantine{{IdentityID: "test", BaseRef: "master", Status: "active"}}
		in.Now = in.Now.Add(80 * time.Hour)
		if got := Classify(in); got.Verdict != resultActionRequired {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("confidence floor", func(t *testing.T) {
		in := fixture()
		in.Trunk[8].Status = triage.StatusFailed
		in.Trunk[9].Status = triage.StatusFailed // c=2 → confidence 2/3
		in.Policy.Thresholds.ConfidenceFloor = 0.7
		if got := Classify(in); got.Verdict != resultFailure || !strings.Contains(got.Reason, "LOW_CONFIDENCE") {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("unstable flake is exonerated, not low-confidence", func(t *testing.T) {
		in := fixture()
		in.BaseSHA = ""
		for i := 0; i < 5; i++ {
			in.Trunk[i].Status = triage.StatusFailed // raw 50%, r̂ = 6/12
		}
		got := Classify(in)
		if got.Verdict != resultSuccess || got.Findings[0].Class != "FLAKY_CONFIRMED" || got.Confidence < in.Policy.Thresholds.ConfidenceFloor {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("broken exoneration survives until the stale guard", func(t *testing.T) {
		in := fixture()
		in.Trunk[8].Status = triage.StatusFailed
		in.Trunk[9].Status = triage.StatusFailed
		in.Now = in.Trunk[9].ObservedAt.Add(60 * time.Hour) // past the old 48h decay, inside 72h
		if got := Classify(in); got.Verdict != resultSuccess {
			t.Fatalf("%+v", got)
		}
		in.Now = in.Trunk[9].ObservedAt.Add(73 * time.Hour)
		if got := Classify(in); got.Verdict != resultActionRequired {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("blocking findings outrank infra", func(t *testing.T) {
		in := fixture()
		infra := in.PR[0]
		infra.IdentityID = "stub"
		infra.IsInfraStub = true
		in.PR = append(in.PR, infra)
		if got := Classify(in); got.Verdict != resultFailure || got.Counts.Infra != 1 || got.Counts.Blocking != 2 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("incomplete", func(t *testing.T) {
		in := fixture()
		in.Group.Status = "incomplete"
		if got := Classify(in); got.Verdict != resultIncomplete {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("signature cluster", func(t *testing.T) {
		in := fixture()
		in.BaseSHA = ""
		in.Trunk[0].Status = triage.StatusFailed
		in.PR[0].ErrorSignature = "novel"
		in.PR[0].ErrorExcerpt = otherExcerpt
		in.PR[0].FailureLocus = otherLocus
		for _, id := range []string{"two", "three"} {
			o := in.PR[0]
			o.IdentityID = id
			in.PR = append(in.PR, o)
			for _, tr := range append([]triage.Observation(nil), in.Trunk[:10]...) {
				tr.IdentityID = id
				in.Trunk = append(in.Trunk, tr)
			}
		}
		got := Classify(in)
		for _, f := range got.Findings {
			if f.Class != "REGRESSION_CLUSTER" {
				t.Fatalf("%+v", got)
			}
		}
	})
	t.Run("drifted signature that matches trunk locus is not a cluster", func(t *testing.T) {
		in := fixture()
		in.BaseSHA = ""
		in.Trunk[8].Status = triage.StatusFailed
		in.Trunk[9].Status = triage.StatusFailed
		in.PR[0].ErrorSignature = "novel-hash-same-locus"
		for _, id := range []string{"two", "three"} {
			o := in.PR[0]
			o.IdentityID = id
			in.PR = append(in.PR, o)
			for _, tr := range append([]triage.Observation(nil), in.Trunk[:10]...) {
				tr.IdentityID = id
				in.Trunk = append(in.Trunk, tr)
			}
		}
		got := Classify(in)
		for _, f := range got.Findings {
			if f.Class != classBrokenOnTrunk {
				t.Fatalf("%+v", got)
			}
		}
	})
	t.Run("cross-PR flake when trunk is clean", func(t *testing.T) {
		in := fixture() // ten clean trunk runs
		in.CrossPR = map[string]triage.CrossPRStats{"test": {DistinctPRs: 3, Fails: 4}}
		got := Classify(in)
		if got.Verdict != resultSuccess || got.Findings[0].Class != classFlakyCrossPR {
			t.Fatalf("%+v", got)
		}
		in.CrossPR["test"] = triage.CrossPRStats{DistinctPRs: 2, Fails: 4}
		if got := Classify(in); got.Findings[0].Class != classRegression {
			t.Fatalf("two PRs must not be enough: %+v", got)
		}
		in.CrossPR["test"] = triage.CrossPRStats{DistinctPRs: 5, Fails: 9}
		in.Trunk = nil
		in.TrunkSeen = map[string]bool{"test": true}
		if got := Classify(in); got.Findings[0].Class != classInsufficient {
			t.Fatalf("insufficient trunk data still wins: %+v", got)
		}
		in = fixture()
		in.Policy.Thresholds.CrossPRMinPRs = 0
		in.CrossPR = map[string]triage.CrossPRStats{"test": {DistinctPRs: 9, Fails: 9}}
		if got := Classify(in); got.Findings[0].Class != classRegression {
			t.Fatalf("disabled rule must not exonerate: %+v", got)
		}
	})
	t.Run("signature recurring on other PRs is not a novel cluster", func(t *testing.T) {
		in := fixture()
		in.BaseSHA = ""
		in.PR[0].ErrorSignature = "novel"
		in.PR[0].ErrorExcerpt = otherExcerpt
		in.PR[0].FailureLocus = otherLocus
		in.CrossPR = map[string]triage.CrossPRStats{}
		for _, id := range []string{"test", "two", "three"} {
			in.CrossPR[id] = triage.CrossPRStats{DistinctPRs: 4, Fails: 4}
		}
		for _, id := range []string{"two", "three"} {
			o := in.PR[0]
			o.IdentityID = id
			in.PR = append(in.PR, o)
			for _, tr := range append([]triage.Observation(nil), in.Trunk[:10]...) {
				tr.IdentityID = id
				in.Trunk = append(in.Trunk, tr)
			}
		}
		in.CrossPRSignatures = map[string]int{"novel": 3}
		got := Classify(in)
		for _, f := range got.Findings {
			if f.Class != classFlakyCrossPR {
				t.Fatalf("%+v", got)
			}
		}
	})
	t.Run("area excess", func(t *testing.T) {
		in := fixture()
		in.BaseSHA = ""
		in.Trunk[0].Status = triage.StatusFailed
		o := in.PR[0]
		o.IdentityID = "two"
		o.ErrorSignature = "another"
		in.PR = append(in.PR, o)
		for _, tr := range append([]triage.Observation(nil), in.Trunk...) {
			tr.IdentityID = "two"
			tr.Status = triage.StatusPassed
			if tr.ReportGroupID == "b" {
				tr.Status = triage.StatusFailed
			}
			in.Trunk = append(in.Trunk, tr)
		}
		got := Classify(in)
		if got.Counts.Blocking != 1 || got.Findings[0].Class != "REGRESSION_AREA" {
			t.Fatalf("%+v", got)
		}
	})
}

func TestInputHashCanonical(t *testing.T) {
	in := fixture()
	in.ChangedFiles = []string{"b", "a"}
	first := InputsHash(in)
	in.ChangedFiles = []string{"a", "b", "a"}
	for i, j := 0, len(in.Trunk)-1; i < j; i, j = i+1, j-1 {
		in.Trunk[i], in.Trunk[j] = in.Trunk[j], in.Trunk[i]
	}
	if first != InputsHash(in) {
		t.Fatal("ordering changed hash")
	}
	in.Policy.Mode = "enforce"
	if first == InputsHash(in) {
		t.Fatal("policy did not change hash")
	}
}
