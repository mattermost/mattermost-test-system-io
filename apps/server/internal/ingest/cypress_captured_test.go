package ingest

import (
	"os"
	"strings"
	"testing"
)

// This report was produced by a real Cypress 15.18.0 Electron browser run,
// Mochawesome 7.1.4, Mattermost's capture hooks, and the TSIO dispatcher.
// Regeneration instructions accompany the source fixture in the action.
func TestCypressCapturedBrowserRetry(t *testing.T) {
	body, err := os.ReadFile("testdata/cypress_retry_enriched.json")
	if err != nil {
		t.Fatal(err)
	}
	seq := 0
	cases := flatCases(extractCypress(body, &seq))
	if len(cases) != 4 {
		t.Fatalf("got %d attempt rows, want 4 (retry, clean pass, pending)", len(cases))
	}
	failed, passed := cases[0], cases[1]
	if failed.Title != "fails once then passes" || passed.Title != failed.Title {
		t.Fatalf("unexpected captured test titles: %q, %q", failed.Title, passed.Title)
	}
	if failed.Status != StatusFailed || passed.Status != StatusPassed {
		t.Fatalf("got %s then %s, want failed then passed", failed.Status, passed.Status)
	}
	for _, c := range cases[:2] {
		if got := rollupOf(t, c); got != (runRollup{2, 1, false}) {
			t.Errorf("incorrect retry rollup: %+v", got)
		}
		if c.DurationMs <= 0 {
			t.Error("captured attempt duration was lost")
		}
	}
	if failed.ErrorMessage == nil || !strings.Contains(*failed.ErrorMessage, "retry contract") || failed.ErrorStack == nil {
		t.Fatal("failed attempt lost its captured error/stack")
	}
	if passed.ErrorMessage != nil || passed.RetryCount != 1 {
		t.Fatal("passing retry has an error or incorrect retry index")
	}
	if len(failed.Attachments) != 1 || len(passed.Attachments) != 0 {
		t.Fatal("screenshot must belong only to the attempt that failed")
	}
	if cases[2].Status != StatusPassed || cases[3].Status != StatusSkipped {
		t.Fatal("clean pass or skipped test changed meaning")
	}
}
