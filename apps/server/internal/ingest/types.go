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
	Sequence     int
	StartTime    *time.Time
	Attachments  []ExtractedAttachment
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
