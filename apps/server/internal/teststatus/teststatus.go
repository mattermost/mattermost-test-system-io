// Package teststatus defines the canonical test attempt classification,
// per-test verdict rollup, per-spec verdict rollup, and pass-rate rules
// shared across ingest, orchestration, reports, and SSR pages.
//
// It intentionally depends only on the Go standard library so any internal
// package can import it without introducing dependency cycles.
package teststatus

// Canonical attempt and verdict status strings.
const (
	Passed      = "passed"
	Failed      = "failed"
	Skipped     = "skipped"
	Flaky       = "flaky"
	TimedOut    = "timedOut"
	Interrupted = "interrupted"
	Pending     = "pending"
)

// AttemptClass is the coarse classification of a single attempt status.
type AttemptClass int

const (
	// ClassFail covers failed, timedOut, interrupted, unknown, empty, and any unrecognized status.
	ClassFail AttemptClass = iota
	// ClassPass covers passed and flaky.
	ClassPass
	// ClassSkip covers skipped and pending.
	ClassSkip
)

// ClassifyAttempt maps a single attempt status string to ClassPass, ClassSkip,
// or ClassFail (fail-closed for unknown or empty values).
func ClassifyAttempt(status string) AttemptClass {
	switch status {
	case Passed, Flaky:
		return ClassPass
	case Skipped, Pending:
		return ClassSkip
	default:
		return ClassFail
	}
}

// IsPass reports whether status classifies as passing (passed or flaky).
func IsPass(status string) bool {
	return ClassifyAttempt(status) == ClassPass
}

// IsSkip reports whether status classifies as skipped (skipped or pending).
func IsSkip(status string) bool {
	return ClassifyAttempt(status) == ClassSkip
}

// IsFail reports whether status classifies as failing.
func IsFail(status string) bool {
	return ClassifyAttempt(status) == ClassFail
}

// TestVerdict computes the canonical verdict for one test's retry chain.
// Returns "" when attempts is empty (missing attempts are not counted).
func TestVerdict(attempts []string, frameworkFlaky bool) string {
	if len(attempts) == 0 {
		return ""
	}
	var anyPass, anyFail, anyFlaky bool
	for _, s := range attempts {
		if s == Flaky {
			anyFlaky = true
		}
		switch ClassifyAttempt(s) {
		case ClassPass:
			anyPass = true
		case ClassFail:
			anyFail = true
		case ClassSkip:
		}
	}
	if (anyPass && anyFail) || frameworkFlaky || anyFlaky {
		return Flaky
	}
	if anyFail {
		return Failed
	}
	if anyPass {
		return Passed
	}
	return Skipped
}

// SpecVerdict computes the canonical verdict for a spec from its test verdicts.
// A spec with zero tests (spec-did-not-run) or any failed test is "failed".
func SpecVerdict(testVerdicts []string) string {
	if len(testVerdicts) == 0 {
		return Failed
	}
	var hasFlaky, hasPass bool
	for _, v := range testVerdicts {
		if IsFail(v) {
			return Failed
		}
		if v == Flaky {
			hasFlaky = true
		}
		if IsPass(v) {
			hasPass = true
		}
	}
	if hasFlaky {
		return Flaky
	}
	if hasPass {
		return Passed
	}
	return Skipped
}

// PassRate computes (passed + flaky) * 100 / (passed + flaky + failed),
// excluding skipped tests from the denominator. Returns (0, false) when
// there are no passing, flaky, or failing tests.
func PassRate(passed, failed, flaky int) (float64, bool) {
	pass := passed + flaky
	denom := pass + failed
	if denom <= 0 {
		return 0, false
	}
	return float64(pass*100) / float64(denom), true
}

// PassRatePercent returns the integer pass rate percentage (0..100) using
// (passed + flaky) * 100 / (passed + flaky + failed), or -1 when the
// denominator is zero or negative.
func PassRatePercent(passed, failed, flaky int) int {
	pass := passed + flaky
	denom := pass + failed
	if denom <= 0 {
		return -1
	}
	return (pass * 100) / denom
}
