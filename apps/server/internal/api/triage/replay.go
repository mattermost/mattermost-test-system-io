package triage

// Replay candidates — the collection window's one moving part.
//
// While the calling repository's workflows are unmerged, nothing writes a
// verdict, so history accumulates but accuracy does not. The replay job closes
// that gap: it walks runs TSIO has already ingested, re-adjudicates each one
// through the same evidence pack, classifier, model and policy layer a live
// triage job would use, and records the result as a real ledger row marked
// `replay` (migration 000034). No CI job reads those rows, so nothing flips.
//
// This file is only the worklist. Everything that decides anything lives in
// the action, deliberately: a second implementation of the policy gate in Go
// would be a second thing to keep in sync, and the gate is the part that must
// never drift.

import (
	"net/http"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

type replayCandidate struct {
	GroupID    string    `json:"group_id"`
	Repository string    `json:"repository"`
	Branch     string    `json:"branch"`
	CommitSHA  string    `json:"commit_sha"`
	GHRunID    string    `json:"gh_run_id"`
	GHPRNumber *int      `json:"gh_pr_number"`
	Name       string    `json:"name"`
	Failed     int       `json:"failed"`
	CreatedAt  time.Time `json:"created_at"`
}

// ReplayCandidates serves GET /api/v1/triage/replay/candidates?repo=&days=&limit=
// — ingested runs that have at least one failed test and no verdict yet.
//
// Public read, same posture as the other aggregate reads: it returns run
// coordinates the report list already exposes, and the replay job consults it
// before every batch.
func (h *Handlers) ReplayCandidates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	days := parseInt(q.Get("days"), 30)
	if days < 1 || days > 180 {
		api.WriteError(w, r, errRepoRequiredWith("days must be between 1 and 180"))
		return
	}
	limit := parseInt(q.Get("limit"), 50)
	if limit < 1 || limit > 500 {
		api.WriteError(w, r, errRepoRequiredWith("limit must be between 1 and 500"))
		return
	}
	branch := q.Get("branch") // empty = every branch

	// A run qualifies when it has a failed case and no ledger row for that
	// (repository, commit_sha, gh_run_id). Matching on the run rather than on
	// the cluster is deliberate: a partially-adjudicated run would otherwise
	// come back forever, and the verdict upsert is keyed per cluster anyway, so
	// re-running a whole group is idempotent.
	rows, err := h.Pool.Query(r.Context(), `
		SELECT g.id::text, g.repository, g.branch, g.commit_sha, g.gh_run_id,
		       g.gh_pr_number, g.name, count(*)::int AS failed, g.created_at
		FROM report_groups g
		JOIN reports rp    ON rp.report_group_id = g.id
		JOIN suites s      ON s.report_id = rp.id
		JOIN test_cases tc ON tc.suite_id = s.id
		WHERE (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
		  AND ($2 = '' OR g.branch = $2)
		  AND g.created_at >= now() - make_interval(days => $3)
		  AND tc.status = 'failed'
		  AND NOT EXISTS (
		      SELECT 1 FROM triage_verdicts v
		      WHERE v.repository = g.repository
		        AND v.commit_sha = g.commit_sha
		        AND v.gh_run_id  = g.gh_run_id
		  )
		GROUP BY g.id, g.repository, g.branch, g.commit_sha, g.gh_run_id,
		         g.gh_pr_number, g.name, g.created_at
		ORDER BY g.created_at DESC
		LIMIT $4
	`, repo, branch, days, limit)
	if err != nil {
		h.logError("replay candidates", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	out := []replayCandidate{}
	for rows.Next() {
		var c replayCandidate
		if err := rows.Scan(&c.GroupID, &c.Repository, &c.Branch, &c.CommitSHA,
			&c.GHRunID, &c.GHPRNumber, &c.Name, &c.Failed, &c.CreatedAt); err != nil {
			h.logError("replay candidates scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		h.logError("replay candidates rows", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repo":       repo,
		"branch":     branch,
		"days":       days,
		"candidates": out,
		"count":      len(out),
	})
}
