package ingest

import "testing"

// Playwright and Cypress used to disagree about what a "failure" was.
// Playwright wrote one row per attempt but stamped every one of them with the
// test's rolled-up status, so a retry-survivor produced two 'flaky' rows and
// no record of which attempt failed. Cypress wrote a single final-state row
// with a hardcoded retry_count of 0, so the same run produced one 'passed'
// row and no record that a retry happened at all.
//
// Both CI configs retry once (playwright.config.ts `retries: isCI ? 1 : 0`,
// cypress.config.ts `retries.runMode: 1`), so this is the shape of every
// retried test in production, and any rate computed across both frameworks
// was comparing a per-run number against a per-attempt one.
//
// These tests pin the one shape both parsers now produce.

// runRollup is the triple every attempt row of one run carries.
type runRollup struct {
	attempts       int
	attemptsFailed int
	runFailed      bool
}

func rollupOf(t *testing.T, c ExtractedCase) runRollup {
	t.Helper()
	return runRollup{c.Attempts, c.AttemptsFailed, c.RunFailed}
}

// flatCases collects every case across the extracted suites, in order.
func flatCases(suites []ExtractedSuite) []ExtractedCase {
	var out []ExtractedCase
	for _, s := range suites {
		out = append(out, s.Cases...)
	}
	return out
}

// oneFailOnePassPlaywright is a Playwright JSON report for a single test that
// failed on its first attempt and passed on the retry — the exact output
// `retries: 1` produces. Playwright labels the test itself "flaky".
const oneFailOnePassPlaywright = `{
  "suites": [
    {
      "title": "tests/channels/sidebar.spec.ts",
      "file": "tests/channels/sidebar.spec.ts",
      "specs": [
        {
          "title": "renders the sidebar",
          "tests": [
            {
              "projectName": "chrome",
              "status": "flaky",
              "results": [
                {"status": "failed", "duration": 900,
                 "errors": [{"message": "expected sidebar to be visible"}], "retry": 0},
                {"status": "passed", "duration": 400, "retry": 1}
              ]
            }
          ]
        }
      ]
    }
  ]
}`

// oneFailOnePassCypress is a synthetic TSIO extension payload for focused unit
// tests, not native Mochawesome output. The real producer acceptance fixture
// is exercised by TestCypressCapturedBrowserRetry below.
const oneFailOnePassCypress = `{
  "stats": {"start": "2026-01-01T00:00:00.000Z"},
  "results": [
    {
      "title": "root",
      "file": "tests/integration/channels/sidebar_spec.js",
      "fullFile": "tests/integration/channels/sidebar_spec.js",
      "suites": [],
      "tests": [
        {
          "title": "renders the sidebar",
          "fullTitle": "channels renders the sidebar",
          "duration": 400,
          "state": "passed",
          "pass": true,
          "attempts": [
            {"state": "failed", "fail": true, "duration": 900,
             "err": {"message": "expected sidebar to be visible"}},
            {"state": "passed", "pass": true, "duration": 400}
          ]
        }
      ]
    }
  ]
}`

// TestRetrySemantics_BothParsersAgreeOnOneFailOnePass checks the parser contract:
// the same run, reported by two frameworks, must produce the same run-level
// rollup. It is the whole reason the three columns exist — without it, no
// rate can be computed across both frameworks.
func TestRetrySemantics_BothParsersAgreeOnOneFailOnePass(t *testing.T) {
	want := runRollup{attempts: 2, attemptsFailed: 1, runFailed: false}

	pwSeq, cySeq := 0, 0
	pw := flatCases(extractPlaywright([]byte(oneFailOnePassPlaywright), &pwSeq))
	cy := flatCases(extractCypress([]byte(oneFailOnePassCypress), &cySeq))

	if len(pw) != 2 {
		t.Fatalf("playwright rows = %d, want 2 (one per attempt)", len(pw))
	}
	if len(cy) != 2 {
		t.Fatalf("cypress rows = %d, want 2 (one per attempt)", len(cy))
	}
	for i, c := range pw {
		if got := rollupOf(t, c); got != want {
			t.Errorf("playwright row %d rollup = %+v, want %+v", i, got, want)
		}
	}
	for i, c := range cy {
		if got := rollupOf(t, c); got != want {
			t.Errorf("cypress row %d rollup = %+v, want %+v", i, got, want)
		}
	}
}

// TestRetrySemantics_PlaywrightKeepsPerAttemptTruth guards the regression that
// started this: mapPlaywrightStatus used to return StatusFlaky for every
// attempt of a flaky test, so the failing attempt and the passing attempt were
// stored identically and attempts_failed could not be recovered from the rows.
func TestRetrySemantics_PlaywrightKeepsPerAttemptTruth(t *testing.T) {
	seq := 0
	cases := flatCases(extractPlaywright([]byte(oneFailOnePassPlaywright), &seq))
	if len(cases) != 2 {
		t.Fatalf("rows = %d, want 2", len(cases))
	}
	if cases[0].Status != StatusFailed {
		t.Errorf("attempt 0 status = %q, want %q", cases[0].Status, StatusFailed)
	}
	if cases[1].Status != StatusPassed {
		t.Errorf("attempt 1 status = %q, want %q", cases[1].Status, StatusPassed)
	}
	for i, want := range []int{0, 1} {
		if cases[i].RetryCount != want {
			t.Errorf("attempt %d retry_count = %d, want %d", i, cases[i].RetryCount, want)
		}
	}
	// The failing attempt owns the error; the passing one does not.
	if cases[0].ErrorMessage == nil {
		t.Error("attempt 0 has no error message, want the failure's")
	}
	if cases[1].ErrorMessage != nil {
		t.Errorf("attempt 1 error message = %q, want none", *cases[1].ErrorMessage)
	}
	// No row stores the rolled-up verdict: 'flaky' is derived from the run.
	for i, c := range cases {
		if c.Status == StatusFlaky {
			t.Errorf("attempt %d status = %q, want a per-attempt status", i, c.Status)
		}
	}
}

// TestRetrySemantics_CypressKeepsPerAttemptTruth is the Cypress half.
func TestRetrySemantics_CypressKeepsPerAttemptTruth(t *testing.T) {
	seq := 0
	cases := flatCases(extractCypress([]byte(oneFailOnePassCypress), &seq))
	if len(cases) != 2 {
		t.Fatalf("rows = %d, want 2 (one per attempt), got %d", 2, len(cases))
	}
	if cases[0].Status != StatusFailed {
		t.Errorf("attempt 0 status = %q, want %q", cases[0].Status, StatusFailed)
	}
	if cases[1].Status != StatusPassed {
		t.Errorf("attempt 1 status = %q, want %q", cases[1].Status, StatusPassed)
	}
	for i, want := range []int{0, 1} {
		if cases[i].RetryCount != want {
			t.Errorf("attempt %d retry_count = %d, want %d", i, cases[i].RetryCount, want)
		}
	}
	if cases[0].ErrorMessage == nil {
		t.Error("attempt 0 has no error message, want the failure's")
	}
}

// TestRetrySemantics_RunFailedOnlyWhenEveryAttemptFailed pins the one boolean
// that means the same thing in both frameworks. A run with a surviving
// attempt is a flake, not a failure, and counting it as one is how a
// failure-rate ends up describing attempts instead of runs.
func TestRetrySemantics_RunFailedOnlyWhenEveryAttemptFailed(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		want     runRollup
	}{
		{"both attempts failed", []string{StatusFailed, StatusFailed},
			runRollup{2, 2, true}},
		{"failed then passed", []string{StatusFailed, StatusPassed},
			runRollup{2, 1, false}},
		{"passed first try", []string{StatusPassed},
			runRollup{1, 0, false}},
		{"single failure, no retry", []string{StatusFailed},
			runRollup{1, 1, true}},
		{"timeout counts as a failed attempt", []string{StatusTimedOut, StatusTimedOut},
			runRollup{2, 2, true}},
		{"interrupted counts as a failed attempt", []string{StatusFailed, StatusInterrupted},
			runRollup{2, 2, true}},
		{"skipped is not a failure", []string{StatusSkipped},
			runRollup{1, 0, false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts := make([]ExtractedCase, len(tt.statuses))
			for i, s := range tt.statuses {
				attempts[i] = ExtractedCase{Status: s}
			}
			stampRunRollup(attempts)
			for i, c := range attempts {
				if got := rollupOf(t, c); got != tt.want {
					t.Errorf("attempt %d rollup = %+v, want %+v", i, got, tt.want)
				}
			}
		})
	}
}

// TestRetrySemantics_CypressWithoutAttemptsIsOneAttempt covers a report from a
// run with retries disabled, and older reporters that omit the array. It must
// not read as a zero-attempt run.
func TestRetrySemantics_CypressWithoutAttemptsIsOneAttempt(t *testing.T) {
	const noAttempts = `{
  "results": [
    {
      "title": "root", "file": "a_spec.js", "fullFile": "a_spec.js", "suites": [],
      "tests": [{"title": "t", "fullTitle": "t", "duration": 5, "state": "failed",
                 "fail": true, "err": {"message": "boom"}}]
    }
  ]
}`
	seq := 0
	cases := flatCases(extractCypress([]byte(noAttempts), &seq))
	if len(cases) != 1 {
		t.Fatalf("rows = %d, want 1", len(cases))
	}
	want := runRollup{attempts: 1, attemptsFailed: 1, runFailed: true}
	if got := rollupOf(t, cases[0]); got != want {
		t.Errorf("rollup = %+v, want %+v", got, want)
	}
}

// TestRetrySemantics_CypressAttachmentsLandOnExactlyOneAttempt guards the
// screenshot linker: mochawesome hangs the screenshot on the test, not on an
// attempt, so splitting a test into attempt rows must not duplicate it — the
// linker matches by basename and a second row claiming the same file would
// make the match ambiguous.
func TestRetrySemantics_CypressAttachmentsLandOnExactlyOneAttempt(t *testing.T) {
	const withScreenshot = `{
  "results": [
    {
      "title": "root", "file": "a_spec.js", "fullFile": "a_spec.js", "suites": [],
      "tests": [{
        "title": "t", "fullTitle": "t", "duration": 5, "state": "passed", "pass": true,
        "context": "{\"title\":\"screenshot\",\"value\":\"screenshots/a_spec.js/t.png\"}",
        "attempts": [
          {"state": "failed", "fail": true, "duration": 9},
          {"state": "passed", "pass": true, "duration": 5}
        ]
      }]
    }
  ]
}`
	seq := 0
	cases := flatCases(extractCypress([]byte(withScreenshot), &seq))
	if len(cases) != 2 {
		t.Fatalf("rows = %d, want 2", len(cases))
	}
	withAttachments := 0
	for _, c := range cases {
		if len(c.Attachments) > 0 {
			withAttachments++
		}
	}
	if withAttachments != 1 {
		t.Errorf("%d rows carry attachments, want exactly 1", withAttachments)
	}
	if len(cases[len(cases)-1].Attachments) != 1 {
		t.Error("attachments should hang on the final attempt")
	}
}

// TestRetrySemantics_SequenceIsContiguousAcrossAttempts guards the ordinal
// column: attempts are rows, and their ordinals must stay dense and ordered
// so the per-suite ordering the UI reads back is stable.
func TestRetrySemantics_SequenceIsContiguousAcrossAttempts(t *testing.T) {
	seq := 0
	cases := flatCases(extractCypress([]byte(oneFailOnePassCypress), &seq))
	for i, c := range cases {
		if c.Sequence != i {
			t.Errorf("row %d sequence = %d, want %d", i, c.Sequence, i)
		}
	}
	if seq != len(cases) {
		t.Errorf("seq advanced to %d, want %d", seq, len(cases))
	}
}
