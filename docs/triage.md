# Flaky test detection and CI quarantine

TSIO records test execution facts, computes trunk health per lane and base branch,
and explains which PR failures block CI. GitHub Actions publishes the result using
its workflow token. TSIO receives only its existing API authentication or a GitHub
OIDC token; never send a GitHub write token to the server.

## Deploy and backfill

1. Deploy migrations `000027`–`000034` and the server together. All new identity
   and observation writes share the ingest/completion transaction. Existing
   producers can omit the new metadata; inference preserves compatibility.
2. Backfill each repository. The command processes existing uploaded cases and
   orchestration attempts and can safely be restarted:

   ```sh
   tsioctl db backfill-identities --repository mattermost/mattermost-mobile --since 2026-09-01T00:00:00Z
   tsioctl db backfill-identities --repository mattermost/mattermost --since 2026-09-01T00:00:00Z
   ```

3. Inspect `/triage/health`. Check identity churn across two weeks, lane inference,
   and known failing tests before adding producer workflows. Renames intentionally
   create a new identity; shared MM-T IDs do not merge histories.
4. Review and apply the separate patches in `docs/triage-producer-patches/`.
   Deploy the TSIO action first. The patches reference the actions as
   `@TSIO_TRIAGE_RELEASE_SHA`, a deliberate placeholder that fails loudly at job
   start if left in place; substitute the TSIO release SHA that ships the triage
   action (see `docs/triage-producer-patches/README.md`) before opening the
   producer PRs. No producer checkout or remote repository is changed by
   generating these patches.

The worker subscribes to report completion, recovers missed events every minute,
and refreshes all known trunk lanes at startup and every 24 hours. Projection
refreshes and backfills are idempotent. Multiple server replicas serialize a
lane's refresh with a PostgreSQL advisory lock. Identity enrichment serializes
shared identity upserts per repository/framework to avoid opposite-order shard
upsert deadlocks; this adds transaction contention during simultaneous uploads.

## Policies and rollout

Every missing policy defaults to **shadow**. In shadow mode the action publishes
an explanatory check/comment and leaves required raw statuses authoritative.
No shared GitHub repository or organization variables are changed.

Policies are scoped to repository and existing required-status context. URL-encode
both path segments, including embedded slashes. A session with the existing admin
role can mutate policies. Operational API-key access additionally uses the
existing `X-Admin-Key`; ordinary ingest credentials cannot administer quarantine.
The following examples assume `TSIO_URL`, `TSIO_API_KEY`, and `TSIO_ADMIN_KEY` are
provided securely by the operator:

```sh
curl --fail-with-body -X PUT \
  "$TSIO_URL/api/v1/triage/policies/mattermost%2Fmattermost-mobile/e2e-test%2Fdetox-ios" \
  -H "X-API-Key: $TSIO_API_KEY" -H "X-Admin-Key: $TSIO_ADMIN_KEY" \
  -H 'Content-Type: application/json' \
  --data '{"mode":"shadow","thresholds":{}}'
```

Observe each context for two weeks. Compare raw failures and recorded human
overrides with the verdicts using `GET /api/v1/triage/verdicts?repository=...`.
Target less than 1% false exoneration and at least 70% coverage of failures humans
already waive. Human review supplies the ground truth; a success verdict alone
is not evidence of correctness. Enable `enforce` one context at a time, starting
with `e2e-test/detox-ios`, then `e2e-test/playwright-full/enterprise`.

Use the same PUT with `{"mode":"enforce","thresholds":{}}` to enforce. Omitted
threshold keys preserve the current values. The action then writes the existing
required context from the verdict. `NEUTRAL` is nonblocking only when the policy
explicitly chooses neutral for insufficient data. Infrastructure and incomplete
runs never become successful through statistical exoneration.

### Kill switch

PUT `{"mode":"off","thresholds":{}}` to the same policy URL. The action produces
outputs but skips GitHub publishing, and producer workflows use their raw result.
The worker continues maintaining evidence while suppressing new auto-quarantines
for an off policy. No GitHub variable change or new release is needed.

If broken/flaky identities exceed `max_exonerated_ratio` (default 0.25), the lane's
known enforced context automatically returns to shadow and emits
`triage.lane.alarm` on the existing event hub. Monitor this event along with worker
errors. Other contexts and policies already set to off are unchanged.

## Read a verdict

Open the action's `verdict-url` or `/triage/verdicts/<id>`. Blocking findings appear
first, followed by exonerated failures. Each finding shows the PR failure count,
trunk runs/failures/streak, error signature/locus evidence, and the reason for its
class. Follow the identity link for per-lane health and the execution timeline.
The page also includes the engine version, exact thresholds, confidence, and
recorded human override.

The engine evaluates infrastructure, PR ownership, quarantine, new tests,
insufficient evidence, broken trunk, divergent failures, confirmed flakes,
suspicious flakes, cross-PR flakes, and regression in that order.

`FLAKY_CROSS_PR` (threshold `cross_pr_min_prs`, default 3) is the evidence
trunk cannot provide: a test that passes on trunk but failed on at least three
*other* PRs in the window (and passed somewhere in the window) is flaky in the
PR environment, not broken by this PR. The backtest on production history
showed this is the dominant failure mode on mobile: tests with 9–15 clean
trunk runs that fail on many unrelated PRs (server health checks, timeouts,
element visibility). A failure signature that already recurs on two or more
other PRs is likewise not treated as a novel cluster. Set the threshold to 0
to disable the rule for a context. New error clusters and excess
failures in a spec can turn individual exonerations into regressions. Skipped
executions do not erase failures; recovered retries count as flakes. Empty or
uncaptured failed orchestration specs require action rather than giving a green
result. Infrastructure is excluded from trunk health.

The probability check is `((fails + flaky + 1) / (runs + 2)) ^ failing_executions`.
Built-in failed retries increase the exponent. Confidence measures evidence
sufficiency, not the flake probability: a confirmed flake carries
`runs / (runs + 1)` and a broken-trunk exoneration carries
`streak / (streak + 1)`; the verdict's confidence is the minimum over exonerated
findings and must reach `confidence_floor`. Trunk freshness is enforced only by
`stale_trunk_hours` (it no longer decays confidence), so a broken-trunk
exoneration stays valid across a weekend without trunk runs. A blocking
non-infrastructure finding always yields `FAILURE`, even when infrastructure
stubs are present; infrastructure alone yields `ACTION_REQUIRED`. Trunk runs are distinct completed
report groups, filtered by repository, framework, lane and base branch. Stale
trunk evidence (>72 hours by default) prevents exoneration. Incomplete groups
return HTTP 409 with a stored `INCOMPLETE` verdict; pending groups return HTTP 202.
Each individual long poll is capped at 60 seconds, while the action retries for
up to its configured overall deadline (default ten minutes).

Identical effective inputs reuse the audit row. Policy, evidence, quarantine,
engine version and time-dependent guard changes participate in the input hash.
Historical verdicts are retained. Bump `EngineVersion` whenever decision rules
change.

### Chronology and threshold constraints

ASSUMPTION: the merge-base SHA has a trunk run, or timestamp ordering provides a
usable conservative proxy. TSIO has no Git graph. A matching SHA is the pivot;
otherwise the newest trunk run older than the PR group's creation time is the
fallback. A newer passing trunk run ends a broken streak; a PR still showing that
failure receives the rebase hint rather than inheriting the old break.

Health uses the raw failure rate `(fails + flaky) / runs < 0.02` for `healthy`
(the Laplace-smoothed rate is at least `1/(runs+2)` and could never reach `0.02`
at the default 30-run cap, which made automatic release unattainable). The other
classes still use the smoothed rate, so 30 clean runs are `healthy` and one flake
in 30 is `flaky`. Automatic release requires `healthy` plus the configured
consecutive pass count. Uncovered ranges remain `unknown`.

Cluster detection treats a PR failure whose signature, locus, or wording already
matches trunk's dominant failure as a known failure; only findings with no such
match can form a novel-signature cluster.

## Backtesting against history (`as_of` replay)

`POST /api/v1/triage/verdicts` accepts an admin-only `as_of` timestamp. The
engine then sees only trunk observations, quarantine entries and freshness as
they were at that instant; the result is returned (with the nil UUID as `id`)
and never persisted, so backtests cannot pollute the audit trail. `as_of`
cannot be combined with `wait_for_completion_ms`.

Two tools use it:

```sh
# 1. Seed a database from another deployment's public read API (no DB/S3 access
#    needed). Keeps original ids and timestamps; safe to re-run.
tsioctl db import-remote --base-url https://test-io.test.mattermost.com/api/v1 \
  --repository mattermost/mattermost-mobile --frameworks detox,maestro \
  --since 2026-07-18T00:00:00Z

# 2. Replay every PR run through the engine and compare with GitHub ground truth
#    (verified/override label events, later runs of the same PR, merge base and
#    changed files via `gh`).
TSIO_API_KEY=... TSIO_ADMIN_KEY=... scripts/triage_backtest.py \
  --base-url http://localhost:8080/api/v1 --repository mattermost/mattermost-mobile \
  --since 2026-07-18T00:00:00Z --out backtest-mobile.md
```

The report buckets each failing PR run as WAIVED (a maintainer applied the
verified-flaky label while that run was current), RERUN_PASSED (same commit
passed on a rerun), FIXED_BY_AUTHOR (a later commit passed: most likely a real
regression), OVERRIDDEN (blanket `E2E/Override`, no evidence either way) or
UNRESOLVED. WAIVED and RERUN_PASSED runs should receive SUCCESS;
FIXED_BY_AUTHOR runs must receive FAILURE. The false-exoneration count on
FIXED_BY_AUTHOR runs is the number that gates `enforce`.

Caveats of imported history: `full_title` is rebuilt from the flat suite rows
(consistent across the import, not byte-identical to live ingest),
`base_sha`/`changed_files` come from GitHub at backtest time, and orchestration
retests appear as separate shard observations rather than `attempts` rows.

## Manual quarantine and release

An admin can open quarantine on `/triage/tests/<identity-id>`, choosing a base
branch, optional lane, and issue URL. An omitted lane means all lanes; use a
specific lane for platform-specific failures. Manual entries are never released
automatically. Infrastructure stubs cannot be quarantined.

```sh
curl --fail-with-body -X POST "$TSIO_URL/api/v1/triage/quarantine" \
  -H "X-API-Key: $TSIO_API_KEY" -H "X-Admin-Key: $TSIO_ADMIN_KEY" \
  -H 'Content-Type: application/json' \
  --data '{"identity_id":"<identity UUID>","base_ref":"main","lane":"ios","reason":"manual","issue_url":"https://github.com/mattermost/mattermost-mobile/issues/123"}'

curl --fail-with-body -X DELETE "$TSIO_URL/api/v1/triage/quarantine/<entry UUID>" \
  -H "X-API-Key: $TSIO_API_KEY" -H "X-Admin-Key: $TSIO_ADMIN_KEY"
```

Release updates the audited row; it does not delete evidence. Optional
`expires_at` is an RFC3339 timestamp. Expired entries stop exonerating immediately.
Auto quarantine opens after three sustained broken/flaky trunk refresh outcomes,
not three repetitions of the same administrative refresh. Healthy recovery
releases auto entries only. Quarantine never outranks a PR editing the test area,
an infrastructure failure, or a systemic new-error cluster.

Existing human labels and the manual status workflow remain authoritative.
The action records the actor, label, resulting state and authenticated publisher
through `/triage/verdicts/<id>/override`. Removing a label must revoke its waiver;
a recorded historical override is an audit entry, not perpetual authorization.

Only a *blanket* bypass label is honored live: `E2E/Override`, which the mobile
label manager re-applies on every push, and any `override-label` a producer
passes with the same semantics. `E2E/Verified` and `E2E Tests/verified` are
per-SHA verifications that persist on the PR after later pushes; reading them
live would silently waive every subsequent commit, so they count only through
the `(verified)` status marker on the head SHA being finalized. In shadow mode
the producers' required-status path is unchanged from today, including the
`E2E/Override` conversion and description format.

## Runner feeds and phase two

`GET /api/v1/triage/quarantine` accepts repository, framework, lane, base_ref, and:

| Format | Result |
| --- | --- |
| `json` | Active identities, reasons, dates and issue URLs |
| `playwright-grep-invert` | Escaped MM-T alternation, or `(?!)` when empty |
| `jest-name-list` | Original full Jest names, one per line |
| `spec-files` | Only files whose known tests are all quarantined |

The regex feed omits an MM-T ID if an unquarantined identity shares it. Always
specify lane and base branch when constructing runner exclusions. Feeds are
public, like report reads. Phase one keeps tests running so health can recover.
The producer Playwright helper consumes `QUARANTINE_REGEX` only when
`E2E_TRIAGE_SKIP_QUARANTINED=true`; its default is disabled. A phase-two caller
should select entries older than the policy's `skip_after_days` using the JSON
feed before setting an exclusion. This release does not automatically fetch and
skip tests or create issues.

## Operations and validation

Force a rebuild with admin `POST /api/v1/triage/health/refresh` and
`{"repository":"mattermost/mattermost-mobile","base_ref":"main","lane":"ios"}`.
Inspect `/swagger-ui/` for all request/response contracts. Public health and
observation lists return opaque cursors; pass `next_cursor` unchanged.

Global initial threshold defaults use the `TRIAGE_*` environment variables listed
in `internal/triage/types.go`. Persisted per-context policies take precedence. Contexts sharing the same
identity/lane/base-branch health key must use compatible health and quarantine
thresholds; verdict computation still uses the requested context policy.
Invalid ranges fail configuration/policy validation. Missing producer metadata
is inferred conservatively; explicit invalid values are rejected.

Run `make ci` before publishing, including PostgreSQL 18.3 testcontainers, action
unit tests and rebuilt distribution, UI tests/type checks, formatting and lint.
The producer patch README lists its script/workflow checks. Local tests do not
establish production GitHub permissions or two-week shadow agreement; those are
rollout checks after deployment. No remote push or deployment is part of this
implementation task.

## Second judge: the adjudicator

The deterministic engine decides the clear cases. For borderline findings
(`REGRESSION`, `REGRESSION_CLUSTER`, `REGRESSION_AREA`, `OWNED_BY_PR` through a
shared helper, `INSUFFICIENT_DATA`, `FLAKY_SUSPICIOUS`) a second judge, Claude
(`claude-haiku-4-5` by default, structured JSON output), receives one evidence
pack per finding: full error and stack, trunk history, the other PRs the same
test failed on, the PR's changed files and the diff hunks of files named in the
error (all hunks for PRs of at most eight files). It answers
`cause ∈ {caused_by_pr, flaky_environment, bug_on_master, test_bug}` with a
confidence and cited evidence ids.

Production path (same prompt, schema and matrix as the offline harness):

1. TSIO serves the statistical half of each pack: `GET /api/v1/triage/verdicts/{id}/evidence`
   (OIDC or admin). Packs exist only for adjudicable findings.
2. The triage action (`src/adjudicate.ts`) adds the PR title, changed files and
   diff hunks from GitHub, calls the model with `anthropic-api-key`, applies the
   matrix, and records the outcome with
   `POST /api/v1/triage/verdicts/{id}/adjudication` (stored in
   `pr_verdicts.adjudication`, event `triage.verdict.adjudicated`). The engine
   verdict is never rewritten; the adjudication sits next to it and the action's
   `engine-verdict` / `verdict` outputs expose both.
3. The final verdict drives the required status, check run and PR comment. The
   comment gains a "Second judge" section with the cause, confidence and a
   one-paragraph explanation per finding, so the developer reads "flaky, recurs
   on #123 and #456" or "caused by this PR: hunk in app/login.ts" instead of a
   bare red status.

Only PR runs are adjudicated. Trunk runs, `INCOMPLETE` verdicts and runs whose
`ACTION_REQUIRED` comes from infrastructure keep the engine result. Inputs:
`anthropic-api-key` (absent: engine only), `adjudicator-model`,
`adjudicate-min-confidence` (0.85), `adjudicate=false` as the kill switch. The
producer patches pass `secrets.ANTHROPIC_API_KEY` through the PR workflow chains
only.

Decision matrix:

| Engine | Adjudicator | Result |
|---|---|---|
| `NEW_TEST`, `INFRA`, `OWNED_BY_PR` on the failing spec itself | not called | block |
| exonerated (`BROKEN_ON_TRUNK`, `FLAKY_CONFIRMED`, `FLAKY_CROSS_PR`) | may veto with `caused_by_pr` ≥ 0.9 citing a diff hunk | unblock unless vetoed |
| borderline | `flaky_environment` / `bug_on_master` / `test_bug` ≥ 0.85 citing cross-PR recurrence or a hunk | unblock, else block |

The adjudicator can never unblock an infrastructure failure or a spec the PR
edited, and an unavailable model leaves the engine's decision in place. It is
scored offline against the backtest ground truth before it gets any authority:
`scripts/triage_adjudicate.py --dry-run` builds the packs and estimates tokens,
the real run caches every response by evidence hash and prints the
engine-vs-adjudicated agreement table. Any change to the prompt, schema or
thresholds is made in `scripts/triage_adjudicate.py` first, re-scored
(`docs/backtests/2026-09-17-adjudicator-mobile.md` is the baseline: Haiku 4.5
unblocked 67 of 157 recurring-elsewhere runs and 19 of 32 waived runs with 0 of
143 likely regressions unblocked), then copied verbatim into
`src/adjudicate.ts`.
