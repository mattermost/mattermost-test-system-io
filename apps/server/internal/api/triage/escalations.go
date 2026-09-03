package triage

// Product defects E2E triage surfaced — recorded and counted, never mirrored.
//
// When a test is correct and the product is wrong, the agent must not touch the
// test. It files a defect in the issue tracker and tells this service that it
// did.
//
// The division of labor matters, because getting it wrong is how this rots:
//
//	The tracker owns ticket STATE. Is it open, assigned, fixed? Only the tracker
//	knows, and it is always current. The agent deduplicates against the tracker
//	directly — a label query on the test id — so a ticket somebody closes this
//	afternoon stops suppressing a new escalation this evening.
//
//	This service owns the METRIC. Which tests produce product defects, how many,
//	and when. That is a question about test history, which is this service's
//	job, and it is answered from events that already happened.
//
// So the table is append-only and has no resolved_at. A copy of "is it still
// open" would be stale the moment anyone closed a ticket, and the test could
// then never be escalated again when it regressed — worse than not tracking it,
// because it fails silently.

import (
	"net/http"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

type escalationInput struct {
	TestID           string  `json:"test_id"`
	Repository       string  `json:"repository"`
	IssueKey         string  `json:"issue_key"`
	IssueURL         string  `json:"issue_url"`
	Summary          string  `json:"summary"`
	ClusterSignature *string `json:"cluster_signature"`
	// SuspectRange is the last_pass..failing_since range that justified the
	// defect, when one existed. Absent is the honest value for a break that
	// predates the history window — see baselineFor.
	SuspectRange *string `json:"suspect_range"`

	// EscalatedBy is deliberately absent: attribution comes from the
	// authenticated principal, never from the body.
}

// Escalation is one filed defect, as it happened.
type Escalation struct {
	ID               string    `json:"id"`
	ExternalTestID   string    `json:"external_test_id"`
	ClusterSignature *string   `json:"cluster_signature,omitempty"`
	IssueKey         string    `json:"issue_key"`
	IssueURL         string    `json:"issue_url"`
	Summary          string    `json:"summary,omitempty"`
	SuspectRange     *string   `json:"suspect_range,omitempty"`
	EscalatedBy      string    `json:"escalated_by,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// RecordEscalation serves POST /api/v1/triage/escalations.
//
// Call it after the ticket exists, not before — this records something that
// happened. Deduplication is not this endpoint's job: the agent asks the
// tracker, and only files (and so only posts here) when no open ticket exists
// for the test.
func (h *Handlers) RecordEscalation(w http.ResponseWriter, r *http.Request) {
	var in escalationInput
	if err := decodeJSONBody(w, r, &in); err != nil {
		api.WriteError(w, r, errBadRequest("malformed JSON body"))
		return
	}
	if in.TestID == "" || in.Repository == "" {
		api.WriteError(w, r, errBadRequest("test_id and repository are required"))
		return
	}
	if in.IssueKey == "" || in.IssueURL == "" {
		api.WriteError(w, r, errBadRequest(
			"issue_key and issue_url are required — an escalation with no ticket records nothing"))
		return
	}

	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}

	var e Escalation
	if err := h.Pool.QueryRow(r.Context(), `
		INSERT INTO triage_defect_escalations
			(repository, external_test_id, cluster_signature, issue_key, issue_url,
			 summary, suspect_range, escalated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id::text, external_test_id, cluster_signature, issue_key, issue_url,
		          summary, suspect_range, escalated_by, created_at
	`, normalizeRepo(in.Repository), in.TestID, in.ClusterSignature, in.IssueKey, in.IssueURL,
		in.Summary, in.SuspectRange, subjectLabel(subject)).Scan(
		&e.ID, &e.ExternalTestID, &e.ClusterSignature, &e.IssueKey, &e.IssueURL,
		&e.Summary, &e.SuspectRange, &e.EscalatedBy, &e.CreatedAt); err != nil {
		h.logError("record escalation", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"escalation": e})
}

type defectRow struct {
	TestID    string    `json:"test_id"`
	Defects   int       `json:"defects"`
	LastAt    time.Time `json:"last_escalated_at"`
	LatestKey string    `json:"latest_issue_key"`
	LatestURL string    `json:"latest_issue_url"`
}

// Defects serves GET /api/v1/triage/defects?repo=&window= — the metric.
//
// How many product defects E2E surfaced in the window, and which tests produced
// them. A test appearing repeatedly here is not flaky: it is a test that keeps
// catching real bugs, which is the opposite signal and must never be read off
// the flakiness leaderboard.
//
// Public: counts per test id, the same class of data the flakiness leaderboard
// already exposes. Ticket state is deliberately absent — ask the tracker.
func (h *Handlers) Defects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	window := orDefault(q.Get("window"), "90d")
	since, err := parseSince(window)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	rows, err := h.Pool.Query(r.Context(), `
		SELECT external_test_id,
		       count(*)::int,
		       max(created_at),
		       (array_agg(issue_key ORDER BY created_at DESC))[1],
		       (array_agg(issue_url ORDER BY created_at DESC))[1]
		FROM triage_defect_escalations
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND created_at >= $2::timestamptz
		GROUP BY external_test_id
		ORDER BY count(*) DESC, max(created_at) DESC
		LIMIT 100
	`, normalizeRepo(repo), since)
	if err != nil {
		h.logError("defects rollup", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	out := []defectRow{}
	total := 0
	for rows.Next() {
		var d defectRow
		if err := rows.Scan(&d.TestID, &d.Defects, &d.LastAt, &d.LatestKey, &d.LatestURL); err != nil {
			h.logError("defects scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		total += d.Defects
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		h.logError("defects rows", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repo":   normalizeRepo(repo),
		"window": window,
		// Escalation EVENTS, not open tickets: some of these are long fixed.
		"total_escalations": total,
		"tests":             out,
	})
}

// escalationsFor returns the defects filed for one test, newest first. Folded
// into signature-issues so a single call tells an agent everything already
// known about a failure: prior verdicts, fix attempts, and defects.
//
// This is history, not a lock. An entry means "a defect was filed once", which
// is a reason to go and check the tracker — never a reason to skip filing
// without checking.
func (h *Handlers) escalationsFor(r *http.Request, repo, testID string) ([]Escalation, error) {
	if testID == "" {
		return nil, nil
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT id::text, external_test_id, cluster_signature, issue_key, issue_url,
		       summary, suspect_range, escalated_by, created_at
		FROM triage_defect_escalations
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND external_test_id = $2
		ORDER BY created_at DESC
		LIMIT 10
	`, repo, testID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Escalation{}
	for rows.Next() {
		var e Escalation
		if err := rows.Scan(&e.ID, &e.ExternalTestID, &e.ClusterSignature, &e.IssueKey,
			&e.IssueURL, &e.Summary, &e.SuspectRange, &e.EscalatedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
