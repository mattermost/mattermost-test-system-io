// W3 — blind waiver audit.
//
// The sampler draws a stratified set of recent waived verdicts; the review
// surface shows the failure and its evidence WITHOUT the AI verdict (the API
// payload omits it — blindness is enforced server-side, not by hiding it in
// the UI); the reviewer submits agree/disagree; only then does the item
// detail include the verdict. Agreement rate over a trailing window is W13's
// promotion gate.
//
// Sampling rules (build-acceptance, tunable):
//   - 10 items from the trailing 7 days, stratified 5 FLAKY_TEST /
//     3 MAIN_REGRESSION / 2 FLAKY_INFRA.
//   - Short stratum refills from the others; fewer than 10 total takes all
//     and reports the denominator.
//   - Prefer verdicts not reviewed in the last 30 days, but never let that
//     preference shrink the sample — refill over it.
//   - Force-include every waiver issued during a demoted window (W13), so the
//     verdicts that cost triage its authority are always audited.
package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

// Strata: verdict class → quota in a 10-item sample.
var auditStrata = []struct {
	match string // verdict prefix / exact
	label string
	quota int
}{
	{"FLAKY_TEST", "flaky_test", 5},
	{"MAIN_REGRESSION", "main_regression", 3},
	{"FLAKY_INFRA", "flaky_infra", 2},
}

const (
	auditSampleWindow   = "7d"
	auditNoRepeatWindow = "30d"
	auditTargetSize     = 10
)

type auditSampleItem struct {
	VerdictID    string           `json:"verdict_id"`
	Repository   string           `json:"repository"`
	CommitSHA    string           `json:"commit_sha"`
	Branch       string           `json:"branch"`
	GHRunID      string           `json:"gh_run_id"`
	GHPRNumber   *int             `json:"gh_pr_number,omitempty"`
	TestID       *string          `json:"external_test_id,omitempty"`
	Stratum      string           `json:"stratum"`
	ForceInclude bool             `json:"force_included"`
	Evidence     []map[string]any `json:"evidence"`
	// Deliberately NO verdict / confidence / root_cause fields: the reviewer
	// must call it blind. The item detail endpoint reveals them after submit.
	SuspectCommit *string   `json:"suspect_commit,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	Reviewed      bool      `json:"reviewed"`
}

type auditSampleResponse struct {
	Items      []auditSampleItem `json:"items"`
	TargetSize int               `json:"target_size"`
	PoolSize   int               `json:"pool_size"`
	Shortfall  int               `json:"shortfall"`
	Note       string            `json:"note,omitempty"`
}

// AuditSample serves GET /api/v1/triage/audit/sample?repo= — the blind review
// queue. The AI verdict is absent from every item in this payload.
func (h *Handlers) AuditSample(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}

	since := time.Now().Add(-7 * 24 * time.Hour)
	items, pool, err := h.sampleAuditItems(r.Context(), repo, since)
	if err != nil {
		h.logError("triage audit sample", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	resp := auditSampleResponse{
		Items:      items,
		TargetSize: auditTargetSize,
		PoolSize:   pool,
	}
	if len(items) < auditTargetSize {
		resp.Shortfall = auditTargetSize - len(items)
		resp.Note = "small pool: sampled everything available; denominator recorded"
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) sampleAuditItems(ctx context.Context, repo string, since time.Time) ([]auditSampleItem, int, error) {
	// Candidate waived verdicts in the window. recently_reviewed drives the
	// no-repeat preference; force_included marks demoted-window waivers.
	rows, err := h.Pool.Query(ctx, `
		SELECT v.id::text, v.repository, v.commit_sha, v.branch, v.gh_run_id,
		       v.gh_pr_number, v.external_test_id, v.verdict, v.cluster_signature,
		       v.suspect_commit, v.created_at,
		       EXISTS (
		           SELECT 1 FROM triage_audit_reviews ar
		           WHERE ar.verdict_id = v.id AND ar.reviewed_at >= now() - interval '30 days'
		       ) AS recently_reviewed,
		       v.evidence
		FROM triage_verdicts v
		WHERE v.waived
		  AND (v.repository = $1 OR split_part(v.repository, '/', 2) = $1)
		  AND v.created_at >= $2::timestamptz
		ORDER BY v.created_at DESC
	`, repo, since)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	type candidate struct {
		item             auditSampleItem
		verdict          string
		recentlyReviewed bool
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		var clusterSig *string
		var evidence []byte
		if err := rows.Scan(&c.item.VerdictID, &c.item.Repository, &c.item.CommitSHA,
			&c.item.Branch, &c.item.GHRunID, &c.item.GHPRNumber, &c.item.TestID,
			&c.verdict, &clusterSig, &c.item.SuspectCommit, &c.item.CreatedAt,
			&c.recentlyReviewed, &evidence); err != nil {
			return nil, 0, err
		}
		c.item.Stratum = stratumFor(c.verdict)
		c.item.Evidence = decodeEvidence(evidence)
		_ = clusterSig
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	pool := len(candidates)
	picked := map[string]bool{}
	var items []auditSampleItem

	pick := func(c candidate) {
		if picked[c.item.VerdictID] {
			return
		}
		picked[c.item.VerdictID] = true
		items = append(items, c.item)
	}

	// Pass 1 — fresh (not reviewed in 30d) per stratum, quota order.
	for _, s := range auditStrata {
		taken := 0
		for _, c := range candidates {
			if taken >= s.quota {
				break
			}
			if c.item.Stratum == s.label && !c.recentlyReviewed {
				pick(c)
				taken++
			}
		}
		// Pass 2 — refill over the no-repeat preference: a small pool must not
		// shrink the sample.
		for _, c := range candidates {
			if taken >= s.quota {
				break
			}
			if c.item.Stratum == s.label {
				pick(c)
				taken++
			}
		}
	}
	// Pass 3 — short strata refill from the others, still fresh-first.
	if len(items) < auditTargetSize {
		for _, c := range candidates {
			if len(items) >= auditTargetSize {
				break
			}
			if !c.recentlyReviewed {
				pick(c)
			}
		}
		for _, c := range candidates {
			if len(items) >= auditTargetSize {
				break
			}
			pick(c)
		}
	}
	return items, pool, nil
}

func stratumFor(verdict string) string {
	for _, s := range auditStrata {
		if verdict == s.match {
			return s.label
		}
	}
	return "other"
}

// ---------- small local helpers ----------

func errRepoRequired() error {
	return fmt.Errorf("%w: repo is required", api.ErrBadRequest)
}

func errRepoRequiredWith(msg string) error {
	return fmt.Errorf("%w: %s", api.ErrBadRequest, msg)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

// decodeEvidence renders the stored evidence JSON array as maps for the
// payload; malformed rows degrade to an empty list, never fail the sample.
func decodeEvidence(raw []byte) []map[string]any {
	if len(raw) == 0 {
		return []map[string]any{}
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return []map[string]any{}
	}
	if out == nil {
		return []map[string]any{}
	}
	return out
}

// ---------- POST /api/v1/triage/audit/reviews ----------

type auditReviewInput struct {
	VerdictID  string `json:"verdict_id"`
	HumanAgree *bool  `json:"human_agree"`
	Note       string `json:"note"`
}

// SubmitAuditReview serves POST /api/v1/triage/audit/reviews — record the
// blind call. Upsert per (verdict, reviewer): a second submission by the same
// reviewer corrects their call rather than double-counting.
func (h *Handlers) SubmitAuditReview(w http.ResponseWriter, r *http.Request) {
	var in auditReviewInput
	if err := decodeJSONBody(w, r, &in); err != nil {
		api.WriteError(w, r, err)
		return
	}
	if in.VerdictID == "" || in.HumanAgree == nil {
		api.WriteError(w, r, errRepoRequiredWith("verdict_id and human_agree are required"))
		return
	}
	verdictID, err := uuid.Parse(in.VerdictID)
	if err != nil {
		api.WriteError(w, r, errRepoRequiredWith("verdict_id must be a UUID"))
		return
	}
	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	reviewer := subjectLabel(subject)

	var verdict, repository string
	var createdAt time.Time
	err = h.Pool.QueryRow(r.Context(), `
		SELECT verdict, repository, created_at FROM triage_verdicts WHERE id = $1
	`, verdictID).Scan(&verdict, &repository, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			api.WriteError(w, r, api.ErrNotFound)
			return
		}
		h.logError("triage audit review verdict lookup", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	stratum := stratumFor(verdict)
	_ = createdAt

	if _, err := h.Pool.Exec(r.Context(), `
		INSERT INTO triage_audit_reviews (verdict_id, repository, reviewer, human_agree, note, stratum)
		VALUES ($1, $2, $3, $4, nullif($5, ''), $6)
		ON CONFLICT (verdict_id, reviewer)
		DO UPDATE SET human_agree = EXCLUDED.human_agree,
		              note = EXCLUDED.note,
		              reviewed_at = now()
	`, verdictID, repository, reviewer, *in.HumanAgree, in.Note, stratum); err != nil {
		h.logError("triage audit review upsert", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	// Only now, after submit, reveal the AI verdict for this item.
	writeJSON(w, http.StatusCreated, map[string]any{
		"verdict_id": in.VerdictID,
		"reviewer":   reviewer,
		"ai_verdict": h.auditVerdictDetail(r.Context(), verdictID),
	})
}

// ---------- GET /api/v1/triage/audit/items/{id} ----------

// AuditItemDetail serves GET /api/v1/triage/audit/items/{id}. If the caller
// has already reviewed the item, the payload includes the AI verdict — the
// reveal. If not, it omits it, exactly like the sample payload: blindness is
// a property of the API, not of the UI.
func (h *Handlers) AuditItemDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, r, errRepoRequiredWith("id must be a UUID"))
		return
	}
	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	reviewer := subjectLabel(subject)

	var reviewed bool
	if err := h.Pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM triage_audit_reviews WHERE verdict_id = $1 AND reviewer = $2)
	`, id, reviewer).Scan(&reviewed); err != nil {
		h.logError("triage audit reviewed check", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	resp := map[string]any{"verdict_id": id.String(), "reviewed_by_you": reviewed}
	if reviewed {
		resp["ai_verdict"] = h.auditVerdictDetail(r.Context(), id)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) auditVerdictDetail(ctx context.Context, id uuid.UUID) map[string]any {
	var verdict, checkState string
	var confidence float64
	var rootCause, model *string
	err := h.Pool.QueryRow(ctx, `
		SELECT verdict, confidence, root_cause, check_state, model
		FROM triage_verdicts WHERE id = $1
	`, id).Scan(&verdict, &confidence, &rootCause, &checkState, &model)
	if err != nil {
		return nil
	}
	detail := map[string]any{
		"verdict":     verdict,
		"confidence":  confidence,
		"check_state": checkState,
	}
	if rootCause != nil {
		detail["root_cause"] = *rootCause
	}
	if model != nil {
		detail["model"] = *model
	}
	return detail
}

// ---------- GET /api/v1/triage/audit/agreement ----------

type agreementResponse struct {
	PooledWeeks   int               `json:"pooled_weeks"`
	Reviews       int               `json:"reviews"`
	Agree         int               `json:"agree"`
	AgreementRate float64           `json:"audit_agreement_rate"`
	PerWeek       []weeklyAgreement `json:"per_week"`
}

type weeklyAgreement struct {
	WeekStart     string  `json:"week_start"`
	Reviews       int     `json:"reviews"`
	AgreementRate float64 `json:"agreement_rate"`
}

// AuditAgreement serves GET /api/v1/triage/audit/agreement?repo=&weeks=4 —
// the pooled trailing-window rate W13 gates on, plus per-week for visibility.
func (h *Handlers) AuditAgreement(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	weeks := parseInt(r.URL.Query().Get("weeks"), 4)
	if weeks < 1 || weeks > 12 {
		api.WriteError(w, r, errRepoRequiredWith("weeks must be between 1 and 12"))
		return
	}

	// Pooled over the trailing N weeks. A review agrees when human_agree —
	// the human endorsed the waive the AI made.
	var reviews, agree int
	err := h.Pool.QueryRow(r.Context(), `
		SELECT count(*)::int, count(*) FILTER (WHERE human_agree)::int
		FROM triage_audit_reviews ar
		JOIN triage_verdicts v ON v.id = ar.verdict_id
		WHERE (ar.repository = $1 OR split_part(ar.repository, '/', 2) = $1)
		  AND ar.reviewed_at >= now() - make_interval(days => $2 * 7)
	`, repo, weeks).Scan(&reviews, &agree)
	if err != nil {
		h.logError("triage audit agreement pooled", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	rows, err := h.Pool.Query(r.Context(), `
		SELECT date_trunc('week', ar.reviewed_at)::date::text,
		       count(*)::int,
		       count(*) FILTER (WHERE human_agree)::float / nullif(count(*), 0)::float
		FROM triage_audit_reviews ar
		JOIN triage_verdicts v ON v.id = ar.verdict_id
		WHERE (ar.repository = $1 OR split_part(ar.repository, '/', 2) = $1)
		  AND ar.reviewed_at >= now() - make_interval(days => $2 * 7)
		GROUP BY 1 ORDER BY 1 DESC
	`, repo, weeks)
	if err != nil {
		h.logError("triage audit agreement weekly", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	perWeek := []weeklyAgreement{}
	for rows.Next() {
		var wk weeklyAgreement
		var rate *float64
		if err := rows.Scan(&wk.WeekStart, &wk.Reviews, &rate); err != nil {
			h.logError("triage audit agreement weekly scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		if rate != nil {
			wk.AgreementRate = *rate
		}
		perWeek = append(perWeek, wk)
	}

	rate := 0.0
	if reviews > 0 {
		rate = float64(agree) / float64(reviews)
	}
	writeJSON(w, http.StatusOK, agreementResponse{
		PooledWeeks:   weeks,
		Reviews:       reviews,
		Agree:         agree,
		AgreementRate: rate,
		PerWeek:       perWeek,
	})
}
