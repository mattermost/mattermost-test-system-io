package testreport

import "github.com/google/uuid"

// TestCaseStatus is a Playwright test outcome state.
type TestCaseStatus string

// Test case outcome states (match Playwright's reporter values; "flaky" is
// derived by the consolidator when early retries fail and the last retry passes).
const (
	TestCaseStatusPassed      TestCaseStatus = "passed"
	TestCaseStatusFailed      TestCaseStatus = "failed"
	TestCaseStatusSkipped     TestCaseStatus = "skipped"
	TestCaseStatusFlaky       TestCaseStatus = "flaky"
	TestCaseStatusTimedOut    TestCaseStatus = "timedOut"
	TestCaseStatusInterrupted TestCaseStatus = "interrupted"
)

// TestCase is an individual test outcome.
type TestCase struct {
	ID           uuid.UUID
	SuiteID      uuid.UUID
	Title        string
	Status       TestCaseStatus
	RetryCount   int
	DurationMs   *int64
	ErrorMessage *string
	ErrorStack   *string
	Annotations  []byte // JSONB
	Ordinal      int
}
