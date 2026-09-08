package triagework

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// expire is called with the row locked. Using the database clock prevents
// disagreement among workers. The last expired attempt exhausts the item.
func expire(ctx context.Context, tx pgx.Tx, item *Item) (bool, error) {
	if item.State != stateLeased {
		return false, nil
	}
	var expired bool
	if err := tx.QueryRow(ctx, `SELECT lease_expires_at<=clock_timestamp() FROM triage_repairs WHERE id=$1`, item.ID).Scan(&expired); err != nil {
		return false, err
	}
	if !expired {
		return false, nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO triage_repair_attempts(repair_id,attempt,lease_token,worker,author,outcome,account)
 VALUES($1,$2,$3,$4,'system:lease-expiry','expired','Worker lease expired without a completed account')`, item.ID, item.Attempt, item.LeaseToken, item.worker)
	if err != nil {
		return false, err
	}
	state := stateQueued
	if item.Attempt >= 3 {
		state = stateNeedsHuman
	}
	_, err = tx.Exec(ctx, `UPDATE triage_repairs SET state=$2,lease_token=NULL,lease_expires_at=NULL,worker=NULL,updated_at=clock_timestamp() WHERE id=$1`, item.ID, state)
	if err != nil {
		return false, err
	}
	item.State = state
	item.LeaseToken = ""
	item.LeaseExpiresAt = nil
	return true, nil
}

// Claim atomically leases one prioritized repair to a worker.
func (h *Handlers) Claim(w http.ResponseWriter, r *http.Request) {
	if _, err := actor(r); err != nil {
		h.fail(w, err)
		return
	}
	var in struct {
		Repository string `json:"repository"`
		Worker     string `json:"worker"`
	}
	if err := decode(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	if !validRepository(in.Repository) || !bounded(in.Worker, 300) {
		h.fail(w, bad("repository and worker are required and bounded"))
		return
	}
	if err := scope(r, in.Repository); err != nil {
		h.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// SKIP LOCKED prevents workers blocking each other. A bounded batch also
	// accounts for exhausted expired rows before finding the next live item.
	var selected *Item
	for range 200 {
		item, e := scanItem(tx.QueryRow(ctx, `SELECT `+itemColumns+` FROM triage_repairs r
 WHERE r.repository=$1 AND (r.state='queued' OR (r.state='leased' AND r.lease_expires_at<=clock_timestamp()))
 ORDER BY observed_distinct_prs_failed DESC,r.created_at,r.id LIMIT 1 FOR UPDATE OF r SKIP LOCKED`, in.Repository))
		if errors.Is(e, pgx.ErrNoRows) {
			break
		}
		if e != nil {
			h.fail(w, e)
			return
		}
		if _, e = expire(ctx, tx, item); e != nil {
			h.fail(w, e)
			return
		}
		if item.State == stateNeedsHuman {
			continue
		}
		token := uuid.NewString()
		_, err = tx.Exec(ctx, `UPDATE triage_repairs SET state='leased',attempt=attempt+1,worker=$2,lease_token=$3,lease_expires_at=clock_timestamp()+($4*interval '1 second'),updated_at=clock_timestamp() WHERE id=$1`, item.ID, in.Worker, token, h.ttl().Seconds())
		if err != nil {
			h.fail(w, err)
			return
		}
		selected, err = lockedItem(ctx, tx, item.ID)
		if err != nil {
			h.fail(w, err)
			return
		}
		break
	}
	if err = tx.Commit(ctx); err != nil {
		h.fail(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": selected})
}

// Heartbeat renews a nonexpired fenced lease.
func (h *Handlers) Heartbeat(w http.ResponseWriter, r *http.Request) {
	if _, err := actor(r); err != nil {
		h.fail(w, err)
		return
	}
	id, err := itemID(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	var in struct {
		LeaseToken string `json:"lease_token"`
	}
	if err = decode(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	if err = requireToken(in.LeaseToken); err != nil {
		h.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
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
	expired, err := expire(ctx, tx, item)
	if err != nil {
		h.fail(w, err)
		return
	}
	if expired {
		if err = tx.Commit(ctx); err != nil {
			h.fail(w, err)
			return
		}
		h.fail(w, conflict("lease expired"))
		return
	}
	if item.State != stateLeased || !tokenValid(item, in.LeaseToken) {
		h.fail(w, conflict("lease is not active or token was superseded"))
		return
	}
	_, err = tx.Exec(ctx, `UPDATE triage_repairs SET lease_expires_at=clock_timestamp()+($2*interval '1 second'),updated_at=clock_timestamp() WHERE id=$1`, id, h.ttl().Seconds())
	if err != nil {
		h.fail(w, err)
		return
	}
	item, err = lockedItem(ctx, tx, id)
	if err != nil {
		h.fail(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		h.fail(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": item})
}

type completeRequest struct {
	LeaseToken  string `json:"lease_token"`
	Outcome     string `json:"outcome"`
	Account     string `json:"account"`
	EvidenceURL string `json:"evidence_url"`
	PRURL       string `json:"pr_url"`
}

func validateCompletion(in completeRequest) error {
	if err := requireToken(in.LeaseToken); err != nil {
		return err
	}
	switch in.Outcome {
	case "failed", "blocked", stateRepairPR, stateProductSuspect:
	default:
		return bad("invalid outcome")
	}
	if !bounded(in.Account, 16000) || (in.EvidenceURL != "" && !webURL(in.EvidenceURL)) || (in.Outcome != stateRepairPR && in.PRURL != "") {
		return bad("bounded account and valid outcome-specific evidence are required")
	}
	if (in.Outcome == stateRepairPR || in.Outcome == stateProductSuspect) && !webURL(in.EvidenceURL) {
		return bad("terminal outcomes require an evidence_url")
	}
	return nil
}

// Complete appends the fenced worker account and advances its repair cycle.
func (h *Handlers) Complete(w http.ResponseWriter, r *http.Request) {
	author, err := actor(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	id, err := itemID(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	var in completeRequest
	if err = decode(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	if err = validateCompletion(in); err != nil {
		h.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
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
	if in.Outcome == stateRepairPR && !repairURL(in.PRURL, item.Repository) {
		h.fail(w, bad("repair_pr requires a pull request URL in the repair repository"))
		return
	}
	expired, err := expire(ctx, tx, item)
	if err != nil {
		h.fail(w, err)
		return
	}
	if expired {
		if err = tx.Commit(ctx); err != nil {
			h.fail(w, err)
			return
		}
		h.fail(w, conflict("lease expired"))
		return
	}
	if item.State != stateLeased || !tokenValid(item, in.LeaseToken) {
		h.fail(w, conflict("lease is not active or outcome is already final"))
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO triage_repair_attempts(repair_id,attempt,lease_token,worker,author,outcome,account,evidence_url,pr_url) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, item.Attempt, in.LeaseToken, item.worker, author, in.Outcome, in.Account, in.EvidenceURL, in.PRURL)
	if err != nil {
		h.fail(w, err)
		return
	}
	state := in.Outcome
	if state == "failed" || state == "blocked" {
		state = stateQueued
		if item.Attempt >= 3 {
			state = stateNeedsHuman
		}
	}
	// Only product_suspect retains its completed token for the separate defect
	// operation. It can never claim again or turn into a repair PR.
	var token *string
	if state == stateProductSuspect {
		token = &in.LeaseToken
		if _, err = tx.Exec(ctx, `INSERT INTO triage_product_observations(repair_id,report_group_id,evidence,author) SELECT id,report_group_id,evidence,$2 FROM triage_repairs WHERE id=$1`, id, author); err != nil {
			h.fail(w, err)
			return
		}
	}
	_, err = tx.Exec(ctx, `UPDATE triage_repairs SET state=$2,lease_token=$3,lease_expires_at=NULL,worker=NULL,updated_at=clock_timestamp() WHERE id=$1`, id, state, token)
	if err != nil {
		h.fail(w, err)
		return
	}
	item, err = lockedItem(ctx, tx, id)
	if err != nil {
		h.fail(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		h.fail(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": item})
}
