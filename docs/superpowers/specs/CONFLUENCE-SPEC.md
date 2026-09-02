# E2E Flakiness Management — Specification for Review

> **Status:** Ready for review · **Owner:** Yasser Khan · **Last updated:** 2 Sep 2026
> **Decision needed by:** Fri 4 Sep 2026 · **Reviewers:** @saturnino @eva @nuno
> **Code:** all three PRs are updated and pushed — see §10

---

## Table of contents

1. [Why this exists](#1-why-this-exists)
2. [What a developer will actually experience](#2-what-a-developer-will-actually-experience)
3. [How a verdict is reached](#3-how-a-verdict-is-reached)
4. [The master side](#4-the-master-side)
5. [Why this is not the old bucket list](#5-why-this-is-not-the-old-bucket-list)
6. [What is measured, and what is not](#6-what-is-measured-and-what-is-not)
7. [Rollout](#7-rollout)
8. [What it costs you](#8-what-it-costs-you)
9. [Decisions we need from you](#9-decisions-we-need-from-you)
10. [Delivery status](#10-delivery-status)
11. [Appendix: API surface](#11-appendix-api-surface)

---

## 1. Why this exists

Two problems, one system.

**A developer cannot tell whether a red E2E check is their fault.** Master's raw
pass-rate is **88.40%**, so most PRs meet at least one failure that has nothing
to do with their change. The rational response is to re-run until green, which
trains everyone to ignore E2E results — and that is how a real regression ships.

**Flaky tests are never actually fixed.** New flaky tests arrive at **1.5/day**.
Without a mechanism that ends in a merged fix, the stock only grows.

### The one thing that killed the previous attempt

The old bucket list let tests be marked "known flaky" and then never looked at
again. Two properties made it fail, and both are designed against here:

| Old failure | This design |
|---|---|
| A waiver improved the number the team was judged by | The **raw** pass-rate is published and waivers cannot move it. §5 |
| Tests entered the bucket and were never seen again | Every waiver is a **ledger row with its evidence**; quarantine requires an **owner and a deadline** |

---

## 2. What a developer will actually experience

Three cases, and what the check does.

### Case 1 — "It's flaky, not my change" → check goes **green**

> `MM-T2001 channel switcher opens` — fails 40% of the time on master. Your PR
> hit it once in three runs.

The check goes **green**. The comment says why, and the waiver is recorded with
its evidence. You are not asked to re-run anything.

The reasoning is not "this test is flaky so ignore it" — it is **"this much
failure is what this test's own history predicts"**. One failure in three runs
for a 40% test has probability 0.78 under its own baseline. Nothing unusual
happened.

### Case 2 — "Your PR broke this" → check stays **red**

> `MM-T2002 message draft persists across reload` — spotless on master (0/20),
> failed 3 of 3 on your PR, and the stack names `drafts.tsx`, which you edited.

The check stays **red**, the comment names the suspect, and the ledger records a
`PR_REGRESSION`. This is never waivable, and no quarantine can hide it.

### Case 3 — "This came from master" → **your** check goes green, **master** stays red

> `MM-T2004 system console loads plugin list` — passed on master until commit
> `d…14`, has failed every run since. Your PR merely ran it.

Your check goes **green** as a bystander. The same failure on a master run stays
**red**, is attributed to the commit range between `last_pass` and
`failing_since`, and goes into the fix queue. **The pain lands on the owner, not
on whoever's PR happened to hit it.**

### The hard case, and why it is the one that matters

> `MM-T5824 ABAC file access policy renders` — 40% flaky on master **and** failed
> 3 of 3 on this PR.

Historically flaky *and* genuinely broken. The old classifier could not tell
this apart from case 1, because it could only reach "your PR broke it" for tests
with a spotless history — which a flaky test never has.

It is now caught by arithmetic, not by model judgment: 3-of-3 for a 40% test has
probability **0.064**, below the 0.10 threshold, so "it flaked again" does not
explain it. **The check stays red regardless of what the AI concluded or how
confident it claimed to be.**

---

## 3. How a verdict is reached

```
E2E run fails
      │
      ▼
┌──────────────────────────────────────────────┐
│ 1. DETERMINISTIC CLASSIFIER (no AI)          │
│    Reads this test's history: pass/fail      │
│    series, flip count, failure rate,         │
│    failing-since commit, config deltas.      │
│    Decides outright where it can.            │
└──────────────────┬───────────────────────────┘
                   │ ambiguous cases only
                   ▼
┌──────────────────────────────────────────────┐
│ 2. AI ADJUDICATION (advisory)                │
│    Gets error, stack, screenshots, history,  │
│    the PR diff, and the failing test source. │
│    Returns a verdict + confidence + reasons. │
└──────────────────┬───────────────────────────┘
                   │ advisory only
                   ▼
┌──────────────────────────────────────────────┐
│ 3. POLICY GATE — owns every green            │
│    Refuses a waiver, whatever the AI said:   │
│      • the failure rate shifted here         │
│      • the PR diff touches the failing area  │
│      • the product deliberately refused      │
│      • confidence below 0.85                 │
│      • fewer than two independent citations   │
│      • release branches: never               │
│      • master: never waives a master bug     │
└──────────────────┬───────────────────────────┘
                   ▼
      Ledger row (mandatory) ──► check flipped
      No ledger row ──► check NOT flipped
```

**The design principle:** the AI is one input to a decision the policy layer
owns. The AI can only ever *propose* a green; five independent rules can refuse
it. This matters because we measured the model's calibration and it was poor —
see §6.

### Verdicts

| Verdict | Meaning | Waivable on a PR? |
|---|---|---|
| `FLAKY_TEST` | Timing race, correct product state | Yes |
| `FLAKY_INFRA` | Environment, bootstrap, emulator | Yes |
| `FLAKY_SERVER` | Network/transport failure | Yes |
| `MAIN_REGRESSION` | Already broken on master | Yes (bystander) — **never on master** |
| `PR_REGRESSION` | This change broke it | **No** |
| `TEST_DEBT` | Test drives unsupported state | **No** |
| `INCONCLUSIVE` | Evidence contradictory | **No** |

---

## 4. The master side

### 4.1 Detection

- **Raw pass-rate**, published, unaffected by waivers.
- **Five alert rules**, deduplicated: one channel post per 24h per subject, one
  GitHub issue opened per story and then updated in place.
- Replaces the manual 09:00 spot check.

### 4.2 The fix queue, ranked by blast radius

Ranked by **how many distinct PRs a test broke** — realized developer cost —
then by master failure count.

| Rank | Test | PRs broken | Master failures | Rate |
|---|---|---|---|---|
| 1 | MM-T2007 | **6** | 3 | 15% |
| 2 | MM-T2001 | 1 | 8 | 40% |
| 3 | MM-T5824 | 1 | 8 | 40% |

Ranking by master failure count alone would put MM-T2007 fourth — below two
tests that had each cost exactly one developer. When you can only fix a fraction
of what arrives, *what* you fix matters more than how fast.

### 4.3 The agent fix loop

The loop investigates a queued test, opens a PR, and a human reviews before
merge. **Nothing lands on master from an agent without a reviewer.**

Six mechanical bans reject the cheap ways an agent "fixes" flakiness by hiding a
real bug. CI enforces them, so the reviewer judges the *approach* rather than
policing the obvious:

1. bare sleep / fixed wait
2. retry wrapper around a previously un-retried assertion
3. loosened assertion
4. deleted assertion
5. skip or ignore tag
6. raised timeout at test, suite or config level

The override label is **human-only** and applying it writes a ledger row.

> ### ⚠️ Product bugs are routed, never fixed
> The loop can only edit `e2e-tests/**`. When it diagnoses a product bug it does
> not attempt a fix — it routes to the owning team via CODEOWNERS with the
> evidence attached. An agent editing product code to make a test pass is the
> exact outcome this design exists to prevent.
>
> **Open gap:** CODEOWNERS has no `e2e-tests/**` entry, so routing currently
> falls back to test infra.

### 4.4 The loop cannot keep up, and we should say so

| Input | Value |
|---|---|
| Arrival | **1.5 new flaky tests/day** (measured) |
| Re-measurement window | 7 days (irreducible — it is the proof a fix worked) |
| Review latency | up to 7 days on a weekly rotation |
| Cycle time | 9–14 days |
| Drain | **0.10–0.37/day** |
| Concurrency needed to break even | **20–32** |
| Concurrency cap | **5** (review capacity, not agent capacity) |

**With the naive sampling strategy, coverage is 6–25% and the backlog grows by
roughly 1.1–1.4 tests/day.** That is not the end of the story — see below.

This is the most important number in this document, and it inverts a natural
assumption. "Fix master and PR pain goes away" is right in direction — a fix
removes a recurring source of noise permanently, where a waiver only mitigates
one occurrence — but it does **not** close at planned capacity. **PR-side
waivers are load-bearing indefinitely, not a temporary bridge.**

Only one input is a real lever. The window is irreducible, attempts-per-fix is a
property of the tests, concurrency is capped by review capacity. That leaves
**review latency**:

| Review latency | Cycle | Drain | Change |
|---|---|---|---|
| 7 days (weekly rotation) | 14d | 0.24/day | — |
| **2 days (SLA)** | **9d** | **0.37/day** | **+55%** |

### The 7-day window was never irreducible — and that changes the answer

An earlier version of this document claimed the re-measurement window could not
be shortened because it *is* the proof a fix worked. That was wrong, and the
error was in the sampling strategy rather than the arithmetic.

The 7 days exist only because the naive way to collect 20 samples of a test is
to wait for 20 natural master runs. **Re-run the one test 20 times with
`--grep` on its `MM-T` id and 20 samples take a single job.** It is also a
*better* measurement: 20 consecutive executions at one commit isolate that
test's own flakiness, whereas 20 master runs spread over a week confound it
with everything else that landed.

| Configuration | Drain | vs arrival 1.47/day |
|---|---|---|
| Baseline — wait for master, weekly review, concurrency 2 | 0.10/day | 7% |
| \+ targeted re-measurement | 0.18/day | 12% |
| \+ targeted re-measurement **and** 48h review SLA at concurrency 5 | **1.48/day** | **keeps up** ✅ |

**Both levers are required.** Targeted re-measurement alone at a weekly review
cadence reaches only 0.46/day. This is pinned as a test, not asserted as a
claim, so it cannot quietly stop being true.

So the loop *can* keep master clean — which is what makes gating PRs from the
start defensible. It needs decisions **D5** (the review SLA) and the
re-measurement workflow, which is built and shipped (§10).

### 4.5 Quarantine — the third state

Before this, a chronically flaky test had two fates: fixed (slow, review-bound)
or waived on every PR forever. Quarantine is the third, and it is **stricter**
than an automatic waiver:

| Guardrail | Enforced how |
|---|---|
| **Owner** | Mandatory, `NOT NULL` — rejected at the API without one |
| **Deadline** | Mandatory, max 30 days |
| **Expiry** | Computed at read time — a forgotten quarantine **lapses by itself**, no cron. The test goes red again by default |
| **Creator** | Taken from the authenticated identity, never from the request body |
| **Master** | Untouched. The test keeps running, keeps counting in the raw pass-rate, keeps its queue position |
| **Audit** | Lapse is stamped `system:expiry`; every application is counted |

**Four things a quarantine can never hide:** `PR_REGRESSION`, a product refusal,
an overlapping diff, and a shifted failure rate. It never applies to master or
release runs.

---

## 5. Why this is not the old bucket list

Three mechanisms, each verified by an automated test:

**1. Waiving cannot improve the number you are judged by.** The raw pass-rate is
computed from run outcomes; waivers are a separate column. Verified live: after
writing waivers and a quarantine, `raw_pass_rate` and `raw_failures` were
unchanged.

**2. Nothing is silently ignored.** Every waiver is a ledger row carrying the
evidence that justified it — including the p-value and threshold that decided
the ABAC case. **If the ledger write fails, the check is not flipped.**

**3. The AI's authority is re-earned.** A weekly blind human sample reviews
waivers. The payload genuinely does not contain the answer — verified by
asserting that `verdict`, `confidence`, `root_cause`, `model`, `stratum` and
`suspect_commit` are all absent from the raw API response. If agreement slips,
the system drops a rollout phase automatically.

---

## 6. What is measured, and what is not

> **Read this section before approving.** The mechanism is proven; the AI's
> judgment is not.

### Verified

| Claim | Evidence |
|---|---|
| Seven PR scenarios reach the correct check state | 7/7, real API + real policy layer |
| The hard ABAC case is caught | Refused by the rate-shift gate |
| Waivers cannot move the raw pass-rate | Live, before/after |
| Nothing greens without a ledger row | Automated test |
| The blind audit payload has no verdict | Automated test, six banned keys |
| Master never waives a master regression | Automated test, all phases |
| Release branches waive nothing | Automated test |
| Six stabilization bans | 14/14 |
| Test suite | 152 unit (ai-triage) + 46 triage unit + 43 stabilization + triage e2e, golangci-lint 0. **Caveat:** `internal/config` has 4 failures on a developer machine — `loadDotenv()` picks up the local `.env`, so `TSIO_DATABASE_URL`/`TSIO_S3_*` leak into the assertions. Pre-existing, unrelated to triage, and green in CI (proven in a clean worktree). Non-hermetic tests are still a real if minor defect. |

### Not verified — stated plainly

| Gap | Why it matters |
|---|---|
| **The pipeline HAS now called a frontier model — once.** mattermost#38154 run 33678302436 invoked `claude-sonnet-4-6` in CI on 4 real failures across 3 clusters. | Supersedes the earlier "never called" line. But **n=3 clusters, all on tests with `runs=0`**, none independently confirmed, and nothing gated (a missing `/triage/phase` on staging pinned the run to shadow). Still no accuracy number. |
| **No accuracy or calibration number exists.** Earlier rounds used a local 31B which was **60% correct while stating 0.90 confidence**. | The 0.85 confidence floor protects nothing against a model that says 0.9 on everything. This is precisely why the policy gate — not the model — owns every green. |
| **The screenshot path HAS now fired.** On the same run, 2 of 3 clusters cited screenshots and one reasoned from image content ("the Members panel is fully rendered… but the visible button is labelled 'Add'"). | Supersedes the earlier "never exercised" line — this was the single biggest measurement gap in rounds 4–6. Still only 3 clusters, and none of the verdicts is independently confirmed. |
| **The agent fix loop has never run end to end.** The six bans pass, but no fix PR has been opened. | §4.3 is designed, not demonstrated. |

**What this means for approval.** Phase 0 is shadow mode: nothing flips,
everything observes. The risk of starting is bounded by construction, and it is
the only way to obtain the two things every remaining question needs —
production-model verdicts and failures that carry screenshots. **Do not promote
past Phase 0 until those numbers exist.**

---

## 7. Rollout

The ladder below changed after the throughput work in §4.4. The original plan
was four weeks of shadow before anything could gate. Now that the loop can
actually keep master clean, **PR gating starts in week one, with master
enforcement alongside it** — because gating PRs is only safe if master is being
kept clean, and keeping master clean is only worth it if PRs stop being red for
reasons nobody owns. The two halves need each other.

| Phase | PR checks | Master checks | Exit criteria |
|---|---|---|---|
| **1 — PR gate + master watch** (week 1) | **gate** — may green on a waived flake | red stays red; alerting live; queue ranked | zero false greens in the ledger audit |
| **2 — Master gate** (+2 weeks) | gate | may green on a *confirmed* flake | blind-audit agreement holds |
| **3 — Loop** (+2 weeks) | gate | gate | agent fix PRs begin, review SLA holding |

Phase drops happen automatically if blind-audit agreement slips.

**Why starting at the gate is defensible rather than reckless.** Every green
still has to pass five independent policy refusals that the AI cannot influence
(§3), a green is impossible without a ledger row, and `PR_REGRESSION` is never
waivable. The failure mode of a wrong verdict in week one is one wrongly-green
check on a PR whose author can see the reasoning and the evidence — not a silent
one, and not a release.

**Expect master to look bad in week one.** The raw pass-rate is 88.40% today,
so enforcing master will make it visibly red a lot. That is the point: it is
already broken, and the current arrangement just distributes the pain across
every PR author instead of showing it in one place. The blast-radius ranking
(§4.2) and quarantine (§4.5) exist so the queue is worked worst-first rather
than all at once.

### One decision still blocks the AI half

Turn on **screenshot upload for failing specs** and wire the **production API
key** to the triage job (decision **D4**). Without screenshots the rule written
for the hard case cannot fire, and without the key the pipeline cannot call a
frontier model at all. The deterministic half — rate-shift, config-delta,
history — works without either.

## 8. What it costs you

| | Effort | When |
|---|---|---|
| Blind waiver audit | **45 min/week** | From Phase 0 |
| Stabilization PR review | **up to 2h/week** | From Phase 3 |

One named test-infra reviewer per week. **If the open-PR count exceeds the
budget, concurrency drops rather than review quality.** A reviewer may not audit
a waiver on a test whose stabilization PR they reviewed that week.

---

## 9. Decisions we need from you

Silence on a line = we proceed with the default.

| # | Decision | Default | Who |
|---|---|---|---|
| **D1** | Master checks may go green on confirmed flakes (Phase 2+) | Yes | @saturnino |
| **D2** | Start at **Phase 1 — PR gating live in week one**, not four weeks of shadow | Yes | @saturnino |
| **D3** | Named weekly test-infra rotation at the budget in §8 | Yes | @eva |
| **D4** | Turn on screenshot upload + wire the production API key | Yes — **blocks Phase 0** | @nuno |
| **D5** | **48-hour stabilization review SLA** (+55% drain, §4.4) | Yes | @eva |
| **D6** | Chronic flakes green bystander PRs; the forcing function is master red + the queue, not PR red | Yes | @saturnino |
| **D7** | Add a CODEOWNERS `e2e-tests/**` entry | **shipped** | @eva to confirm the team handle |
| **D8** | Enforce master alongside the PR gate from week one, accepting a visibly red master while the queue drains | Yes | @saturnino |

> **D6 deserves a real look.** It reverses an earlier decision. A test failing
> more than 10% of the time on master currently turns **every** PR red,
> including PRs whose authors had nothing to do with it — which made the
> system's primary promise unreachable. The fix moves that pressure to master,
> where the fix is owned. The trade: a chronic flake stops blocking PRs, so the
> only thing driving it to be fixed is master red plus its queue position. If
> you would rather chronic flakes keep PRs red, say so and we revert one commit.

---

## 10. Delivery status

**All three PRs are updated and pushed.** Nothing described in this document is
sitting on an unpushed branch.

| PR | Contains | Branch |
|---|---|---|
| [tsio#101](https://github.com/mattermost/mattermost-test-system-io/pull/101) | The server, the policy layer, the action, the web surfaces, and the scheduled master-health alerting | `claude/e2e-ai-triage-api` |
| [mattermost#38154](https://github.com/mattermost/mattermost/pull/38154) | PR + master triage wiring, targeted re-measurement, the stabilization loop, the ban gate, CODEOWNERS | `e2e/ai-flake-triage-demo` |
| [toolkit#3](https://github.com/mattermost/mattermost-test-automation-toolkit/pull/3) | The maintainer override path (`/e2e-triage-override`) | `claude/e2e-ai-triage` |

### What was wired in this pass

| Piece | Where | Notes |
|---|---|---|
| **Master triage** | already existed | `e2e-tests-on-merge.yml` passes `report_type=MASTER`, the template maps it to `run-type=MAIN`. This was on a pending list by mistake. |
| **Master health alerting** | tsio `triage-master-health.yml` | Every 2h. This is what retires the 09:00 spot check. Needs one secret. |
| **Targeted re-measurement** | mattermost `e2e-flaky-remeasure.yml` | `--grep` + `--repeat-each`, `--retries=0`. The piece that makes the loop keep up (§4.4). |
| **Stabilization loop** | mattermost `e2e-stabilization-loop.yml` | Off until `E2E_STABILIZATION_LOOP=on`; manual runs default to dry. |
| **The six bans, in CI** | mattermost `e2e-stabilization-bans.yml` | On every PR touching `e2e-tests/**`, not just agent PRs. Human-only override label. |
| **Run-config capture (W9)** | both templates + dispatch-begin | Unlocks the config-delta pre-tag — the only verdict with no AI in the loop. |
| **CODEOWNERS** | mattermost | `/e2e-tests/` → `@mattermost/test-infra`. |
| **Demo hardcodes removed** | mattermost templates | `E2E_TRIAGE_DEMO: fail` and six `use-staging: true` now read repo variables. |

### Setup needed before it runs

| What | Where | Why |
|---|---|---|
| Secret `TSIO_ALERTS_API_KEY` | tsio repo | The alerting job. `tsioctl keys issue --name master-health-alerts` |
| Variable `E2E_STABILIZATION_LOOP=on` | mattermost | Enables the fix loop (Phase 3) |
| Screenshot upload + `ANTHROPIC_API_KEY` | mattermost | Decision **D4** — the AI half |
| Confirm `@mattermost/test-infra` is the right handle | mattermost | CODEOWNERS routing |

### Still open, deliberately

| Item | Why it is not done |
|---|---|
| **Release-cut guard, workflow half** | The TSIO half (`/triage/release-guard`) is built and callable. The release automation it must pause was never located — flagged since W0. Needs someone who knows where the release-cut job lives. |
| **`report-upload` action has no tests** | Pre-existing gap, found while fixing three other actions whose test globs were silently running nothing. Out of scope here; worth its own PR. |
| **The loop has never opened a fix PR** | It is wired, off by default, and dry on manual runs. First real run is a Phase 3 activity. |

### Reproducing the demo locally

```bash
bash docs/superpowers/specs/r7-demo/demo.sh
```

Builds a throwaway database, migrates to head, seeds seven scenarios, starts the
real server, and drives the real endpoints and the real policy layer. `--keep`
leaves it running; `--cleanup` tears it down. Not part of any PR — it hardcodes
local dev container names and credentials.

---

## 11. Appendix: API surface

**Public reads** — the CI action and the test runner consult these without a credential.

| Endpoint | Purpose |
|---|---|
| `GET /triage/evidence` | Everything needed to adjudicate one run |
| `GET /triage/pass-rates` | Raw and effective pass-rates |
| `GET /triage/alerts/evaluation` | Dry-run alert evaluation |
| `GET /triage/quarantine` | Active quarantines |
| `GET /triage/stabilization/throughput` | Arrival vs drain, and the binding lever |
| `GET /triage/phase` | Current rollout phase |

**Authenticated writes**

| Endpoint | Purpose |
|---|---|
| `POST /triage/verdicts` | Write ledger rows |
| `POST /triage/quarantine` | Quarantine a test (owner + deadline mandatory) |
| `POST /triage/quarantine/{id}/release` | End one early (reason mandatory) |
| `POST /triage/stabilization/promote` | Push a test up the queue |
| `POST /triage/audit/reviews` | Submit a blind audit call |
| `POST /triage/phase` | Change rollout phase |

**Reference:** `docs/superpowers/specs/PROJECT-STATUS.md` (current status and
every measured number) · `2026-08-31-e2e-flakiness-management-strategy.md` (the
full design with alternatives considered).
