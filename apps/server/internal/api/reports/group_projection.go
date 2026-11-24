package reports

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// One projected report_groups row.
const groupProjectionSelectCols = `
	id, repository, name, run_group, status, branch, commit_sha,
	gh_run_id, gh_run_attempt, created_at, gh_pr_number,
	stage, event_name, base_ref, head_ref, platform, server_version, app_version, edition,
	test_stats_json, orchestration_json
`

type groupProjectionRow struct {
	ID           uuid.UUID
	Repository   string
	Name         string
	RunGroup     string
	Status       string
	Branch       string
	CommitSHA    string
	GHRunID      string
	GHRunAttempt string
	CreatedAt    time.Time
	GHPRNumber   *int
	RunContext
	Stats *testStats
	Orch  *orchestrationSummary
}

func scanGroupProjection(s interface{ Scan(dst ...any) error }) (groupProjectionRow, error) {
	var row groupProjectionRow
	var runGroup *string
	var eventName, baseRef, headRef, platform, serverVersion, appVersion, edition *string
	var statsRaw, orchRaw []byte
	if err := s.Scan(
		&row.ID, &row.Repository, &row.Name, &runGroup, &row.Status, &row.Branch, &row.CommitSHA,
		&row.GHRunID, &row.GHRunAttempt, &row.CreatedAt, &row.GHPRNumber,
		&row.Stage, &eventName, &baseRef, &headRef, &platform, &serverVersion, &appVersion, &edition,
		&statsRaw, &orchRaw,
	); err != nil {
		return groupProjectionRow{}, err
	}
	if runGroup != nil {
		row.RunGroup = *runGroup
	}
	row.EventName = derefOrEmpty(eventName)
	row.BaseRef = derefOrEmpty(baseRef)
	row.HeadRef = derefOrEmpty(headRef)
	row.Platform = derefOrEmpty(platform)
	row.ServerVersion = derefOrEmpty(serverVersion)
	row.AppVersion = derefOrEmpty(appVersion)
	row.Edition = derefOrEmpty(edition)
	row.Stats = parseProjectedTestStats(statsRaw)
	row.Orch = parseProjectedOrchestration(orchRaw)
	return row, nil
}

func parseProjectedTestStats(raw []byte) *testStats {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var agg groupStats
	if err := json.Unmarshal(raw, &agg); err != nil {
		return nil
	}
	return statsFromAgg(agg)
}

func parseProjectedOrchestration(raw []byte) *orchestrationSummary {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var orch orchestrationSummary
	if err := json.Unmarshal(raw, &orch); err != nil {
		return nil
	}
	return &orch
}

func (row groupProjectionRow) groupDTO() groupDTO {
	return groupDTO{
		ID:           row.ID,
		Name:         row.Name,
		RunGroup:     row.RunGroup,
		Status:       row.Status,
		Repository:   row.Repository,
		Branch:       row.Branch,
		CommitSHA:    row.CommitSHA,
		GHRunID:      row.GHRunID,
		GHRunAttempt: row.GHRunAttempt,
		CreatedAt:    row.CreatedAt,
		GHPRNumber:   row.GHPRNumber,
		RunContext:   row.RunContext,
	}
}

func (row groupProjectionRow) isLiveHomeRun() bool {
	if row.Orch != nil && row.Orch.Status == "in_progress" {
		return true
	}
	switch row.Status {
	case "complete", "failed", "incomplete":
		return false
	default:
		return row.Status == "in_progress" || row.Status == ""
	}
}

func (row groupProjectionRow) toRunEntry() runEntry {
	g := row.groupDTO()
	var stats groupStats
	if row.Stats != nil {
		stats = groupStats{
			TotalCases: row.Stats.Total,
			Passed:     row.Stats.Passed,
			Failed:     row.Stats.Failed,
			Skipped:    row.Stats.Skipped,
			Flaky:      row.Stats.Flaky,
			DurationMs: row.Stats.DurationMs,
			// Keep wall-clock spans for the home duration column.
			NumberedWallClockMs: row.Stats.WallClockMs,
			RetestWallClockMs:   row.Stats.RetestWallClockMs,
		}
	}
	e := toRunEntry(g, stats)
	e.Orchestration = row.Orch
	return e
}
