package triage

import "testing"

func TestNormalizeError_stripsVolatileTokens(t *testing.T) {
	a := normalizeError("Timed out after 15234ms waiting for 0xdeadbeef id=018f0000-0000-7000-8000-000000000001")
	b := normalizeError("Timed out after 99ms waiting for 0xabc id=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if a != b {
		t.Fatalf("signatures diverged:\n  %q\n  %q", a, b)
	}
	if a != "timed out after <n>ms waiting for <hex> id=<id>" {
		t.Fatalf("normalized = %q", a)
	}
}

func TestClusterFailures_collapsesIdenticalCauses(t *testing.T) {
	msg := "element not visible: Join Call"
	failures := make([]evidenceFailure, 0, 300)
	for i := 0; i < 300; i++ {
		m := msg
		failures = append(failures, evidenceFailure{
			FullTitle:    "suite › test",
			Status:       "failed",
			ErrorMessage: &m,
		})
	}
	// One different cause should stay its own cluster.
	other := "ENOENT: no such file"
	failures = append(failures, evidenceFailure{
		FullTitle:    "suite › other",
		Status:       "failed",
		ErrorMessage: &other,
	})

	got, truncated := clusterFailures(failures)
	if truncated {
		t.Fatal("two causes should not truncate")
	}
	if len(got) != 2 {
		t.Fatalf("clusters = %d, want 2", len(got))
	}
	if got[0].MemberCount != 300 {
		t.Fatalf("largest cluster = %d, want 300", got[0].MemberCount)
	}
	if got[1].MemberCount != 1 {
		t.Fatalf("second cluster = %d, want 1", got[1].MemberCount)
	}
}

func TestClusterFailures_collapsesVolatileTokens(t *testing.T) {
	a := "timeout waiting for 018f0000-0000-7000-8000-000000000001 after 15234ms"
	b := "timeout waiting for aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee after 99ms"
	got, truncated := clusterFailures([]evidenceFailure{
		{FullTitle: "t1", Status: "failed", ErrorMessage: &a},
		{FullTitle: "t2", Status: "failed", ErrorMessage: &b},
	})
	if truncated {
		t.Fatal("two equivalent errors should not truncate")
	}
	if len(got) != 1 {
		t.Fatalf("clusters = %d, want 1", len(got))
	}
	if got[0].MemberCount != 2 {
		t.Fatalf("members = %d, want 2", got[0].MemberCount)
	}
}

func TestClusterFailures_capsClusterCount(t *testing.T) {
	var failures []evidenceFailure
	for i := 0; i < maxClusters+5; i++ {
		msg := "unique cause " + string(rune('A'+i))
		failures = append(failures, evidenceFailure{
			FullTitle:    msg,
			Status:       "failed",
			ErrorMessage: &msg,
		})
	}
	got, truncated := clusterFailures(failures)
	if !truncated {
		t.Fatal("expected truncation past maxClusters")
	}
	if len(got) != maxClusters {
		t.Fatalf("clusters = %d, want cap %d", len(got), maxClusters)
	}
}
