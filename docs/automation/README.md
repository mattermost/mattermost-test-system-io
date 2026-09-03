# E2E flake triage — what shipped

Test System IO is a **data plane**. It stores what happened to every test and
answers questions about it. It forms no opinion about a failure, with one
deliberate exception: `/triage/attribution`, which is arithmetic over stored
history rather than judgement.

The agent that classifies, fixes and verifies runs in **Cursor cloud
automation**, where it can build and run a Mattermost server and prove a fix by
running the test. That is why this repository no longer contains a CI action, a
policy layer, or a model call.

## The two problems, and what solves each

**A developer cannot tell whether a red E2E check is their fault.** Master's raw
pass-rate is 88.40%, so most pull requests meet a failure that has nothing to do
with them. The rational response is to re-run until green, which trains everyone
to ignore E2E results — and that is how a real regression ships.

→ `GET /triage/attribution` answers it in one request, and the PR Diagnoser
greens the check when the answer is `MASTER_BROKEN` or `KNOWN_FLAKE`.

**Flaky tests are never actually fixed.** New ones arrive at 1.5/day.

→ `GET /triage/queue` ranks them by blast radius, the Master Guardian attempts
the fix and proves it 3/3, and `POST /triage/attempts` remembers what failed so
the third failure hands the test to a person with notes.

**A failing test is sometimes a real bug, and editing it would hide one.**

→ The Guardian files a Jira defect instead of touching the test, and
`POST /triage/escalations` records that it did. `GET /triage/defects` is the
metric: which tests keep catching real bugs. That list is the opposite of the
flakiness leaderboard and must never be confused with it.

**Who owns what.** Jira owns whether a ticket is open — the Guardian dedups
against it with a `e2e-flake-<MM-T id>` label query, so a ticket closed this
afternoon stops suppressing an escalation this evening. Test System IO owns the
metric, as an append-only event log with no `resolved_at`. A mirrored copy of
ticket state would go stale the moment somebody closed one, and a regression
could then never be escalated again — silently.

## The attribution decision

Everything a check turns on. Evaluated in this order, and the order is
load-bearing — `PR_SUSPECT` is decided first so nothing below can reach past it.

| Outcome | When | `can_green` |
|---|---|---|
| `NO_FAILURE` | The observation carries no failures | yes — nothing to attribute |
| `PR_SUSPECT` | Clean on the baseline in the window, failing here | **never** |
| `MASTER_BROKEN` | The baseline is failing this test right now | yes — bystander |
| `KNOWN_FLAKE` | The failure count is what this test's own rate predicts | yes |
| `NEEDS_REPRODUCTION` | No baseline, thin history, too reliable to dismiss, or the arithmetic refuses | no |

Three guards keep `KNOWN_FLAKE` honest:

- **≥ 5 baseline runs.** Below that a rate is a guess.
- **≥ 5% baseline failure rate.** A test that works 98% of the time is not a
  flake anyone should be told to ignore.
- **p ≥ 0.10** for the observed failures under the baseline rate.

That last one is the case the previous design could not reach. A test that is 40%
flaky on master *and* failed 3 of 3 on a PR has p = 0.064, so "it flaked again"
does not explain the run and the check stays red — while the same test failing
1 of 3 has p = 0.784 and greens. One threshold separates them, with no model and
no hand-written special case.

## API surface

**Public reads** — no credential; CI and the agent call these on every failure.

| Endpoint | Purpose |
|---|---|
| `GET /reports/consolidated` | The failing specs for a run (pre-existing) |
| `GET /tests/history` | One test's outcomes across commits |
| `GET /tests/flakiness` | The flakiness leaderboard |
| `GET /tests/failing-elsewhere` | Is this failing on other branches right now |
| `GET /triage/attribution` | **Is this the PR's fault, and may the check go green** |
| `GET /triage/evidence` | One run's failures, clustered, with history and screenshots |
| `GET /triage/pass-rates` | Raw and effective pass-rates |
| `GET /triage/queue` | The fix queue, ranked by blast radius |
| `GET /triage/signature-issues` | Has anyone already filed this |
| `GET /triage/accuracy` | Verdict accuracy and the false-green count |
| `GET /triage/defects` | Product defects E2E surfaced, per test — the bug-catching metric |

**Authenticated writes**

| Endpoint | Purpose |
|---|---|
| `POST /triage/verdicts` | Record a decision — required before greening a check |
| `POST /triage/verdicts/{id}/correction` | A human overrules a verdict |
| `POST /triage/attempts` | What a fix attempt did, and its handover note |
| `POST /triage/escalations` | Record that a product defect was filed in Jira |
| `GET /triage/verdicts` | The ledger rows — authenticated, they name their author |

## Why this is not the old "known flaky" list

Three mechanisms, each with a test that fails if it stops being true.

1. **A waiver cannot move the number the team is judged by.** Raw pass-rates are
   computed from run outcomes; waivers are a separate column.
   `TestLedger_AWaiverNeverMovesTheRawPassRate`.
2. **Nothing greens without a record.** The Diagnoser writes a verdict carrying
   its evidence before it touches a check, and if that write fails the check
   stays red. `TestLedger_EveryWaiverCarriesItsEvidenceAndItsAuthor`.
3. **A test cannot be attempted forever.** Three failed attempts hand it to a
   person, with what was tried each time.
   `TestFixAttempts_HandOverToAHumanAfterRepeatedFailure`.

And the failure that mattered most in the previous attempt is designed out: a
green never hides a suspected regression, because `PR_SUSPECT` is decided before
any branch that can grant one.

## The prompts

- [`master-guardian.md`](./master-guardian.md) — fixes master, verifies 3/3, and
  for a product bug names the introducing commit and author instead of touching
  the test.
- [`pr-diagnoser.md`](./pr-diagnoser.md) — tells a developer whose fault it is,
  and greens the check when it is not theirs.

## Cost

The Diagnoser's expensive path is a server build plus three runs. It is only
taken for `PR_SUSPECT` and `NEEDS_REPRODUCTION`; `MASTER_BROKEN` and
`KNOWN_FLAKE` are settled by one HTTP request. The prompt's last output line
reports the ratio, so if reproduction starts approaching the number of failing
tests that is visible rather than silently expensive.

## What is not proven yet

Stated plainly, because the previous design's spec was too confident.

- **The agent has never run end to end.** No fix PR has been opened. The bounded
  failure mode — a closed PR, reviewed by a human, with six mechanical bans on
  the cheap ways to fake a fix — is why it is worth attempting before it is
  proven.
- **No accuracy number exists.** `GET /triage/accuracy` is the instrument; it
  reports zero until verdicts accumulate. The first honest number is the ratio of
  fix PRs opened to fix PRs merged, after two weeks of the Master Guardian.
- **Single-commit author attribution succeeds 16% of the time** on measured
  production data. The Guardian is instructed never to name an author on a range
  it could not narrow to one commit, so expect CODEOWNERS routing to be the
  common path.
- **No defect has been escalated yet.** The recording path is tested end to end;
  the Jira half depends on credentials the automation does not yet hold.

## Setup

| What | Where | Why |
|---|---|---|
| `TSIO_API_KEY` | Cursor automation secrets | The three writes. `tsioctl keys issue --name e2e-guardian` |
| Jira credentials | Cursor automation secrets | The Guardian files defects and dedups by label. **Without these, goal 2 does not work at all** — no API design compensates. |
| Screenshot upload for failing specs | mattermost CI | The screenshot is decisive for timeout and visibility failures |
| `e2e-stabilization-bans.yml` | mattermost | The mechanical guardrail on the agent's own PRs |
| CODEOWNERS `e2e-tests/**` | mattermost | Routing for product bugs the Guardian will not fix |
