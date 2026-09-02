package triage

// Rate-shift detection: telling "flaked again" apart from "broke for real".
//
// # THE STRUCTURAL FAULT THIS FIXES
//
// The deterministic classifier can only reach PR_REGRESSION when the test has
// never failed on the baseline branch (classify.go requires Failed == 0). A
// historically flaky test can never satisfy that, so for exactly the class of
// failure that costs the most — a flaky test that this time broke for real —
// "flaked again" and "broke for real" are indistinguishable to the classifier.
// Both land on FLAKY_TEST, and a FLAKY_TEST verdict is waivable.
//
// # THE SIGNAL
//
// A test's own baseline failure rate is the null hypothesis. If this test fails
// at 40% on master, then 3 failures in 3 runs on a PR is unremarkable. If it
// fails at 5% on master, 3-of-3 is not. The question is not "is this test
// flaky" (it is) but "is THIS MUCH failure explainable by its usual flakiness".
//
// That is an exact binomial tail test. Under the null "this PR's runs are draws
// from the baseline rate p", the probability of seeing at least k failures in n
// runs is P(X >= k | n, p). When that probability is small, the null does not
// explain the observation and the rate has shifted at this commit.
//
// # HOW THE THRESHOLD WAS CHOSEN
//
// alpha IS the false-red rate this gate adds, by construction. For a test that
// really is just flaky at its baseline rate p, the observation is a genuine
// draw from the null, so the probability that it trips the gate is exactly
// alpha. That makes the choice a direct purchase against the project's own
// published bars rather than a number fitted to a sample:
//
//	false greens (waived a real bug) → bar 0     — what this gate buys down
//	false reds   (kept noise red)    → bar ≤ 20% — what this gate spends
//
// alpha = 0.10 spends half the false-red budget and is the loosest conventional
// significance level. It is deliberately generous because the cost structure is
// asymmetric: a waived real bug is a shipped bug plus lost trust, while a false
// red costs a developer ten minutes.
//
// Worked consequence, stated up front rather than discovered: the ABAC case
// (MM-T5824 / MM-T5820) sits at a 40% baseline rate and failed 3-of-3 on
// pr-37732. P(X >= 3 | n=3, p=0.40) = 0.4^3 = 0.064. That clears alpha = 0.10
// and is refused; it would NOT clear alpha = 0.05. A 40%-flaky test going
// 3-of-3 is the weakest shift this gate is required to catch, and it sets the
// floor for alpha.
//
// NOT FITTED TO A HELD-OUT SAMPLE. The 41-case labeled dataset from rounds 4-6
// was never committed and does not exist on disk, so alpha could not be tuned
// against it. The derivation above is from the bars, not from case outcomes —
// which is the stronger basis anyway, but the distinction is recorded honestly.
const (
	// rateShiftAlpha is the binomial tail probability below which this
	// commit's failure count is not explained by the test's baseline rate.
	// See the derivation above: alpha is also the false-red rate added.
	rateShiftAlpha = 0.10

	// minBaselineRuns is the smallest baseline sample from which a failure
	// rate is worth estimating. Below it the null hypothesis is noise, and a
	// gate driven by noise refuses waivers at random.
	minBaselineRuns = 5

	// minPRRunsForShift is the smallest number of runs on this PR that can
	// carry a shift. A single failure is weak evidence by construction:
	// P(X >= 1 | n=1, p) = p, which only clears alpha for baselines already
	// under 10% — and those tests are not the flaky ones this gate is for.
	minPRRunsForShift = 2
)

// RateShift is the baseline-vs-current failure rate comparison for one test:
// its rate on the baseline branch versus its rate across this PR's runs.
//
// OK is false when the comparison is not computable (no PR context, too small
// a baseline, too few PR runs). Absence of the signal is NOT evidence of no
// shift — it must never itself justify a waiver, only decline to refuse one.
type RateShift struct {
	OK bool `json:"ok"`

	BaselineRuns   int     `json:"baseline_runs"`
	BaselineFailed int     `json:"baseline_failed"`
	BaselineRate   float64 `json:"baseline_rate"`

	PRRuns   int     `json:"pr_runs"`
	PRFailed int     `json:"pr_failed"`
	PRRate   float64 `json:"pr_rate"`

	// PValue is P(X >= PRFailed | n=PRRuns, p=BaselineRate) — the chance this
	// PR's failures are an ordinary draw from the test's usual flakiness.
	PValue float64 `json:"p_value"`

	// Shifted is the gate's answer: the rate rose, and it rose by more than
	// the baseline rate explains at alpha.
	Shifted bool `json:"shifted"`

	// Alpha is echoed so a ledger row records the threshold it was judged
	// against, not just the outcome.
	Alpha float64 `json:"alpha"`
}

// ComputeRateShift runs the binomial tail test for one test's baseline rate
// against its rate on the current PR.
//
// baselineFailed and prFailed must both count a flaky (failed-then-recovered)
// run as a failure, matching HistorySummary.FailureRate — a run that needed a
// retry did not cleanly pass.
func ComputeRateShift(baselineRuns, baselineFailed, prRuns, prFailed int) RateShift {
	rs := RateShift{
		BaselineRuns:   baselineRuns,
		BaselineFailed: baselineFailed,
		PRRuns:         prRuns,
		PRFailed:       prFailed,
		Alpha:          rateShiftAlpha,
		PValue:         1,
	}
	if baselineRuns > 0 {
		rs.BaselineRate = float64(baselineFailed) / float64(baselineRuns)
	}
	if prRuns > 0 {
		rs.PRRate = float64(prFailed) / float64(prRuns)
	}

	// Not computable — leave OK false and Shifted false. Declining to refuse
	// is the only safe default here: this function never grants a waiver, so
	// an unavailable signal preserves the pre-existing behavior exactly.
	if baselineRuns < minBaselineRuns || prRuns < minPRRunsForShift || prFailed <= 0 {
		return rs
	}

	rs.OK = true
	rs.PValue = binomTailAtLeast(prFailed, prRuns, rs.BaselineRate)
	// A rate that fell is never a shift worth refusing a waiver over, and the
	// one-sided test must not fire on an improvement.
	rs.Shifted = rs.PRRate > rs.BaselineRate && rs.PValue <= rateShiftAlpha
	return rs
}

// binomTailAtLeast returns P(X >= k) for X ~ Binomial(n, p).
//
// n is a history window (≤ a few dozen runs), so the direct sum is exact
// enough and needs no log-space guard.
func binomTailAtLeast(k, n int, p float64) float64 {
	if k <= 0 {
		return 1
	}
	if k > n || n <= 0 {
		return 0
	}
	switch {
	case p <= 0:
		// A test that has never failed on the baseline cannot explain any
		// failure at all: the null assigns this observation probability 0.
		return 0
	case p >= 1:
		// A test that always fails on the baseline explains anything.
		return 1
	}

	q := 1 - p
	tail := 0.0
	for i := k; i <= n; i++ {
		tail += binomCoef(n, i) * pow(p, i) * pow(q, n-i)
	}
	// Guard against float drift pushing a probability outside [0, 1].
	switch {
	case tail < 0:
		return 0
	case tail > 1:
		return 1
	}
	return tail
}

// binomCoef returns C(n, k) computed multiplicatively so the intermediate
// values stay small — n! overflows float64 precision well before n = 170.
func binomCoef(n, k int) float64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	c := 1.0
	for i := 0; i < k; i++ {
		c = c * float64(n-i) / float64(i+1)
	}
	return c
}

// pow is integer-exponent exponentiation; math.Pow would work but this keeps
// the tail sum free of its edge-case handling for 0^0.
func pow(base float64, exp int) float64 {
	if exp <= 0 {
		return 1
	}
	v := 1.0
	for i := 0; i < exp; i++ {
		v *= base
	}
	return v
}
