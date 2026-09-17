package identity

import (
	"testing"

	"github.com/google/uuid"
)

func TestCollapseAttemptRetries(t *testing.T) {
	id := uuid.New()
	g := group{Repository: "mattermost/mattermost", Framework: "playwright"}
	cases := []observation{{AttemptID: &id, File: "a.ts", Title: "MM-T1 test", FullTitle: "[chromium] [admin] MM-T1 test", Status: retryFailed, Message: "assertion", Stack: "stack", DurationMS: 10}, {AttemptID: &id, File: "a.ts", Title: "MM-T1 test", FullTitle: "[chromium] [admin] MM-T1 test", Status: "passed", RetryCount: 1, DurationMS: 20}, {AttemptID: &id, File: "a.ts", Title: "MM-T1 test", FullTitle: "[chromium] [guest] MM-T1 test", Status: "passed"}}
	got := collapseAttemptRetries(g, cases)
	if len(got) != 2 || got[0].Status != retryFlaky || got[0].RetryCount != 1 || got[0].DurationMS != 30 || got[0].Message != "assertion" || got[0].Stack != "stack" {
		t.Fatalf("retry evidence lost: %+v", got)
	}
}

func TestSkippedRetryDoesNotEraseFailure(t *testing.T) {
	got := collapseAttemptRetries(group{Repository: "r", Framework: "playwright"}, []observation{
		{File: "a.ts", Title: "test", Status: retryFailed},
		{File: "a.ts", Title: "test", Status: "skipped", RetryCount: 1},
	})
	if len(got) != 1 || got[0].Status != retryFailed {
		t.Fatalf("skipped retry erased failed execution: %+v", got)
	}
}
