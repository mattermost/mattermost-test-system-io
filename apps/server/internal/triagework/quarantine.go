package triagework

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Quarantine records an owner, ticket and expiry within the serialized cap.
func (h *Handlers) Quarantine(w http.ResponseWriter, r *http.Request) {
	author, err := actor(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	var in struct {
		RepairID  string    `json:"repair_id"`
		Owner     string    `json:"owner"`
		Ticket    string    `json:"ticket"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err = decode(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	if _, e := uuid.Parse(in.RepairID); e != nil {
		h.fail(w, bad("invalid repair_id"))
		return
	}
	if !bounded(in.Owner, 200) || !bounded(in.Ticket, 1000) || in.ExpiresAt.IsZero() {
		h.fail(w, bad("owner, ticket and expires_at are required"))
		return
	}
	capLimit := h.QuarantineCap
	if capLimit <= 0 {
		h.fail(w, conflict("quarantine is disabled until a positive cap is configured"))
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
	// Global cap has one lock namespace across all API replicas and repositories.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(823749012)`); err != nil {
		h.fail(w, err)
		return
	}
	item, err := lockedItem(ctx, tx, in.RepairID)
	if err != nil {
		h.fail(w, err)
		return
	}
	if err = scope(r, item.Repository); err != nil {
		h.fail(w, err)
		return
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT $1::timestamptz>clock_timestamp() AND $1::timestamptz<=clock_timestamp()+interval '14 days'`, in.ExpiresAt).Scan(&valid); err != nil {
		h.fail(w, err)
		return
	}
	if !valid {
		h.fail(w, bad("quarantine expiry must be in the future and within 14 days"))
		return
	}
	var active int
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_or(repair_id=$1),false) FROM triage_quarantines WHERE expires_at>clock_timestamp()`, in.RepairID).Scan(&active, &exists); err != nil {
		h.fail(w, err)
		return
	}
	if exists {
		h.fail(w, conflict("repair already has an active quarantine"))
		return
	}
	if active >= capLimit {
		h.fail(w, conflict("quarantine cap reached"))
		return
	}
	var q QuarantineItem
	q.RepairID = in.RepairID
	q.Repository = item.Repository
	q.Owner = in.Owner
	q.Ticket = in.Ticket
	q.Author = author
	q.ExpiresAt = in.ExpiresAt
	q.Active = true
	err = tx.QueryRow(ctx, `INSERT INTO triage_quarantines(repair_id,owner,ticket,author,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING id::text,created_at`, in.RepairID, in.Owner, in.Ticket, author, in.ExpiresAt).Scan(&q.ID, &q.CreatedAt)
	if err != nil {
		h.fail(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		h.fail(w, err)
		return
	}
	write(w, http.StatusCreated, map[string]any{"item": q})
}

// ListQuarantine reports active and expired quarantine records.
func (h *Handlers) ListQuarantine(w http.ResponseWriter, r *http.Request) {
	repo, limit, err := listParams(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := h.Pool.Query(ctx, `SELECT q.id::text,q.repair_id::text,r.repository,q.owner,q.ticket,q.author,q.expires_at,q.created_at,q.expires_at>clock_timestamp(),NOT EXISTS(SELECT 1 FROM triage_quarantines active WHERE active.repair_id=q.repair_id AND active.expires_at>clock_timestamp()) FROM triage_quarantines q JOIN triage_repairs r ON r.id=q.repair_id WHERE r.repository=$1 ORDER BY q.created_at DESC,q.id LIMIT $2`, repo, limit+1)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer rows.Close()
	items := []QuarantineItem{}
	for rows.Next() {
		var q QuarantineItem
		if err = rows.Scan(&q.ID, &q.RepairID, &q.Repository, &q.Owner, &q.Ticket, &q.Author, &q.ExpiresAt, &q.CreatedAt, &q.Active, &q.BlockingEligible); err != nil {
			h.fail(w, err)
			return
		}
		items = append(items, q)
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
