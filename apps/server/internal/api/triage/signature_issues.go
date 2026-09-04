package triage

// "Has anyone already filed this?"
//
// Before an agent opens an issue or a fix PR it has to know whether one exists,
// or every recurrence of the same flake produces another duplicate. Doing that
// with a GitHub search means guessing at title wording; the ledger already
// knows, because every verdict carries the failure-signature hash that grouped
// the failures, and the rows that produced an issue carry its URL.
//
// Matching is on the signature or the test id, and every part of the answer —
// prior verdicts, fix attempts, escalations — honors both. A signature is the
// normalized error text, so it survives a test being renamed; a test id is
// stable across an error message being reworded. A request carrying only one of
// them must not get a partial answer, because "nothing found" is precisely the
// reply that produces a duplicate ticket.

import (
	"net/http"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

type signatureRef struct {
	VerdictID      string    `json:"verdict_id"`
	ExternalTestID *string   `json:"external_test_id,omitempty"`
	Signature      *string   `json:"cluster_signature,omitempty"`
	Verdict        string    `json:"verdict"`
	RootCause      *string   `json:"root_cause,omitempty"`
	PRURL          *string   `json:"pr_url,omitempty"`
	CommitSHA      string    `json:"commit_sha"`
	Branch         string    `json:"branch"`
	CreatedAt      time.Time `json:"created_at"`
}

// SignatureIssues serves GET /api/v1/triage/signature-issues.
//
// Public read: it returns prior verdicts and the fix PRs they led to, which is
// the same class of data the ledger list already exposes, minus the identity
// columns — no `corrected_by`, no OIDC subject.
func (h *Handlers) SignatureIssues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	testID := q.Get("test_id")
	signature := q.Get("signature")
	if testID == "" && signature == "" {
		api.WriteError(w, r, errBadRequest("test_id or signature is required"))
		return
	}
	since, err := parseSince(orDefault(q.Get("window"), "90d"))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	// Prior verdicts on the same failure. Ordered newest-first and capped: the
	// caller wants to know whether this is known and what came of it, not the
	// full history.
	rows, err := h.Pool.Query(r.Context(), `
		SELECT v.id::text, v.external_test_id, v.cluster_signature, v.verdict,
		       v.root_cause, v.commit_sha, v.branch, v.created_at
		FROM triage_verdicts v
		WHERE (v.repository = $1 OR split_part(v.repository, '/', 2) = $1)
		  AND v.created_at >= $2::timestamptz
		  AND ( ($3 <> '' AND v.external_test_id = $3)
		     OR ($4 <> '' AND v.cluster_signature = $4) )
		ORDER BY v.created_at DESC
		LIMIT 20
	`, normalizeRepo(repo), since, testID, signature)
	if err != nil {
		h.logError("signature issues verdicts", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	refs := []signatureRef{}
	for rows.Next() {
		var ref signatureRef
		if err := rows.Scan(&ref.VerdictID, &ref.ExternalTestID, &ref.Signature,
			&ref.Verdict, &ref.RootCause, &ref.CommitSHA, &ref.Branch, &ref.CreatedAt); err != nil {
			h.logError("signature issues scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		h.logError("signature issues rows", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	// Fix attempts carry the PR each one opened, which is the more direct
	// answer to "is someone already fixing this". Matched on either key for the
	// same reason as escalations below.
	attempts, err := h.attemptRefs(r, normalizeRepo(repo), testID, signature)
	if err != nil {
		h.logError("signature issues attempts", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	// Defects previously filed for this test. History, not a lock: an entry
	// means a defect was filed once, which is a reason to check the tracker —
	// never a reason to skip filing without checking, because the tracker owns
	// whether that ticket is still open.
	escalations, err := h.escalationsFor(r, normalizeRepo(repo), testID, signature)
	if err != nil {
		h.logError("signature issues escalations", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	// The handover verdict, computed from the same rule the queue uses. It has
	// to be here and not only on /triage/queue: this is the endpoint an agent
	// consults before deciding whether to attempt a test, and without it the
	// three-strikes rule silently never fires — the agent re-attempts a test it
	// has already given up on, forever.
	failedAttempts := 0
	lastOutcome := ""
	for i, a := range attempts {
		if i == 0 {
			lastOutcome = a.Outcome
		}
		if a.Outcome == "failed" || a.Outcome == "blocked" {
			failedAttempts++
		}
	}
	needsHuman := lastOutcome != outcomeFixed &&
		(failedAttempts >= MaxFixAttempts || lastOutcome == outcomeNeedsHuman)

	openPR := ""
	for _, a := range attempts {
		if a.PRURL != nil && a.Outcome == outcomeFixed {
			openPR = *a.PRURL
			break
		}
	}
	lastIssue := ""
	if len(escalations) > 0 {
		lastIssue = escalations[0].IssueURL
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repo":      normalizeRepo(repo),
		"test_id":   testID,
		"signature": signature,
		// known is the field a caller branches on: this failure has been seen
		// and adjudicated before, so treat a fresh investigation as optional.
		"known":       len(refs) > 0 || len(attempts) > 0 || len(escalations) > 0,
		"open_fix_pr": openPR,
		// The most recent defect filed for this test. Its state is unknown
		// here by design — resolve it against the tracker before acting.
		"last_issue_url": lastIssue,
		"prior_verdict":  refs,
		"fix_attempts":   attempts,
		// The agent has failed enough times that this test is a person's now.
		// Same rule and same field name as the queue entry carries.
		"needs_human": needsHuman,
		"escalations": escalations,
	})
}

type fixAttemptRef struct {
	Outcome   string    `json:"outcome"`
	Detail    string    `json:"detail"`
	PRURL     *string   `json:"pr_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handlers) attemptRefs(r *http.Request, repo, testID, signature string) ([]fixAttemptRef, error) {
	if testID == "" && signature == "" {
		return nil, nil
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT outcome, detail, pr_url, created_at
		FROM stabilization_fix_attempts
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND ( ($2 <> '' AND external_test_id = $2)
		     OR ($3 <> '' AND cluster_signature = $3) )
		ORDER BY created_at DESC
		LIMIT 20
	`, repo, testID, signature)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []fixAttemptRef{}
	for rows.Next() {
		var a fixAttemptRef
		if err := rows.Scan(&a.Outcome, &a.Detail, &a.PRURL, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
