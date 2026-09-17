// Package triage contains the shared evidence and policy contracts for CI triage.
package triage

import "time"

// EngineVersion changes whenever identity-independent classification rules change.
const EngineVersion = "triage-4"

// Thresholds is the per-context policy. Defaults are deliberately shadow-only.
type Thresholds struct {
	WindowDays              int     `json:"window_days" env:"TRIAGE_WINDOW_DAYS" envDefault:"14"`
	MaxTrunkRuns            int     `json:"max_trunk_runs" env:"TRIAGE_MAX_TRUNK_RUNS" envDefault:"30"`
	MinTrunkRuns            int     `json:"min_trunk_runs" env:"TRIAGE_MIN_TRUNK_RUNS" envDefault:"5"`
	BrokenStreak            int     `json:"broken_streak" env:"TRIAGE_BROKEN_STREAK" envDefault:"2"`
	FlakyMinRate            float64 `json:"flaky_min_rate" env:"TRIAGE_FLAKY_MIN_RATE" envDefault:"0.05"`
	PMin                    float64 `json:"p_min" env:"TRIAGE_P_MIN" envDefault:"0.05"`
	ClusterMin              int     `json:"cluster_min" env:"TRIAGE_CLUSTER_MIN" envDefault:"3"`
	AreaSlack               int     `json:"area_slack" env:"TRIAGE_AREA_SLACK" envDefault:"0"`
	StaleTrunkHours         int     `json:"stale_trunk_hours" env:"TRIAGE_STALE_TRUNK_HOURS" envDefault:"72"`
	ConfidenceFloor         float64 `json:"confidence_floor" env:"TRIAGE_CONFIDENCE_FLOOR" envDefault:"0.6"`
	InsufficientDataPolicy  string  `json:"insufficient_data_policy" env:"TRIAGE_INSUFFICIENT_DATA_POLICY" envDefault:"block"`
	SkipAfterDays           int     `json:"skip_after_days" env:"TRIAGE_SKIP_AFTER_DAYS" envDefault:"14"`
	MaxExoneratedRatio      float64 `json:"max_exonerated_ratio" env:"TRIAGE_MAX_EXONERATED_RATIO" envDefault:"0.25"`
	AutoQuarantineAfterRuns int     `json:"auto_quarantine_after_runs" env:"TRIAGE_AUTO_QUARANTINE_AFTER_RUNS" envDefault:"3"`
	ReleaseAfterPasses      int     `json:"release_after_passes" env:"TRIAGE_RELEASE_AFTER_PASSES" envDefault:"10"`
	RetireDays              int     `json:"retire_days" env:"TRIAGE_RETIRE_DAYS" envDefault:"30"`
	// CrossPRMinPRs is the number of distinct *other* PRs on which a test must
	// have failed within the window (while passing somewhere) to count as
	// flaky when trunk alone shows no instability. 0 disables the rule.
	CrossPRMinPRs int `json:"cross_pr_min_prs" env:"TRIAGE_CROSS_PR_MIN_PRS" envDefault:"3"`
}

// DefaultThresholds returns the blueprint defaults.
func DefaultThresholds() Thresholds {
	return Thresholds{WindowDays: 14, MaxTrunkRuns: 30, MinTrunkRuns: 5, BrokenStreak: 2, FlakyMinRate: .05, PMin: .05, ClusterMin: 3, StaleTrunkHours: 72, ConfidenceFloor: .6, InsufficientDataPolicy: "block", SkipAfterDays: 14, MaxExoneratedRatio: .25, AutoQuarantineAfterRuns: 3, ReleaseAfterPasses: 10, RetireDays: 30, CrossPRMinPRs: 3}
}

// Policy is scoped to the GitHub required-status context.
type Policy struct {
	Repository string     `json:"repository"`
	Context    string     `json:"context"`
	Mode       string     `json:"mode"`
	Thresholds Thresholds `json:"thresholds"`
	UpdatedBy  string     `json:"updated_by,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Group identifies a fully ingested report group.
type Group struct {
	ID           string    `json:"id"`
	Repository   string    `json:"repository"`
	Framework    string    `json:"framework"`
	Name         string    `json:"name"`
	CommitSHA    string    `json:"commit_sha"`
	GHRunID      string    `json:"gh_run_id"`
	GHRunAttempt string    `json:"gh_run_attempt"`
	GHPRNumber   *int      `json:"gh_pr_number,omitempty"`
	Branch       string    `json:"branch"`
	BranchKind   string    `json:"branch_kind"`
	BaseRef      string    `json:"base_ref"`
	BaseSHA      string    `json:"base_sha"`
	Lane         string    `json:"lane"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// Observation is an append-only test execution with identity metadata attached.
type Observation struct {
	ID             string    `json:"id"`
	IdentityID     string    `json:"identity_id"`
	ReportGroupID  string    `json:"report_group_id"`
	TestCaseID     string    `json:"test_case_id,omitempty"`
	AttemptID      string    `json:"attempt_id,omitempty"`
	Repository     string    `json:"repository"`
	Framework      string    `json:"framework"`
	File           string    `json:"file"`
	FullTitle      string    `json:"full_title"`
	MMTID          string    `json:"mm_t_id,omitempty"`
	StableKey      string    `json:"stable_key"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	BranchKind     string    `json:"branch_kind"`
	Branch         string    `json:"branch"`
	BaseRef        string    `json:"base_ref"`
	BaseSHA        string    `json:"base_sha"`
	CommitSHA      string    `json:"commit_sha"`
	Lane           string    `json:"lane"`
	Status         string    `json:"status"`
	AttemptIndex   int       `json:"attempt_index"`
	RetryCount     int       `json:"retry_count"`
	DurationMS     int64     `json:"duration_ms"`
	ErrorSignature string    `json:"error_signature,omitempty"`
	ErrorExcerpt   string    `json:"error_excerpt,omitempty"`
	FailureLocus   string    `json:"failure_locus,omitempty"`
	IsInfraStub    bool      `json:"is_infra_stub"`
	ObservedAt     time.Time `json:"observed_at"`
	GroupCreatedAt time.Time `json:"group_created_at"`
}

// Quarantine is an audited suppression, optionally scoped to one lane.
type Quarantine struct {
	ID         string     `json:"id"`
	IdentityID string     `json:"identity_id"`
	Lane       *string    `json:"lane"`
	BaseRef    string     `json:"base_ref"`
	Source     string     `json:"source"`
	Reason     string     `json:"reason"`
	Status     string     `json:"status"`
	Evidence   any        `json:"evidence"`
	IssueURL   string     `json:"issue_url,omitempty"`
	CreatedBy  string     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
	ReleasedBy string     `json:"released_by,omitempty"`
}

// Stats aggregates one terminal outcome per distinct trunk report group.
type Stats struct {
	Runs              int        `json:"runs"`
	Passes            int        `json:"passes"`
	Fails             int        `json:"fails"`
	Flaky             int        `json:"flaky"`
	InstabilityRate   float64    `json:"instability_rate"`
	ConsecutiveFails  int        `json:"consecutive_fails"`
	ConsecutivePasses int        `json:"consecutive_passes"`
	LastPassAt        *time.Time `json:"last_pass_at"`
	LastFailAt        *time.Time `json:"last_fail_at"`
	LastPassSHA       string     `json:"last_pass_sha"`
	LastFailSHA       string     `json:"last_fail_sha"`
	DominantSignature string     `json:"dominant_signature,omitempty"`
	DominantExcerpt   string     `json:"dominant_excerpt,omitempty"`
	DominantLocus     string     `json:"dominant_locus,omitempty"`
	RecentPass        bool       `json:"recent_pass"`
	LatestAt          time.Time  `json:"latest_at"`
}

// CrossPRStats summarizes one identity's executions on other PRs in the window.
type CrossPRStats struct {
	DistinctPRs int `json:"distinct_prs"`
	Fails       int `json:"fails"`
	Passes      int `json:"passes"`
}

// IsFailure includes interrupted executions: interruption is never a pass.
func IsFailure(status string) bool {
	return status == "failed" || status == "timedOut" || status == "interrupted"
}

// Observation status and branch constants shared by projections.
const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusFlaky   = "flaky"
	StatusSkipped = "skipped"
	BranchTrunk   = "trunk"
)
