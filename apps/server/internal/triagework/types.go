// Package triagework owns durable test repair, quarantine, and defect escalation.
package triagework

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	stateLeased         = "leased"
	stateNeedsHuman     = "needs_human"
	stateProductSuspect = "product_suspect"
	stateRepairPR       = "repair_pr"
	stateQueued         = "queued"
	stateResolved       = "resolved"
)

// Handlers must be mounted behind middleware which overwrites the actor and
// repository headers after authenticating a dedicated triage credential.
type Handlers struct {
	Pool               *pgxpool.Pool
	Logger             *slog.Logger
	Jira               Jira
	QuarantineCap      int
	LeaseTTL           time.Duration
	SourceWorkflowRefs []string
}

// Jira provides live queries, never a local mirror of resolution state.
type Jira interface {
	FindUnresolved(context.Context, string) (*Issue, error)
	FindSubmission(context.Context, string) (*Issue, error)
	IsUnresolved(context.Context, string) (bool, error)
	Create(context.Context, string, string, string, string) (*Issue, error)
}

// Issue is a Jira identity and configured-origin URL.
type Issue struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

// Evidence is the immutable master run and test identity for work.
type Evidence struct {
	Repository          string          `json:"repository"`
	Framework           string          `json:"framework"`
	Name                string          `json:"name"`
	Branch              string          `json:"branch"`
	CommitSHA           string          `json:"commit_sha"`
	GHRunID             string          `json:"gh_run_id"`
	GHRunAttempt        string          `json:"gh_run_attempt"`
	ImageDigest         string          `json:"image_digest"`
	ReportGroupID       string          `json:"report_group_id"`
	StableKey           string          `json:"stable_key"`
	File                string          `json:"file"`
	FullTitle           string          `json:"full_title"`
	Project             string          `json:"project"`
	EnvironmentMetadata json.RawMessage `json:"environment_metadata"`
}

// Item is a durable repair cycle with its completed attempt accounts.
type Item struct {
	Evidence
	ID                        string     `json:"id"`
	Owner                     string     `json:"owner"`
	Ticket                    string     `json:"ticket"`
	State                     string     `json:"state"`
	Attempt                   int        `json:"attempt"`
	LeaseToken                string     `json:"lease_token,omitempty"`
	LeaseExpiresAt            *time.Time `json:"lease_expires_at,omitempty"`
	CreatedAt                 time.Time  `json:"created_at"`
	BlockingEligible          bool       `json:"blocking_eligible"`
	ObservedDistinctPRsFailed int        `json:"observed_distinct_prs_failed"`
	Attempts                  []Attempt  `json:"attempts"`
	worker                    string
}

// Attempt is an append-only account of one automated claim.
type Attempt struct {
	Attempt     int       `json:"attempt"`
	Worker      string    `json:"worker"`
	Author      string    `json:"author"`
	Outcome     string    `json:"outcome"`
	Account     string    `json:"account"`
	EvidenceURL string    `json:"evidence_url"`
	PRURL       string    `json:"pr_url"`
	CreatedAt   time.Time `json:"created_at"`
}

// QuarantineItem reports bounded suppression metadata, not altered raw outcomes.
type QuarantineItem struct {
	ID               string    `json:"id"`
	RepairID         string    `json:"repair_id"`
	Repository       string    `json:"repository"`
	Owner            string    `json:"owner"`
	Ticket           string    `json:"ticket"`
	Author           string    `json:"author"`
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`
	Active           bool      `json:"active"`
	BlockingEligible bool      `json:"blocking_eligible"`
}

// DefectItem is a caught product suspect and optional Jira submission receipt.
type DefectItem struct {
	ID            string    `json:"id"`
	RepairID      string    `json:"repair_id"`
	ReportGroupID string    `json:"report_group_id"`
	SubmissionID  string    `json:"submission_id,omitempty"`
	Repository    string    `json:"repository"`
	StableKey     string    `json:"stable_key"`
	State         string    `json:"state"`
	Summary       string    `json:"summary"`
	Author        string    `json:"author"`
	JiraKey       string    `json:"jira_key,omitempty"`
	JiraURL       string    `json:"jira_url,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}
