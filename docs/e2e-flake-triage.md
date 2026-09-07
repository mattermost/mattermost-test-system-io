# E2E flake triage

This document is written in two tenses, and the distinction is the point.

- **Exists** — the code is on this branch and a named test covers it. Every
  test named here is given as `file` → `function`, so the claim can be checked
  in one grep.
- **Proposed** — designed, argued for, not built. No endpoint, no table, no
  test. Anything under a *Proposed* heading is a plan, not a capability.

An earlier version of this document described the proposed half in the present
tense and named three tests that were never written. Everything below that says
"exists" has been checked against the branch.

Numbers are labelled **measured** or **modelled**. A modelled number is
arithmetic over assumptions, and is not evidence of anything.

---

## 1. What this is for

**A developer cannot tell whether a red E2E check is their fault.** Master's
raw pass-rate is **88.40%** (measured), so most pull requests meet a failure
that has nothing to do with them. The rational response is to re-run until
green, which trains everyone to ignore E2E results — and that is how a real
regression ships.

**Flaky tests are never actually fixed.** New ones arrive at **1.5/day**
(measured).

Test System IO is a **data plane**. It stores what happened to every test and
answers questions about it. It forms no opinion about a failure. Whether a
failure is flaky, a regression, or caused by the pull request is decided by a
consumer — which can build and run the product and prove its answer. This
repository contains no policy layer, no model call, and no endpoint that sets a
commit status.

---

## 2. Exists: what is stored

### 2.1 Test identity — `stable_key`

Every `test_cases` row carries a `stable_key`: the identity a history lookup is
keyed on. It is the MM-T case id where the repository uses them, and the test's
full title where it does not (mattermost/desktop and mattermost-mobile carry no
ids). Two prefixes disambiguate it:

| Prefix | Source | Why |
|---|---|---|
| `project` | Playwright's `projectName` | The same title under chrome and firefox is two independent series. Merged, a firefox-only regression reads as a flake because chrome keeps passing. |
| `file` | `suites.file` | Cypress, Detox and Maestro build `full_title` from the describe/it chain with no file in it, so the same test name in two spec files collapses to one key. |

The key is computed by a `BEFORE INSERT OR UPDATE` trigger
(`migrations/000028_test_cases_stable_key.up.sql`), so there is one definition
of a test's identity rather than one in Go and one in SQL that can drift.

Covered by:

- `tests/e2e/testhistory/project_disambiguation_e2e_test.go` →
  `TestStableKey_DisambiguatesIdenticalTitlesAcrossProjects`
- `tests/e2e/testhistory/project_disambiguation_e2e_test.go` →
  `TestStableKey_FrameworksWithoutProjectsKeepTheUnprefixedKey`
- `tests/e2e/testhistory/stable_key_disambiguation_e2e_test.go` →
  `TestStableKey_DisambiguatesIdenticalTitlesAcrossFiles`
- `tests/e2e/testhistory/ingestion_e2e_test.go` →
  `TestIngestion_WritesTheIdentityHistoryIsKeyedOn`

### 2.2 Retry semantics — the one shape both frameworks produce

Both CI configs retry once:

| Config | Setting |
|---|---|
| `e2e-tests/playwright/playwright.config.ts` | `retries: testConfig.isCI ? 1 : 0` |
| `e2e-tests/cypress/cypress.config.ts` | `retries: { runMode: 1 }` |

So a reported failure in CI is **two attempts out of two**, and a "flake" is a
run where one attempt survived. This has a consequence the rest of the document
depends on, and §4 works through it.

Each attempt is stored as its own row carrying its own status, alongside a
run-level rollup repeated across the attempts of that run:

| Column | Meaning |
|---|---|
| `attempts` | attempts this test got in this run |
| `attempts_failed` | how many failed (`timedOut` and `interrupted` count; `skipped` does not) |
| `run_failed` | true **iff every attempt failed** |

`run_failed` is the only failure signal that means the same thing in both
frameworks. **Every rate is computed over runs, never over attempts**: a
retried attempt shares the leaked state or the slow container that failed the
first one, so a run's attempts are not independent draws, and counting them as
such overstates both the sample size and the failure rate.

This is new. Before it, the two frameworks stored different things for the same
run — Playwright stamped every attempt with the test's rolled-up `flaky`
status, and Cypress stored one final-state row with `retry_count` hardcoded to
0, so a retry-survivor rolled up as a **clean pass**. Any rate computed across
both was comparing a per-run number with a per-attempt one.

Covered by:

- `internal/ingest/retry_semantics_test.go` →
  `TestRetrySemantics_BothParsersAgreeOnOneFailOnePass`
- `internal/ingest/retry_semantics_test.go` →
  `TestRetrySemantics_PlaywrightKeepsPerAttemptTruth`
- `internal/ingest/retry_semantics_test.go` →
  `TestRetrySemantics_CypressKeepsPerAttemptTruth`
- `internal/ingest/retry_semantics_test.go` →
  `TestRetrySemantics_RunFailedOnlyWhenEveryAttemptFailed`
- `tests/e2e/testhistory/retry_parity_e2e_test.go` →
  `TestRetryParity_PlaywrightAndCypressStoreTheSameRun`
- `tests/e2e/testhistory/retry_parity_e2e_test.go` →
  `TestRetryParity_BothRollUpAsFlakyNotPassed`
- `tests/e2e/testhistory/retry_parity_e2e_test.go` →
  `TestRetryParity_ARunWhereEveryAttemptFailedIsAFailure`

### 2.3 The per-run rollup

A test's rows are rolled up to one outcome per report group, in two levels
(`internal/api/testhistory/handlers.go`, `groupRollupSQL`): within a shard,
rows are a run's attempts; across shards, they are separate runs. A test that
both passed and failed within a group is **flaky**, not failed.

Covered by:

- `tests/e2e/testhistory/flaky_rollup_e2e_test.go` →
  `TestHistory_ARetrySurvivorRollsUpAsFlakyNotPassed`
- `internal/api/testhistory/summarize_test.go` → `TestSummarize_*` (nine cases,
  including the empty series, an unbroken failure streak that reaches the start
  of the window, and skipped runs being excluded from the denominator)

---

## 3. Exists: the API surface

Three endpoints answer triage questions. All are public reads with no
credential.

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/reports/consolidated` | The failing specs for a run (pre-existing) |
| `GET /api/v1/tests/history` | One test's outcomes across commits, plus a computed summary |
| `GET /api/v1/tests/evidence` | One run's failures — error, stack, screenshots — clustered by normalized error |

That is the whole triage surface. `/tests/history` returns, per run:
`outcome`, `attempts`, `attempts_failed`, `run_failed`, and a summary carrying
`failure_rate`, `flake_rate`, `flips`, `last_pass_commit` and
`failing_since_commit`.

`failing_since_commit` needs one warning: when the failing streak reaches the
oldest run in the window, it names the oldest commit the window *has*, which is
a floor on the answer and not the answer. `last_pass_commit` being present is
what makes the pair a bisectable range. See
`internal/api/testhistory/summarize_test.go` →
`TestSummarize_StreakReachingTheStartOfTheWindow`.

### 3.1 Proposed: the rest of the API surface

None of the following exists. There is no `internal/api/triage` package, no
`/triage/*` route in `internal/server/server.go`, and no occurrence of "triage"
in `api/openapi.yaml`.

| Proposed endpoint | Purpose |
|---|---|
| `GET /triage/attribution` | Is this the PR's fault (see §4) |
| `GET /tests/flakiness` | The flakiness leaderboard |
| `GET /tests/failing-elsewhere` | Is this failing on other branches right now |
| `GET /triage/pass-rates` | Raw and effective pass-rates |
| `GET /triage/queue` | The fix queue, ranked by blast radius |
| `GET /triage/quarantine` | The quarantine list and its ages (see §7) |

Nothing above should be built before it has a caller that exists, and no field
should be added before it has a reader.

Deliberately **not** proposed, and not to be built: any endpoint that sets a
commit status. Setting a check is the consumer's action, taken with its own
credentials, on its own evidence. Moving it here would make this repository the
thing that greens a build, which is exactly the authority a data plane should
not hold.

---

## 4. Proposed: the attribution decision

**Status: proposed.** Nothing below is implemented.

### 4.1 Why the previous rule does not survive retries

The previous version of this rule required, for a `KNOWN_FLAKE` verdict, that
`p = P(at least k failures | baseline rate) >= 0.10`, guarded by a baseline
failure rate of at least 5% and at least 5 baseline runs.

With retries on, a reported failure is 2-of-2, so `p = r²` for a per-attempt
failure rate `r`. Requiring `p >= 0.10` therefore requires:

```
r² ≥ 0.10   →   r ≥ 0.316
```

A test must fail **more than 31.6% of attempts** to be dismissible as a known
flake. Almost nothing clears that. Everything else falls through to
`NEEDS_REPRODUCTION` — the expensive path, a server build plus repeated runs —
which is the opposite of what the rule was for.

The guards are broken in a second, quieter way. At a single observation,
`P(at least 1 failure) = r` exactly, so `p ≥ 0.10` *is* `r ≥ 0.10`. The
documented `failure_rate >= 0.05` floor can never bind: it is dead code, and
the real floor was always 10%.

Neither problem is a tuning error. Both come from applying a significance test
to a quantity that was never a sample of independent draws.

### 4.2 The v1 rule: no statistics

For v1, no statistics at all. One lookup against the master baseline, three
outcomes:

| Outcome | When |
|---|---|
| `not_pr_caused` | This `stable_key` failed on the master baseline within the last K master runs |
| `pr_suspect` | Spotless on master across the window, failing here |
| `unknown` | Thin baseline, or the key is absent from the baseline |

Four constraints on any implementation of this, each of which is a way the
previous design could have granted a green it should not have:

1. **The baseline is master, and only master.** A key is resolved against the
   master baseline. A key that is not already in that baseline is `unknown` —
   never `not_pr_caused`, never "master is broken". A `stable_key` is derived
   from a test's title and file, both of which a pull request controls. If an
   unrecognised key could reach any branch that grants a green, then adding a
   test named after a known-flaky one would select a green check.

2. **`pr_suspect` is evaluated first.** It is decided before any branch that
   can grant a green, so nothing below can reach past it.

3. **`unknown` is not a green.** Thin evidence is a reason to look, not a
   reason to pass.

4. **Nothing greens without a stored record of why, written before the check is
   touched.** If that write fails, the check stays red. A green whose reasoning
   was never recorded is indistinguishable from a bug.

### 4.3 Proposed: what statistics must look like if they return

Not before there is a labelled dataset to tune against. When there is:

- **Rates over runs, not attempts.** Per §2.2. An attempt-level rate double-counts
  the shared cause of a retried failure.
- **A lower bound, not a point estimate.** A Wilson or Jeffreys lower bound on
  the failure rate, so a test with 1 failure in 3 runs cannot present a 33%
  rate as though it were established.
- **A staleness guard on the master-broken branch.** If the baseline
  observation is older than `max(2 × master cadence, 4h)`, the branch is
  unassertable. Master being broken four hours ago is not evidence that it is
  broken now, and treating it as such greens a PR against a master that has
  since been fixed.

---

## 5. Proposed: the master fix loop

**Status: proposed. Nothing has run end to end.** No fix pull request has been
opened, by this system or any prototype of it. The agent that would do the work
does not run in this repository, and the prompt describing it is not in this
repository either.

The intended shape: rank flaky tests by blast radius, attempt a fix, prove it by
running the test repeatedly, and open a pull request a human reviews. Three
constraints, none of them implemented:

- **A test is never edited to make a product bug pass.** When the failure is a
  real defect, the loop files a ticket and names the introducing commit; it does
  not touch the test. The list of tests that keep catching real bugs is the
  opposite of the flakiness leaderboard and must never be confused with it.
- **A fix must be proven by running, not by reasoning.** Repeated green runs,
  against a server built from the commit under test.
- **Bounded attempts.** A test that resists repeated attempts goes to a person
  with a record of what was tried, rather than being retried forever.

Ticket state is owned by the tracker, not mirrored here. A mirrored copy goes
stale the moment somebody closes a ticket, and a regression could then never be
escalated again — silently.

---

## 6. Proposed: why this would not be the old known-flaky list

**Status: proposed.** The three mechanisms below are the argument for why a
waiver system would not decay into the list it replaces. **None of them is
built, and none of the tests named in the previous version of this document
exists** — `TestLedger_AWaiverNeverMovesTheRawPassRate`,
`TestLedger_EveryWaiverCarriesItsEvidenceAndItsAuthor` and
`TestFixAttempts_HandOverToAHumanAfterRepeatedFailure` were named as though
they covered these guarantees. Grep returns nothing for any of them, nor for
`waiver`, `ledger`, `handover` or `needs_human` anywhere in the repository.

Each mechanism, if built, needs a test that fails when the guarantee stops
holding. Until such a test exists and is named here by file and function, the
guarantee is a claim.

1. **A waiver cannot move the number the team is judged by.** Raw pass-rates
   are computed from run outcomes; waivers live in a separate column and are
   reported separately.
2. **Nothing greens without a record.** The verdict, carrying its evidence and
   its author, is written *before* the check is touched. If the write fails,
   the check stays red.
3. **A test cannot be attempted forever.** Repeated failed attempts hand it to
   a person, with what was tried each time.

And the failure that mattered most in the previous attempt is designed out: a
green never hides a suspected regression, because `pr_suspect` is decided
before any branch that can grant one (§4.2).

---

## 7. Proposed: bounded quarantine

**Status: proposed.** No quarantine column, table, endpoint or list exists.

A test above a failure-rate threshold on master moves out of the blocking set
into a non-blocking lane. It still runs and still reports; it just does not fail
the build. This is the honest version of what teams do anyway when they start
ignoring E2E results — with the ignoring made explicit, counted, and expiring.

Quarantine is a waiver by another name, so it is constrained by the same
reasoning as §6:

- **A ticket and a CODEOWNERS owner.** A quarantined test with no owner is an
  abandoned test.
- **A hard expiry, after which the test is deleted rather than restored.** This
  is the load-bearing constraint. An expiry that restores the test to blocking
  produces a red build nobody asked for and a second quarantine within the day.
  Deletion forces the decision the quarantine deferred: either somebody owns
  this test, or the coverage was not worth its cost.
- **A cap on list size.** The N+1th test is *refused* until one leaves. Without
  a cap, quarantine has no back-pressure and grows to fit whatever is broken —
  which is exactly how the old known-flaky list got its length.
- **A separate column, so the raw pass-rate is unaffected.** Quarantining a
  test must not be able to improve the number the team is judged by. If it can,
  the incentive points at quarantining rather than fixing.
- **Count and age on the same dashboard as the pass-rate.** A pass-rate shown
  without the quarantine count beside it is not a measurement of health, and a
  quarantine list whose age distribution is not visible has no expiry in
  practice regardless of what the policy says.

The threshold that admits a test is a policy number, and there is no labelled
data to set it from yet (§8). Until there is, any number written here would be
modelled at best.

---

## 8. What is not proven

Stated plainly, because the previous version of this document was too
confident. This section is longer than it was, not shorter.

**Never run end to end.** No triage agent has run against this data plane. No
fix pull request has been opened. No check has been greened or left red by any
of this. The reads in §3 are exercised by tests, not by a consumer in
production.

**No accuracy number exists, and no instrument to produce one exists.** The
previous version said accuracy "reports zero until verdicts accumulate", which
implied an endpoint that would report it. There is no such endpoint and no
verdict store. Until verdicts are recorded and compared against outcomes, the
accuracy of any rule in §4 is unknown — including the v1 rule proposed here.

**No labelled dataset exists.** Nothing separates, in stored form, a failure
that was the PR's fault from one that was not. Every threshold in §4 and §7 is
therefore unvalidated, which is the reason §4.2 proposes no statistics at all:
there is nothing to tune against, and a statistical rule tuned against nothing
is a guess with error bars drawn on it.

**Single-commit author attribution succeeds 16.0% of the time** (measured, on
production data). Expect CODEOWNERS routing to be the common path, and never
name an author on a range that could not be narrowed to one commit.

**No defect has been escalated.** No integration with a ticket tracker exists in
this repository.

**The 88.40% pass-rate and the 1.5 new flakes/day are measured; nothing derived
from them here is.** No claim is made about how many pull requests the proposed
rule would green, how much reproduction cost it would avoid, or how far the
pass-rate would move. Those numbers would be modelled, and modelling them
against no labelled data would produce a number with the shape of evidence and
none of the substance.

**The retry semantics fix does not repair stored history.** Rows ingested before
it cannot be reconstructed: a Cypress retry-survivor from before the change
stored a single `passed` row, and the attempt that would prove otherwise was
never written. Rates over older windows understate flakiness for Cypress by an
amount nobody has measured.

**Migration 28's cost was not measured against production.** The migration is
written so its cost does not depend on the row count — every statement in it is
catalog-only — precisely because that count could not be obtained from a
development checkout. The row-count-proportional work (backfill, index build)
runs out-of-band via `tsioctl db backfill-stable-key`, which prints the count it
is working through. Until that command is run against production, the real size
of this table is still unknown.

---

## 9. Setup

| What | Where | Why |
|---|---|---|
| `tsioctl db backfill-stable-key` | Run once after deploying migration 28 | Populates `stable_key` on pre-existing rows and builds its index concurrently. Until it runs, older rows have no history. |
| Screenshot upload for failing specs | mattermost CI | The screenshot is decisive for timeout and visibility failures |
| CODEOWNERS `e2e-tests/**` | mattermost | Routing for product bugs a fix loop would not fix (§5) |
