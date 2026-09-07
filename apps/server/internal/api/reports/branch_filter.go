package reports

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	branchFilterMainMaster = "branch:main-master"
	branchFilterRelease    = "kind:release"
	branchFilterPR         = "kind:pr"
)

// Home branch filter option.
type BranchFilterOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Branch filter param values: "", "branch:main-master", "kind:release", "kind:pr".
type BranchFilter string

func (f BranchFilter) Active() bool {
	return strings.TrimSpace(string(f)) != ""
}

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
		names := []string{"main", "master"}
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
	case lower == "master":
		return branchKindMainMaster, false, true
	case strings.HasPrefix(lower, "release-"):
		return branchKindRelease, false, true
	default:
		return 0, false, false
	}
}

// Branch filter options for one repository.
func LoadBranchFilterOptions(ctx context.Context, pool *pgxpool.Pool, repository string) ([]BranchFilterOption, error) {
	repoFilter := RepositoryFilter(repository)
	if !repoFilter.Active() {
		return nil, nil
	}
	if pool == nil {
		return nil, fmt.Errorf("reports: nil pool")
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
				mainLabel = "master"
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
			mainLabel = "master"
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

// Resolve filter; default when only one option exists.
func ResolveBranchFilter(requested string, options []BranchFilterOption) string {
	if v := strings.TrimSpace(requested); v != "" {
		return v
	}
	if len(options) == 1 {
		return options[0].Value
	}
	return ""
}
