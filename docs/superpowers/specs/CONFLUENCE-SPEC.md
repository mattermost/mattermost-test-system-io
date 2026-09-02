# E2E Flakiness Management — Specification for Review

> **Status:** Ready for review · **Owner:** Yasser Khan · **Last updated:** 2 Sep 2026
> **Decision needed by:** Fri 4 Sep 2026 · **Reviewers:** @saturnino @eva @nuno
> **Code:** `mattermost-test-system-io` branch `feat/flakiness-management` (88 commits, **not yet pushed** — see §10)

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

**Coverage is 6–25%. The backlog grows by roughly 1.1–1.4 tests/day.**

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

A 48-hour review SLA is the single highest-value change available. It is a
process decision, not code — see decision **D5** in §9.

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
| Test suite | 127 unit + full e2e, lint clean |

### Not verified — stated plainly

| Gap | Why it matters |
|---|---|
| **The automated pipeline has never called a frontier model.** Round 7's verdicts were produced by Opus 5 applying the documented rules to real evidence packs *by hand*; no `ANTHROPIC_API_KEY` was available. | Closes the "only ever tested on a small local model" gap, but **n=7 and not blind** — the scenarios were authored by the same person who judged them. This measures the mechanism, not accuracy. |
| **No accuracy or calibration number exists.** Earlier rounds used a local 31B which was **60% correct while stating 0.90 confidence**. | The 0.85 confidence floor protects nothing against a model that says 0.9 on everything. This is precisely why the policy gate — not the model — owns every green. |
| **The screenshot path has never been exercised.** The rule written for the hard case needs a screenshot; the test data had none. | Two scenarios were decided from error text alone. |
| **The agent fix loop has never run end to end.** The six bans pass, but no fix PR has been opened. | §4.3 is designed, not demonstrated. |

**What this means for approval.** Phase 0 is shadow mode: nothing flips,
everything observes. The risk of starting is bounded by construction, and it is
the only way to obtain the two things every remaining question needs —
production-model verdicts and failures that carry screenshots. **Do not promote
past Phase 0 until those numbers exist.**

---

## 7. Rollout

| Phase | What gates | Exit criteria |
|---|---|---|
| **0 — Shadow** (4 weeks) | Nothing. Observes and comments only. | Production-model verdicts on screenshot-bearing failures; zero false greens measured |
| **1 — PR gate** (2 weeks min) | PR checks may go green on waived flakes | Blind-audit agreement holds |
| **2 — Master** (2 weeks min) | Master checks may go green on confirmed flakes | Agreement holds |
| **3 — Loop** | Agent fix PRs begin | — |

Minimum **8 weeks** from the start of shadow mode. Phase drops happen
automatically if blind-audit agreement slips.

**Blocking Phase 0** — one decision, not code: turn on **screenshot upload for
failing specs** and wire the **production API key** to the triage job. Without
both, Phase 0 produces another four weeks of unmeasurable data.

---

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
| **D2** | Start Phase 0 shadow mode | Yes | @saturnino |
| **D3** | Named weekly test-infra rotation at the budget in §8 | Yes | @eva |
| **D4** | Turn on screenshot upload + wire the production API key | Yes — **blocks Phase 0** | @nuno |
| **D5** | **48-hour stabilization review SLA** (+55% drain, §4.4) | Yes | @eva |
| **D6** | Chronic flakes green bystander PRs; the forcing function is master red + the queue, not PR red | Yes | @saturnino |
| **D7** | Add a CODEOWNERS `e2e-tests/**` entry | Yes | @eva |

> **D6 deserves a real look.** It reverses an earlier decision. A test failing
> more than 10% of the time on master currently turns **every** PR red,
> including PRs whose authors had nothing to do with it — which made the
> system's primary promise unreachable. The fix moves that pressure to master,
> where the fix is owned. The trade: a chronic flake stops blocking PRs, so the
> only thing driving it to be fixed is master red plus its queue position. If
> you would rather chronic flakes keep PRs red, say so and we revert one commit.

---

## 10. Delivery status

> ### ⚠️ The master-side work is not in any open PR yet
> The three open PRs are the **PR-triage MVP** — the earlier, smaller half:
>
> | PR | Scope | Branch |
> |---|---|---|
> | [tsio#101](https://github.com/mattermost/mattermost-test-system-io/pull/101) | Per-test history, flakiness, verdict ledger | `claude/e2e-ai-triage-api` |
> | [mattermost#38154](https://github.com/mattermost/mattermost/pull/38154) | PR-side triage workflow | `e2e/ai-flake-triage-demo` |
> | [toolkit#3](https://github.com/mattermost/mattermost-test-automation-toolkit/pull/3) | Reusable adjudication workflow | `claude/e2e-ai-triage` |
>
> Everything in §4 and §5 of this document lives in **35 further commits** on
> `feat/flakiness-management`, which is **local-only and unpushed**. It needs its
> own PR, stacked on tsio#101.

### Still to build

| Item | Repo | Blocked on |
|---|---|---|
| MAIN triage job, W10 ban workflow, W9 flag passing, release-cut guard (workflow half) | toolkit + mattermost | tsio PR review |
| CODEOWNERS `e2e-tests/**` entry | mattermost | D7 |
| Locating the existing 09:00 spot check to retire it | — | @eva |

### Reproducing the demo

```bash
bash docs/superpowers/specs/r7-demo/demo.sh
```

Builds a throwaway database, migrates it to head, seeds seven scenarios, starts
the real server, and drives the real endpoints and the real policy layer.
`--keep` leaves it running; `--cleanup` tears it down.

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
