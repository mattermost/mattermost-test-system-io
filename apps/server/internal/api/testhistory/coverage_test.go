package testhistory

import (
	"fmt"
	"testing"
)

func TestClusterFailures_KeepsEveryMemberIdentity(t *testing.T) {
	msg := "element not visible"
	failures := make([]evidenceFailure, 301)
	for i := range failures {
		failures[i] = evidenceFailure{StableKey: fmt.Sprintf("MM-T%d", i), Status: "failed", ErrorMessage: &msg}
	}
	clusters, truncated := clusterFailures(failures)
	if len(clusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(clusters))
	}
	if truncated || len(clusters[0].Members) != len(failures) {
		t.Fatalf("lost failing identities: clusters=%d members=%d truncated=%v", len(clusters), len(clusters[0].Members), truncated)
	}
	for i, member := range clusters[0].Members {
		if member.StableKey != failures[i].StableKey {
			t.Fatalf("member %d = %q, want %q", i, member.StableKey, failures[i].StableKey)
		}
	}
}
