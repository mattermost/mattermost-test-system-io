# E2E triage baseline

The "before" measurement. Run it **before** any triage behaviour changes an
outcome — once a system starts greening checks, the state it was meant to
improve on no longer exists, and every claim about whether it helped becomes
unfalsifiable.

```bash
psql "$TSIO_DATABASE_URL" \
  -v repo=mattermost/mattermost -v days=90 \
  -f scripts/baseline/baseline.sql
```

Variables: `repo` (default `mattermost/mattermost`), `days` (90), `branch`
(`master`), `min_runs` (5 — the floor below which a baseline is called thin).

Read-only. It creates two `TEMP VIEW`s and one `pg_temp` function, all of which
vanish with the session. Nothing is written.

**Record the output with the window and this file's git SHA.** The "after" must
be produced by the same file or it is not a comparison — which is the whole
reason this is a committed script rather than a set of queries someone pasted
into psql once.

---

## What it measures

| Section | Number | Sound? |
|---|---|---|
| 0, 0b | Coverage — groups, runs, tests, date span, per framework | yes |
| 1 | Share of PR blocks caused by non-PR failures (target < 5%) | yes |
| 2 | Master `run_failure_rate` per suite | yes |
| 2 | Master `flake_rate` per suite | **Playwright only** — see below |
| G | Report groups a PR run touches (distribution) | yes |
| h | Hard breaks vs intermittent among currently-failing master tests | yes |
| cadence | Master runs per day | yes |
| arrival | Tests newly going non-clean on master, per day | partial — see below |

Every number is **measured**. Nothing is projected, and no number should be
quoted without its denominator from section 0.

---

## The Cypress gap

**`flake_rate` for Cypress is not merely uncounted on historical data — the
evidence was never written, and no query can recover it.**

Before commit `5fc97ba`, the two parsers stored a retry-survivor (one attempt
failed, the retry passed) differently:

| Framework | Stored as | Recoverable |
|---|---|---|
| Playwright | every attempt stamped `flaky` | **yes** |
| Cypress | one row, `status='passed'`, `retry_count=0` | **no** |
| Detox, Maestro | single attempt, no retry concept | n/a — `~0` is true |

The bug that made Playwright's data *wrong* is what makes its flake rate
*recoverable*. Cypress's discarded the evidence entirely. `consolidate.go` is
the only writer of `test_cases` — the orchestration path has its own Cypress
parser that does read `attempts`, but it never reaches that table — so there is
no second source to recover from.

### The trap this sets

After `5fc97ba`, Cypress `flake_rate` rises from a structural `~0` to its true
value. Anyone comparing before to after will see the Cypress flake rate appear
to **explode**, and the obvious reading — "the triage work made things worse" —
is exactly backwards. It is the measurement starting to work.

Write that down before the first comparison, not after somebody raises it in a
review.

### What to do instead

- Compare Cypress `flake_rate` only across windows that both start **after**
  `5fc97ba` is deployed. There is no valid before/after for it.
- Use `run_failure_rate` for Cypress trend work in the meantime. It is sound on
  historical data: a run where every attempt failed was stored as `failed`
  either way.
- Treat the first post-fix Cypress `flake_rate` as a **new baseline**, not as a
  delta.

The `arrival` section inherits the same gap: a Cypress test whose only
instability was retry-survival never registers as arriving at all.

---

## Notes on the method

**Rates are over runs, never attempts.** A test's rows inside one report group
are its attempts, and both CI configs retry once. Retried attempts share the
leaked state or slow container that failed the first one, so they are not
independent draws — counting them would inflate both the sample size and the
failure rate, worst for exactly the tests these numbers are about. Every query
rolls rows up to one outcome per (test, report group) first.

**Number 1 looks only backwards.** For each failing test in a PR run, it
consults the K master runs of that test that happened *before* the PR run.
Using master runs from after the PR would let hindsight clear a pull request
that genuinely broke something — the single most flattering way to get this
number wrong.

**Unknown does not clear a run.** A test with fewer than `min_runs` master runs
before the PR is thin, not innocent. A run containing one counts as *not*
cleared, matching the rule that thin evidence is a reason to look rather than to
pass. The residue table breaks the uncleared runs into "had a spotless test"
versus "only thin baselines", because those need different work: the first is
the reproduction path, the second is a coverage problem.

**K sensitivity is reported, not a single K.** K is a policy choice. If the
answer moves sharply with it, that is itself the finding and no single number
should be quoted.

**The baseline excludes pull-request runs** by `gh_pr_number IS NULL` as well as
by branch. A pull request can be opened against a branch named `master` in a
fork, and a baseline mixed with other people's unmerged changes is not a
baseline.

**No new columns are read.** Nothing here touches `test_cases.attempts`,
`attempts_failed` or `run_failed`: those are NULL for every row written before
`5fc97ba`, so a query using them would silently report the window since the fix
rather than the window asked for.

---

## What G is for

`G` is fan-out — how many report groups a PR CI run touches. It decides whether
a better classifier can move number 1 at all.

If a PR run touches many groups, the chance that at least one carries a failure
the rule cannot clear grows with it, and the binding constraint is fan-out or
master's own pass-rate rather than the verdict. In that case the next work is
impact-based selection — running fewer, better-chosen specs — not a sharper
rule. Read G before deciding what to build next.

---

## Verification

The SQL was validated against PostgreSQL 18.3 with the real migrations applied
and seeded data covering each case it measures: a permanently-broken master
test, an intermittent one, a clean one, and one spotless on master that fails
on half the pull requests. Number 1 returned exactly 50% at every K, which is
the planted answer — 6 of 12 PR runs contain the spotless-on-master failure.

That proves the queries run and compute what they claim. It says nothing about
the production numbers, which nobody has seen: there is no production or
staging database reachable from a development checkout, which is why this ships
as a script for CI to run rather than as a committed result.
