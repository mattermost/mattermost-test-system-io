# E2E flakiness management — project status

**Date:** 2026-09-02 · **Branch:** `feat/flakiness-management` · **Round:** 7 (final)

Every number below says which model produced it. Local and production are not
interchangeable; conflating them is how this project nearly redesigned a working
classifier.

---

## 1. The four capabilities

| # | Capability | Status | Number | Artifact |
|---|---|---|---|---|
| 1 | My PR broke a test | **UNMEASURED** | last measured 41% false greens (local 31B, round 6) — bar is 0 | [final-capability-report.md](final-capability-report.md) |
| 2 | It's a flaky test, not me | **UNMEASURED** | last measured 28% false reds (local 31B, round 6) — bar is ≤20% | [final-capability-report.md](final-capability-report.md) |
| 3 | It came from master and is a real bug | **PARTIAL** | mechanism proven by e2e; rate still n=1 | `TestWaiverNeverEditsHistoryOrRates`, `TestMasterAlertingFiresAndDedups` |
| 4 | It watches master and opens a fix PR | **NO (not run)** | six bans green: 14/14 | `scripts/lib/stabilization-ban-checker.test.js` |

**Why 1 and 2 moved from round 6's NO/PARTIAL to UNMEASURED.** Round 6's numbers
came from the local 31B on a sample with **no screenshots**. `agent.ts` rule 2 —
*"screenshot or error shows a WRONG PRODUCT STATE → bug, not flake"* — is the
rule written for exactly this failure class, and it needs a screenshot. The one
mechanism designed to catch the hard case could not fire in the test that judged
it. Under this round's own rule ("do not report a rate from a sample that lacked
the deciding evidence"), those rates are not reportable. They are recorded above
as the last observed values, not as the system's accuracy.

### Capability 4 is deliberately narrow

The loop fixes **tests**. Product bugs are **routed to the owning team via
CODEOWNERS and never fixed**. An agent editing product code to make a test pass
is the precise outcome this design exists to prevent — that is a feature, not a
gap. The six mechanical bans (`ban-bare-wait`, `ban-raised-timeout`,
`ban-retry-wrapper`, `ban-skip-tag`, `ban-deleted-assertion`,
`ban-loosened-assertion`) reject the masking edits before any push; all 14 of
their tests pass. **CODEOWNERS still has no `e2e-tests/**` entry**, so routing
currently falls back to test infra.

---

## 2. What is measured vs assumed

### Measured, this round, on this machine

| What | Result | How |
|---|---|---|
| Rate-shift gate (Task B) | 112 TS tests (was 101), Go triage package green, golangci-lint 0 | `npm test`, `go test`, `golangci-lint run` |
| Both ABAC cases refused end-to-end | 2/2, through `decide()` at phase 1 | `policy.test.ts` |
| Unshifted control still waives | 2/2 | `policy.test.ts` |
| Full e2e suite | all packages green (contract, admin_cli, oidc, orchestration, reports, triage) | `make test-server-e2e`, Docker |
| Blind audit is blind | 6 banned keys absent from the raw sample payload; `ai_verdict` absent before submit, revealed after | `TestBlindAuditSampleAndReview` |
| A waiver never edits history or rates | pass | `TestWaiverNeverEditsHistoryOrRates` |
| Master alerting fires and dedups | pass | `TestMasterAlertingFiresAndDedups` |
| Phase ladder enforced at the API | pass | `TestPhaseGateEndpoints` |
| Six stabilization bans | 14/14 | `stabilization-ban-checker.test.js` |

### Assumed, or not measurable here

- **The production model has never run.** Not in round 6, not in round 7. There
  is no `ANTHROPIC_API_KEY` in this environment. Every AI-layer number in this
  project's history is from `gemma4:31b-cloud` (local). At the 0.85+ confidence
  bucket that model was **60% correct while stating 0.90** — the confidence floor
  is decorative for it. Whether a frontier model is calibrated here is **unknown**.
- **The screenshot caveat still applies.** The vision path has never been
  exercised on a hard case. The local TSIO database holds **2 screenshot rows
  total**, both from a synthetic `tsio-demo` run on 2026-07-08, neither for a
  hard case.
- **Round 4–6 results are unreproducible.** The 41-case labelled dataset and the
  backtest harness were never committed and are not on disk (searched: no file
  contains `MM-T5779`, `pr-38074`, or `pr-37732`). Round 6 committed only its
  report. This is the third time this project has lost uncommitted work.
- **The local DB cannot host the demo.** It is at migration **22**; the triage
  and ledger tables arrive in 27–32. It contains only `mattermost/desktop` and
  `mattermost-mobile` groups from 2026-07-08→11 — no `mattermost/mattermost`
  Playwright data, no MM-T cases. The e2e tests above pass because they build
  their own migrated schema in testcontainers.

### The three Phase 0 numbers already in hand

From [backtest-results.md](backtest-results.md), production TSIO data:

| Number | Value | Sets |
|---|---|---|
| Baseline **raw** master pass-rate | **88.40%** (1,033,851 / 1,169,580 over 1,552 master groups) | the M2 alert floor |
| New-flaky arrival rate | **1.5 / day** (44 tests first failed in 30d) | M3 concurrency |
| Single-commit attribution rate | **16.0%** (319 / 1,995) | M4 wiring |

Attribution is below 20%, so **M4 ships ledger-only — no author pings.**

---

## 3. Recommendation

**Start Phase 0 (4 weeks shadow). One thing blocks it, and it is not code.**

Phase 0 is shadow mode: nothing flips, everything observes and comments
(`modeForPhase` returns `shadow` at phase 0, and `canWaiveAtPhase` refuses every
waiver there). The risk of starting is therefore bounded by construction — a
wrong verdict in shadow costs a wrong comment, not a shipped bug. And shadow
mode is the only way to obtain the two things every remaining question needs:
**production-model verdicts** and **failures that carry screenshots**.

Round 6 recommended not starting because the false-green rate was 41%. That
recommendation rested on a measurement that cannot gate a shadow phase: it was a
weaker model judging a sample stripped of the deciding evidence, and the gate it
was arguing about (`canWaive`) does not grant anything at phase 0 anyway.

**The blocker:** a decision that shadow-phase evidence capture is turned on —
screenshot upload for failing Playwright specs, and the production
`ANTHROPIC_API_KEY` wired to the triage job. Without both, Phase 0 produces
another 4 weeks of unmeasurable data and round 8 asks the same question.

Do **not** promote past Phase 0 on current evidence. Promotion to phase 1 (PR
checks may green) needs the false-green count measured at 0 on production-model
verdicts over a screenshot-bearing sample.

---

## 4. Still deferred, with owners

| Item | Owner | Blocked on |
|---|---|---|
| **W12** — waiver-authority auto-demotion tuning | test infra | 4 weeks of shadow data |
| **mattermost/toolkit wiring** — MAIN triage job, W10 workflow, W9 flag passing, release-cut workflow (second half) | test infra | toolkit PR review |
| **Locating the 09:00 spot check** | **Eva** | — |
| **CODEOWNERS `e2e-tests/**` entry** | test infra | routing falls back to test infra until it lands |
| **Re-run rounds 4–6 measurement on the production model** | test infra | `ANTHROPIC_API_KEY` + screenshot-bearing sample (see §3) |
| **Commit the backtest harness and dataset** | test infra | — (lost three times; do it with the next measurement) |

---

## 5. What round 7 changed

One commit: **`47db888`** — the R7-B rate-shift gate.

**The structural fault it fixes** (`classify.go`): `PR_REGRESSION` requires
`Failed == 0`, so a historically flaky test can never reach it. For the most
expensive error class — a flaky test that this time broke for real — "flaked
again" and "broke for real" were indistinguishable to the classifier. Both land
on `FLAKY_TEST`, which is waivable.

**The fix is a policy gate, not a prompt hint**, because the model is measurably
overconfident and because the policy gate, not the model, owns every green:

- `rateshift.go` — an exact binomial tail test. Under the null *"this PR's runs
  are draws from this test's own baseline rate p"*, compute `P(X ≥ k | n, p)`. A
  small p-value means the baseline does not explain this commit.
- `Signals.RateShift` + the `rate_shifted_at_commit` citation, surfaced in the
  evidence pack and as a citable line in the agent prompt.
- `canWaive` gains `rateShiftedAtCommit`: a shifted rate refuses **any** `FLAKY_*`
  waiver, whatever the model said and whatever confidence it claimed. It sits
  above the confidence floor on purpose — confidence must not buy past it.

**Threshold: α = 0.10, chosen from the bars.** α *is* the false-red rate this
gate adds: for a test that really is just flaky at rate p, the observation is a
genuine draw from the null, so it trips the gate with probability exactly α. So
the choice is a direct purchase against the published bars — false greens bar 0,
false reds bar ≤ 20% — and α = 0.10 spends half the false-red budget. It is also
the level required to catch a 40%-baseline test at 3-of-3 (p = 0.4³ = 0.064);
α = 0.05 would not. **Not fitted to a held-out sample** — that sample does not
exist (§2).

**Fail-open on absence.** An uncomputable comparison (no PR runs, baseline < 5
runs, < 2 PR runs) is never `shifted`, so a missing signal reduces exactly to
the prior behavior. The gate can only ever refuse a waiver, never grant one —
which is why fail-open is safe here.

**Blast radius.** `canWaive`'s new parameter is optional, so existing call sites
compile and behave identically; the classifier's verdict selection is unchanged
(only citations and reason text gain a clause); the new
`testhistory.LookupPRFailureCounts` is additive. The four failing
`internal/config` tests are pre-existing and environmental — that package does
not import either changed package (`go list -deps`: 0 matches), and the failures
show the developer's real `apps/server/.env` values leaking into the test
process.

**What it does not fix.** It does not make the model better, and it does not
touch the calibration problem. It removes the *most expensive* error class from
the model's reach — which is the point.
