// W13 — the rollout phase gate.
//
// One phase value (0–3) every gating decision reads from. The bar and the
// demotion triggers are the plan's PROPOSED-BUILD-DEFAULT policy numbers,
// listed in the drift report for the team's Decisions table:
//
//	promote bar: pooled 4-week audit agreement ≥ 0.95,
//	             organic false-greens ≤ 2 per rolling 30 days,
//	             zero false-greens reaching a release branch,
//	             plus two consecutive clean weeks before promotion is offered.
//	demote triggers (any one, immediately, by exactly one phase):
//	             pooled agreement < 0.90,
//	             false-greens > 2 in 30 days,
//	             one confirmed release-branch false-green.
//
// Demotion applies to runs starting after it — no retroactive check flips.
// Promotion is always a human action; this code only computes eligibility.
package triage

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

const (
	PhaseShadow      = 0
	PhasePRGate      = 1
	PhaseMasterGate  = 2
	PhaseSelfHealing = 3

	PromoteAgreement  = 0.95
	DemoteAgreement   = 0.90
	MaxFalseGreens30d = 2
	PromoteCleanWeeks = 2
)

// PhaseInputs are the measured numbers the gate reads. Release-branch
// false-greens are a stub until W5's release-run linkage exists (drift
// report); zero means "no release false-green detected", never "unchecked".
type PhaseInputs struct {
	Reviews            int       `json:"reviews"`
	AgreementPooled    float64   `json:"agreement_pooled"`
	FalseGreens30d     int       `json:"false_greens_30d"`
	ReleaseFalseGreens int       `json:"release_false_greens"`
	WeeklyAgreement    []float64 `json:"-"` // most-recent-first weekly rates
}

// PhaseDecision is what the gate says. Demote is automatic; PromoteEligible
// only unlocks the human action.
type PhaseDecision struct {
	Demote          bool   `json:"demote"`
	DemoteReason    string `json:"demote_reason,omitempty"`
	PromoteEligible bool   `json:"promote_eligible"`
	BarMet          bool   `json:"bar_met"`
	CleanWeeks      int    `json:"clean_weeks"`
	Report          string `json:"report"`
}

// EvaluatePhaseGate is the pure core — unit-tested, no I/O.
func EvaluatePhaseGate(in PhaseInputs) PhaseDecision {
	d := PhaseDecision{}

	// Demotion triggers, fail-closed ordering: the worst reason wins.
	switch {
	case in.ReleaseFalseGreens > 0:
		d.Demote = true
		d.DemoteReason = "release-branch false-green detected"
	case in.Reviews > 0 && in.AgreementPooled < DemoteAgreement:
		// Zero reviews is no signal, not bad signal — it must neither demote
		// nor promote. Only a measured agreement below the floor demotes.
		d.Demote = true
		d.DemoteReason = "pooled audit agreement below demote floor"
	case in.FalseGreens30d > MaxFalseGreens30d:
		d.Demote = true
		d.DemoteReason = "false-greens over 30d limit"
	}

	// Clean-week streak: trailing weeks (most-recent-first) at or above the
	// promote bar. A week with no reviews does not count either way.
	for _, w := range in.WeeklyAgreement {
		if w < PromoteAgreement {
			break
		}
		d.CleanWeeks++
		if d.CleanWeeks >= PromoteCleanWeeks {
			break
		}
	}

	d.BarMet = !d.Demote &&
		in.Reviews > 0 &&
		in.AgreementPooled >= PromoteAgreement &&
		in.FalseGreens30d <= MaxFalseGreens30d &&
		in.ReleaseFalseGreens == 0
	d.PromoteEligible = d.BarMet && d.CleanWeeks >= PromoteCleanWeeks

	d.Report = "demote=" + boolStr(d.Demote) +
		" promote_eligible=" + boolStr(d.PromoteEligible) +
		" agreement=" + trimF(in.AgreementPooled) +
		" false_greens=" + itoa(in.FalseGreens30d) +
		" clean_weeks=" + itoa(d.CleanWeeks)
	return d
}

// ---------- GET /api/v1/triage/phase ----------

type phaseResponse struct {
	Phase     int       `json:"phase"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

// Phase serves GET /api/v1/triage/phase — the single source every gating
// decision reads. Public: CI jobs and the web both need it without auth.
func (h *Handlers) Phase(w http.ResponseWriter, r *http.Request) {
	var resp phaseResponse
	if err := h.Pool.QueryRow(r.Context(), `
		SELECT phase, updated_at, updated_by FROM triage_phase WHERE id = 1
	`).Scan(&resp.Phase, &resp.UpdatedAt, &resp.UpdatedBy); err != nil {
		h.logError("triage phase read", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------- GET /api/v1/triage/phase/evaluation ----------

// PhaseEvaluation serves GET /api/v1/triage/phase/evaluation?repo= — the
// live inputs and the gate's decision, WITHOUT applying anything. What a
// weekly reviewer looks at; what the scheduled job would apply.
func (h *Handlers) PhaseEvaluation(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	in, err := h.loadPhaseInputs(r.Context(), repo)
	if err != nil {
		h.logError("triage phase inputs", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	decision := EvaluatePhaseGate(in)
	writeJSON(w, http.StatusOK, map[string]any{
		"repo":     repo,
		"inputs":   in,
		"decision": decision,
		"policy": map[string]any{
			"promote_agreement":   PromoteAgreement,
			"demote_agreement":    DemoteAgreement,
			"max_false_greens":    MaxFalseGreens30d,
			"promote_clean_weeks": PromoteCleanWeeks,
		},
	})
}

// ---------- POST /api/v1/triage/phase ----------

type setPhaseInput struct {
	Phase  int    `json:"phase"`
	Reason string `json:"reason"`
}

// SetPhase serves POST /api/v1/triage/phase — the human action. Promotion is
// only legal when the gate says eligible; demotion to a lower phase is always
// legal (a human can always pull authority faster than the metrics).
func (h *Handlers) SetPhase(w http.ResponseWriter, r *http.Request) {
	var in setPhaseInput
	if err := decodeJSONBody(w, r, &in); err != nil {
		api.WriteError(w, r, fmt.Errorf("%w: malformed JSON body", api.ErrBadRequest))
		return
	}
	if in.Phase < PhaseShadow || in.Phase > PhaseSelfHealing {
		api.WriteError(w, r, fmt.Errorf("%w: phase must be between 0 and 3", api.ErrBadRequest))
		return
	}
	subject, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	who := subjectLabel(subject)

	var current int
	if err := h.Pool.QueryRow(r.Context(), `
		SELECT phase FROM triage_phase WHERE id = 1
	`).Scan(&current); err != nil {
		h.logError("triage phase read for set", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	if in.Phase > current {
		// Promotion: only when the gate offers it.
		repo := r.URL.Query().Get("repo")
		if repo == "" {
			api.WriteError(w, r, fmt.Errorf("%w: repo query param is required to verify promotion eligibility", api.ErrBadRequest))
			return
		}
		inputs, err := h.loadPhaseInputs(r.Context(), repo)
		if err != nil {
			h.logError("triage phase inputs for set", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		decision := EvaluatePhaseGate(inputs)
		if !decision.PromoteEligible {
			api.WriteError(w, r, fmt.Errorf("%w: promotion not offered: %s", api.ErrBadRequest, decision.Report))
			return
		}
	}

	if _, err := h.Pool.Exec(r.Context(), `
		UPDATE triage_phase SET phase = $1, updated_at = now(), updated_by = $2 WHERE id = 1
	`, in.Phase, who); err != nil {
		h.logError("triage phase set", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	writeJSON(w, http.StatusOK, phaseResponse{Phase: in.Phase, UpdatedAt: time.Now(), UpdatedBy: who})
}

// ---------- POST /api/v1/triage/phase/evaluate ----------

// ApplyPhaseEvaluation serves POST /api/v1/triage/phase/evaluate?repo= — the
// scheduled job's call. Applies demotion only (by exactly one phase, floor at
// shadow), never promotion. Returns the decision it acted on.
func (h *Handlers) ApplyPhaseEvaluation(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	in, err := h.loadPhaseInputs(r.Context(), repo)
	if err != nil {
		h.logError("triage phase inputs for apply", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	decision := EvaluatePhaseGate(in)

	var current int
	if err := h.Pool.QueryRow(r.Context(), `
		SELECT phase FROM triage_phase WHERE id = 1
	`).Scan(&current); err != nil {
		h.logError("triage phase read for apply", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	if decision.Demote && current > PhaseShadow {
		next := current - 1
		if _, err := h.Pool.Exec(r.Context(), `
			UPDATE triage_phase SET phase = $1, updated_at = now(), updated_by = 'auto-gate' WHERE id = 1
		`, next); err != nil {
			h.logError("triage phase demote", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		// The channel post is a stub until W7's notifier lands (drift report);
		// the decision + new phase are in the response and the ledger-adjacent
		// logs now.
		if h.Logger != nil {
			h.Logger.Info("triage phase demoted",
				"from", current, "to", next, "reason", decision.DemoteReason, "repo", repo)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"action":   "demoted",
			"from":     current,
			"to":       next,
			"decision": decision,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": "none", "decision": decision})
}

// loadPhaseInputs gathers the measured numbers: pooled agreement (4 weeks),
// weekly rates most-recent-first, and 30d false-greens from the accuracy
// metric. Release-branch false-greens stay a stub until W5's linkage.
func (h *Handlers) loadPhaseInputs(ctx context.Context, repo string) (PhaseInputs, error) {
	var in PhaseInputs

	var agreementReviews int
	var pooled *float64
	if err := h.Pool.QueryRow(ctx, `
		SELECT count(*)::int, count(*) FILTER (WHERE human_agree)::float / nullif(count(*), 0)::float
		FROM triage_audit_reviews ar
		JOIN triage_verdicts v ON v.id = ar.verdict_id
		WHERE (ar.repository = $1 OR split_part(ar.repository, '/', 2) = $1)
		  AND ar.reviewed_at >= now() - interval '28 days'
	`, repo).Scan(&agreementReviews, &pooled); err != nil {
		return in, err
	}
	if pooled != nil {
		in.AgreementPooled = *pooled
	}
	in.Reviews = agreementReviews

	rows, err := h.Pool.Query(ctx, `
		SELECT count(*) FILTER (WHERE human_agree)::float / nullif(count(*), 0)::float
		FROM triage_audit_reviews ar
		JOIN triage_verdicts v ON v.id = ar.verdict_id
		WHERE (ar.repository = $1 OR split_part(ar.repository, '/', 2) = $1)
		  AND ar.reviewed_at >= now() - make_interval(days => $2 * 7)
		GROUP BY date_trunc('week', ar.reviewed_at)
		ORDER BY date_trunc('week', ar.reviewed_at) DESC
	`, repo, PromoteCleanWeeks)
	if err != nil {
		return in, err
	}
	defer rows.Close()
	for rows.Next() {
		var rate *float64
		if err := rows.Scan(&rate); err != nil {
			return in, err
		}
		if rate != nil {
			in.WeeklyAgreement = append(in.WeeklyAgreement, *rate)
		}
	}
	if err := rows.Err(); err != nil {
		return in, err
	}

	if err := h.Pool.QueryRow(ctx, `
		SELECT count(*)::int
		FROM triage_verdicts
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND waived AND corrected_verdict IS NOT NULL
		  AND created_at >= now() - interval '30 days'
	`, repo).Scan(&in.FalseGreens30d); err != nil {
		return in, err
	}
	return in, nil
}

// ---------- report formatting ----------

func boolStr(b bool) string {
	return strconv.FormatBool(b)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func trimF(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}
