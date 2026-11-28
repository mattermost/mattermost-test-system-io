package reports

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const branchMaster = "master"

const (
	branchFilterMainMaster = "branch:main-master"
	branchFilterRelease    = "kind:release"
	branchFilterPR         = "kind:pr"
)

// BranchFilterOption is a home branch filter option.
type BranchFilterOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// BranchFilter is a branch filter param value: "", "branch:main-master", "kind:release", "kind:pr".
type BranchFilter string

// Active reports whether a filter is set.
func (f BranchFilter) Active() bool {
	return strings.TrimSpace(string(f)) != ""
}

// Value returns the raw filter value.
func (f BranchFilter) Value() string {
	return strings.TrimSpace(string(f))
}

func (f BranchFilter) appendSQL(b *strings.Builder, args *[]any) {
	if !f.Active() {
		return
	}
	v := f.Value()
	switch v {
	case branchFilterMainMaster, "branch:main", "branch:master":
		names := []string{"main", branchMaster}
		placeholders := make([]string, 0, len(names)*3)
		for _, name := range names {
			barePH := fmt.Sprintf("$%d", len(*args)+1)
			headPH := fmt.Sprintf("$%d", len(*args)+2)
			tagPH := fmt.Sprintf("$%d", len(*args)+3)
			placeholders = append(placeholders, "branch = "+barePH, "branch = "+headPH, "branch = "+tagPH)
			*args = append(*args, name, "refs/heads/"+name, "refs/tags/"+name)
		}
		b.WriteString(" AND (")
		b.WriteString(strings.Join(placeholders, " OR "))
		b.WriteString(")")
	case branchFilterRelease:
		b.WriteString(" AND (branch ~* '^refs/heads/release-' OR branch ~* '^refs/tags/release-' OR branch ~* '^release-')")
	case branchFilterPR:
		b.WriteString(" AND ((gh_pr_number IS NOT NULL AND gh_pr_number > 0) OR branch ~* '^refs/heads/pr-[0-9]+$' OR branch ~* '^pr-[0-9]+$')")
	default:
		if !strings.HasPrefix(v, "branch:") {
			return
		}
		name := strings.TrimSpace(v[7:])
		if name == "" {
			return
		}
		barePH := fmt.Sprintf("$%d", len(*args)+1)
		headPH := fmt.Sprintf("$%d", len(*args)+2)
		tagPH := fmt.Sprintf("$%d", len(*args)+3)
		b.WriteString(" AND (branch = ")
		b.WriteString(barePH)
		b.WriteString(" OR branch = ")
		b.WriteString(headPH)
		b.WriteString(" OR branch = ")
		b.WriteString(tagPH)
		b.WriteString(")")
		*args = append(*args, name, "refs/heads/"+name, "refs/tags/"+name)
	}
}

const (
	branchKindMainMaster = iota
	branchKindRelease
	branchKindPR
)

func classifyBranchFilterRow(branch string, prNum *int) (kind int, sawMain bool, ok bool) {
	if prNum != nil && *prNum > 0 {
		return branchKindPR, false, true
	}
	bare := stripRefPrefix(branch)
	if bare == "" {
		return 0, false, false
	}
	if _, ok := parsePRBranch(bare); ok {
		return branchKindPR, false, true
	}
	lower := strings.ToLower(bare)
	switch {
	case lower == "main":
		return branchKindMainMaster, true, true
	case lower == branchMaster:
		return branchKindMainMaster, false, true
	case strings.HasPrefix(lower, "release-"):
		return branchKindRelease, false, true
	default:
		return 0, false, false
	}
}

// LoadBranchFilterOptions lists branch filter options for one repository.
func LoadBranchFilterOptions(ctx context.Context, pool *pgxpool.Pool, repository string) ([]BranchFilterOption, error) {
	repoFilter := RepositoryFilter(repository)
	if !repoFilter.Active() {
		return nil, nil
	}
	if pool == nil {
		return nil, errors.New("reports: nil pool")
	}

	where := strings.Builder{}
	where.WriteString("1=1")
	args := []any{}
	repoFilter.appendSQL(&where, &args, "repository")

	rows, err := pool.Query(ctx, `
		SELECT branch, gh_pr_number, max(created_at) AS latest
		  FROM report_groups
		 WHERE `+where.String()+`
		 GROUP BY branch, gh_pr_number
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		hasMainMaster bool
		mainLabel     string
		hasRelease    bool
		hasPR         bool
	)
	for rows.Next() {
		var branch string
		var prNum *int
		var latest time.Time
		if err := rows.Scan(&branch, &prNum, &latest); err != nil {
			return nil, err
		}
		kind, sawMain, ok := classifyBranchFilterRow(branch, prNum)
		if !ok {
			continue
		}
		switch kind {
		case branchKindMainMaster:
			hasMainMaster = true
			if sawMain {
				mainLabel = "main"
			} else if mainLabel == "" {
				mainLabel = branchMaster
			}
		case branchKindRelease:
			hasRelease = true
		case branchKindPR:
			hasPR = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]BranchFilterOption, 0, 3)
	if hasMainMaster {
		if mainLabel == "" {
			mainLabel = branchMaster
		}
		out = append(out, BranchFilterOption{
			Value: branchFilterMainMaster,
			Label: mainLabel,
		})
	}
	if hasRelease {
		out = append(out, BranchFilterOption{
			Value: branchFilterRelease,
			Label: "release",
		})
	}
	if hasPR {
		out = append(out, BranchFilterOption{
			Value: branchFilterPR,
			Label: "pr",
		})
	}
	return out, nil
}

// ResolveBranchFilter validates the filter; defaults when only one option exists.
func ResolveBranchFilter(requested string, options []BranchFilterOption) string {
	if v := strings.TrimSpace(requested); v != "" {
		return v
	}
	if len(options) == 1 {
		return options[0].Value
	}
	return ""
}

// Valid run stages persisted on report_groups.stage.
const (
	RunStagePR      = "pr"
	RunStageMain    = "main"
	RunStageRelease = "release"
	RunStageNightly = "nightly"
	RunStageManual  = "manual"
	RunStageOther   = "other"
)

// RunContext carries optional CI run context metadata attached to a report group.
type RunContext struct {
	Stage         string `json:"stage,omitempty"`
	EventName     string `json:"event_name,omitempty"`
	BaseRef       string `json:"base_ref,omitempty"`
	HeadRef       string `json:"head_ref,omitempty"`
	Platform      string `json:"platform,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
	AppVersion    string `json:"app_version,omitempty"`
	Edition       string `json:"edition,omitempty"`
}

// Normalize trims leading and trailing whitespace from every context field.
func (c *RunContext) Normalize() {
	c.Stage = strings.TrimSpace(c.Stage)
	c.EventName = strings.TrimSpace(c.EventName)
	c.BaseRef = strings.TrimSpace(c.BaseRef)
	c.HeadRef = strings.TrimSpace(c.HeadRef)
	c.Platform = strings.TrimSpace(c.Platform)
	c.ServerVersion = strings.TrimSpace(c.ServerVersion)
	c.AppVersion = strings.TrimSpace(c.AppVersion)
	c.Edition = strings.TrimSpace(c.Edition)
}

// IsValidRunStage reports whether s is a valid report_groups.stage value.
func IsValidRunStage(s string) bool {
	switch s {
	case RunStagePR, RunStageMain, RunStageRelease, RunStageNightly, RunStageManual, RunStageOther:
		return true
	default:
		return false
	}
}

// ResolveRunStage resolves the canonical stage from an explicit stage,
// GitHub event name, or branch/PR classification via classifyBranchFilterRow.
func ResolveRunStage(explicitStage, eventName, branch string, prNum *int) string {
	if s := strings.TrimSpace(explicitStage); IsValidRunStage(s) {
		return s
	}
	switch strings.ToLower(strings.TrimSpace(eventName)) {
	case "schedule":
		return RunStageNightly
	case "workflow_dispatch":
		return RunStageManual
	case "pull_request", "pull_request_target", "merge_group":
		return RunStagePR
	}
	kind, _, ok := classifyBranchFilterRow(branch, prNum)
	if ok {
		switch kind {
		case branchKindPR:
			return RunStagePR
		case branchKindMainMaster:
			return RunStageMain
		case branchKindRelease:
			return RunStageRelease
		}
	}
	return RunStageOther
}
