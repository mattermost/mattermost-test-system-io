package triage

// Fix attempts — the half of "the AI tries, a human fixes it if that fails"
// that was previously only implied.
//
// The agent fix loop attempts a repair and a human reviews before anything
// merges, so an unsuccessful attempt is cheap and bounded. What was missing is
// any memory of it. Without one:
//
//   - the loop re-attempts the same unfixable test every cycle, spending model
//     calls on a problem it has already failed, while the rest of the queue
//     waits behind it;
//   - nobody can see which queued tests have already defeated the agent, so
//     "a human should take this one" is a judgement no data supports.
//
// The existing loop guard counts autofix commits on a single PR branch. That
// stops an AI↔CI ping-pong inside one PR and says nothing across cycles.
//
// So: every attempt is recorded with its outcome and the agent's own account
// of what it tried. The queue exposes the tally, and the loop refuses a test
// that has already failed MaxFixAttempts times — at which point the test is
// the human's, with the attempts as the handover notes.

import (
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

// MaxFixAttempts is how many times the agent may fail on one test before it is
// handed to a human. Three is not a tuned number — it is small on purpose: an
// agent that has failed three times on the same test with three different
// errors is not one more attempt away, and the cost of being wrong here is a
// test that waits for a person who was going to be needed anyway.
const MaxFixAttempts = 3

const (
	// outcomeFixed makes a detail optional, and releases a test from a human.
	outcomeFixed = "fixed"
	// outcomeNeedsHuman is an explicit escalation: the caller has decided this
	// test is a person's without waiting to exhaust the attempt budget. It is
	// deliberately NOT counted as a failed attempt — nothing was tried — but it
	// hands the test over on its own when it is the most recent outcome.
	outcomeNeedsHuman = "needs_human"
)

var validFixOutcomes = map[string]bool{
	outcomeFixed:      true,
	"failed":          true,
	"blocked":         true,
	outcomeNeedsHuman: true,
}

type fixAttemptInput struct {
	TestID           string  `json:"test_id"`
	Repository       string  `json:"repository"`
	Outcome          string  `json:"outcome"`
	Detail           string  `json:"detail"`
	PRURL            *string `json:"pr_url"`
	ClusterSignature *string `json:"cluster_signature"`

	// AttemptedBy is deliberately absent: attribution comes from the
	// authenticated principal, never from the body. Same rule as quarantine.
}

// fixAttemptSummary is what the queue carries per test.
type fixAttemptSummary struct {
	Attempts    int        `json:"attempts"`
	Failed      int        `json:"failed"`
	LastOutcome string     `json:"last_outcome,omitempty"`
	LastDetail  string     `json:"last_detail,omitempty"`
	LastAt      *time.Time `json:"last_attempt_at,omitempty"`
	// NeedsHuman is computed, not stored: a forgotten flag would go stale the
	// moment someone fixed the test by hand, whereas this is always a
	// statement about the rows that exist right now.
	//
	// True when the attempt budget is spent, or when the caller escalated
	// explicitly. Either way a later `fixed` clears it — the most recent
	// attempt is the one that counts, or a test the agent eventually repaired
	// would stay parked on a person forever.
	NeedsHuman bool `json:"needs_human"`
}

// RecordFixAttempt serves POST /api/v1/triage/attempts.
//
// Authenticated: this is what decides whether the loop tries a test again, so
// a forged "fixed" would let anything silently re-enter the loop, and a forged
// "failed" would park a test on a human who never agreed to take it.
func (h *Handlers) RecordFixAttempt(w http.ResponseWriter, r *http.Request) {
	var in fixAttemptInput
	if err := decodeJSONBody(w, r, &in); err != nil {
		// The decoder's own error is a parser detail, and mapError sends
		// anything it does not recognize to 500. A malformed body is the
		// caller's mistake, so say so.
		api.WriteError(w, r, errBadRequest("malformed JSON body"))
		return
	}
	if in.TestID == "" || in.Repository == "" {
		api.WriteError(w, r, fmt.Errorf("%w: test_id and repository are required", api.ErrBadRequest))
		return
	}
	if !validFixOutcomes[in.Outcome] {
		api.WriteError(w, r, fmt.Errorf(
			"%w: outcome must be one of fixed, failed, blocked, needs_human", api.ErrBadRequest))
		return
	}
	// A failed attempt with no account of what was tried is not a handover,
	// it is a shrug — and this row exists to be read by whoever picks the test
	// up next.
	if in.Outcome != outcomeFixed && in.Detail == "" {
		api.WriteError(w, r, fmt.Errorf(
			"%w: detail is required for a %s attempt — it is what the next person reads",
			api.ErrBadRequest, in.Outcome))
		return
	}

	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		// Unreachable behind RequireAuth, but a route left unprotected by a
		// future edit must fail closed rather than write an unattributed row —
		// and must say 401 rather than 500, which mapError would otherwise
		// return for an error it does not recognize.
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}

	repo := normalizeRepo(in.Repository)
	var id string
	if err := h.Pool.QueryRow(r.Context(), `
		INSERT INTO stabilization_fix_attempts
			(repository, external_test_id, outcome, detail, pr_url, cluster_signature, attempted_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id::text
	`, repo, in.TestID, in.Outcome, in.Detail, in.PRURL, in.ClusterSignature,
		subjectLabel(subject)).Scan(&id); err != nil {
		h.logError("record fix attempt", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	summary, err := h.fixAttemptsFor(r, repo, in.TestID)
	if err != nil {
		// The attempt IS recorded; only the echo failed. Saying so beats a 500
		// that invites the caller to retry and write the row twice.
		h.logError("fix attempt summary", err)
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "test_id": in.TestID})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          id,
		"test_id":     in.TestID,
		"attempts":    summary.Attempts,
		"failed":      summary.Failed,
		"needs_human": summary.NeedsHuman,
	})
}

func (h *Handlers) fixAttemptsFor(r *http.Request, repo, testID string) (fixAttemptSummary, error) {
	var s fixAttemptSummary
	var lastOutcome, lastDetail *string
	var lastAt *time.Time
	err := h.Pool.QueryRow(r.Context(), `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE outcome IN ('failed','blocked'))::int,
		       (array_agg(outcome    ORDER BY created_at DESC))[1],
		       (array_agg(detail     ORDER BY created_at DESC))[1],
		       max(created_at)
		FROM stabilization_fix_attempts
		WHERE repository = $1 AND external_test_id = $2
	`, repo, testID).Scan(&s.Attempts, &s.Failed, &lastOutcome, &lastDetail, &lastAt)
	if err != nil {
		return s, err
	}
	if lastOutcome != nil {
		s.LastOutcome = *lastOutcome
	}
	if lastDetail != nil {
		s.LastDetail = *lastDetail
	}
	s.LastAt = lastAt
	// A test the agent has already fixed is not the human's problem, whatever
	// the earlier failures were — the last attempt is the one that counts.
	s.NeedsHuman = s.LastOutcome != outcomeFixed &&
		(s.Failed >= MaxFixAttempts || s.LastOutcome == outcomeNeedsHuman)
	return s, nil
}

// loadFixAttempts returns the per-test attempt summary for a repo, keyed by
// external_test_id, so the queue can annotate its entries in one round trip.
func (h *Handlers) loadFixAttempts(r *http.Request, repo string) (map[string]fixAttemptSummary, error) {
	rows, err := h.Pool.Query(r.Context(), `
		SELECT external_test_id,
		       count(*)::int,
		       count(*) FILTER (WHERE outcome IN ('failed','blocked'))::int,
		       (array_agg(outcome ORDER BY created_at DESC))[1],
		       (array_agg(detail  ORDER BY created_at DESC))[1],
		       max(created_at)
		FROM stabilization_fix_attempts
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		GROUP BY external_test_id
	`, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]fixAttemptSummary{}
	for rows.Next() {
		var testID string
		var s fixAttemptSummary
		var lastOutcome, lastDetail *string
		if err := rows.Scan(&testID, &s.Attempts, &s.Failed, &lastOutcome, &lastDetail, &s.LastAt); err != nil {
			return nil, err
		}
		if lastOutcome != nil {
			s.LastOutcome = *lastOutcome
		}
		if lastDetail != nil {
			s.LastDetail = *lastDetail
		}
		s.NeedsHuman = s.LastOutcome != outcomeFixed &&
			(s.Failed >= MaxFixAttempts || s.LastOutcome == outcomeNeedsHuman)
		out[testID] = s
	}
	return out, rows.Err()
}
