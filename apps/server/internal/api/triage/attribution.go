package triage

// Attribution — "is this failure the pull request's fault?", answered from
// stored history alone.
//
// This is the short-circuit. The agent's expensive path is to build Mattermost
// from the PR head and run the failing spec three times; at an 88.4% master
// pass-rate most pull requests meet a failure, so paying that on every one is
// not affordable. Most failures do not need it: if the same test is already
// failing on master, or its failure count is what its own baseline predicts,
// the answer is settled and no server has to be started.
//
// Everything here is arithmetic over rows the server already holds. No model is
// consulted, and none can be — which is the point. The agent may overrule a
// NEEDS_REPRODUCTION into a verdict after it runs the test, but it cannot talk
// this endpoint into a greener answer than the history supports.
//
// The four outcomes, in the order they are decided:
//
//	PR_SUSPECT       the test is clean on master and failing here. Never green.
//	                 Decided first so nothing below can reach past it.
//	MASTER_BROKEN    already failing on master right now. The PR is a bystander.
//	KNOWN_FLAKE      this many failures is unremarkable for this test's own
//	                 baseline rate.
//	NEEDS_REPRODUCTION  history cannot settle it. Go run the test.
//
// Only MASTER_BROKEN and KNOWN_FLAKE carry can_green.

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

const (
	// AttrPRSuspect means the test does not fail on the baseline and failed
	// here. The pull request owns it until proven otherwise.
	AttrPRSuspect = "PR_SUSPECT"
	// AttrMasterBroken means the baseline is currently failing this test too.
	AttrMasterBroken = "MASTER_BROKEN"
	// AttrKnownFlake means the observed failures are explained by the test's
	// own baseline failure rate.
	AttrKnownFlake = "KNOWN_FLAKE"
	// AttrNeedsReproduction means history is insufficient or contradictory.
	AttrNeedsReproduction = "NEEDS_REPRODUCTION"
)

const (
	// minBaselineRuns is the evidence floor. Below it, "this test is flaky" is
	// a guess: a test with two baseline runs can produce any rate at all. The
	// agent reproduces instead, which is exactly the case reproduction is for.
	minBaselineRuns = 5

	// flakeRateFloor is the rate below which a test is not called flaky, however
	// the arithmetic lands. A test that fails 2% of the time is not a flake a
	// developer should be asked to ignore — it is a test that mostly works and
	// just broke.
	flakeRateFloor = 0.05

	// unremarkableP is the threshold under which "it flaked again" stops
	// explaining what happened. If the probability of seeing at least this many
	// failures in this many attempts is below it, the baseline does not account
	// for the run and the agent has to look.
	//
	// This is the MM-T5824 case: a 40% flake failing 3 of 3 has p = 0.064, so it
	// is refused, while the same test failing 1 of 3 has p = 0.784 and is not.
	// A single threshold covers both without anyone classifying them by hand.
	unremarkableP = 0.10
)

// AttributionResult is the whole answer. Every field the decision used is
// echoed, because an agent that disagrees needs to see the inputs, and because
// a green check has to be explicable months later.
type AttributionResult struct {
	Repository     string `json:"repository"`
	ExternalTestID string `json:"external_test_id"`
	Branch         string `json:"branch"`
	CommitSHA      string `json:"commit_sha,omitempty"`

	Outcome string `json:"outcome"`
	// CanGreen is the only field a CI integration needs. It is never true for
	// PR_SUSPECT or NEEDS_REPRODUCTION.
	CanGreen bool   `json:"can_green"`
	Reason   string `json:"reason"`
	// NeedsReproduction tells the agent to spend a server build. It is the
	// inverse of "history settled it", not of CanGreen.
	NeedsReproduction bool `json:"needs_reproduction"`

	Baseline AttributionBaseline `json:"baseline"`
	Observed AttributionObserved `json:"observed"`
}

// AttributionBaseline is what the default branch says about this test.
type AttributionBaseline struct {
	Branch       string     `json:"branch"`
	Window       string     `json:"window"`
	Runs         int        `json:"runs"`
	Failed       int        `json:"failed"`
	FailureRate  float64    `json:"failure_rate"`
	FailingNow   bool       `json:"failing_now"`
	LastPass     *string    `json:"last_pass_commit,omitempty"`
	FailingSince *string    `json:"failing_since_commit,omitempty"`
	LastRunAt    *time.Time `json:"last_run_at,omitempty"`
}

// AttributionObserved is what happened on the branch being asked about.
type AttributionObserved struct {
	Attempts int `json:"attempts"`
	Failed   int `json:"failed"`
	// PValue is P(at least Failed failures in Attempts) under the baseline
	// rate. Nil when there is no usable baseline to compute it against.
	PValue *float64 `json:"p_value,omitempty"`
	// Threshold is echoed so the caller never has to know it out of band.
	Threshold float64 `json:"threshold"`
}

// Attribution serves GET /api/v1/triage/attribution.
//
// Public: it returns counters about a test, the same class of data the
// flakiness leaderboard already exposes without a credential, and both the CI
// integration and the agent consult it on every failure.
func (h *Handlers) Attribution(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		api.WriteError(w, r, errRepoRequired())
		return
	}
	testID := q.Get("test_id")
	if testID == "" {
		api.WriteError(w, r, errBadRequest("test_id is required"))
		return
	}
	baselineBranch := orDefault(q.Get("baseline_branch"), "master")
	window := orDefault(q.Get("window"), "30d")
	since, sinceErr := parseSince(window)
	if sinceErr != nil {
		api.WriteError(w, r, sinceErr)
		return
	}

	// Parsed strictly rather than through the shared parseInt, which quietly
	// substitutes its default for anything it cannot read. Silently answering a
	// different question than the one asked is tolerable for a window size and
	// not for the counts that decide whether a check goes green.
	attempts, err := strictInt(q.Get("attempts"), 1)
	if err != nil || attempts < 1 {
		api.WriteError(w, r, errBadRequest("attempts must be a positive integer"))
		return
	}
	failed, err := strictInt(q.Get("failed"), attempts)
	if err != nil || failed < 0 || failed > attempts {
		api.WriteError(w, r, errBadRequest("failed must be an integer between 0 and attempts"))
		return
	}

	base, err := h.baselineFor(r.Context(), normalizeRepo(repo), testID, baselineBranch, since)
	if err != nil {
		h.logError("attribution baseline", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	base.Branch = baselineBranch
	base.Window = window

	res := Decide(base, AttributionObserved{Attempts: attempts, Failed: failed})
	res.Repository = normalizeRepo(repo)
	res.ExternalTestID = testID
	res.Branch = q.Get("branch")
	res.CommitSHA = q.Get("commit")

	writeJSON(w, http.StatusOK, res)
}

// Decide is the whole decision, kept pure so it can be tested without a
// database and read without tracing SQL. Order matters and is load-bearing:
// PR_SUSPECT is evaluated before anything that can grant a green, so no later
// branch can reach past it.
func Decide(base AttributionBaseline, obs AttributionObserved) AttributionResult {
	res := AttributionResult{
		Baseline: base,
		Observed: obs,
	}
	res.Observed.Threshold = unremarkableP

	// No baseline at all. A brand-new test, a renamed one, or a repository that
	// does not use MM-T ids — all indistinguishable here, and none of them a
	// reason to green a check.
	if base.Runs == 0 {
		res.Outcome = AttrNeedsReproduction
		res.NeedsReproduction = true
		res.Reason = "no baseline runs for this test in the window — run it to find out"
		return res
	}

	// Clean on the baseline and failing here. This is the message the whole
	// system exists to deliver, so it is decided first and is never waivable.
	if base.Failed == 0 && obs.Failed > 0 {
		res.Outcome = AttrPRSuspect
		res.Reason = "test has never failed on " + base.Branch +
			" in this window and failed here — this change is the suspect"
		return res
	}

	// Currently red on the baseline. Whatever broke it, this run merely ran it.
	if base.FailingNow {
		res.Outcome = AttrMasterBroken
		res.CanGreen = true
		res.Reason = "already failing on " + base.Branch + " — this run is a bystander"
		if base.FailingSince != nil {
			res.Reason += "; failing since " + short(*base.FailingSince)
			if base.LastPass != nil {
				res.Reason += " (last passed " + short(*base.LastPass) + ")"
			}
		}
		return res
	}

	// Thin history. Some failures exist, but not enough runs to call a rate.
	if base.Runs < minBaselineRuns {
		res.Outcome = AttrNeedsReproduction
		res.NeedsReproduction = true
		res.Reason = "only " + itoa(base.Runs) + " baseline run(s) — too few to call this flaky"
		return res
	}

	// Not flaky enough to hand-wave. A 2%-failing test that just failed is a
	// test that broke, not a flake anyone should be asked to ignore.
	if base.FailureRate < flakeRateFloor {
		res.Outcome = AttrNeedsReproduction
		res.NeedsReproduction = true
		res.Reason = "baseline failure rate " + pct(base.FailureRate) +
			" is below the " + pct(flakeRateFloor) + " flake floor — too reliable to dismiss"
		return res
	}

	// The arithmetic. Does this test's own baseline explain this many failures?
	p := atLeast(obs.Attempts, obs.Failed, base.FailureRate)
	res.Observed.PValue = &p
	if p < unremarkableP {
		res.Outcome = AttrNeedsReproduction
		res.NeedsReproduction = true
		res.Reason = "failed " + itoa(obs.Failed) + " of " + itoa(obs.Attempts) +
			" against a " + pct(base.FailureRate) + " baseline (p=" + f3(p) + " < " + f3(unremarkableP) +
			") — historical flakiness does not explain this"
		return res
	}

	res.Outcome = AttrKnownFlake
	res.CanGreen = true
	res.Reason = "failed " + itoa(obs.Failed) + " of " + itoa(obs.Attempts) +
		" against a " + pct(base.FailureRate) + " baseline (p=" + f3(p) +
		") — this is what this test does"
	return res
}

// strictInt parses an integer, returning the fallback only for an ABSENT
// value. A present-but-unparseable value is an error the caller must see.
func strictInt(raw string, dflt int) (int, error) {
	if raw == "" {
		return dflt, nil
	}
	return strconv.Atoi(raw)
}

// atLeast returns P(X >= k) for X ~ Binomial(n, rate): the probability of
// seeing at least this many failures if the test is simply behaving as it
// always has. Computed by summing the complement, which is the shorter tail for
// every case that matters here.
func atLeast(n, k int, rate float64) float64 {
	if k <= 0 {
		return 1
	}
	if rate <= 0 {
		return 0
	}
	if rate >= 1 {
		return 1
	}
	below := 0.0
	// P(X = i) built iteratively rather than through factorials, which overflow
	// float64 well before the run counts this sees.
	term := math.Pow(1-rate, float64(n))
	for i := 0; i < k; i++ {
		below += term
		term *= rate / (1 - rate) * float64(n-i) / float64(i+1)
	}
	p := 1 - below
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// baselineFor rolls the test's outcomes up per report group on the baseline
// branch. Per-group rather than per-case on purpose: a sharded suite writes one
// case row per shard, and counting rows would report a rate for how the suite
// is sharded rather than for how the test behaves.
func (h *Handlers) baselineFor(ctx context.Context, repo, testID, branch string, since *time.Time) (AttributionBaseline, error) {
	var b AttributionBaseline

	rows, err := h.Pool.Query(ctx, `
		SELECT g.commit_sha,
		       g.created_at,
		       bool_or(tc.status = 'failed') AS failed
		FROM report_groups g
		JOIN reports r      ON r.report_group_id = g.id
		JOIN suites s       ON s.report_id = r.id
		JOIN test_cases tc  ON tc.suite_id = s.id
		WHERE (g.repository = $1 OR split_part(g.repository, '/', 2) = $1)
		  AND g.branch = $2
		  AND tc.external_test_id = $3
		  AND g.created_at >= $4::timestamptz
		GROUP BY g.id, g.commit_sha, g.created_at
		ORDER BY g.created_at DESC
	`, repo, branch, testID, since)
	if err != nil {
		return b, err
	}
	defer rows.Close()

	// Walked newest-first, so the first run decides FailingNow and the first
	// pass closes the failing streak that started after it.
	first := true
	streakOpen := false
	for rows.Next() {
		var sha string
		var at time.Time
		var failed bool
		if err := rows.Scan(&sha, &at, &failed); err != nil {
			return b, err
		}
		b.Runs++
		if failed {
			b.Failed++
		}
		if first {
			b.FailingNow = failed
			b.LastRunAt = &at
			streakOpen = failed
			first = false
		}
		if streakOpen {
			if failed {
				c := sha
				b.FailingSince = &c
			} else {
				c := sha
				b.LastPass = &c
				streakOpen = false
			}
		}
	}
	if err := rows.Err(); err != nil {
		return b, err
	}
	if b.Runs > 0 {
		b.FailureRate = float64(b.Failed) / float64(b.Runs)
	}
	return b, nil
}
