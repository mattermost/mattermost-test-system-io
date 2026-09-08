package triagework

import (
	"context"
	"net/http"
	"time"
)

// Resolve records a trusted human's repair attestation separately from the
// three automated attempts. This API does not independently verify PR merging.
func (h *Handlers) Resolve(w http.ResponseWriter, r *http.Request) {
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
	var in struct {
		Account     string `json:"account"`
		EvidenceURL string `json:"evidence_url"`
		PRURL       string `json:"pr_url"`
	}
	if err = decode(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	if !bounded(in.Account, 16000) || !webURL(in.EvidenceURL) {
		h.fail(w, bad("human resolution requires account and evidence_url"))
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
	if !repairURL(in.PRURL, item.Repository) {
		h.fail(w, bad("human resolution requires a repair PR URL in this repository"))
		return
	}
	if item.State != stateNeedsHuman && item.State != stateRepairPR {
		h.fail(w, conflict("only needs_human or repair_pr work can be resolved"))
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO triage_repair_resolutions(repair_id,author,account,evidence_url,pr_url) VALUES($1,$2,$3,$4,$5)`, id, author, in.Account, in.EvidenceURL, in.PRURL); err != nil {
		h.fail(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE triage_repairs SET state='resolved',updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
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
	write(w, http.StatusOK, map[string]any{"item": item, "resolution_basis": "trusted_human_attestation"})
}
