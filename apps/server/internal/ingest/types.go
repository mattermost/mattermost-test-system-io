// Package ingest parses framework-specific test-report JSON (Playwright,
// Cypress/mochawesome, Detox/Jest) into a framework-agnostic intermediate
// form, then writes it into Postgres as suites + test_cases + attachments.
//
// Screenshots are staged into report_screenshots at upload time and linked
// to their test_cases after extraction completes.
package ingest

import "time"

// ExtractedSuite is a flat suite with its test cases. Nested suite hierarchy
// is flattened by the framework-specific extractors: each extracted suite has
// no children; the parent-child relationship lives in the DB as
// suites.parent_suite_id at insertion time (see consolidate.go).
type ExtractedSuite struct {
	Title     string
	FilePath  *string // optional — some frameworks (e.g. Cypress root) have no file
	StartTime *time.Time
	Cases     []ExtractedCase
}

// ExtractedCase is one test case before persistence.
type ExtractedCase struct {
	Title        string
	FullTitle    string // ancestor-prefixed (used by search + screenshot linker)
	Status       string // one of: passed, failed, skipped, flaky, timedOut, interrupted
	DurationMs   int64
	RetryCount   int
	ErrorMessage *string
	// ErrorStack is the framework's own stack trace, kept separate from
	// ErrorMessage so evidence can show both and clustering can normalize on
	// the shorter, more stable message. Nil where the framework's failure
	// report carries no distinct stack (Jest's failureMessages already embed
	// it in the message).
	ErrorStack  *string
	Sequence    int
	StartTime   *time.Time
	Attachments []ExtractedAttachment

	// Project is the framework's parallel-execution dimension — Playwright's
	// projectName (chrome, firefox, ...). Nil for frameworks that have no
	// such concept. It is part of a test's identity: the same title run under
	// two projects is two independent series, and folding them together lets
	// a browser-specific regression read as a flake because the other
	// browser keeps passing.
	Project *string

	// Attempts, AttemptsFailed and RunFailed are the run-level rollup for the
	// one test these attempt rows belong to. Every attempt row of the same
	// test in the same run carries the same three values, so a consumer can
	// read run-level truth off any single row without re-grouping.
	//
	// They exist because Playwright and Cypress disagree about what a
	// "failure" is: Playwright reports every attempt, Cypress reports the
	// final state. A rate computed across both without this rollup compares
	// different things. RunFailed is the one boolean that means the same
	// thing in both: every attempt failed.
	//
	// Rates are computed over runs, not attempts. A retried attempt shares
	// the leaked state or slow container that failed the first one, so the
	// attempts of one run are not independent draws and counting them as
	// such overstates both the sample size and the failure rate.
	Attempts       int
	AttemptsFailed int
	RunFailed      bool
}

// stampRunRollup computes the run-level rollup over one test's attempt rows
// and writes it onto every one of them. Call it with the attempts of a single
// test in a single run, in framework order.
//
// It does not touch per-attempt Status: the whole point is that an attempt
// keeps its own truth while the run-level verdict rides alongside it.
func stampRunRollup(attempts []ExtractedCase) {
	failed := 0
	for _, a := range attempts {
		if isAttemptFailure(a.Status) {
			failed++
		}
	}
	// A run failed only when no attempt survived. One passing attempt out of
	// two is a flake, not a failure, and must not be counted as one.
	runFailed := len(attempts) > 0 && failed == len(attempts)
	for i := range attempts {
		attempts[i].Attempts = len(attempts)
		attempts[i].AttemptsFailed = failed
		attempts[i].RunFailed = runFailed
	}
}

// stampSingleAttempt is stampRunRollup for a framework whose report carries
// one final result per test and no attempt list (Detox/Jest, Maestro/JUnit).
// Such a row is a one-attempt run: it failed iff that attempt failed.
func stampSingleAttempt(c *ExtractedCase) {
	c.Attempts = 1
	if isAttemptFailure(c.Status) {
		c.AttemptsFailed = 1
		c.RunFailed = true
	}
}

// isAttemptFailure reports whether one attempt's status is a failure.
// timedOut and interrupted count: the attempt did not produce a pass.
// skipped does not — a skipped attempt says nothing about stability.
func isAttemptFailure(status string) bool {
	switch status {
	case StatusFailed, StatusTimedOut, StatusInterrupted:
		return true
	}
	return false
}

// ExtractedAttachment is a reference a framework made to a file (Cypress
// context path, Playwright attachment). Screenshots that land in
// report_screenshots via the upload path are linked separately.
type ExtractedAttachment struct {
	Path        string
	ContentType *string
	Retry       int
	Sequence    int
	// S3Key and Missing are populated by the consolidator once it cross-checks
	// against report_screenshots; at parse time both are zero.
	S3Key   *string
	Missing bool
}

// Normalized test-case statuses (what we write into test_cases.status).
const (
	StatusPassed      = "passed"
	StatusFailed      = "failed"
	StatusSkipped     = "skipped"
	StatusFlaky       = "flaky"
	StatusTimedOut    = "timedOut"
	StatusInterrupted = "interrupted"
)

// Framework-native state strings that should normalize to StatusSkipped.
// They aren't statuses in our schema — the frameworks (mocha/Jest/Playwright)
// emit them in their report JSON and we map them inbound.
const (
	cypressStatePending = "pending"
)
