// Package verdict implements deterministic PR classification without database access.
package verdict

import (
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

// Inputs contains a bounded snapshot of all facts used by the engine.
type Inputs struct {
	Now           time.Time
	Group         triage.Group
	Policy        triage.Policy
	Context       string
	Lane          string
	BaseRef       string
	BaseSHA       string
	ChangedFiles  []string
	PR            []triage.Observation
	Trunk         []triage.Observation
	Quarantine    []triage.Quarantine
	Abandoned     []string
	LatestTrunkAt time.Time
	BaseTime      time.Time
	// TrunkSeen records completed trunk history outside the rolling window and other lanes.
	TrunkSeen map[string]bool
	// UnrepresentedFailures are terminal failed units without failing test facts.
	UnrepresentedFailures []string
	// CrossPR holds, per identity, this test's executions on *other* PRs in
	// the window; CrossPRSignatures counts distinct other PRs per failure
	// signature. Both are evidence that a failure is environmental rather than
	// caused by this PR.
	CrossPR           map[string]triage.CrossPRStats
	CrossPRSignatures map[string]int
}

// Counts are test counts, not orchestration-unit counts.
type Counts struct {
	Passed     int `json:"passed"`
	Failed     int `json:"failed"`
	Flaky      int `json:"flaky"`
	Skipped    int `json:"skipped"`
	Exonerated int `json:"exonerated"`
	Blocking   int `json:"blocking"`
	Infra      int `json:"infra"`
}

// PRStats explains the execution count and signature match used in a finding.
type PRStats struct {
	FailCount      int    `json:"fail_count"`
	ErrorExcerpt   string `json:"error_excerpt"`
	FailureLocus   string `json:"failure_locus"`
	SignatureMatch bool   `json:"signature_match"`
}

// Links point to the evidence, history and any active suppression.
type Links struct {
	TSIOCaseURL    string `json:"tsio_case_url"`
	TSIOHistoryURL string `json:"tsio_history_url"`
	QuarantineID   string `json:"quarantine_id,omitempty"`
	IssueURL       string `json:"issue_url,omitempty"`
}

// Finding is one terminal failing identity, with its full reasoning evidence.
type Finding struct {
	IdentityID string       `json:"identity_id"`
	MMTID      string       `json:"mm_t_id,omitempty"`
	File       string       `json:"file"`
	FullTitle  string       `json:"full_title"`
	Lane       string       `json:"lane"`
	Class      string       `json:"class"`
	Blocking   bool         `json:"blocking"`
	Reason     string       `json:"reason"`
	Trunk      triage.Stats `json:"trunk"`
	PR         PRStats      `json:"pr"`
	Links      Links        `json:"links"`
	Signature  string       `json:"-"`
	Confidence float64      `json:"-"`
}

// Markdown is rendered on the server so producers only transport the decision.
type Markdown struct {
	CheckSummary      string `json:"check_summary"`
	PRComment         string `json:"pr_comment"`
	StatusDescription string `json:"status_description"`
}

// Override is a human decision recorded independently of the engine verdict.
type Override struct {
	Actor          string    `json:"actor"`
	Label          string    `json:"label"`
	At             time.Time `json:"at"`
	ResultingState string    `json:"resulting_state"`
	RecordedBy     string    `json:"recorded_by,omitempty"`
}

// Verdict is the public API representation and the persisted audit projection.
type Verdict struct {
	ID             string            `json:"id"`
	ReportGroupID  string            `json:"report_group_id"`
	Mode           string            `json:"mode"`
	Verdict        string            `json:"verdict"`
	Reason         string            `json:"reason,omitempty"`
	Confidence     float64           `json:"confidence"`
	ComputedAt     time.Time         `json:"computed_at"`
	EngineVersion  string            `json:"engine_version"`
	Counts         Counts            `json:"counts"`
	Findings       []Finding         `json:"findings"`
	ThresholdsUsed triage.Thresholds `json:"thresholds_used"`
	Markdown       Markdown          `json:"markdown"`
	HumanOverride  *Override         `json:"human_override,omitempty"`
	Adjudication   *Adjudication     `json:"adjudication,omitempty"`
}

const (
	classInfra           = "INFRA"
	classFlakyCrossPR    = "FLAKY_CROSS_PR"
	classBrokenOnTrunk   = "BROKEN_ON_TRUNK"
	resultSuccess        = "SUCCESS"
	classInsufficient    = "INSUFFICIENT_DATA"
	classRegression      = "REGRESSION"
	resultFailure        = "FAILURE"
	resultIncomplete     = "INCOMPLETE"
	resultActionRequired = "ACTION_REQUIRED"
)
