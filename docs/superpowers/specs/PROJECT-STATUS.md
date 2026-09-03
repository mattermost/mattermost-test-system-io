# E2E flakiness management — project status

**Date:** 2026-09-03 · **Branch:** `feat/flakiness-management` · **Round:** 8

**Round 8 cut the machinery that had drifted away from the two goals, and
built the one piece that was missing.**

Removed: the rollout-phase ladder (`phase.go`, migration `000029`,
`/triage/phase`), the throughput model (`throughput.go`), the SLA report
(`sla.go`), the release-cut guard (no consumer, blocked since W0), and two of
the five alert rules (`pass_rate_trend_7d` was `pass_rate_drop_24h` over a
longer window; `cross_pr_cluster` announced per-test what the ranked queue
already says). Gating is now owned by the calling workflow's `mode` input
alone, and the rollout is a merge order rather than server state — see §3.

Added: the **replay job** — a scheduled workflow plus `task: replay` on the
existing action — which re-adjudicates already-ingested runs through the live
policy layer and records `replay`-marked ledger rows. That is what turns the
collection window into a measurement instead of a wait.

Numbers below that were produced "at phase N" were produced in gate mode; the
classifier and policy layer are unchanged.

**Quarantine stays, and an earlier round-8 note calling it redundant was
wrong.** It is not a second spelling of the automatic waiver: `canWaive` checks
it *above* the NEVER_WAIVE set, the 0.85 confidence floor and the citation
rule, so it is the only path that can green a test the classifier cannot judge
at all (INCONCLUSIVE, TEST_DEBT, low confidence). Without it those go red
forever, which is the pain this system exists to remove. The four things it can
never hide — PR_REGRESSION, a product refusal, diff overlap, a shifted failure
rate — are checked before it applies.

Every number below says which model produced it. Local and production are not
interchangeable; conflating them is how this project nearly redesigned a working
classifier.

---

## 1. The three goals

Run live on 2026-09-02: a throwaway DB migrated to head (32), the real TSIO
server, the real `GET /api/v1/triage/evidence`, the real `decide()`/`canWaive`
from the action, and **Opus 5 as the triage model** (not the local 31B). Six
scenarios, **6/6 correct**.

| # | Goal | Status | Evidence |
|---|---|---|---|
| 1 | Tell a developer whether a PR failure is flaky or theirs — green if flaky | **YES** | 6/6 below |
| 2 | Watch master health and fix flaky/failing tests regularly | **YES — wired end to end** (alerting scheduled, queue ranked, loop shipped but off by default) | §4b + §4c |
| 3 | Point out a failure caused by something merged to master, and by whom | **YES (commit range)**, author lookup needs GitHub | `failing_since` + `last_pass` below |

### Goal 1 — the six scenarios, real API, real policy

| Scenario | Test | Setup | Check | Correct |
|---|---|---|---|---|
| A | MM-T2001 | 40% flake on master, 1-of-3 here (p=0.784, unshifted) | **SUCCESS** | ✅ |
| B | MM-T2002 | spotless on master, 3-of-3 here, stack names the edited `drafts.tsx` | **FAILURE** | ✅ |
| C | MM-T5824 | ABAC: 40% flake **and** 3-of-3 here (p=0.064, shifted), CI-only diff | **FAILURE** | ✅ |
| D | MM-T2004 | bystander PR hitting an already-broken master test | **SUCCESS** | ✅ |
| E | MM-T2005 | failed then recovered on retry, 5% on master | **SUCCESS** | ✅ |
| F | MM-T2006 | 10% flake, 1-of-3 here (p=0.271, unshifted) | **SUCCESS** | ✅ |

**Scenario C is the round-6 false green, now caught** — and caught by policy, not
by the model: the rate-shift gate refuses it whatever the verdict says.

### Goal 2 — master health, live

- **Raw** pass-rate `79.17%` with `raw_failures=25`, `waived_failures=0`,
  `effective_failures=25` — recomputed *after* waivers were written, and
  unchanged. Waiving cannot improve the number the team is judged by.
- Alert fired: `new_failing_streak` on **MM-T2004**, `streak=6 / 20 runs`.
- Stabilization queue ranked by blast radius (§4b): MM-T2007 (6 PRs hit) first,
  then MM-T5824 and MM-T2001 (40% each, 1 PR each). The chronic flakes that now
  go green on PRs are exactly the ones at the top of the fix queue — the forcing
  function moved to master, it did not disappear.

### Goal 3 — master regression and its author

The same MM-T2004 failure, seen from a MAIN run in gate mode:
`MAIN_REGRESSION`, `waived=false`, **check FAILURE**, reason *"MAIN runs never
waive MAIN_REGRESSION — the baseline is this run"*, with
`last_pass=d…13` / `failing_since=d…14` recorded in the ledger. That commit
range is exactly what author attribution consumes; resolving the range to a
GitHub handle needs the GitHub API (`blame_commits`), which is wired but was not
called here. On this data the range is a single commit, which is the 16% case
where attribution is precise enough to name someone.

### The ledger — nothing greened silently

Five rows written through `POST /api/v1/triage/verdicts`, each with its
evidence persisted as JSON (3, 2, 1, 1 and 2 citation objects respectively),
including the p-value and α that decided scenario C.

### Honest limits on this run

- **I was the model, and I was not blind.** I designed the scenarios, so this
  measures the *mechanism* end to end, not model accuracy on unseen cases. The
  model-independent findings (the two arithmetic faults below, the gates, the
  ledger, the raw-rate guarantee) do not depend on that. A blind accuracy number
  still requires production traffic.
- **The data is seeded, not production.** Real production failures for
  `mattermost/mattermost` are not reachable from this environment (see §2).
- **Still no screenshots**, so the vision path (`agent.ts` rule 2) is still
  unexercised; scenarios B and C were decided from error text alone.
- **Capability 4's fix loop was not run** — the workflow, the six bans and the CI
  ban gate are all shipped and verified against real diffs, but no fix PR has
  been opened. It is off until `E2E_STABILIZATION_LOOP=on`, and manual runs
  default to dry. Product bugs are **routed via CODEOWNERS, never fixed**, by
  design; the `/e2e-tests/` CODEOWNERS entry now exists (§4c).

### What actually blocked goal 1 — and it was not the model

Two rules were exact complements:

```
classify.go pre-tags FLAKY_TEST only when FailureRate >= 0.10
amnesty denies a waiver     whenever   FailureRate >= 0.10   (inclusive)
```

So a history-based flake verdict could **never** be waived. Any test flakier
than 10% on master turned every PR that touched it red, whoever opened it.
Scenarios A and F both came back FAILURE / *"amnesty denied"* on the first run
while the model's verdict was correct in both. Three rounds measured model
accuracy while a deterministic rule made the primary promise unreachable. Fixed
in `e13544d` (R7-C) by extending the existing W4 bystander principle to
`FLAKY_*` on PR runs — see §5.

### Capability 4 is deliberately narrow

The loop fixes **tests**. Product bugs are **routed to the owning team via
CODEOWNERS and never fixed**. An agent editing product code to make a test pass
is the precise outcome this design exists to prevent — that is a feature, not a
gap. The six mechanical bans (`ban-bare-wait`, `ban-raised-timeout`,
`ban-retry-wrapper`, `ban-skip-tag`, `ban-deleted-assertion`,
`ban-loosened-assertion`) reject the masking edits before any push; all 14 of
their tests pass, and they are now also enforced in mattermost CI on every PR
touching `e2e-tests/**`. The `/e2e-tests/` CODEOWNERS entry is shipped, so
routing no longer falls back to the repo root.

---

## 2. What is measured vs assumed

### Measured, this round, on this machine

| What | Result | How |
|---|---|---|
| R7-B/C policy gates + L2/L3 levers | 154 TS tests, 32 Go triage unit + 17 triage e2e green, golangci-lint 0 across internal/… and tests/… | `npm test`, `go test`, `golangci-lint run` |
| Both ABAC cases refused end-to-end | 2/2, through `decide()` in gate mode | `policy.test.ts` |
| Unshifted control still waives | 2/2 | `policy.test.ts` |
| Full e2e suite | all packages green. One caveat, recorded rather than hidden: `TestOrchestrationHappyPath` timed out once under full-suite testcontainers contention and passed in 4s in isolation — an infra flake, and `orchestration` imports no changed package (`go list -deps`: 0 matches) | `make test-server-e2e`, Docker |
| Blind audit is blind | 6 banned keys absent from the raw sample payload; `ai_verdict` absent before submit, revealed after | `TestBlindAuditSampleAndReview` |
| A waiver never edits history or rates | pass | `TestWaiverNeverEditsHistoryOrRates` |
| Master alerting fires and dedups | pass | `TestMasterAlertingFiresAndDedups` |
| Six stabilization bans | 14/14 | `stabilization-ban-checker.test.js` |

### Assumed, or not measurable here

- **A frontier model judged the six scenarios; the automated pipeline still has
  not called one.** There is no `ANTHROPIC_API_KEY` here, so `agent.ts` cannot
  reach `api.anthropic.com`. Round 7's verdicts were produced by **Opus 5**
  applying `agent.ts`'s rule table to the real evidence packs by hand, then fed
  into the real `decide()`. That closes the "the design was only ever judged by a
  31B" gap but **not** the calibration gap: n=6, non-blind, and no stated-vs-actual
  confidence curve. Every *rate* in rounds 4–6 remains `gemma4:31b-cloud`, which
  was **60% correct while stating 0.90**.
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

### The three baseline numbers already in hand

From [backtest-results.md](backtest-results.md), production TSIO data:

| Number | Value | Sets |
|---|---|---|
| Baseline **raw** master pass-rate | **88.40%** (1,033,851 / 1,169,580 over 1,552 master groups) | the M2 alert floor |
| New-flaky arrival rate | **1.5 / day** (44 tests first failed in 30d) | M3 concurrency |
| Single-commit attribution rate | **16.0%** (319 / 1,995) | M4 wiring |

Attribution is below 20%, so **M4 ships ledger-only — no author pings.**

---

## 3. Recommendation

**Merge tsio#101 now. Hold mattermost#38154 until the accuracy number exists.**

This replaces "start Phase 0 (4 weeks shadow)". It buys the same thing — a
measurement window in which nothing can flip a check — without a shadow-mode
flag, a phase ladder, or any code path that exists only to be inert.

**Why it flips nothing.** The action lives in tsio#101 but nothing calls it
until the mattermost workflows land. Uncalled is not the same as a dead branch:
there is no `phase` to fetch, no ladder to demote, and the only gating control
is the workflow's own `mode` input, which fails closed to shadow.

**Why the window is not idle.** Test history is derived from `report_groups` →
`reports` → `suites` → `test_cases`, which TSIO already ingests, and migration
`000027` backfills `external_test_id` on apply. Baselines, failure rates,
`failing_since` and the blast-radius ranking are therefore populated
retroactively the moment the server ships. Master health alerting
(`triage-master-health.yml`) also runs on TSIO alone and needs only
`TSIO_ALERTS_API_KEY` — that is what retires the 09:00 spot check.

**History is not verdicts, and the blocking number is a verdict number.**
Nothing writes `triage_verdicts` while the action is unwired, so step 1 alone
would produce baselines and still no accuracy figure. The **replay job** closes
that, and it is built: `triage-replay.yml` in this repo runs `task: replay` on
the existing action twice a day, walks failing runs TSIO already holds, and
puts each through the same `fetchEvidence` → classifier → `investigate` →
`decide` → `writeLedger` path a live run uses.

Not shadow-flagged: the rows are real, `check_state` and `waived` are real, and
nothing reads them because no CI is listening. Two properties make it a
measurement rather than a rehearsal — it decides in **gate** mode (a
shadow-mode replay would record `waived=false` everywhere and measure nothing),
and every row is marked `replay` so `GET /triage/accuracy?source=replay` counts
it apart from live. They are never averaged: a replay verdict is decided with
later runs of the same test already in the database.

Both properties are pinned by tests — `replay.test.ts` for gate mode,
`replay_e2e_test.go` for the separation and for the worklist draining — because
each is silent when it breaks.

Round 6 recommended not starting because the false-green rate was 41%. That
measurement was a weaker model judging a sample stripped of the deciding
evidence, and it argued about a gate (`canWaive`) that grants nothing while the
mattermost half is unmerged.

**The blocker moved.** The production `ANTHROPIC_API_KEY` is now needed as a
secret in **this** repo, for the replay job, and it is needed at step 1 rather
than step 2. Screenshot upload in mattermost is still the other half. Without
the key the window produces another month of unmeasurable data and round 9 asks
the same question — so the job refuses to start rather than quietly recording a
classifier-only number under the same name.

Do **not** merge mattermost#38154 on current evidence. It needs the false-green
count measured at 0 on production-model verdicts over a screenshot-bearing
sample.

---

## 4. Still deferred, with owners

| Item | Owner | Blocked on |
|---|---|---|
| **Accept or reject the R7-C policy reversal** — chronic flakes now green bystander PRs; the forcing function is master red + the stabilization queue, not PR red | **needs a human call** | review of `e13544d` |
| **Set a 48-hour stabilization review SLA** — the lever on drain, now stated rather than modelled | **Eva** (rotation owner) | rotation decision, no code |
| **Narrow or remove R7-C once quarantine adoption is real** | test infra | quarantine in use (§4b) |
| **Run the replay job against production data** | test infra | `ANTHROPIC_API_KEY` in this repo (D4). The job itself is built and refuses to start without the key. |
| **mattermost/toolkit wiring** — MAIN triage job, W10 workflow, W9 flag passing, release-cut workflow (second half) | test infra | toolkit PR review |
| **Locating the 09:00 spot check** | **Eva** | — |
| **CODEOWNERS `e2e-tests/**` entry** | test infra | **shipped** — confirm `@mattermost/test-infra` is the right handle |
| **Re-run rounds 4–6 measurement on the production model** | test infra | `ANTHROPIC_API_KEY` + screenshot-bearing sample (see §3) |
| **Commit the backtest harness and dataset** | test infra | — (lost three times; do it with the next measurement) |

---

## 4b. The three levers — "fix master so PRs suffer less"

The instinct is right: a fix removes a recurring source of PR noise
permanently, a waiver only mitigates one occurrence. But the loop **provably
cannot drain** at planned capacity, so prevention alone does not get there:

```
arrival     = 1.5 new flaky tests/day   (measured: 44 in 30d)
window_days = max(7, 20/35 runs per day) = 7      ← the floor governs
cycle_days  = review_latency + 7         = 9..14
drain       = concurrency / (cycle × 1.5) = 0.10..0.37/day
break-even needs concurrency 20..32       — the cap is 5
```

Coverage is **6–25%**; the backlog grows ~1.1–1.4/day. Three levers were
implemented against that.

| Lever | What shipped | Commit |
|---|---|---|
| **1. Make the constraint visible** | ~~`GET /triage/stabilization/throughput`~~ — **removed in round 8.** It served a queueing model over HTTP, affected no verdict, and pinning its output as a test proved the arithmetic rather than the world. The constraint is now stated in prose (spec §4.4) and will be measured by running the loop. | `79ec6c9`, reverted |
| **2. Rank by blast radius** | The queue now leads on **distinct PRs a test failed on**, then master failure count. Realized developer cost, not "most broken". Falls back to the old master-only order when there is no PR data. | `68b02c0` |
| **3. Quarantine — the missing third state** | Owned, expiring, auditable. Migration 000033 + `POST/GET/release`. | `8a836cc` |

**Lever 1's other half is not code.** Review latency is the only real input:
`window_days` is irreducible if you want proof a fix worked, `attempts_per_fix`
is a property of the tests, and concurrency is capped by review capacity. A
weekly named rotation means up to 7 days; **a 48-hour review SLA cuts cycle
14 → 9 days and raises drain ~55%** (0.24 → 0.37/day at the cap). That is Eva's
rotation decision, and it is the single highest-value change available.

### Lever 2, live

| Rank | Test | Affected PRs | Master failures | Rate |
|---|---|---|---|---|
| 1 | MM-T2007 | **6** | 3 | 15% |
| 2 | MM-T5824 | 1 | 8 | 40% |
| 3 | MM-T2001 | 1 | 8 | 40% |

The old ordering buried MM-T2007 at #4 behind two 40% flakes that had each cost
exactly one developer.

### Lever 3, live

Quarantining MM-T2007 (owner `@test-infra`, 14 days):

- PR run → **SUCCESS**, reason `… (quarantined test (owner @test-infra, 13d left, expires 2026-09-16))`
- Same failure on a MAIN run → **FAILURE**, `amnesty denied` — master keeps the forcing function
- Raw master pass-rate **unchanged** (80.00%, `raw_failures` 28, `waived` 0)
- Still **#1 in the fix queue** — quarantine buys time, not forgiveness

Four things quarantine may never hide, each tested: `PR_REGRESSION`, a product
refusal, an overlapping diff, and a shifted failure rate. MAIN and RELEASE are
excluded by construction. Within those bounds it works even on `INCONCLUSIVE`
and bypasses the confidence floor — it is a human pre-authorization, not a
verdict, and that is most of its value.

**Why quarantine is not the old bucket list.** The bucket list failed because
tests went in and were never seen again. Every guardrail it lacked is mandatory
here and `NOT NULL`: owner, reason, creator (from the authenticated subject,
never the body), and a deadline capped at 30 days. `active` is computed at read
time, so a forgotten quarantine **lapses by itself** — no cron, no sweeper — and
the lapsed row is stamped `system:expiry` so the trail shows it ran out rather
than being canceled by a person.

**Honest note on R7-C.** The chronic-flake carve-out is auto-quarantine without
the guardrails: it greens bystander PRs indefinitely, with no owner and no
expiry. Explicit quarantine is strictly stricter. Once quarantine adoption is
real, R7-C should be narrowed or removed — it is kept for now only because
removing it would break goal 1 for any test flakier than 10% until someone
quarantines each one by hand.

---

## 4c. Wiring completed (final pass)

Everything that was on a "still to build" list is now shipped and pushed, except
two items with stated reasons.

| Piece | Where | State |
|---|---|---|
| Master triage (`run-type=MAIN`) | mattermost template | **already existed** — my pending list was wrong |
| Master health alerting, every 2h | tsio `triage-master-health.yml` | shipped; needs `TSIO_ALERTS_API_KEY` |
| Targeted re-measurement (`--grep`, `--retries=0`) | mattermost `e2e-flaky-remeasure.yml` | shipped |
| Stabilization loop | mattermost `e2e-stabilization-loop.yml` | shipped, off until `E2E_STABILIZATION_LOOP=on` |
| Six bans in CI | mattermost `e2e-stabilization-bans.yml` | shipped; verified against real diffs |
| Run-config capture (W9) | dispatch-begin + both templates | shipped — unlocks the config-delta pre-tag |
| CODEOWNERS `/e2e-tests/` | mattermost | shipped |
| Demo hardcodes | mattermost templates | removed (`E2E_TRIAGE_DEMO`, 6x `use-staging`) |

**Still open, with reasons:** the release-cut guard's *workflow* half (the TSIO
half is built; the release automation it must pause has never been located,
flagged since W0), and the `report-upload` action having no test script at all
(a pre-existing gap found while fixing three other actions).

**Two bugs found by wiring rather than by reading.** Three actions ran their
tests from an explicit file list instead of a glob, so any test added to them
was silently never executed — I hit it immediately when my new test file did not
move the suite total. And the committed `ai-triage` dist had drifted from source,
failing the `actions-dist-check` CI gate. Both fixed.

**The rollout changed, twice.** Round 7 moved PR gating to week one on the
strength of the throughput model. Round 8 removed both the model and the phase
ladder: the rollout is now a merge order (spec §7), and the measurement window
is "tsio#101 merged, mattermost#38154 not" — see §3.

---

## 5. What round 7 changed

Two commits, each fixing a fault that no amount of model quality could reach.

### `e13544d` — R7-C, the chronic-flake bystander carve-out

The arithmetic fault in §1: `FLAKY_TEST` needs `rate >= 0.10`, amnesty denies at
`rate >= 0.10`, so the history-based flake verdict was never waivable and goal 1
was unreachable for any test flakier than 10%.

The fix extends the W4 principle the code already states — *"amnesty's pain must
land on master, not on bystander PR authors"* — from `MAIN_REGRESSION` to
`FLAKY_*` on PR runs. The PR author did not make the test flaky.

Safe now and not before, because R7-B runs first: anything reaching the carve-out
has a failure count its own baseline explains. Deliberately unchanged so the
forcing function *moves* rather than disappears — MAIN runs still require
amnesty (master goes hard red), RELEASE still waives nothing, and rate shift,
diff overlap, product refusal, the 0.85 floor and the two-citation rule all
still apply.

**This reverses an existing test** (`"W4: expired amnesty still denies FLAKY on a
PR"`). The reversal is a policy decision, flagged for review rather than slipped
in; the arithmetic is recorded in the test body, with five new tests pinning what
the carve-out must *not* rescue.

### `47db888` — R7-B, the rate-shift gate

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
