package triageassessment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// RunEvidenceGroup is the exact catalog identity, not an authorization decision.
type RunEvidenceGroup struct {
	Selector
	ID                   string    `json:"id"`
	Branch               string    `json:"branch"`
	Framework            string    `json:"framework"`
	CreatedAt            time.Time `json:"created_at"`
	Status               string    `json:"status"`
	TotalReportsExpected int       `json:"total_reports_expected"`
}

// RunEvidence contains original registration environments and individual stored
// attempt rows. Nullable attempt rollups remain null for legacy observations.
// This export deliberately has no clearance or attribution field.
type RunEvidence struct {
	SchemaVersion     int               `json:"schema_version"`
	Group             RunEvidenceGroup  `json:"group"`
	Complete          bool              `json:"complete"`
	Truncated         bool              `json:"truncated"`
	TrustedSource     bool              `json:"trusted_source"`
	Reasons           []string          `json:"reasons"`
	SourceWorkflowSHA string            `json:"source_workflow_sha"`
	SourceWorkflowRef string            `json:"source_workflow_ref"`
	Reports           []json.RawMessage `json:"reports"`
	Tests             []json.RawMessage `json:"tests"`
}

type runEvidenceLimits struct{ reports, tests, rowBytes, outputBytes int }

var defaultRunEvidenceLimits = runEvidenceLimits{reports: 2048, tests: 100000, rowBytes: 256 << 10, outputBytes: 16 << 20}

// RunEvidence serves a bounded read-only snapshot for an exact run selector.
// Only stored verified claim fields are checked; raw claims, assertions and
// upload principals are never included in the response.
func (h *Handlers) RunEvidence(w http.ResponseWriter, r *http.Request) {
	q, err := runEvidenceSelector(r)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	evidence, err := h.runEvidence(r.Context(), q, defaultRunEvidenceLimits)
	if errors.Is(err, pgx.ErrNoRows) {
		api.WriteError(w, r, api.ErrNotFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, evidence)
}

func runEvidenceSelector(r *http.Request) (Selector, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return Selector{}, fmt.Errorf("%w: malformed run selector query", api.ErrBadRequest)
	}
	for key, values := range q {
		if (key != "repository" && key != "commit_sha" && key != "gh_run_id" && key != "gh_run_attempt" && key != "name") || len(values) != 1 {
			return Selector{}, fmt.Errorf("%w: only exact run selector fields are supported", api.ErrBadRequest)
		}
	}
	s := Selector{Repository: q.Get("repository"), CommitSHA: q.Get("commit_sha"), GHRunID: q.Get("gh_run_id"), GHRunAttempt: q.Get("gh_run_attempt"), Name: q.Get("name")}
	return s, validate(s)
}

func (h *Handlers) runEvidence(ctx context.Context, selector Selector, limits runEvidenceLimits) (RunEvidence, error) {
	ev := RunEvidence{SchemaVersion: 1, Group: RunEvidenceGroup{Selector: selector}, Reasons: []string{}, Reports: []json.RawMessage{}, Tests: []json.RawMessage{}}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ev, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var pr *int
	var oversizedGroup bool
	err = tx.QueryRow(ctx, `SELECT g.id::text,CASE WHEN octet_length(g.branch)<=$6 THEN g.branch ELSE '' END,
 octet_length(g.branch)>$6,g.framework,g.created_at,g.status,coalesce(g.total_reports_expected,0),g.gh_pr_number,coalesce((`+completeGroupSQL+`),false)
 FROM report_groups g WHERE g.repository=$1 AND g.commit_sha=$2 AND g.gh_run_id=$3 AND g.gh_run_attempt=$4 AND g.name=$5`,
		selector.Repository, selector.CommitSHA, selector.GHRunID, selector.GHRunAttempt, selector.Name, limits.rowBytes).
		Scan(&ev.Group.ID, &ev.Group.Branch, &oversizedGroup, &ev.Group.Framework, &ev.Group.CreatedAt, &ev.Group.Status, &ev.Group.TotalReportsExpected, &pr, &ev.Complete)
	if err != nil {
		return ev, err
	}
	if oversizedGroup {
		ev.truncate("run_evidence_output_limit")
		return ev, tx.Commit(ctx)
	}
	if !ev.Complete {
		ev.Reasons = append(ev.Reasons, "exact_run_incomplete")
	}
	workflowRef := runEvidenceWorkflow(ev.Group, pr)
	if workflowRef == "" {
		ev.Reasons = append(ev.Reasons, "unsupported_source_suite")
	}
	var reportCount, testCount int
	err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM reports WHERE report_group_id=$1),
 (SELECT count(*) FROM reports r JOIN suites s ON s.report_id=r.id JOIN test_cases t ON t.suite_id=s.id WHERE r.report_group_id=$1)`, ev.Group.ID).Scan(&reportCount, &testCount)
	if err != nil {
		return ev, err
	}
	if reportCount > limits.reports || testCount > limits.tests {
		ev.truncate("run_evidence_count_limit")
		return ev, tx.Commit(ctx)
	}
	var sourceTrusted bool
	var identities int
	var sourceSHA, sourceEvent string
	err = tx.QueryRow(ctx, `SELECT coalesce(bool_and(coalesce(
 o.issuer='https://token.actions.githubusercontent.com' AND o.repository=$2 AND o.ref='refs/heads/master'
 AND o.raw_claims->>'run_id'=$3 AND o.raw_claims->>'run_attempt'=$4
 AND o.raw_claims->>'sha' ~ '^[a-f0-9]{40}$' AND o.raw_claims->>'workflow_ref'=$5
 AND o.raw_claims->>'event_name' IN ('workflow_dispatch','push','schedule'),false)),false),
 count(DISTINCT (o.raw_claims->>'sha',o.raw_claims->>'workflow_ref',o.raw_claims->>'event_name')),
 coalesce(min(CASE WHEN o.raw_claims->>'sha' ~ '^[a-f0-9]{40}$' THEN o.raw_claims->>'sha' END),''),
 coalesce(min(CASE WHEN o.raw_claims->>'event_name' IN ('workflow_dispatch','push','schedule') THEN o.raw_claims->>'event_name' END),'')
 FROM reports r LEFT JOIN oidc_claims o ON o.report_id=r.id WHERE r.report_group_id=$1`,
		ev.Group.ID, selector.Repository, selector.GHRunID, selector.GHRunAttempt, workflowRef).
		Scan(&sourceTrusted, &identities, &sourceSHA, &sourceEvent)
	if err != nil {
		return ev, err
	}
	sourceTrusted = sourceTrusted && identities == 1 && reportCount > 0 && workflowRef != ""
	if sourceTrusted {
		ev.SourceWorkflowSHA, ev.SourceWorkflowRef = sourceSHA, workflowRef
	} else {
		ev.Reasons = append(ev.Reasons, "missing_or_mismatched_verified_source_claims")
	}
	var receiptsTrusted bool
	err = tx.QueryRow(ctx, runEvidenceReceiptsSQL, ev.Group.ID, selector.Repository, selector.CommitSHA,
		selector.GHRunID, selector.GHRunAttempt, ev.Group.TotalReportsExpected, ev.Group.Framework,
		selector.Name, ev.Group.Branch, pr, workflowRef, sourceSHA, sourceEvent).Scan(&receiptsTrusted)
	if err != nil {
		return ev, err
	}
	if !receiptsTrusted {
		ev.Reasons = append(ev.Reasons, "missing_or_mismatched_registration_receipts")
	}
	ev.TrustedSource = sourceTrusted && receiptsTrusted
	// Reserve space for the actual envelope and a possible truncation reason.
	// Each row is size-checked in PostgreSQL before reaching this process.
	envelope, err := json.Marshal(ev)
	if err != nil {
		return ev, err
	}
	remaining := limits.outputBytes - len(envelope) - 128
	if remaining < 0 {
		ev.Group.Branch = ""
		ev.truncate("run_evidence_output_limit")
		return ev, tx.Commit(ctx)
	}
	ev.Reports, err = runEvidenceRows(ctx, tx, runEvidenceReportsSQL, ev.Group.ID, limits.rowBytes, &remaining)
	if err != nil {
		if errors.Is(err, errRunEvidenceLimit) {
			ev.truncate("run_evidence_output_limit")
			return ev, tx.Commit(ctx)
		}
		return ev, err
	}
	ev.Tests, err = runEvidenceRows(ctx, tx, runEvidenceTestsSQL, ev.Group.ID, limits.rowBytes, &remaining)
	if errors.Is(err, errRunEvidenceLimit) {
		ev.truncate("run_evidence_output_limit")
	} else if err != nil {
		return ev, err
	}
	return ev, tx.Commit(ctx)
}

func (ev *RunEvidence) truncate(reason string) {
	ev.Complete, ev.Truncated, ev.TrustedSource = false, true, false
	ev.Reasons = append(ev.Reasons, reason)
}

func runEvidenceWorkflow(group RunEvidenceGroup, pr *int) string {
	if group.Repository != "mattermost/mattermost" || len(group.CommitSHA) != 40 {
		return ""
	}
	name := strings.TrimSuffix(group.Name, "-master")
	framework := strings.SplitN(name, "-", 2)[0]
	if (name != "cypress-full-enterprise" && name != "cypress-full-fips" && name != "playwright-full-enterprise" && name != "playwright-full-fips") || group.Framework != framework {
		return ""
	}
	if group.Name == name+"-master" {
		if group.Branch != "master" || pr != nil {
			return ""
		}
		return "mattermost/mattermost/.github/workflows/e2e-tests-on-merge.yml@refs/heads/master"
	}
	return "mattermost/mattermost/.github/workflows/e2e-tests-ci.yml@refs/heads/master"
}

var errRunEvidenceLimit = errors.New("run evidence output bound exceeded")

func runEvidenceRows(ctx context.Context, tx pgx.Tx, query, groupID string, rowBytes int, remaining *int) ([]json.RawMessage, error) {
	result := []json.RawMessage{}
	rows, err := tx.Query(ctx, query, groupID, rowBytes)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return result, err
		}
		if len(raw) == 0 {
			return result, errRunEvidenceLimit
		}
		// Go's response encoder additionally escapes HTML-sensitive characters;
		// account for those emitted bytes instead of only PostgreSQL's JSON size.
		encoded, err := json.Marshal(json.RawMessage(raw))
		if err != nil {
			return result, err
		}
		if len(encoded)+1 > *remaining {
			return result, errRunEvidenceLimit
		}
		*remaining -= len(encoded) + 1
		result = append(result, json.RawMessage(encoded))
	}
	return result, rows.Err()
}

const runEvidenceReportsSQL = `SELECT CASE WHEN octet_length(row_to_json(e)::text)<=$2 THEN row_to_json(e)::text ELSE NULL END
 FROM (SELECT r.id::text,coalesce(r.gh_job_id,'') AS gh_job_id,coalesce(r.gh_job_name,'') AS gh_job_name,r.status,
 r.registration_receipt->'environment_metadata' AS environment_metadata
 FROM reports r WHERE r.report_group_id=$1 ORDER BY r.id) e`

const runEvidenceTestsSQL = `SELECT CASE WHEN octet_length(row_to_json(e)::text)<=$2 THEN row_to_json(e)::text ELSE NULL END
 FROM (SELECT r.id::text AS report_id,s.id::text AS suite_id,t.id::text,t.stable_key,t.file,t.full_title,t.project,t.status,
 t.retry_count,t.run_failed,t.attempts,t.attempts_failed,t.error_message
 FROM reports r JOIN suites s ON s.report_id=r.id JOIN test_cases t ON t.suite_id=s.id
 WHERE r.report_group_id=$1 ORDER BY r.id,s.id,t.ordinal,t.id) e`

// Registration receipts use "commit", not "commit_sha". Every report binds
// the tested revision, group identity, job identity and original environment.
// A count must come from that registration or an independently verified begin
// receipt for this same group and source workflow; group metadata cannot prove it.
const runEvidenceReceiptsSQL = `SELECT coalesce(bool_and(coalesce(
 r.upload_principal ~ '^oidc:[a-f0-9]{64}$'
 AND r.registration_receipt->>'repository'=$2 AND r.registration_receipt->>'commit'=$3
 AND r.registration_receipt->>'gh_run_id'=$4 AND r.registration_receipt->>'gh_run_attempt'=$5
 AND r.registration_receipt->>'framework'=$7 AND r.registration_receipt->>'name'=$8
 AND r.registration_receipt->>'branch'=$9
 AND r.registration_receipt->'gh_pr_number' IS NOT DISTINCT FROM to_jsonb($10::integer)
 AND coalesce(r.gh_job_id,'')<>'' AND r.registration_receipt->>'gh_job_id'=r.gh_job_id
 AND coalesce(r.gh_job_name,'')<>'' AND r.registration_receipt->>'gh_job_name'=r.gh_job_name
 AND jsonb_typeof(r.registration_receipt->'environment_metadata')='object'
 AND (r.registration_receipt->'total_reports_expected'=to_jsonb($6::integer)
 OR (NOT r.registration_receipt ? 'total_reports_expected' AND EXISTS(
 SELECT 1 FROM report_group_begin_receipts b WHERE b.report_group_id=r.report_group_id
 AND b.receipt->>'repository'=$2 AND b.receipt->>'commit'=$3
 AND b.receipt->>'gh_run_id'=$4 AND b.receipt->>'gh_run_attempt'=$5
 AND b.receipt->'total_reports_expected'=to_jsonb($6::integer)
 AND b.receipt->>'framework'=$7 AND b.receipt->>'name'=$8 AND b.receipt->>'branch'=$9
 AND b.receipt->'gh_pr_number' IS NOT DISTINCT FROM to_jsonb($10::integer)
 AND b.verified_claims->>'iss'='https://token.actions.githubusercontent.com'
 AND b.verified_claims->>'repository'=$2 AND b.verified_claims->>'ref'='refs/heads/master'
 AND b.verified_claims->>'sha'=$12 AND b.verified_claims->>'run_id'=$4 AND b.verified_claims->>'run_attempt'=$5
 AND b.verified_claims->>'workflow_ref'=$11 AND b.verified_claims->>'event_name'=$13))),false)),false)
 FROM reports r WHERE r.report_group_id=$1`
