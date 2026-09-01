// W15c — SLA clocks derived from the ledger.
//
// The spec's SLA table is process; this makes the tracking mechanical so the
// weekly review has a one-click list. Clocks start at the verdict's
// created_at; a human correction stops the clock (the verdict was wrong, not
// slow); a stabilization promotion hands the test to the queue (queue items
// are tracked by the loop, not by the SLA clock).
//
// Policy numbers come from the spec's SLA table (proposed, team-owned):
//
//	MAIN_REGRESSION single-commit blame .... 2 business days (author)
//	MAIN_REGRESSION unattributed ............ 5 business days (infra queue)
//	FLAKY_INFRA ............................. 2 business days (infra)
//	FLAKY_TEST, amnesty expired ............. 5 business days (test owner)
//	TEST_DEBT ............................... groomed weekly, no clock
//
// M4's advisory period (first 4 weeks of author pings) runs clockless for
// single-commit blame — the drift report records the flag-off date.

package triage

import (
	"net/http"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// SLA clock states (shared with the tests — goconst wants one binding).
const (
	slaStateOpen   = "open"
	slaStateFlag1  = "flag1"
	slaStateFlag2  = "flag2"
	slaStateNone   = "none"
	slaStateClosed = "closed"

	slaVerdictMainRegression = "MAIN_REGRESSION"
	slaVerdictFlakyInfra     = "FLAKY_INFRA"
	slaVerdictFlakyTest      = "FLAKY_TEST"

	slaMainRegressionAttributed = 2 * 24 * time.Hour
	slaMainRegressionQueue      = 5 * 24 * time.Hour
	slaFlakyInfra               = 2 * 24 * time.Hour
	slaFlakyTestExpired         = 5 * 24 * time.Hour
)

// SLAClock is the pure core: given a verdict's class, attribution, age, and
// whether a human has since acted, produce the SLA state.
//
//	state open .... clock running, within SLA
//	state flag1 ... past 1x — weekly review list
//	state flag2 ... past 2x — owning team's lead notified
//	state closed . correction recorded or handed to the queue — no clock
//	state none .... no clock applies (test debt, before-merge, advisory blame)
//
// advisoryBlame: during M4's advisory period (a dated config flag, not a
// comment) single-commit blame pings are informational — attribution is
// being measured, not enforced — so attributed MAIN_REGRESSION runs clockless.
func SLAClock(verdict string, attributed bool, age time.Duration, corrected, promoted bool, advisoryBlame bool) (state string, limit time.Duration) {
	switch {
	case corrected || promoted:
		return slaStateClosed, 0
	case advisoryBlame && verdict == slaVerdictMainRegression && attributed:
		return slaStateNone, 0
	case verdict == slaVerdictMainRegression:
		if attributed {
			limit = slaMainRegressionAttributed
		} else {
			limit = slaMainRegressionQueue
		}
	case verdict == slaVerdictFlakyInfra:
		limit = slaFlakyInfra
	case verdict == slaVerdictFlakyTest:
		// Only amnesty-expired flakes carry an SLA (the waived ones are
		// recorded, not chased); the caller passes age only for expired ones.
		limit = slaFlakyTestExpired
	default:
		// PR_REGRESSION is before-merge; TEST_DEBT is groomed weekly;
		// INCONCLUSIVE/BUILD_OR_ENV_ERROR route elsewhere. No clock.
		return slaStateNone, 0
	}
	switch {
	case age >= 2*limit:
		return slaStateFlag2, limit
	case age >= limit:
		return slaStateFlag1, limit
	default:
		return slaStateOpen, limit
	}
}

type slaRow struct {
	VerdictID      string    `json:"verdict_id"`
	ExternalTestID *string   `json:"external_test_id,omitempty"`
	Verdict        string    `json:"verdict"`
	Branch         string    `json:"branch"`
	CommitSHA      string    `json:"commit_sha"`
	SuspectCommit  *string   `json:"suspect_commit,omitempty"`
	Attributed     bool      `json:"attributed"`
	CreatedAt      time.Time `json:"created_at"`
	AgeDays        int       `json:"age_days"`
	State          string    `json:"state"`
	LimitDays      int       `json:"limit_days"`
}

// SLAReport serves GET /api/v1/triage/sla?repo=&advisory_until=ISO-date —
// the past-SLA list the weekly meeting opens. Ordered worst-first (flag2
// before flag1 before open). advisory_until implements the M4 advisory
// period: verdicts created before that date carry no blame clock.
func (h *Handlers) SLAReport(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	var advisoryUntil time.Time
	if v := r.URL.Query().Get("advisory_until"); v != "" {
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			api.WriteError(w, r, errRepoRequiredWith("advisory_until must be an ISO date (YYYY-MM-DD)"))
			return
		}
		advisoryUntil = parsed
	}

	rows, err := h.Pool.Query(r.Context(), `
		SELECT v.id::text, v.external_test_id, v.verdict, v.branch, v.commit_sha,
		       v.suspect_commit, v.created_at,
		       (v.suspect_commit IS NOT NULL)                    AS attributed,
		       (v.corrected_verdict IS NOT NULL)                 AS corrected,
		       EXISTS (
		           SELECT 1 FROM stabilization_promotions p
		           WHERE (p.repository = v.repository
		              OR (split_part(p.repository, '/', 2) = split_part(v.repository, '/', 2)
		                  AND split_part(p.repository, '/', 2) <> ''))
		             AND p.external_test_id IS NOT DISTINCT FROM v.external_test_id
		             AND NOT p.resolved
		       ) AS promoted
		FROM triage_verdicts v
		WHERE (v.repository = $1 OR split_part(v.repository, '/', 2) = $1)
		  AND v.verdict IN ($2, $3)
		  AND v.created_at >= now() - interval '30 days'
		ORDER BY v.created_at ASC
	`, repo, slaVerdictMainRegression, slaVerdictFlakyInfra)
	if err != nil {
		h.logError("sla report query", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	defer rows.Close()

	now := time.Now()
	out := []slaRow{}
	for rows.Next() {
		var row slaRow
		var corrected, promoted bool
		if err := rows.Scan(&row.VerdictID, &row.ExternalTestID, &row.Verdict, &row.Branch,
			&row.CommitSHA, &row.SuspectCommit, &row.CreatedAt,
			&row.Attributed, &corrected, &promoted); err != nil {
			h.logError("sla report scan", err)
			api.WriteError(w, r, api.ErrInternal)
			return
		}
		age := now.Sub(row.CreatedAt)
		advisory := !advisoryUntil.IsZero() && row.CreatedAt.Before(advisoryUntil)
		state, limit := SLAClock(row.Verdict, row.Attributed, age, corrected, promoted, advisory)
		if state == "none" || state == "closed" {
			continue
		}
		row.AgeDays = int(age.Hours() / 24)
		row.State = state
		row.LimitDays = int(limit.Hours() / 24)
		out = append(out, row)
	}

	// Worst first: flag2, then flag1, then open; age descending within each.
	sortSLARows(out)
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "entries": out})
}

func sortSLARows(rows []slaRow) {
	rank := map[string]int{"flag2": 0, "flag1": 1, "open": 2}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			if rank[a.State] > rank[b.State] || (rank[a.State] == rank[b.State] && a.AgeDays < b.AgeDays) {
				rows[j-1], rows[j] = b, a
			} else {
				break
			}
		}
	}
}
