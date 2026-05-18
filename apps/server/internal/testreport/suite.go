package testreport

import "github.com/google/uuid"

// Suite is a Playwright describe-block (may be nested).
type Suite struct {
	ID            uuid.UUID
	ReportID      uuid.UUID
	ParentSuiteID uuid.NullUUID
	Title         string
	File          string
	Line          *int
	Col           *int
	DurationMs    *int64
	Ordinal       int
}
