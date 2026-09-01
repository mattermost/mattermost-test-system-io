// W1 — raw vs effective pass rates.
//
// One labeled source for both numbers, because alerting and check status need
// opposite views of the same window:
//
//   - raw_pass_rate: every rolled outcome that is not a clean pass counts as a
//     failure. A flaky outcome (passed AND failed inside one group) is not a
//     clean pass, so it counts — same rule amnesty applies. Alerting and
//     dashboards read this: a flake is a problem to surface, not to hide.
//   - effective_pass_rate: waived failures are subtracted. The check-status
//     row reads this: a waived flake is not the PR's fault, so it must not
//     keep the check red.
//
// Waived failures are the ledger's waived verdict rows in the same window,
// counted by member_count so a cluster-level waiver (external_test_id NULL)
// still subtracts every test it covered. Master windows (branch = the
// baseline) count only master-run verdicts (gh_pr_number IS NULL) — a PR-side
// waiver must never make master look greener than it is.
//
// No caller may fetch an unlabelled "pass rate".

package triage

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// ratesResponse is the labeled pair. Field names are the contract; nothing
// downstream may recompute either number from the other.
type ratesResponse struct {
	Repository        string  `json:"repository"`
	Branch            string  `json:"branch"`
	Window            string  `json:"window"`
	RolledOutcomes    int     `json:"rolled_outcomes"`
	RawFailures       int     `json:"raw_failures"`
	WaivedFailures    int     `json:"waived_failures"`
	EffectiveFailures int     `json:"effective_failures"`
	RawPassRate       float64 `json:"raw_pass_rate"`
	EffectivePassRate float64 `json:"effective_pass_rate"`
}

// RollupRates is the pure core of W1's gate: given a window's rolled outcomes,
// the raw failure count, and the waived count from the ledger, produce both
// labeled rates. Exposed for unit testing; the handler only loads inputs.
//
// raw counts waived and real failures alike; effective counts only the real
// ones. Waived can exceed rawFailures when a waiver aged out of the outcome
// window but not the verdict window — clamp, never negative.
func RollupRates(rolledOutcomes, rawFailures, waived int) (raw, effective float64) {
	eff := rawFailures - waived
	if eff < 0 {
		eff = 0
	}
	return passRate(rolledOutcomes, rawFailures), passRate(rolledOutcomes, eff)
}

func passRate(outcomes, failures int) float64 {
	if outcomes <= 0 {
		return 0
	}
	return (float64(outcomes-failures) / float64(outcomes)) * 100
}

// Rates serves GET /api/v1/triage/pass-rates?repo=&branch=&window=24h|7d|30d.
// Reads are public, matching the other triage reads.
func (h *Handlers) Rates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		api.WriteError(w, r, fmt.Errorf("%w: repo is required", api.ErrBadRequest))
		return
	}
	branch := orDefault(q.Get("branch"), "main")
	window := orDefault(q.Get("window"), "7d")
	since, err := parseSince(window)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	outcomes, rawFailures, err := h.loadRolledOutcomes(r.Context(), repo, branch, *since)
	if err != nil {
		h.logError("triage rates rolled outcomes", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	waived, err := h.loadWaivedFailures(r.Context(), repo, branch, *since)
	if err != nil {
		h.logError("triage rates waived failures", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	raw, effective := RollupRates(outcomes, rawFailures, waived)
	resp := ratesResponse{
		Repository:        repo,
		Branch:            branch,
		Window:            window,
		RolledOutcomes:    outcomes,
		RawFailures:       rawFailures,
		WaivedFailures:    waived,
		EffectiveFailures: rawFailures - waived,
		RawPassRate:       raw,
		EffectivePassRate: effective,
	}
	if resp.EffectiveFailures < 0 {
		resp.EffectiveFailures = 0
	}
	writeJSON(w, http.StatusOK, resp)
}

// loadRolledOutcomes counts per-test rolled outcomes on repo+branch since the
// window start. Rollup key: external_test_id where the convention is used,
// else the title — the same stability argument the history endpoint makes.
func (h *Handlers) loadRolledOutcomes(ctx context.Context, repo, branch string, since time.Time) (outcomes, rawFailures int, err error) {
	// A test that both passed and failed inside one group is flaky, and flaky
	// counts toward raw failures: it did not cleanly pass.
	err = h.Pool.QueryRow(ctx, `
		WITH matched AS (
			SELECT g.id,
			       coalesce(tc.external_test_id, 't:' || coalesce(nullif(tc.full_title, ''), tc.title)) AS test_key,
			       tc.status
			FROM report_groups g
			JOIN reports r ON r.report_group_id = g.id
			JOIN suites s ON s.report_id = r.id
			JOIN test_cases tc ON tc.suite_id = s.id
			WHERE (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
			  AND g.branch = $2
			  AND g.created_at >= $3::timestamptz
		),
		rolled AS (
			SELECT test_key,
			       bool_or(status IN ('passed', 'flaky'))                   AS ever_passed,
			       bool_or(status IN ('failed', 'timedOut', 'interrupted')) AS ever_failed
			FROM matched
			GROUP BY test_key
		)
		SELECT count(*)::int,
		       count(*) FILTER (WHERE ever_failed)::int
		FROM rolled
		WHERE ever_passed OR ever_failed
	`, repo, branch, since).Scan(&outcomes, &rawFailures)
	return outcomes, rawFailures, err
}

// loadWaivedFailures sums member_count of waived verdicts in the window.
// Master windows count master-run verdicts only; PR windows count all —
// a PR's own waivers are what its effective rate should reflect.
func (h *Handlers) loadWaivedFailures(ctx context.Context, repo, branch string, since time.Time) (waived int, err error) {
	prFilter := ""
	if branch == "main" || branch == "master" {
		prFilter = "AND gh_pr_number IS NULL"
	}
	err = h.Pool.QueryRow(ctx, `
		SELECT coalesce(sum(member_count), 0)::int
		FROM triage_verdicts
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND waived
		  AND created_at >= $2::timestamptz
	`+prFilter, repo, since).Scan(&waived)
	return waived, err
}
