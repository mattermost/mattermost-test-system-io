package ingest

import (
	"testing"
	"time"
)

// The reports dispatch-run's deadlineLease (.github/actions/test-system-io-dispatch-run/src/spawn.ts) writes
// for a lease killed at the worker deadline, byte for byte. They hold no test: the shard still uploads, so the
// report group can complete, and the killed specs' results come from the worker that retests them.
const (
	deadlinePlaywrightReport = `{"config":{},"suites":[],"errors":[],"stats":{"startTime":"1970-01-01T00:00:01.000Z","duration":60000,"expected":0,"unexpected":0,"flaky":0,"skipped":0},"killedAtDeadline":{"reason":"killed at worker deadline (deadline-minutes); spec did not finish in this worker","specs":["a.spec.ts"]}}`
	deadlineCypressReport    = `{"stats":{"suites":0,"tests":0,"passes":0,"pending":0,"failures":0,"start":"1970-01-01T00:00:01.000Z","end":"1970-01-01T00:01:01.000Z","duration":60000},"results":[],"killedAtDeadline":{"reason":"killed at worker deadline (deadline-minutes); spec did not finish in this worker","specs":["a.spec.ts"]}}`
)

func TestDeadlineReportsHoldNoSuites(t *testing.T) {
	for _, tc := range []struct {
		framework, body string
		wantStart       bool
	}{
		{"playwright", deadlinePlaywrightReport, false},
		{"cypress", deadlineCypressReport, true},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			seq := 0
			suites, start, end := Extract(tc.framework, []byte(tc.body), &seq)
			if len(suites) != 0 {
				t.Fatalf("Extract(%s) = %d suites, want none", tc.framework, len(suites))
			}
			// The finalizer retries an empty parse with auto-detection (reports/stateless.go extractReport):
			// that must find nothing either, so no other parser invents a suite from the report.
			if again, _, _ := Extract("", []byte(tc.body), &seq); len(again) != 0 {
				t.Fatalf("auto-detected %d suites, want none", len(again))
			}
			if seq != 0 {
				t.Fatalf("case ordinal advanced to %d for a report with no case", seq)
			}
			if !tc.wantStart {
				if start != nil || end != nil {
					t.Fatalf("playwright report stats gave start=%v end=%v, want none", start, end)
				}
				return
			}
			if start == nil || !start.Equal(time.Unix(1, 0)) || end == nil || !end.Equal(time.Unix(61, 0)) {
				t.Fatalf("cypress report stats gave start=%v end=%v", start, end)
			}
		})
	}
}
