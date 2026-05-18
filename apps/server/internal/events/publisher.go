package events

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Publisher writes events onto a Hub with the JSON shapes the dashboard
// consumes. The event-type strings and payload field names below are the
// contract — renaming any of them is a breaking wire change.
//
// All events are emitted with an empty Scope so anonymous subscribers
// receive them; scoping is not used on this surface.
type Publisher struct {
	Hub *Hub
}

// ReportCreated fires when /reports/begin upserts a report_group.
func (p *Publisher) ReportCreated(
	groupID uuid.UUID, framework, repository, ref, sha, actor, runID string, prNumber *int, createdAt time.Time,
) {
	payload := map[string]any{
		"report_id":  groupID.String(),
		"framework":  framework,
		"created_at": isoTime(createdAt),
	}
	if repository != "" {
		payload["repository"] = repository
	}
	if ref != "" {
		payload["ref"] = ref
	}
	if sha != "" {
		payload["sha"] = sha
	}
	if actor != "" {
		payload["actor"] = actor
	}
	if runID != "" {
		payload["run_id"] = runID
	}
	if prNumber != nil {
		payload["pr_number"] = *prNumber
	}
	p.emit("report_created", payload)
}

// ReportUpdated fires when a report group's status flips (auto-finalize to
// `completed` or staleness reaper to `incomplete`) or aggregates change.
func (p *Publisher) ReportUpdated(
	groupID uuid.UUID, status string, completedReports int, stats *TestStats, updatedAt time.Time,
) {
	payload := map[string]any{
		"report_id":  groupID.String(),
		"status":     status,
		"updated_at": isoTime(updatedAt),
	}
	if completedReports > 0 {
		payload["completed_reports"] = completedReports
	}
	if stats != nil {
		payload["test_stats"] = stats
	}
	p.emit("report_updated", payload)
}

// ReportRegistered fires when /reports/register creates a new per-job report
// row inside an existing group.
func (p *Publisher) ReportRegistered(
	groupID, reportID uuid.UUID, displayName, ghJobID, ghJobName, status string, createdAt time.Time,
) {
	payload := map[string]any{
		"report_group_id": groupID.String(),
		"report_id":       reportID.String(),
		"display_name":    displayName,
		"status":          status,
		"created_at":      isoTime(createdAt),
	}
	if ghJobID != "" {
		payload["gh_job_id"] = ghJobID
	}
	if ghJobName != "" {
		payload["gh_job_name"] = ghJobName
	}
	p.emit("report_registered", payload)
}

// ReportEntryUpdated fires when a per-job report row transitions (json upload
// finishes, screenshots upload finishes, terminal status set).
func (p *Publisher) ReportEntryUpdated(
	groupID, reportID uuid.UUID, status string, updatedAt time.Time,
) {
	p.emit("report_entry_updated", map[string]any{
		"report_group_id": groupID.String(),
		"report_id":       reportID.String(),
		"status":          status,
		"updated_at":      isoTime(updatedAt),
	})
}

// SuitesAvailable fires after a report's JSON has been parsed and suites are
// ready to query. Currently emitted at upload-completion time with a
// suite_count of 0 until parsing lands.
func (p *Publisher) SuitesAvailable(reportID uuid.UUID, suiteCount int) {
	p.emit("suites_available", map[string]any{
		"report_id":   reportID.String(),
		"suite_count": suiteCount,
	})
}

// TestStats is the per-report aggregate the ReportUpdated event carries.
type TestStats struct {
	Passed  int  `json:"passed"`
	Failed  int  `json:"failed"`
	Skipped int  `json:"skipped"`
	Flaky   *int `json:"flaky,omitempty"`
	Total   int  `json:"total"`
}

func (p *Publisher) emit(typ string, payload any) {
	if p == nil || p.Hub == nil {
		return
	}
	b, _ := json.Marshal(payload)
	p.Hub.Publish(Event{Type: typ, Timestamp: time.Now().UTC(), Payload: b}, Scope{})
}

func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }
