package verdict

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

func TestTerminalExecutions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcomes []triage.Observation
		status   string
		fails    int
	}{
		{"skipped retest preserves failure", []triage.Observation{{Status: triage.StatusFailed}, {Status: triage.StatusSkipped, AttemptIndex: 1}}, triage.StatusFailed, 1},
		{"passed retest is flaky", []triage.Observation{{Status: triage.StatusFailed, RetryCount: 1}, {Status: triage.StatusPassed, AttemptIndex: 1}}, triage.StatusFlaky, 2},
		{"independent failed executions", []triage.Observation{{Status: triage.StatusFailed, RetryCount: 1}, {Status: triage.StatusFailed, RetryCount: 2, AttemptIndex: 1}}, triage.StatusFailed, 5},
		{"same attempt is not counted twice", []triage.Observation{{Status: triage.StatusFailed, RetryCount: 1, AttemptID: "same"}, {Status: triage.StatusFailed, RetryCount: 2, AttemptID: "same", AttemptIndex: 1}}, triage.StatusFailed, 3},
		{"infra survives pass", []triage.Observation{{Status: triage.StatusFailed, IsInfraStub: true}, {Status: triage.StatusPassed, AttemptIndex: 1}}, triage.StatusFailed, 1},
		{"infra survives skip", []triage.Observation{{Status: triage.StatusFailed, IsInfraStub: true}, {Status: triage.StatusSkipped, AttemptIndex: 1}}, triage.StatusFailed, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, m := terminal(tc.outcomes)
			if got.Status != tc.status || m != tc.fails {
				t.Fatalf("terminal = %+v, failures=%d", got, m)
			}
			if tc.outcomes[0].IsInfraStub && !got.IsInfraStub {
				t.Fatal("infra evidence disappeared")
			}
		})
	}
}

func TestTerminalOrdersByTimeNotInsertion(t *testing.T) {
	base := time.Date(2026, 9, 16, 7, 0, 0, 0, time.UTC)
	got, m := terminal([]triage.Observation{
		{Status: triage.StatusPassed, AttemptIndex: 0, ObservedAt: base.Add(6 * time.Minute)}, // retest shard, inserted first
		{Status: triage.StatusFailed, AttemptIndex: 1, ObservedAt: base},
		{Status: triage.StatusFailed, AttemptIndex: 2, RetryCount: 1, ObservedAt: base},
	})
	if got.Status != triage.StatusFlaky || m != 3 {
		t.Fatalf("retest that passed last must be flaky: %+v m=%d", got, m)
	}
}

func TestNewTestUsesHistoryInsteadOfUploadTime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		seen  bool
		class string
	}{
		{"test began before group registration", false, "NEW_TEST"},
		{"retired identity remains known", true, classInsufficient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fixture()
			in.Trunk = nil
			in.PR[0].FirstSeenAt = in.Group.CreatedAt.Add(-time.Hour)
			in.TrunkSeen = map[string]bool{"test": tc.seen}
			got := Classify(in)
			if got.Findings[0].Class != tc.class {
				t.Fatalf("%+v", got)
			}
		})
	}
	t.Run("different lane means insufficient", func(t *testing.T) {
		in := fixture()
		for i := range in.Trunk {
			in.Trunk[i].Lane = "fips"
		}
		if got := Classify(in); got.Findings[0].Class != classInsufficient {
			t.Fatalf("%+v", got)
		}
	})
}

func TestFileLevelFailureCannotDisappearBehindPassingTests(t *testing.T) {
	in := fixture()
	in.PR[0].Status = triage.StatusPassed
	in.UnrepresentedFailures = []string{"setup.spec.ts"}
	got := Classify(in)
	if got.Verdict != resultActionRequired || got.Counts.Infra != 1 || got.Counts.Passed != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestChronologyUsesGroupCreationAndFallback(t *testing.T) {
	in := fixture()
	in.Trunk[4].Status = triage.StatusFailed
	in.Trunk[5].Status = triage.StatusFailed
	// The older failed run uploaded late, after the actual fix's observation.
	in.Trunk[5].ObservedAt = in.Now.Add(-time.Minute)
	if got := Classify(in); got.Findings[0].Class != classRegression || !strings.Contains(got.Findings[0].Reason, "rebase") {
		t.Fatalf("%+v", got)
	}
	in.BaseSHA = "merge-base-with-no-run"
	in.Group.CreatedAt = in.Trunk[5].GroupCreatedAt.Add(time.Minute)
	if got := Classify(in); got.Findings[0].Class != classRegression || !strings.Contains(got.Findings[0].Reason, "rebase") {
		t.Fatalf("fallback: %+v", got)
	}
}

func TestHashTracksDecisionTimeExactly(t *testing.T) {
	t.Run("unchanged fresh evidence reuses audit", func(t *testing.T) {
		in := fixture()
		in.Trunk[0].Status = triage.StatusFailed
		first := InputsHash(in)
		in.Now = in.Now.Add(time.Minute)
		if first != InputsHash(in) {
			t.Fatal("fresh polling created a different audit identity")
		}
	})
	t.Run("aging inside the stale window reuses audit", func(t *testing.T) {
		in := fixture()
		in.Trunk[8].Status = triage.StatusFailed
		in.Trunk[9].Status = triage.StatusFailed
		in.Now = in.Now.Add(49 * time.Hour)
		first := InputsHash(in)
		in.Now = in.Now.Add(time.Minute)
		if first != InputsHash(in) {
			t.Fatal("confidence no longer depends on age; hash must be stable")
		}
	})
	t.Run("stale transition within an hour", func(t *testing.T) {
		in := fixture()
		in.Trunk[8].Status = triage.StatusFailed
		in.Trunk[9].Status = triage.StatusFailed
		in.Now = in.Trunk[9].ObservedAt.Add(72*time.Hour - time.Second)
		first := InputsHash(in)
		in.Now = in.Now.Add(2 * time.Second)
		if first == InputsHash(in) || Classify(in).Verdict != resultActionRequired {
			t.Fatal("stale boundary lost")
		}
	})
}

func TestInfraRetestCannotBeExonerated(t *testing.T) {
	in := fixture()
	in.PR[0].IsInfraStub = true
	later := in.PR[0]
	later.IsInfraStub = false
	later.Status = triage.StatusPassed
	later.AttemptIndex = 1
	in.PR = append(in.PR, later)
	if got := Classify(in); got.Verdict != resultActionRequired || got.Counts.Infra != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestRetestFlakeDoesNotBlock(t *testing.T) {
	in := fixture()
	later := in.PR[0]
	later.Status = triage.StatusPassed
	later.AttemptIndex = 1
	in.PR = append(in.PR, later)
	if got := Classify(in); got.Verdict != resultSuccess || got.Counts.Flaky != 1 || got.Counts.Failed != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestSignatureSimilarityAndAbsentEvidence(t *testing.T) {
	if SignatureMatch("", "", "", "", "", "") {
		t.Fatal("missing signatures matched")
	}
	if !SignatureMatch("a", "expected member in public channel", "", "b", "expected user member in public channel", "") {
		t.Fatal("nearby messages failed Jaccard match")
	}
	if SignatureMatch("a", "timeout", "file:1", "b", "assertion mismatch", "file:2") {
		t.Fatal("unrelated errors matched")
	}
}

func TestConfidenceIsBounded(t *testing.T) {
	in := fixture()
	in.Trunk = nil
	in.Quarantine = []triage.Quarantine{{IdentityID: "test", BaseRef: "master", Status: "active"}}
	in.Policy.Thresholds.AreaSlack = 1
	got := Classify(in)
	if got.Verdict != resultActionRequired || math.IsNaN(got.Confidence) || got.Confidence < 0 || got.Confidence > 1 {
		t.Fatalf("%+v", got)
	}
}
