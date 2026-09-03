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
- **Three alert rules**, deduplicated: one channel post per 24h per subject,
  one GitHub issue opened per story and then updated in place.
  - `pass_rate_drop_24h` — today against the median of the 7 days before it.
    "Something landed."
  - `pass_rate_floor` — an absolute floor. Off unless a floor is configured.
  - `new_failing_streak` — a test that has newly failed 3+ consecutive master
    runs. This is the arrival detector, and the only per-test rule.
- Replaces the manual 09:00 spot check.

Two rules were cut as redundant rather than wrong. `pass_rate_trend_7d` was
`pass_rate_drop_24h` over a longer window, firing the same severity on the same
subject, and the response to both is the same: look at the queue.
`cross_pr_cluster` fired when a test broke 3+ distinct PRs — which is exactly
what puts it at the top of §4.2, so it announced per-test what the ranked queue
already says. Every alert that fires has to be worth a human turning to look;
two ways of saying one thing trains people to stop looking.

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

### 4.4 Can the loop keep up?

| Input | Value |
|---|---|
| Arrival | **1.5 new flaky tests/day** (measured) |
| Re-measurement window | one job, not 7 days — see below |
| Review latency | up to 7 days on a weekly rotation |
| Concurrency cap | **5** (review capacity, not agent capacity) |

Two things decide this, and only two: how long a fix waits for review, and how
long it takes to prove the fix worked.

**The re-measurement window is not 7 days.** The naive way to collect 20 samples
of a test is to wait for 20 natural master runs, which takes a week. Re-run the
one test 20 times with `--grep` on its `MM-T` id and 20 samples take a single
job. It is also a *better* measurement: 20 consecutive executions at one commit
isolate that test's own flakiness, whereas 20 master runs spread over a week
confound it with everything else that landed. That workflow is built and
shipped (§10).

**Review latency is the other one**, and it is the only input a decision can
change: attempts-per-fix is a property of the tests, and concurrency is capped
by review capacity. A weekly rotation puts a fix a week behind its own
re-measurement; a 48-hour SLA (**D5**) removes that wait.

Both are required. Targeted re-measurement on a weekly review cadence still
leaves the queue growing, because the fix sits waiting longer than it took to
prove.

**We are not modelling this any further.** An earlier draft carried a queueing
model with a `/triage/stabilization/throughput` endpoint that computed a drain
rate against arrival and declared whether the loop kept up. It has been removed:
it affected no verdict, it was a spreadsheet served over HTTP, and pinning its
output as a test proved the arithmetic rather than the world. The honest
statement is the one above — two levers, both needed — and the real number
arrives from running the loop, not from predicting it.

**What this means for PR-side waivers.** Whether or not the loop keeps up, "fix
master and PR pain goes away" is right in direction — a fix removes a recurring
source of noise permanently, where a waiver only mitigates one occurrence — but
the queue is long and the arrival rate is real. **PR-side waivers are
load-bearing for the foreseeable future, not a two-week bridge.**

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
gating is turned off at the workflow variable — a change a human makes and can
see, not an automatic demotion buried in server state.

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
| Master never waives a master regression | Automated test, both modes |
| Release branches waive nothing | Automated test |
| Six stabilization bans | 14/14 |
| Test suite | 154 action unit tests, 32 Go triage unit tests, 17 triage e2e tests against a real server, golangci-lint 0. **Caveat:** `internal/config` has 4 failures on a developer machine — `loadDotenv()` picks up the local `.env`, so `TSIO_DATABASE_URL`/`TSIO_S3_*` leak into the assertions. Pre-existing, unrelated to triage, and green in CI. Non-hermetic tests are still a real if minor defect. |

### Not verified — stated plainly

| Gap | Why it matters |
|---|---|
| **The pipeline HAS now called a frontier model — once.** mattermost#38154 run 33678302436 invoked `claude-sonnet-4-6` in CI on 4 real failures across 3 clusters. | Supersedes the earlier "never called" line. But **n=3 clusters, all on tests with `runs=0`**, none independently confirmed, and nothing gated (the run was in shadow mode). Still no accuracy number. Note the `runs=0` clusters would now be refused by the 3-run history floor regardless. |
| **No accuracy or calibration number exists.** Earlier rounds used a local 31B which was **60% correct while stating 0.90 confidence**. | The 0.85 confidence floor protects nothing against a model that says 0.9 on everything. This is precisely why the policy gate — not the model — owns every green. |
| **The screenshot path HAS now fired.** On the same run, 2 of 3 clusters cited screenshots and one reasoned from image content ("the Members panel is fully rendered… but the visible button is labelled 'Add'"). | Supersedes the earlier "never exercised" line — this was the single biggest measurement gap in rounds 4–6. Still only 3 clusters, and none of the verdicts is independently confirmed. |
| **The agent fix loop has never run end to end.** The six bans pass, but no fix PR has been opened. | §4.3 is designed, not demonstrated. |

**What this means for approval.** Step 1 of §7 — merging tsio#101 and leaving
mattermost#38154 unmerged — flips nothing on any PR and still populates history
retroactively from traffic TSIO already receives. The risk of starting is
bounded by construction, and it is the only way to obtain the two things every
remaining question needs — production-model verdicts and failures that carry
screenshots. **Do not merge the mattermost half until those numbers exist.**

---

## 7. Rollout

**There is no phase ladder any more.** An earlier draft carried one as server
state — a `/triage/phase` value from 0 to 3 that capped what each run type
could gate, with automatic demotion when blind-audit agreement slipped. It has
been removed. What it encoded is already expressed by which pull request is
merged, and a rollout stage that lives in two places (a database row and a
merge state) can disagree with itself.

The rollout is therefore a merge order:

| Step | What is merged | PR checks | Master checks |
|---|---|---|---|
| **1 — collect** | tsio#101 only | untouched (nothing calls the action) | untouched; alerting live, queue ranked |
| **2 — gate PRs** | \+ mattermost#38154 with `mode: gate` | **gate** — may green on a waived flake | red stays red |
| **3 — gate master** | \+ `run-type: MAIN` at `mode: gate` | gate | may green on a *confirmed* flake |
| **4 — loop** | \+ `E2E_STABILIZATION_LOOP=on` | gate | gate |

**Step 1 is the measurement window.** Test history is derived from the report
ingestion TSIO already receives, and migration `000027` backfills
`external_test_id` on apply — so baselines, failure rates and `failing_since`
are populated retroactively the moment the server ships, with no CI change in
the mattermost repo.

**History is not verdicts, though, and the number blocking step 2 is a verdict
number.** Nothing writes to the ledger while no CI job calls the action, so
step 1 on its own would produce a month of baselines and still no accuracy
figure. The **replay job** closes that: a scheduled workflow in this repo
(`triage-replay.yml`) walks failing runs TSIO has already ingested and puts each
through the same evidence pack, classifier, model and policy layer a live run
uses, recording real ledger rows.

Nothing it writes flips anything — it sets no commit status, posts no comment,
and no CI job reads those rows. Two things make it a measurement rather than a
rehearsal:

- it decides in **gate** mode, because the question is what the gate *would*
  have done; a shadow-mode replay would record `waived=false` everywhere;
- every row is marked `replay`, and `GET /triage/accuracy?source=replay` counts
  them separately from live. They are never averaged: a replay verdict is
  decided with later runs of the same test already in the database, so folding
  it into the live figure would overstate what CI does.

**This moves one decision.** D4's API key is needed on the **TSIO** side, not
the mattermost side, and it is needed at step 1 rather than step 2 — without it
replay records deterministic verdicts only, and the resulting figure is the
classifier's rather than the model's.

**Gating is owned by the calling workflow.** The action waives only in
`mode: gate`, an unrecognised mode fails closed to shadow, and the mattermost
templates read that input from a repo variable — so the kill switch is a
variable change, not a revert, and it does not need server state to exist.

**Why this is defensible rather than reckless once step 2 lands.** Every green
still has to pass five independent policy refusals that the AI cannot influence
(§3), a green is impossible without a ledger row, and `PR_REGRESSION` is never
waivable. The failure mode of a wrong verdict is one wrongly-green check on a
PR whose author can see the reasoning and the evidence — not a silent one, and
not a release.

**Expect master to look bad at step 3.** The raw pass-rate is 88.40% today, so
enforcing master will make it visibly red a lot. That is the point: it is
already broken, and the current arrangement just distributes the pain across
every PR author instead of showing it in one place. The blast-radius ranking
(§4.2) and quarantine (§4.5) exist so the queue is worked worst-first rather
than all at once.

### One decision still blocks the AI half

Wire the **production `ANTHROPIC_API_KEY`** — as a secret in **this** repo, for
the replay job — and turn on **screenshot upload for failing specs** in
mattermost (decision **D4**). Without the key the pipeline cannot call a
frontier model at all; without screenshots the rule written for the hard case
cannot fire. The deterministic half — rate-shift, config-delta, history — works
without either, and the replay job says so in its log when the key is absent so
a classifier-only number is never mistaken for the model's.

## 8. What it costs you

| | Effort | When |
|---|---|---|
| Blind waiver audit | **45 min/week** | From step 2 (once verdicts exist) |
| Stabilization PR review | **up to 2h/week** | From step 4 |

One named test-infra reviewer per week. **If the open-PR count exceeds the
budget, concurrency drops rather than review quality.** A reviewer may not audit
a waiver on a test whose stabilization PR they reviewed that week.

---

## 9. Decisions we need from you

Silence on a line = we proceed with the default.

| # | Decision | Default | Who |
|---|---|---|---|
| **D1** | Master checks may go green on confirmed flakes (step 3+) | Yes | @saturnino |
| **D2** | Merge tsio#101 now and hold mattermost#38154 until §6's accuracy number exists | Yes | @saturnino |
| **D3** | Named weekly test-infra rotation at the budget in §8 | Yes | @eva |
| **D4** | Wire `ANTHROPIC_API_KEY` in the **tsio** repo (replay) + turn on screenshot upload in mattermost | Yes — **blocks the measurement, so it blocks step 2** | @nuno |
| **D5** | **48-hour stabilization review SLA** (§4.4) | Yes | @eva |
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
| **Master health alerting** | tsio `triage-master-health.yml` | Every 2h. This is what retires the 09:00 spot check. Needs one secret. Three rules, not five (§4.1). |
| **Replay (the measurement job)** | tsio `triage-replay.yml` + action `task: replay` | Twice a day. Re-adjudicates ingested runs through the live policy layer and records `replay`-marked ledger rows. Flips nothing. This is what turns step 1 of §7 from a waiting period into a measurement. |
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
| Secret `ANTHROPIC_API_KEY` | **tsio repo** | The replay job — decision **D4**, and needed at step 1, not step 2. The job refuses to start without it rather than quietly measuring the classifier alone. |
| Variable `TSIO_REPLAY_REPO` | tsio repo | Optional; defaults to `mattermost/mattermost` |
| Screenshot upload | mattermost | The other half of **D4** — without it the hard-case rule cannot fire |
| Variable `E2E_STABILIZATION_LOOP=on` | mattermost | Enables the fix loop (step 4) |
| Confirm `@mattermost/test-infra` is the right handle | mattermost | CODEOWNERS routing |

### Still open, deliberately

| Item | Why it is not done |
|---|---|
| **Release-cut guard** | **Removed.** The TSIO half was built and callable, but the release automation it was meant to pause was never located — flagged since W0 and never resolved, so nothing ever called it. It is a few lines of SQL to reinstate once someone points at the release-cut job; carrying it meanwhile was carrying an endpoint with no consumer. |
| **`report-upload` action has no tests** | Pre-existing gap, found while fixing three other actions whose test globs were silently running nothing. Out of scope here; worth its own PR. |
| **The loop has never opened a fix PR** | It is wired, off by default, and dry on manual runs. First real run is a step-4 activity. |

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
| `GET /triage/replay/candidates` | Ingested failing runs with no verdict — the replay worklist |
| `GET /triage/accuracy` | Verdict accuracy and false-greens; `source=live` (default) or `replay` |

**Authenticated writes**

| Endpoint | Purpose |
|---|---|
| `POST /triage/verdicts` | Write ledger rows |
| `POST /triage/quarantine` | Quarantine a test (owner + deadline mandatory) |
| `POST /triage/quarantine/{id}/release` | End one early (reason mandatory) |
| `POST /triage/stabilization/promote` | Push a test up the queue |
| `POST /triage/audit/reviews` | Submit a blind audit call |

**Reference:** `docs/superpowers/specs/PROJECT-STATUS.md` (current status and
every measured number) · `2026-08-31-e2e-flakiness-management-strategy.md` (the
full design with alternatives considered).
