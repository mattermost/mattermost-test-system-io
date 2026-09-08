// Package triageassessment records conservative, server-derived triage evidence.
// A historical master failure is an observation, never causal exoneration.
package triageassessment

import (
	"sort"
	"time"
)

// Outcomes describe stored observations, never permission to clear a check.
const (
	NoFailure           = "no_failure"
	PRSuspect           = "pr_suspect"
	ObservedOnMaster    = "observed_on_master"
	Unknown             = "unknown"
	policyVersion       = "shadow-v1"
	baselineWindow      = 14 * 24 * time.Hour
	minimumBaselineRuns = 3
)

// Selector identifies exactly one report group; none of its fields are optional.
type Selector struct {
	Repository   string `json:"repository"`
	CommitSHA    string `json:"commit_sha"`
	GHRunID      string `json:"gh_run_id"`
	GHRunAttempt string `json:"gh_run_attempt"`
	Name         string `json:"name"`
}

// Run identifies the requested run and its report group, when present.
type Run struct {
	Selector
	ReportGroupID string `json:"report_group_id,omitempty"`
}

// Test preserves one server-derived failure key and its baseline evidence.
type Test struct {
	StableKey               string   `json:"stable_key"`
	File                    string   `json:"file"`
	FullTitle               string   `json:"full_title"`
	Project                 string   `json:"project"`
	Framework               string   `json:"framework"`
	Outcome                 string   `json:"outcome"`
	Reasons                 []string `json:"reasons"`
	BaselineRuns            int      `json:"baseline_runs"`
	BaselineDistinctCommits int      `json:"baseline_distinct_commits"`
	// BaselineFailures counts groups with any failed attempts, including flakes.
	BaselineFailures   int        `json:"baseline_failures"`
	BaselineFailedRuns int        `json:"baseline_failed_runs"`
	BaselineFlakyRuns  int        `json:"baseline_flaky_runs"`
	BaselineLatestAt   *time.Time `json:"baseline_latest_at,omitempty"`
	BaselineGroupIDs   []string   `json:"baseline_group_ids"`
}

// Assessment is a preview or an immutable snapshot when ID is populated.
type Assessment struct {
	ID             string         `json:"id,omitempty"`
	Outcome        string         `json:"outcome"`
	CanUnblock     bool           `json:"can_unblock"`
	Reasons        []string       `json:"reasons"`
	Tests          []Test         `json:"tests"`
	Run            Run            `json:"run"`
	RecordedAt     *time.Time     `json:"recorded_at,omitempty"`
	Author         string         `json:"author,omitempty"`
	PolicyVersion  string         `json:"policy_version"`
	BaselinePolicy BaselinePolicy `json:"baseline_policy"`
}

// BaselinePolicy makes the sampling and freshness requirements explicit.
type BaselinePolicy struct {
	WindowHours            int `json:"window_hours"`
	FreshnessHours         int `json:"freshness_hours"`
	MinimumDistinctCommits int `json:"minimum_distinct_commits"`
}

// Identity deliberately includes fields that an external MM-T id can alias.
type identity struct {
	key, file, title, project, framework string
}

type observation struct {
	identity
	groupID, commit string
	createdAt       time.Time
	failed, passed  bool
}

func assessTest(current observation, aliases []observation, history []observation, at time.Time, freshness time.Duration) Test {
	t := Test{StableKey: current.key, File: current.file, FullTitle: current.title,
		Project: current.project, Framework: current.framework, Outcome: Unknown,
		Reasons: []string{}, BaselineGroupIDs: []string{}}
	if current.key == "" || current.file == "" || current.title == "" || current.framework == "" ||
		(current.framework == "playwright" && current.project == "") {
		t.Reasons = append(t.Reasons, "test_identity_missing")
		return t
	}
	for _, row := range aliases {
		if row.key == current.key && row.identity != current.identity {
			t.Reasons = append(t.Reasons, "ambiguous_test_identity")
			return t
		}
	}
	commits := map[string]bool{}
	groups := map[string]bool{}
	for _, row := range history {
		if row.key != current.key {
			continue
		}
		if row.identity != current.identity {
			t.Reasons = append(t.Reasons, "ambiguous_or_renamed_master_identity")
			return t
		}
		if !row.createdAt.Before(at) || row.createdAt.Before(at.Add(-baselineWindow)) || (!row.failed && !row.passed) {
			continue
		}
		if !groups[row.groupID] {
			groups[row.groupID] = true
			t.BaselineGroupIDs = append(t.BaselineGroupIDs, row.groupID)
			if row.failed {
				t.BaselineFailures++
				if row.passed {
					t.BaselineFlakyRuns++
				} else {
					t.BaselineFailedRuns++
				}
			}
		}
		commits[row.commit] = true
		if t.BaselineLatestAt == nil || row.createdAt.After(*t.BaselineLatestAt) {
			v := row.createdAt
			t.BaselineLatestAt = &v
		}
	}
	sort.Strings(t.BaselineGroupIDs)
	t.BaselineRuns = len(groups)
	t.BaselineDistinctCommits = len(commits)
	if t.BaselineDistinctCommits < minimumBaselineRuns {
		t.Reasons = append(t.Reasons, "thin_master_baseline")
		return t
	}
	if t.BaselineLatestAt == nil || at.Sub(*t.BaselineLatestAt) > freshness {
		t.Reasons = append(t.Reasons, "stale_master_baseline")
		return t
	}
	if t.BaselineFailures > 0 {
		t.Outcome = ObservedOnMaster
		t.Reasons = append(t.Reasons, "failure_observed_on_master", "master_failure_does_not_establish_pr_innocence")
	} else {
		t.Outcome = PRSuspect
		t.Reasons = append(t.Reasons, "failure_not_observed_in_sampled_master_baseline")
	}
	return t
}

// An unknown test prevents a run being summarized as wholly understood. Within
// understood runs any PR suspect takes precedence over historical observations.
func aggregate(tests []Test) string {
	outcome := NoFailure
	for _, test := range tests {
		if test.Outcome == Unknown {
			return Unknown
		}
		if test.Outcome == PRSuspect {
			outcome = PRSuspect
		} else if outcome == NoFailure {
			outcome = ObservedOnMaster
		}
	}
	return outcome
}
