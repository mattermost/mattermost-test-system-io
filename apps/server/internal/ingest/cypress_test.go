package ingest

import "testing"

func TestExtractCypress_StatusesAndRetries(t *testing.T) {
	cases := []struct {
		name       string
		testJSON   string
		wantStatus string
		wantRetry  int
		wantFlaky  int
		wantPassed int
		wantFailed int
		wantSkip   int
	}{
		{
			name:       "single pass with boolean flag",
			testJSON:   `{"title":"passes","fullTitle":"suite passes","duration":12,"pass":true,"state":"passed"}`,
			wantStatus: StatusPassed,
			wantRetry:  0,
			wantPassed: 1,
		},
		{
			name:       "legacy state-only pass without booleans or attempts",
			testJSON:   `{"title":"legacy","fullTitle":"suite legacy","duration":10,"state":"passed"}`,
			wantStatus: StatusPassed,
			wantRetry:  0,
			wantPassed: 1,
		},
		{
			name:       "single fail with boolean flag",
			testJSON:   `{"title":"fails","fullTitle":"suite fails","duration":20,"fail":true,"state":"failed","err":{"message":"boom"}}`,
			wantStatus: StatusFailed,
			wantRetry:  0,
			wantFailed: 1,
		},
		{
			name:       "legacy state-only fail",
			testJSON:   `{"title":"legacy fail","fullTitle":"suite legacy fail","duration":20,"state":"failed"}`,
			wantStatus: StatusFailed,
			wantRetry:  0,
			wantFailed: 1,
		},
		{
			name:       "pending skipped",
			testJSON:   `{"title":"skip","fullTitle":"suite skip","duration":0,"pending":true,"state":"pending"}`,
			wantStatus: StatusSkipped,
			wantRetry:  0,
			wantSkip:   1,
		},
		{
			name:       "explicit skipped flag",
			testJSON:   `{"title":"skipped","fullTitle":"suite skipped","duration":0,"skipped":true}`,
			wantStatus: StatusSkipped,
			wantRetry:  0,
			wantSkip:   1,
		},
		{
			name:       "retried pass becomes flaky with retry_count",
			testJSON:   `{"title":"flaky","fullTitle":"suite flaky","duration":45,"pass":true,"state":"passed","attempts":[{"state":"failed"},{"state":"passed"}]}`,
			wantStatus: StatusFlaky,
			wantRetry:  1,
			wantFlaky:  1,
		},
		{
			name:       "retried pass state-only with 3 opaque attempts",
			testJSON:   `{"title":"flaky3","fullTitle":"suite flaky3","duration":60,"state":"passed","attempts":[{},{},{}]}`,
			wantStatus: StatusFlaky,
			wantRetry:  2,
			wantFlaky:  1,
		},
		{
			name:       "retried fail stays failed with retry_count",
			testJSON:   `{"title":"hard fail","fullTitle":"suite hard fail","duration":55,"fail":true,"state":"failed","attempts":[{},{}],"err":{"message":"still broken"}}`,
			wantStatus: StatusFailed,
			wantRetry:  1,
			wantFailed: 1,
		},
		{
			name:       "empty attempts array does not mark flaky",
			testJSON:   `{"title":"clean","fullTitle":"suite clean","duration":10,"pass":true,"state":"passed","attempts":[]}`,
			wantStatus: StatusPassed,
			wantRetry:  0,
			wantPassed: 1,
		},
		{
			name:       "single attempt array does not mark flaky",
			testJSON:   `{"title":"single","fullTitle":"suite single","duration":10,"pass":true,"state":"passed","attempts":[{}]}`,
			wantStatus: StatusPassed,
			wantRetry:  0,
			wantPassed: 1,
		},
		{
			name:       "opaque non-object attempts still count retries",
			testJSON:   `{"title":"opaque","fullTitle":"suite opaque","duration":15,"pass":true,"state":"passed","attempts":["a1",123]}`,
			wantStatus: StatusFlaky,
			wantRetry:  1,
			wantFlaky:  1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seq := 0
			body := []byte(`{"results":[{"title":"root","file":"cypress/e2e/login.cy.ts","suites":[{"title":"Login","tests":[` + tc.testJSON + `]}]}]}`)
			suites := extractCypress(body, &seq)
			if len(suites) != 1 || len(suites[0].Cases) != 1 {
				t.Fatalf("got %d suites, want 1 suite with 1 case", len(suites))
			}
			got := suites[0].Cases[0]
			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.RetryCount != tc.wantRetry {
				t.Errorf("retry_count = %d, want %d", got.RetryCount, tc.wantRetry)
			}
			passed, failed, skipped, flaky, unique := countStatuses(suites[0].Cases)
			if unique != 1 || passed != tc.wantPassed || failed != tc.wantFailed || skipped != tc.wantSkip || flaky != tc.wantFlaky {
				t.Errorf("counts p/f/s/fl/u = %d/%d/%d/%d/%d, want %d/%d/%d/%d/1",
					passed, failed, skipped, flaky, unique, tc.wantPassed, tc.wantFailed, tc.wantSkip, tc.wantFlaky)
			}
		})
	}
}

func TestExtractCypress_Errors(t *testing.T) {
	cases := []struct {
		name      string
		testJSON  string
		wantMsg   string
		wantStack string
	}{
		{
			name:      "estack preserved alongside message",
			testJSON:  `{"title":"t","fail":true,"state":"failed","err":{"message":"AssertionError: expected true","estack":"AssertionError: expected true\n    at Context.eval (login.cy.ts:12:5)"}}`,
			wantMsg:   "AssertionError: expected true",
			wantStack: "AssertionError: expected true\n    at Context.eval (login.cy.ts:12:5)",
		},
		{
			name:      "estack-only fallback populates both message and stack",
			testJSON:  `{"title":"t","fail":true,"state":"failed","err":{"message":"","estack":"Error: stack only\n    at line 1"}}`,
			wantMsg:   "Error: stack only\n    at line 1",
			wantStack: "Error: stack only\n    at line 1",
		},
		{
			name:      "stack fallback when estack absent",
			testJSON:  `{"title":"t","fail":true,"state":"failed","err":{"message":"timeout","stack":"Error: timeout\n    at line 9"}}`,
			wantMsg:   "timeout",
			wantStack: "Error: timeout\n    at line 9",
		},
		{
			name:      "attempt-level err fallback when top-level err empty",
			testJSON:  `{"title":"t","pass":true,"state":"passed","err":{},"attempts":[{"err":{"message":"first try failed","estack":"Error: first try failed\n    at attempt 0"}},{}]}`,
			wantMsg:   "first try failed",
			wantStack: "Error: first try failed\n    at attempt 0",
		},
		{
			name:      "attempt-level singular error fallback when top-level err nil",
			testJSON:  `{"title":"t","pass":true,"state":"passed","attempts":[{"error":{"message":"singular err","stack":"Error: singular err\n    at attempt 0"}},{}]}`,
			wantMsg:   "singular err",
			wantStack: "Error: singular err\n    at attempt 0",
		},
		{
			name:      "partial top-level message preserved and missing stack filled from attempts",
			testJSON:  `{"title":"t","fail":true,"state":"failed","err":{"message":"timeout"},"attempts":[{"err":{"message":"ignored msg"}},{"error":{"stack":"Error: timeout\n    at attempt 1"}}]}`,
			wantMsg:   "timeout",
			wantStack: "Error: timeout\n    at attempt 1",
		},
		{
			name:      "partial top-level estack preserved and missing message filled from attempt",
			testJSON:  `{"title":"t","fail":true,"state":"failed","err":{"estack":"Error: top stack\n    at top"},"attempts":[{"err":{"message":"attempt msg","estack":"Error: attempt stack"}}]}`,
			wantMsg:   "attempt msg",
			wantStack: "Error: top stack\n    at top",
		},
		{
			name:      "complete top-level error unchanged when attempts also have errors",
			testJSON:  `{"title":"t","fail":true,"state":"failed","err":{"message":"top msg","estack":"top stack"},"attempts":[{"err":{"message":"attempt msg","estack":"attempt stack"}}]}`,
			wantMsg:   "top msg",
			wantStack: "top stack",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seq := 0
			body := []byte(`{"results":[{"title":"root","file":"a.cy.ts","tests":[` + tc.testJSON + `]}]}`)
			suites := extractCypress(body, &seq)
			if len(suites) != 1 || len(suites[0].Cases) != 1 {
				t.Fatalf("got %d suites, want 1 suite with 1 case", len(suites))
			}
			got := suites[0].Cases[0]
			if got.ErrorMessage == nil || *got.ErrorMessage != tc.wantMsg {
				t.Errorf("error_message = %v, want %q", got.ErrorMessage, tc.wantMsg)
			}
			if got.ErrorStack == nil || *got.ErrorStack != tc.wantStack {
				t.Errorf("error_stack = %v, want %q", got.ErrorStack, tc.wantStack)
			}
		})
	}
}
