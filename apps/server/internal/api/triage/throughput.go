// R7-L1 — is the stabilization loop keeping up?
//
// THE PROBLEM THIS MAKES VISIBLE. The strategy doc carries a throughput formula
// and the honest admission that "review capacity, not agent capacity, is the
// binding constraint" — but nothing ever computed either side of it. So the
// question that decides whether the whole master-side investment pays off ("do
// we fix flaky tests faster than they appear?") was answerable only by hand,
// from a spreadsheet, using numbers nobody was measuring.
//
// The arithmetic matters because it inverts the intuition. Fixing master so PR
// authors suffer less is the right instinct, and every fix removes a recurring
// source of noise permanently where a waiver only mitigates one occurrence. But
// on the measured numbers the loop drains a small fraction of what arrives:
//
//	window_days = max(7, 20 / master_runs_per_day)   -- the 7-day floor governs
//	cycle_days  = review_latency + window_days
//	drain_rate  = concurrency / (cycle_days * attempts_per_fix)
//	required concurrency >= arrival_rate * cycle_days * attempts_per_fix
//
// With arrival at 1.5/day, a 7-day window and 1.5 attempts per fix, breaking
// even needs concurrency in the 20-32 range against a hard cap of 5. So the
// backlog grows, and PR-side waivers stay load-bearing rather than being a
// temporary bridge to a clean master.
//
// THE WINDOW WAS NEVER IRREDUCIBLE. An earlier version of this comment claimed
// window_days could not be lowered because it is "the proof a fix worked". That
// was wrong, and the error was in the sampling strategy, not the arithmetic: the
// 7-day floor exists only because the naive way to collect 20 samples of a test
// is to wait for 20 natural master runs. Re-run the ONE test 20 times with
// `--grep` on its MM-T id and 20 samples take a single job. Pass targeted=true
// to model that (see targetedWindowDays).
//
// The two levers that matter, on the measured numbers (arrival 1.47/day):
//
//	baseline (wait for master, weekly review, concurrency 2)   drain 0.10/day
//	+ targeted re-measurement                                  drain 0.18/day
//	+ targeted AND a 48h review SLA at concurrency 5           drain 1.48/day  <- keeps up
//
// BOTH are required. Targeted re-measurement alone at a weekly review cadence
// reaches 0.46/day, still short of 1.47. attempts_per_fix is a property of the
// tests, and concurrency is capped by review capacity, so those two are not
// levers. This endpoint exists so that trade is visible to whoever owns the
// rotation.

package triage

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// Defaults for the two inputs this server cannot observe. Both are overridable
// per-call so a reviewer can ask "what if we reviewed in two days?" without a
// deploy, and both are echoed in the response so no number is unexplained.
const (
	// defaultAttemptsPerFix is the spec's assumption until Phase 0 measures it:
	// most tests fixed on the first attempt, some needing 2-3, capped at 3.
	defaultAttemptsPerFix = 1.5
	// defaultReviewLatencyDays reflects a weekly named rotation — a PR opened
	// just after this week's slot waits for the next one.
	defaultReviewLatencyDays = 7.0
	// defaultConcurrency is the pilot value from the spec (W14).
	defaultConcurrency = 2
	// maxConcurrency is the spec's hard cap: above this, review capacity is the
	// binding constraint and raising the number buys nothing.
	maxConcurrency = 5
	// windowFloorDays is the flakiness window's floor: "20 runs or 7 days,
	// whichever covers more runs", so on a busy master the floor governs.
	windowFloorDays = 7.0

	// targetedWindowDays is the re-measurement window when the fix is verified
	// by re-running JUST the one test, N times, in a single dedicated CI job —
	// Playwright `--grep` on the test's MM-T id, Cypress `--spec`.
	//
	// This is the assumption-breaker. The 7-day floor exists only because the
	// naive way to collect 20 samples of a test is to wait for 20 natural
	// master runs. If instead you run the one test 20 times on demand, 20
	// samples take one job. 6 hours is deliberately conservative: it covers
	// queueing, a full environment boot per repetition, and 20 repetitions of
	// a slow E2E spec, and it is still ~28x shorter than the 7-day floor.
	//
	// It is a strictly BETTER measurement, not a shortcut: 20 consecutive
	// executions at one commit isolate the test's own flakiness, whereas 20
	// master runs spread over 7 days confound it with everything else that
	// landed in that week.
	targetedWindowDays = 0.25
)

// The levers throughputAdvice can name, in the order it prefers them.
const (
	leverConcurrency = "concurrency"
	leverWindow      = "remeasurement_window"
	leverReview      = "review_latency"
)

type throughputResponse struct {
	Repo   string `json:"repo"`
	Window string `json:"window"`

	// Measured from this server's own data.
	MasterRunsPerDay  float64 `json:"master_runs_per_day"`
	NewFlakyInWindow  int     `json:"new_flaky_in_window"`
	ArrivalRate       float64 `json:"arrival_rate_per_day"`
	ResolvedInWindow  int     `json:"resolved_in_window"`
	ObservedDrainRate float64 `json:"observed_drain_rate_per_day"`

	// Inputs, echoed so every derived number is reproducible by hand.
	ReviewLatencyDays float64 `json:"review_latency_days"`
	AttemptsPerFix    float64 `json:"attempts_per_fix"`
	Concurrency       int     `json:"concurrency"`
	MaxConcurrency    int     `json:"max_concurrency"`
	// Targeted re-measurement: verify the fix by re-running just this test N
	// times in one job (`--grep MM-Txxxx`) instead of waiting for N natural
	// master runs. Collapses window_days from 7 to hours.
	Targeted bool `json:"targeted_remeasurement"`

	// Derived.
	WindowDays           float64 `json:"window_days"`
	CycleDays            float64 `json:"cycle_days"`
	ModeledDrainRate     float64 `json:"modeled_drain_rate_per_day"`
	RequiredConcurrency  float64 `json:"required_concurrency"`
	CoveragePct          float64 `json:"coverage_pct"`
	DeficitPerDay        float64 `json:"deficit_per_day"`
	BacklogGrowthPerWeek float64 `json:"backlog_growth_per_week"`

	KeepingUp      bool     `json:"keeping_up"`
	BindingLever   string   `json:"binding_lever"`
	Recommendation string   `json:"recommendation"`
	Notes          []string `json:"notes"`
}

// StabilizationThroughput serves
// GET /api/v1/triage/stabilization/throughput?repo=[&window=][&review_latency_days=][&attempts_per_fix=][&concurrency=]
//
// Public read, same reasoning as the queue: the loop and any dashboard should
// be able to ask "am I keeping up?" without a credential round-trip.
func (h *Handlers) StabilizationThroughput(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	window := orDefault(r.URL.Query().Get("window"), "30d")
	since, err := parseSince(window)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if since == nil {
		api.WriteError(w, r, errRepoRequiredWith("window must be bounded, e.g. 30d"))
		return
	}
	windowDays := time.Since(*since).Hours() / 24
	if windowDays < 1 {
		windowDays = 1
	}

	// NOTE: do NOT use the package's parseFloat here. Its contract is "a
	// fraction in [0,1]" (it serves max_failure_rate and the alert floor) and
	// it silently returns the default for anything outside that range. Feeding
	// review_latency_days=2 through it yields 7. These are what-if knobs, and a
	// knob that silently does nothing is worse than no knob at all — so each
	// gets its own range and an out-of-range value is a 400, not a default.
	reviewLatency, err := parseRange(r.URL.Query().Get("review_latency_days"), defaultReviewLatencyDays, 0, 60, "review_latency_days")
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	attempts, err := parseRange(r.URL.Query().Get("attempts_per_fix"), defaultAttemptsPerFix, 1, 10, "attempts_per_fix")
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	concurrency, err := parseRange(r.URL.Query().Get("concurrency"), defaultConcurrency, 1, 100, "concurrency")
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	targeted := r.URL.Query().Get("targeted") == "true"

	resp := throughputResponse{
		Repo:              normalizeRepo(repo),
		Window:            window,
		ReviewLatencyDays: reviewLatency,
		AttemptsPerFix:    attempts,
		Concurrency:       int(concurrency),
		MaxConcurrency:    maxConcurrency,
		Targeted:          targeted,
	}

	if err := h.loadThroughputCounts(r, &resp, since, windowDays); err != nil {
		h.logError("stabilization throughput", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	computeThroughput(&resp)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) loadThroughputCounts(r *http.Request, resp *throughputResponse, since *time.Time, windowDays float64) error {
	ctx := r.Context()
	repo := resp.Repo

	// Master run volume — sets window_days via the 20-runs-or-7-days rule.
	var masterGroups int
	if err := h.Pool.QueryRow(ctx, `
		SELECT count(*)::int FROM report_groups
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND branch IN ('main', 'master')
		  AND created_at >= $2::timestamptz
	`, repo, since).Scan(&masterGroups); err != nil {
		return err
	}
	resp.MasterRunsPerDay = float64(masterGroups) / windowDays

	// Arrival: tests whose FIRST EVER failure on master in this repo falls
	// inside the window. "First ever" is what makes it an arrival rather than
	// a recurrence — a test that has been flaking for months is backlog, not
	// new work appearing.
	if err := h.Pool.QueryRow(ctx, `
		WITH first_fail AS (
			SELECT tc.external_test_id, min(g.created_at) AS first_failed_at
			FROM report_groups g
			JOIN reports rp ON rp.report_group_id = g.id
			JOIN suites s ON s.report_id = rp.id
			JOIN test_cases tc ON tc.suite_id = s.id
			WHERE tc.external_test_id IS NOT NULL
			  AND tc.status IN ('failed', 'timedOut', 'interrupted', 'flaky')
			  AND (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
			  AND g.branch IN ('main', 'master')
			GROUP BY tc.external_test_id
		)
		SELECT count(*)::int FROM first_fail WHERE first_failed_at >= $2::timestamptz
	`, repo, since).Scan(&resp.NewFlakyInWindow); err != nil {
		return err
	}
	resp.ArrivalRate = float64(resp.NewFlakyInWindow) / windowDays

	// Observed drain: stabilization promotions actually resolved in the window.
	// This is the ground truth the model is checked against — if the two
	// disagree, trust this one and fix the inputs.
	if err := h.Pool.QueryRow(ctx, `
		SELECT count(*)::int FROM stabilization_promotions
		WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
		  AND resolved AND updated_at >= $2::timestamptz
	`, repo, since).Scan(&resp.ResolvedInWindow); err != nil {
		return err
	}
	resp.ObservedDrainRate = float64(resp.ResolvedInWindow) / windowDays
	return nil
}

// computeThroughput applies the strategy doc's formula. Split out from the
// handler so the arithmetic is unit-testable without a database.
func computeThroughput(resp *throughputResponse) {
	// Targeted re-measurement decouples the window from master's cadence
	// entirely: the samples come from re-running the one test, so no amount of
	// waiting for master is involved.
	if resp.Targeted {
		resp.WindowDays = targetedWindowDays
	} else {
		// window_days = max(7, 20 / master_runs_per_day). The max is
		// load-bearing: taking the division alone would understate cycle time
		// on a busy master and therefore understate the concurrency needed.
		resp.WindowDays = windowFloorDays
		if resp.MasterRunsPerDay > 0 {
			if byRuns := 20 / resp.MasterRunsPerDay; byRuns > resp.WindowDays {
				resp.WindowDays = byRuns
			}
		}
	}
	resp.CycleDays = resp.ReviewLatencyDays + resp.WindowDays

	denom := resp.CycleDays * resp.AttemptsPerFix
	if denom > 0 {
		resp.ModeledDrainRate = float64(resp.Concurrency) / denom
	}
	resp.RequiredConcurrency = resp.ArrivalRate * denom

	switch {
	case resp.ArrivalRate <= 0:
		// Nothing arriving: trivially keeping up, and say so rather than
		// reporting a misleading 100%.
		resp.CoveragePct = 100
		resp.KeepingUp = true
		resp.BindingLever = slaStateNone
		resp.Recommendation = "no new flaky tests appeared in this window — nothing to drain"
		resp.Notes = append(resp.Notes, "arrival_rate is 0, so coverage is reported as 100% by definition")
	default:
		resp.CoveragePct = 100 * resp.ModeledDrainRate / resp.ArrivalRate
		resp.DeficitPerDay = resp.ArrivalRate - resp.ModeledDrainRate
		resp.KeepingUp = resp.ModeledDrainRate >= resp.ArrivalRate
		if resp.DeficitPerDay < 0 {
			resp.DeficitPerDay = 0
		}
		resp.BacklogGrowthPerWeek = resp.DeficitPerDay * 7
		resp.BindingLever, resp.Recommendation = throughputAdvice(resp)
	}

	resp.CoveragePct = round2(resp.CoveragePct)
	resp.ArrivalRate = round2(resp.ArrivalRate)
	resp.ObservedDrainRate = round2(resp.ObservedDrainRate)
	resp.ModeledDrainRate = round2(resp.ModeledDrainRate)
	resp.RequiredConcurrency = round2(resp.RequiredConcurrency)
	resp.DeficitPerDay = round2(resp.DeficitPerDay)
	resp.BacklogGrowthPerWeek = round2(resp.BacklogGrowthPerWeek)
	resp.MasterRunsPerDay = round2(resp.MasterRunsPerDay)
	resp.CycleDays = round2(resp.CycleDays)
	resp.WindowDays = round2(resp.WindowDays)

	if resp.ObservedDrainRate == 0 && resp.ModeledDrainRate > 0 {
		resp.Notes = append(resp.Notes,
			"observed_drain_rate is 0 — no stabilization promotions were resolved in this window, "+
				"so modeled_drain_rate is an upper bound, not a measurement")
	}
}

// throughputAdvice names the ONE change worth making, preferring the CHEAPEST
// one that actually closes the gap. Ordering matters: telling a calm repo to
// redesign its re-measurement strategy when bumping concurrency by one would
// do is bad advice, and so is suggesting a concurrency number that cannot help.
//
//  1. already keeping up            -> nothing
//  2. concurrency within the cap closes it -> raise concurrency (cheapest)
//  3. targeted re-measurement helps -> stop waiting for master (biggest lever)
//  4. otherwise                     -> review latency, plus quarantine for the rest
func throughputAdvice(resp *throughputResponse) (lever, rec string) {
	if resp.KeepingUp {
		return slaStateNone, "drain meets arrival at the current settings — hold"
	}
	if resp.RequiredConcurrency <= float64(resp.MaxConcurrency) {
		return leverConcurrency, "raise concurrency to " +
			itoa(int(math.Ceil(resp.RequiredConcurrency))) +
			" (within the cap of " + itoa(resp.MaxConcurrency) + ") to meet arrival"
	}
	// Concurrency cannot close it. The biggest remaining lever is to stop
	// waiting for master to re-measure — quantified, not described.
	if !resp.Targeted {
		targeted := *resp
		targeted.Targeted = true
		targeted.Notes = nil
		computeThroughput(&targeted)
		if targeted.ModeledDrainRate > resp.ModeledDrainRate {
			verdict := "."
			if targeted.KeepingUp {
				verdict = " — which KEEPS UP."
			} else if targeted.RequiredConcurrency <= float64(targeted.MaxConcurrency) {
				verdict = " — which KEEPS UP at concurrency " +
					itoa(int(math.Ceil(targeted.RequiredConcurrency))) + "."
			}
			return leverWindow, "verify fixes by re-running just the test " +
				"(--grep on its MM-T id) instead of waiting for master: window " +
				trimF(resp.WindowDays) + "d -> " + trimF(targeted.WindowDays) + "d takes drain " +
				trimF(resp.ModeledDrainRate) + " -> " + trimF(targeted.ModeledDrainRate) +
				"/day against arrival " + trimF(resp.ArrivalRate) + "/day" + verdict
		}
	}
	if resp.RequiredConcurrency > float64(resp.MaxConcurrency) {
		// Show what the only real lever buys, concretely.
		faster := resp.ReviewLatencyDays
		if faster > 2 {
			faster = 2
		}
		cycleAt2 := faster + resp.WindowDays
		drainAtCap := float64(resp.MaxConcurrency) / (cycleAt2 * resp.AttemptsPerFix)
		return leverReview, "required concurrency " +
			trimF(resp.RequiredConcurrency) + " exceeds the cap of " + itoa(resp.MaxConcurrency) +
			", so raising concurrency cannot close this. Review latency is the only real lever: at " +
			trimF(faster) + "d latency and concurrency " + itoa(resp.MaxConcurrency) +
			", drain would be " + trimF(round2(drainAtCap)) + "/day against arrival " +
			trimF(resp.ArrivalRate) + "/day. Quarantine the top of the queue by blast radius for the rest."
	}
	return leverConcurrency, "raise concurrency to " + itoa(int(math.Ceil(resp.RequiredConcurrency))) +
		" (within the cap of " + itoa(resp.MaxConcurrency) + ") to meet arrival"
}

// parseRange reads a bounded numeric query param. Empty means "use the
// default"; anything present but unparseable or out of range is an error, so a
// mistyped what-if never masquerades as a real answer.
func parseRange(v string, dflt, lo, hi float64, name string) (float64, error) {
	if v == "" {
		return dflt, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, errRepoRequiredWith(name + " must be a number")
	}
	if f < lo || f > hi {
		return 0, errRepoRequiredWith(fmt.Sprintf("%s must be between %s and %s",
			name, trimF(lo), trimF(hi)))
	}
	return f, nil
}

func round2(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return math.Round(f*100) / 100
}
