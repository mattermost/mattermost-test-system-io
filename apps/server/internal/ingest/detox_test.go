package ingest

import "testing"

func TestExtractDetox_RetryAttempts(t *testing.T) {
	type attempt struct {
		status string
		retry  int
		err    string
	}
	cases := []struct {
		name       string
		result     string
		attempts   []attempt
		wantCounts [4]int // passed, failed, flaky, unique
	}{
		{
			name:       "no retry",
			result:     `{"title":"t","fullName":"S t","ancestorTitles":["S"],"status":"passed","failureMessages":[]}`,
			attempts:   []attempt{{"passed", 0, ""}},
			wantCounts: [4]int{1, 0, 0, 1},
		},
		{
			name:       "failed then passed on retry is flaky, and the failure is kept",
			result:     `{"title":"t","fullName":"S t","ancestorTitles":["S"],"status":"passed","failureMessages":[],"retryReasons":["Error: element not found"]}`,
			attempts:   []attempt{{"failed", 0, "Error: element not found"}, {"passed", 1, ""}},
			wantCounts: [4]int{0, 0, 1, 1},
		},
		{
			name:       "failed on every attempt is one failure",
			result:     `{"title":"t","fullName":"S t","ancestorTitles":["S"],"status":"failed","failureMessages":["Error: second"],"retryReasons":["Error: first"]}`,
			attempts:   []attempt{{"failed", 0, "Error: first"}, {"failed", 1, "Error: second"}},
			wantCounts: [4]int{0, 1, 0, 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seq := 0
			body := []byte(`{"testResults":[{"testFilePath":"detox/e2e/test/a.e2e.ts","testResults":[` + tc.result + `]}]}`)
			suites := extractDetox(body, &seq)
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
				if c.FullTitle != "S t" || c.Sequence != i {
					t.Errorf("attempt %d: full title %q sequence %d", i, c.FullTitle, c.Sequence)
				}
			}
			passed, failed, _, flaky, unique := countStatuses(got)
			if [4]int{passed, failed, flaky, unique} != tc.wantCounts {
				t.Errorf("counts passed/failed/flaky/unique = %d/%d/%d/%d, want %v", passed, failed, flaky, unique, tc.wantCounts)
			}
		})
	}
}
