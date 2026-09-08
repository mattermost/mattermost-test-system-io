package triagework

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func testLabel(item *Item) string {
	sum := sha256.Sum256([]byte(item.Repository + "\x00" + item.Framework + "\x00" + item.Name + "\x00" + item.StableKey))
	return "tsio-test-" + hex.EncodeToString(sum[:])
}
func submissionLabel(id string) string { return "tsio-submission-" + id }

type defectRequest struct {
	LeaseToken  string `json:"lease_token"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
}
type defectResponse struct {
	State        string `json:"state"`
	SubmissionID string `json:"submission_id"`
	Issue        *Issue `json:"issue,omitempty"`
	Error        string `json:"error,omitempty"`
	Reconciled   bool   `json:"reconciled,omitempty"`
	Deduplicated bool   `json:"deduplicated,omitempty"`
	status       int
}

// Defect requires a completed product_suspect token. Durable submission intent
// commits before external creation, and repair-row ownership serializes callers.
func (h *Handlers) Defect(w http.ResponseWriter, r *http.Request) {
	author, err := actor(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	if h.Jira == nil {
		h.fail(w, &statusError{http.StatusServiceUnavailable, "Jira escalation is disabled"})
		return
	}
	id, err := itemID(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	var in defectRequest
	if err = decode(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	if err = requireToken(in.LeaseToken); err != nil {
		h.fail(w, err)
		return
	}
	if !bounded(in.Summary, 255) || !bounded(in.Description, 16000) {
		h.fail(w, bad("summary and description are required and bounded"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := lockedItem(ctx, tx, id)
	if err != nil {
		h.fail(w, err)
		return
	}
	if err = scope(r, item.Repository); err != nil {
		h.fail(w, err)
		return
	}
	if item.State != stateProductSuspect || !tokenValid(item, in.LeaseToken) {
		h.fail(w, conflict("defect requires the matching completed product_suspect token"))
		return
	}
	decision, err := h.prepareDefect(ctx, tx, item, author, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		h.fail(w, err)
		return
	}
	if decision.status != 0 {
		write(w, decision.status, decision)
		return
	}
	h.submitDefect(ctx, w, item, in, decision.SubmissionID)
}

func (h *Handlers) prepareDefect(ctx context.Context, tx pgx.Tx, item *Item, author string, in defectRequest) (*defectResponse, error) {
	if decision, err := h.reconcilePending(ctx, tx, item); decision != nil || err != nil {
		return decision, err
	}
	issue, err := h.liveIssue(ctx, tx, item)
	if err != nil {
		return nil, err
	}
	submission := uuid.NewString()
	if issue != nil {
		err = tx.QueryRow(ctx, `INSERT INTO triage_defect_submissions(id,repair_id,author,state,summary,description,jira_key,jira_url,report_group_id) VALUES($1,$2,$3,'linked',$4,$5,$6,$7,$8) ON CONFLICT(repair_id,jira_key) WHERE jira_key IS NOT NULL DO UPDATE SET updated_at=clock_timestamp() RETURNING id::text`, submission, item.ID, author, in.Summary, in.Description, issue.Key, issue.URL, item.ReportGroupID).Scan(&submission)
		if err != nil {
			return nil, err
		}
		if err = linkObservation(ctx, tx, item, submission); err != nil {
			return nil, err
		}
		return &defectResponse{State: "linked", SubmissionID: submission, Issue: issue, Deduplicated: true, status: http.StatusOK}, nil
	}
	var handled bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM triage_defect_submissions WHERE repair_id=$1 AND report_group_id=$2 AND state='linked') OR EXISTS(SELECT 1 FROM triage_defect_observation_links WHERE repair_id=$1 AND report_group_id=$2)`, item.ID, item.ReportGroupID).Scan(&handled)
	if err != nil {
		return nil, err
	}
	if handled {
		return nil, conflict("closed Jira recurrence requires a newer verified master observation")
	}
	_, err = tx.Exec(ctx, `INSERT INTO triage_defect_submissions(id,repair_id,author,state,summary,description,report_group_id) VALUES($1,$2,$3,'submitting',$4,$5,$6)`, submission, item.ID, author, in.Summary, in.Description, item.ReportGroupID)
	if err != nil {
		return nil, err
	}
	return &defectResponse{SubmissionID: submission}, nil
}

func (h *Handlers) reconcilePending(ctx context.Context, tx pgx.Tx, item *Item) (*defectResponse, error) {
	var pending, originalGroup string
	err := tx.QueryRow(ctx, `SELECT id::text,report_group_id::text FROM triage_defect_submissions WHERE repair_id=$1 AND state IN ('submitting','uncertain')`, item.ID).Scan(&pending, &originalGroup)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A positive marker match is the only safe recovery from a crash or timeout.
	issue, lookupErr := h.Jira.FindSubmission(ctx, submissionLabel(pending))
	if lookupErr != nil || issue == nil {
		if _, err = tx.Exec(ctx, `UPDATE triage_defect_submissions SET state='uncertain',updated_at=clock_timestamp() WHERE id=$1`, pending); err != nil {
			return nil, err
		}
		return &defectResponse{State: "uncertain", SubmissionID: pending, Error: "submission may already exist; reconcile in Jira before any new creation", status: http.StatusConflict}, nil
	}
	if err = linkSubmission(ctx, tx, pending, issue); err != nil {
		return nil, err
	}
	original := *item
	original.ReportGroupID = originalGroup
	if err = linkObservation(ctx, tx, &original, pending); err != nil {
		return nil, err
	}
	if originalGroup != item.ReportGroupID {
		unresolved, e := h.Jira.IsUnresolved(ctx, issue.Key)
		if e != nil {
			return nil, &statusError{http.StatusBadGateway, "live Jira resolution lookup failed"}
		}
		// Recovery of an old, already closed ticket must not consume a new failure.
		if !unresolved {
			return nil, nil
		}
		if err = linkObservation(ctx, tx, item, pending); err != nil {
			return nil, err
		}
	}
	return &defectResponse{State: "linked", SubmissionID: pending, Issue: issue, Reconciled: true, status: http.StatusOK}, nil
}

func (h *Handlers) liveIssue(ctx context.Context, tx pgx.Tx, item *Item) (*Issue, error) {
	issue, err := h.Jira.FindUnresolved(ctx, testLabel(item))
	if err != nil {
		return nil, &statusError{http.StatusBadGateway, "live Jira dedup lookup failed"}
	}
	if issue != nil {
		return issue, nil
	}
	// Search indexing is eventual. Check the last linked key directly as well.
	var previousKey, previousURL string
	err = tx.QueryRow(ctx, `SELECT jira_key,jira_url FROM triage_defect_submissions WHERE repair_id=$1 AND state='linked' ORDER BY created_at DESC,id DESC LIMIT 1`, item.ID).Scan(&previousKey, &previousURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	open, err := h.Jira.IsUnresolved(ctx, previousKey)
	if err != nil {
		return nil, &statusError{http.StatusBadGateway, "live Jira resolution lookup failed"}
	}
	if open {
		return &Issue{Key: previousKey, URL: previousURL}, nil
	}
	return nil, nil
}

func (h *Handlers) submitDefect(ctx context.Context, w http.ResponseWriter, item *Item, in defectRequest, submission string) {
	// The intent is already committed. Any unsuccessful create is uncertain.
	issue, createErr := h.Jira.Create(ctx, testLabel(item), submissionLabel(submission), in.Summary, in.Description)
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer saveCancel()
	if createErr != nil || issue == nil {
		if _, err := h.Pool.Exec(saveCtx, `UPDATE triage_defect_submissions SET state='uncertain',updated_at=clock_timestamp() WHERE id=$1 AND state='submitting'`, submission); err != nil {
			h.fail(w, err)
			return
		}
		write(w, http.StatusBadGateway, defectResponse{State: "uncertain", SubmissionID: submission, Error: "Jira creation outcome is uncertain; retry reconciles the existing submission only"})
		return
	}
	tag, err := h.Pool.Exec(saveCtx, `UPDATE triage_defect_submissions SET state='linked',jira_key=$2,jira_url=$3,updated_at=clock_timestamp() WHERE id=$1 AND (jira_key IS NULL OR jira_key=$2)`, submission, issue.Key, issue.URL)
	if err = checkUpdate(tag.RowsAffected(), err); err != nil {
		h.fail(w, err)
		return
	}
	write(w, http.StatusCreated, defectResponse{State: "linked", SubmissionID: submission, Issue: issue})
}

func linkSubmission(ctx context.Context, tx pgx.Tx, id string, issue *Issue) error {
	tag, err := tx.Exec(ctx, `UPDATE triage_defect_submissions SET state='linked',jira_key=$2,jira_url=$3,updated_at=clock_timestamp() WHERE id=$1`, id, issue.Key, issue.URL)
	return checkUpdate(tag.RowsAffected(), err)
}

func linkObservation(ctx context.Context, tx pgx.Tx, item *Item, submission string) error {
	_, err := tx.Exec(ctx, `INSERT INTO triage_defect_observation_links(repair_id,report_group_id,submission_id) VALUES($1,$2,$3) ON CONFLICT(repair_id,report_group_id) DO NOTHING`, item.ID, item.ReportGroupID, submission)
	return err
}

// ListDefects reports caught product suspects and submission receipts. It does
// not invent an escaped-release metric or mirror Jira resolution timestamps.
func (h *Handlers) ListDefects(w http.ResponseWriter, r *http.Request) {
	repo, limit, err := listParams(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := h.Pool.Query(ctx, `SELECT r.id::text||':'||o.report_group_id::text,r.id::text,o.report_group_id::text,COALESCE(d.id::text,''),r.repository,r.stable_key,COALESCE(d.state,'not_submitted'),COALESCE(d.summary,''),o.author,COALESCE(d.jira_key,''),COALESCE(d.jira_url,''),o.created_at
    FROM triage_product_observations o JOIN triage_repairs r ON r.id=o.repair_id
    LEFT JOIN LATERAL(SELECT submission.* FROM triage_defect_submissions submission
      WHERE submission.repair_id=o.repair_id AND (submission.report_group_id=o.report_group_id
      OR EXISTS(SELECT 1 FROM triage_defect_observation_links link WHERE link.repair_id=o.repair_id AND link.report_group_id=o.report_group_id AND link.submission_id=submission.id))
      ORDER BY submission.created_at DESC,submission.id DESC LIMIT 1) d ON true
    WHERE r.repository=$1 ORDER BY o.created_at DESC,r.id,o.report_group_id LIMIT $2`, repo, limit+1)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer rows.Close()
	items := []DefectItem{}
	for rows.Next() {
		var d DefectItem
		if err = rows.Scan(&d.ID, &d.RepairID, &d.ReportGroupID, &d.SubmissionID, &d.Repository, &d.StableKey, &d.State, &d.Summary, &d.Author, &d.JiraKey, &d.JiraURL, &d.CreatedAt); err != nil {
			h.fail(w, err)
			return
		}
		items = append(items, d)
	}
	if err = rows.Err(); err != nil {
		h.fail(w, err)
		return
	}
	truncated := len(items) > limit
	if truncated {
		items = items[:limit]
	}
	write(w, http.StatusOK, map[string]any{"items": items, "truncated": truncated})
}
