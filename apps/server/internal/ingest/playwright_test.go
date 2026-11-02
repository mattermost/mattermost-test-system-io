package ingest

import "testing"

func TestExtractPlaywright_RetryAttempts(t *testing.T) {
	type attempt struct {
		status string
		retry  int
		err    string
	}
	cases := []struct {
		name       string
		test       string
		attempts   []attempt
		wantCounts [4]int // passed, failed, flaky, unique
	}{
		{
			name:       "no retry",
			test:       `{"status":"expected","results":[{"status":"passed","retry":0}]}`,
			attempts:   []attempt{{"passed", 0, ""}},
			wantCounts: [4]int{1, 0, 0, 1},
		},
		{
			name:       "failed then passed on retry is flaky, and each attempt keeps its own status",
			test:       `{"status":"flaky","results":[{"status":"failed","retry":0,"errors":[{"message":"Error: first"}]},{"status":"passed","retry":1}]}`,
			attempts:   []attempt{{"failed", 0, "Error: first"}, {"passed", 1, ""}},
			wantCounts: [4]int{0, 0, 1, 1},
		},
		{
			name:       "timed out then passed on retry is flaky",
			test:       `{"status":"flaky","results":[{"status":"timedOut","retry":0,"errors":[{"message":"Test timeout of 30000ms exceeded."}]},{"status":"passed","retry":1}]}`,
			attempts:   []attempt{{"timedOut", 0, "Test timeout of 30000ms exceeded."}, {"passed", 1, ""}},
			wantCounts: [4]int{0, 0, 1, 1},
		},
		{
			name:       "failed on every attempt is one failure",
			test:       `{"status":"unexpected","results":[{"status":"failed","retry":0,"errors":[{"message":"Error: first"}]},{"status":"failed","retry":1,"errors":[{"message":"Error: second"}]}]}`,
			attempts:   []attempt{{"failed", 0, "Error: first"}, {"failed", 1, "Error: second"}},
			wantCounts: [4]int{0, 1, 0, 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seq := 0
			body := []byte(`{"suites":[{"title":"a.spec.ts","file":"a.spec.ts","specs":[{"title":"t","tests":[` + tc.test + `]}]}]}`)
			suites := extractPlaywright(body, &seq)
			if len(suites) != 1 {
				t.Fatalf("got %d suites, want 1", len(suites))
			}
			got := suites[0].Cases
			if len(got) != len(tc.attempts) {
				t.Fatalf("got %d cases, want %d", len(got), len(tc.attempts))
			}
			for i, want := range tc.attempts {
				c := got[i]
				if c.Status != want.status || c.RetryCount != want.retry {
					t.Errorf("attempt %d: status %q retry %d, want %q retry %d", i, c.Status, c.RetryCount, want.status, want.retry)
				}
				if want.err != "" && (c.ErrorMessage == nil || *c.ErrorMessage != want.err) {
					t.Errorf("attempt %d: error %v, want %q", i, c.ErrorMessage, want.err)
				}
			}
			passed, failed, _, flaky, unique := countStatuses(got)
			if [4]int{passed, failed, flaky, unique} != tc.wantCounts {
				t.Errorf("counts passed/failed/flaky/unique = %d/%d/%d/%d, want %v", passed, failed, flaky, unique, tc.wantCounts)
			}
		})
	}
}
