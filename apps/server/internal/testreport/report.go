package testreport

import (
	"time"

	"github.com/google/uuid"
)

// ReportStatus is the ingest lifecycle state of a report.
type ReportStatus string

// Report lifecycle states.
const (
	ReportStatusIngesting ReportStatus = "ingesting"
	ReportStatusReady     ReportStatus = "ready"
	ReportStatusFailed    ReportStatus = "failed"
)

// Report is the domain view of a single Playwright run.
type Report struct {
	ID                    uuid.UUID
	ReportGroupID         uuid.UUID
	Source                string
	CommitSHA             *string
	Branch                *string
	Status                ReportStatus
	IngestError           *string
	TotalSuites           int
	TotalCases            int
	PassedCases           int
	FailedCases           int
	SkippedCases          int
	FlakyCases            int
	DurationMs            *int64
	UploadedByAPIKeyID    uuid.NullUUID
	UploadedByOIDCSubject *string
	IdempotencyKey        *string
	CreatedAt             time.Time
	IngestedAt            *time.Time
}

// ReportGroup is a logical grouping of reports.
type ReportGroup struct {
	ID          uuid.UUID
	Slug        string
	DisplayName string
	Description *string
	CreatedAt   time.Time
}
