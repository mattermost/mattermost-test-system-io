// Package testreport defines the domain types (Report, Suite, TestCase,
// Artifact, ReportJSONFile, ReportGroup) used by handlers.
package testreport

import (
	"time"

	"github.com/google/uuid"
)

// ArtifactKind categorizes a binary attachment.
type ArtifactKind string

// Known artifact kinds.
const (
	ArtifactScreenshot ArtifactKind = "screenshot"
	ArtifactTrace      ArtifactKind = "trace"
	ArtifactVideo      ArtifactKind = "video"
	ArtifactLog        ArtifactKind = "log"
	ArtifactOther      ArtifactKind = "other"
)

// Artifact is a blob stored in object storage, attached to a test case.
type Artifact struct {
	ID          uuid.UUID
	TestCaseID  uuid.UUID
	Kind        ArtifactKind
	ContentType string
	ObjectKey   string
	SizeBytes   int64
	SHA256      string
	CreatedAt   time.Time
}

// ReportJSONFile is the raw Playwright JSON output kept for audit.
type ReportJSONFile struct {
	ID        uuid.UUID
	ReportID  uuid.UUID
	ObjectKey string
	SizeBytes int64
	SHA256    string
	CreatedAt time.Time
}
