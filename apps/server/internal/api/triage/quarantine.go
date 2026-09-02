// R7-L3 — explicit, owned, expiring quarantine.
//
// THE GAP THIS FILLS. A chronically flaky test had two fates: fixed (slow — the
// stabilization queue drains 0.10-0.37/day against 1.5/day arrival) or waived
// on every PR forever. There was no third state, so the amnesty message that
// tells people to "fix or quarantine explicitly" pointed at nothing.
//
// Quarantine is the third state, and it is deliberately the STRICTER one. The
// R7-C chronic-flake carve-out greens a bystander PR automatically with no
// owner and no deadline; a quarantine does the same thing but is only creatable
// by an authenticated human, names an owner, and expires on a date after which
// the test goes red again by itself.
//
// THREE INVARIANTS, each with a test:
//
//  1. Master is untouched. Quarantine is consulted only when deciding a PR
//     check. The test keeps running on master, its failures keep landing in
//     raw_failures, and it keeps its place in the stabilization ranking. The
//     number the team is judged by cannot be improved by quarantining anything.
//  2. Expiry is self-enforcing. Active means released_at IS NULL AND
//     expires_at > now(), evaluated at read time. No cron, no sweeper. A
//     forgotten quarantine lapses instead of persisting.
//  3. Nothing is silent. Creation, release and every application are recorded,
//     and applied_count shows what a quarantine actually bought.

package triage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

// maxQuarantineDays caps how long a single quarantine may last. A quarantine
// longer than a stabilization cycle (review latency + the 7-day re-measurement
// window, so ~14 days at worst) is not a deadline, it is the bucket list with
// extra steps. Renewal is possible but must be an explicit new decision.
const maxQuarantineDays = 30

// QuarantineEntry is one quarantine record. Active is computed at read time so
// callers never have to re-derive the expiry rule.
type QuarantineEntry struct {
	ID             string     `json:"id"`
	Repository     string     `json:"repository"`
	ExternalTestID string     `json:"external_test_id"`
	Owner          string     `json:"owner"`
	Reason         string     `json:"reason"`
	CreatedBy      string     `json:"created_by"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	ReleasedAt     *time.Time `json:"released_at,omitempty"`
	ReleasedBy     *string    `json:"released_by,omitempty"`
	ReleaseReason  *string    `json:"release_reason,omitempty"`
	AppliedCount   int        `json:"applied_count"`
	// Active is the only field the policy layer should branch on.
	Active bool `json:"active"`
	// DaysRemaining is negative once expired; surfaced so a reviewer sees the
	// deadline without doing date arithmetic.
	DaysRemaining int `json:"days_remaining"`
}

type quarantineInput struct {
	TestID    string `json:"test_id"`
	Owner     string `json:"owner"`
	Reason    string `json:"reason"`
	ExpiresAt string `json:"expires_at"`
	Days      int    `json:"days"`
}

type releaseInput struct {
	Reason string `json:"reason"`
}

// normalizeRepo mirrors the M7 fix used by the promotion handler: a bare
// "mattermost" stored here would make split_part matching degenerate.
func normalizeRepo(repo string) string {
	if !strings.Contains(repo, "/") {
		return "mattermost/" + repo
	}
	return repo
}

// Quarantine serves POST /api/v1/triage/quarantine?repo= — put one test behind
// an owned, expiring quarantine.
//
// Every guardrail is enforced here rather than trusted from the caller: owner,
// reason and a bounded deadline are all mandatory, and the identity is taken
// from the authenticated subject, never from the body.
func (h *Handlers) Quarantine(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	var in quarantineInput
	if err := decodeJSONBody(w, r, &in); err != nil {
		api.WriteError(w, r, errRepoRequiredWith("malformed JSON body"))
		return
	}
	if in.TestID == "" {
		api.WriteError(w, r, errRepoRequiredWith("test_id is required"))
		return
	}
	// An unowned quarantine is an orphan — this is the bucket list's failure
	// mode and it is refused at the door.
	if strings.TrimSpace(in.Owner) == "" {
		api.WriteError(w, r, errRepoRequiredWith("owner is required — a quarantine with no owner is how the old bucket list failed"))
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		api.WriteError(w, r, errRepoRequiredWith("reason is required"))
		return
	}

	expires, err := resolveQuarantineExpiry(in, time.Now())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	repo = normalizeRepo(repo)

	entry, err := h.insertQuarantine(r.Context(), repo, in, expires, subjectLabel(subject))
	if err != nil {
		if errors.Is(err, errAlreadyQuarantined) {
			api.WriteError(w, r, errRepoRequiredWith(
				fmt.Sprintf("%s is already under a live quarantine in %s — release it first, or let it expire", in.TestID, repo)))
			return
		}
		h.logError("triage quarantine insert", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

var errAlreadyQuarantined = errors.New("already quarantined")

// resolveQuarantineExpiry accepts either an explicit RFC3339 expires_at or a
// day count, and bounds both. Requiring one of them (no default) is
// deliberate: a caller that forgets the deadline must fail, not silently get
// the maximum.
func resolveQuarantineExpiry(in quarantineInput, now time.Time) (time.Time, error) {
	switch {
	case in.ExpiresAt != "" && in.Days > 0:
		return time.Time{}, errRepoRequiredWith("give either expires_at or days, not both")
	case in.ExpiresAt != "":
		t, err := time.Parse(time.RFC3339, in.ExpiresAt)
		if err != nil {
			return time.Time{}, errRepoRequiredWith("expires_at must be RFC3339")
		}
		return t, validateQuarantineWindow(t, now)
	case in.Days > 0:
		t := now.AddDate(0, 0, in.Days)
		return t, validateQuarantineWindow(t, now)
	default:
		return time.Time{}, errRepoRequiredWith(
			fmt.Sprintf("expires_at or days is required — quarantine is a deadline, not a destination (max %d days)", maxQuarantineDays))
	}
}

func validateQuarantineWindow(expires, now time.Time) error {
	if !expires.After(now) {
		return errRepoRequiredWith("expires_at must be in the future")
	}
	if expires.After(now.AddDate(0, 0, maxQuarantineDays)) {
		return errRepoRequiredWith(fmt.Sprintf("quarantine may not exceed %d days", maxQuarantineDays))
	}
	return nil
}

// insertQuarantine supersedes a lapsed row and inserts the new one in one
// transaction. The supersede is what keeps the live-unique index from blocking
// re-quarantine after an expiry that nobody released: the lapsed row is stamped
// as released by 'system:expiry' AT its own deadline, so the audit trail shows
// it ran out rather than being canceled by a person.
func (h *Handlers) insertQuarantine(ctx context.Context, repo string, in quarantineInput, expires time.Time, by string) (QuarantineEntry, error) {
	var e QuarantineEntry
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return e, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE triage_quarantine
		SET released_at = expires_at,
		    released_by = 'system:expiry',
		    release_reason = 'quarantine expired without being released'
		WHERE repository = $1 AND external_test_id = $2
		  AND released_at IS NULL AND expires_at <= now()
	`, repo, in.TestID); err != nil {
		return e, err
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO triage_quarantine
			(repository, external_test_id, owner, reason, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, repository, external_test_id, owner, reason, created_by,
		          expires_at, created_at, applied_count
	`, repo, in.TestID, strings.TrimSpace(in.Owner), strings.TrimSpace(in.Reason), by, expires)
	if err := row.Scan(&e.ID, &e.Repository, &e.ExternalTestID, &e.Owner, &e.Reason,
		&e.CreatedBy, &e.ExpiresAt, &e.CreatedAt, &e.AppliedCount); err != nil {
		if isUniqueViolation(err) {
			return e, errAlreadyQuarantined
		}
		return e, err
	}
	if err := tx.Commit(ctx); err != nil {
		return e, err
	}
	decorateQuarantine(&e, time.Now())
	return e, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}

// decorateQuarantine computes the two derived fields. Active is the ONLY thing
// the policy layer branches on, and it is evaluated at read time so expiry
// needs no sweeper.
func decorateQuarantine(e *QuarantineEntry, now time.Time) {
	e.Active = e.ReleasedAt == nil && e.ExpiresAt.After(now)
	e.DaysRemaining = int(e.ExpiresAt.Sub(now).Hours() / 24)
}

// ListQuarantine serves GET /api/v1/triage/quarantine?repo=[&test_id=][&all=true]
//
// Public read: the PR triage action and the test runner both need it without a
// credential round-trip, the same reasoning as the stabilization queue. Writes
// stay authenticated.
func (h *Handlers) ListQuarantine(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	repo = normalizeRepo(repo)
	testID := r.URL.Query().Get("test_id")
	includeInactive := r.URL.Query().Get("all") == "true"

	entries, err := h.loadQuarantine(r.Context(), repo, testID, includeInactive)
	if err != nil {
		h.logError("triage quarantine list", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	active := 0
	for _, e := range entries {
		if e.Active {
			active++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"repository":   repo,
		"count":        len(entries),
		"active_count": active,
		"max_days":     maxQuarantineDays,
		"quarantine":   entries,
	})
}

func (h *Handlers) loadQuarantine(ctx context.Context, repo, testID string, includeInactive bool) ([]QuarantineEntry, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT id::text, repository, external_test_id, owner, reason, created_by,
		       expires_at, created_at, released_at, released_by, release_reason, applied_count
		FROM triage_quarantine
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND ($2 = '' OR external_test_id = $2)
		  AND ($3::bool OR (released_at IS NULL AND expires_at > now()))
		ORDER BY created_at DESC
	`, repo, testID, includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now()
	out := []QuarantineEntry{}
	for rows.Next() {
		var e QuarantineEntry
		if err := rows.Scan(&e.ID, &e.Repository, &e.ExternalTestID, &e.Owner, &e.Reason,
			&e.CreatedBy, &e.ExpiresAt, &e.CreatedAt, &e.ReleasedAt, &e.ReleasedBy,
			&e.ReleaseReason, &e.AppliedCount); err != nil {
			return nil, err
		}
		decorateQuarantine(&e, now)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReleaseQuarantine serves POST /api/v1/triage/quarantine/{id}/release — end a
// quarantine early because the test was actually fixed.
func (h *Handlers) ReleaseQuarantine(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		api.WriteError(w, r, errRepoRequiredWith("id is required"))
		return
	}
	var in releaseInput
	if err := decodeJSONBody(w, r, &in); err != nil {
		api.WriteError(w, r, errRepoRequiredWith("malformed JSON body"))
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		api.WriteError(w, r, errRepoRequiredWith("reason is required — a release is a decision too"))
		return
	}
	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	var e QuarantineEntry
	row := h.Pool.QueryRow(r.Context(), `
		UPDATE triage_quarantine
		SET released_at = now(), released_by = $2, release_reason = $3
		WHERE id = $1::uuid AND released_at IS NULL
		RETURNING id::text, repository, external_test_id, owner, reason, created_by,
		          expires_at, created_at, released_at, released_by, release_reason, applied_count
	`, id, subjectLabel(subject), strings.TrimSpace(in.Reason))
	if err := row.Scan(&e.ID, &e.Repository, &e.ExternalTestID, &e.Owner, &e.Reason,
		&e.CreatedBy, &e.ExpiresAt, &e.CreatedAt, &e.ReleasedAt, &e.ReleasedBy,
		&e.ReleaseReason, &e.AppliedCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			api.WriteError(w, r, fmt.Errorf("%w: no open quarantine with id %s", api.ErrNotFound, id))
			return
		}
		h.logError("triage quarantine release", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	decorateQuarantine(&e, time.Now())
	writeJSON(w, http.StatusOK, e)
}

// activeQuarantineFor returns the live quarantine for one test, or nil. Used by
// the evidence pack so the PR triage action gets it in the same payload as
// everything else rather than making a second call per failure.
//
// A lookup error degrades to nil: an unavailable quarantine table must never
// green a check. Absence of a quarantine is the fail-closed direction.
func (h *Handlers) activeQuarantineFor(ctx context.Context, repo, testID string) *QuarantineEntry {
	if testID == "" {
		return nil
	}
	entries, err := h.loadQuarantine(ctx, normalizeRepo(repo), testID, false)
	if err != nil {
		h.logError("triage active quarantine lookup", err)
		return nil
	}
	for i := range entries {
		if entries[i].Active {
			return &entries[i]
		}
	}
	return nil
}

// NoteQuarantineApplied increments the counter that shows what a quarantine
// actually bought. Best-effort by design: failing to count must not fail the
// triage run that already decided.
func (h *Handlers) NoteQuarantineApplied(ctx context.Context, id string) {
	if _, err := h.Pool.Exec(ctx, `
		UPDATE triage_quarantine SET applied_count = applied_count + 1
		WHERE id = $1::uuid AND released_at IS NULL
	`, id); err != nil {
		h.logError("triage quarantine applied_count", err)
	}
}
