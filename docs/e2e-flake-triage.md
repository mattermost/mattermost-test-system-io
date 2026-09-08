# E2E flake triage

Test System IO stores test observations. The Cursor consumer evaluates a failed
PR run and explains the evidence. A maintainer may approve a scoped GitHub status waiver.
Neither a history match nor a green rerun establishes that a PR is harmless.

The original design included a server-side classifier, Guardian, attempt queue,
quarantine and ticket creation. Those capabilities are not implemented here.
They are not prerequisites for this staging demo, and should not be described as
shipped behavior.

## Implemented data contract

| Read | Use |
|---|---|
| `/api/v1/tests/evidence` | Failure identities, grouped errors, screenshots, run identity and ingestion coverage |
| `/api/v1/tests/history` | Per-test outcomes and a summary over the requested time window |
| `/api/v1/orchestration/status` | Unit/worker coverage and terminal outcomes for the same run |
| `/api/v1/reports/consolidated` | Existing run/spec summary |

These are public reads. The server does not classify a failure or change a
GitHub commit status.

Evidence contains `complete` independently of `truncated`. Complete ingestion
requires a `completed` group, a positive declared shard count, and every declared
report registered and `complete`. Every returned cluster carries all its member
stable keys. Row/cluster response bounds still set `truncated=true`; such a
response cannot authorize a waiver. A cluster is a normalized-error grouping,
not proof that its members share one cause.

Ingestion completeness alone does not establish worker coverage. The consumer
also checks completed orchestration, all unit identities, no pending/leased/
abandoned/retest-eligible work, and failed worker attempts represented by the
failure evidence. A worker or hook that failed without test cases remains
unresolved even if all uploaded tests match the baseline.

For trusted baseline history use the full repository slug, `baseline=true`,
`branch=master`, exact master report `name`, and `framework`. This excludes PR
observations, including fork PRs whose source branch happens to be `master`, and
requires completed baseline groups. Mattermost's mapping is explicit:

| PR report name | Corresponding master report name |
|---|---|
| `cypress-full-enterprise` | `cypress-full-enterprise-master` |
| `cypress-full-fips` | `cypress-full-fips-master` |
| `playwright-full-enterprise` | `playwright-full-enterprise-master` |
| `playwright-full-fips` | `playwright-full-fips-master` |

Anchor history with `before=<PR group.created_at>` so later samples do not enter
an earlier decision. The summary covers the requested window independently of
`limit`. Entries remain bounded and disclose `total_entries` and `truncated`;
entries include `group_id`, repository/framework, run attempt and environment
metadata so the consumer can retrieve a baseline's actual evidence.

`failing_since_commit` describes the oldest observed commit in a current failing
streak. It does not identify a causal commit. If no pass is present in the full
requested window, describe only the observed coverage. Even with a last-pass
boundary, a bisect or investigation is needed before attributing blame.

## Test identity and historical coverage

Use each returned `stable_key` verbatim. It is the MM-T identifier where one is
available, otherwise the full title. Playwright project and non-Playwright file
prefixes keep distinct executions separate. Never strip a prefix or guess an
alias to obtain a reassuring baseline.

Old Playwright reports did not persist the project. Their old unprefixed series
cannot be safely joined to a new project-prefixed key. New keys need a fresh
baseline unless retained raw reports are replayed with verified project identity.
The migration/backfill preserves this boundary rather than merging browsers.

The Cypress producer must capture Cypress's actual `after:spec` attempt results;
ordinary Mochawesome final-state JSON does not retain failed retry attempts.
Mattermost's producer changes and the browser-generated fixture establish the
new producer/parser contract. A parser unit test that constructs `attempts[]`
by hand is useful parser coverage, but cannot prove the producer emits it.

For newly complete attempt data, each attempt keeps its own outcome and the
run-level fields `attempts`, `attempts_failed`, and `run_failed` describe the
rollup. A retry-survivor is flaky, not a clean first-try pass; it ordinarily does
not block CI. Historical final-only Cypress reports cannot recover a discarded
failed attempt. Report that coverage gap rather than claiming it was backfilled.

## Cursor consumer and status writer

The full replacement prompt is checked in at
[automation/cursor-e2e-diagnosis.md](automation/cursor-e2e-diagnosis.md), with
activation requirements and decision format in
[automation/README.md](automation/README.md).

The current prompt produces a diagnosis using staging evidence, orchestration,
trusted suite-specific history and the PR diff. It states missing uploads,
historical gaps and unavailable reproductions explicitly. It requires no
`GH_LABEL_TOKEN` or administrator changes. Cursor posts its normal durable PR
review and does not add a label or change a status.

A maintainer can approve Mattermost's existing `E2E Tests - Override Status`
workflow after reviewing that diagnosis. The scoped dispatch binds the assessed
SHA, PR, Cursor review/comment ID and body hash, selected exact status/context
IDs, source workflow runs/attempts and the maintainer's explicit reason. The
workflow stores the approval and a copy of the diagnosis before using its own
`GITHUB_TOKEN` to change statuses. Its helper validates scope and authorship;
it does not independently prove that the diagnosis is causally correct.

The existing `E2E Tests/verified` label remains a broader manual maintainer
mechanism. The scoped workflow avoids it. No Cursor secret is required. The demo
is **Cursor diagnosis followed by maintainer verification**, not an autonomous
classifier making the PR green.

The stricter `automation/apply-e2e-waiver.mjs` automatic prototype is retained
separately and is not invoked by the active diagnostic prompt. It requires
credential isolation, complete evidence, recent baseline, paired immutable-image
reproduction and no diff suspicion before making any automatic waiver. Its
reproduction and diff fields remain agent attestations rather than causal proof.

GitHub status writes are not atomic with reads of the workflow/head. An assessed
SHA cannot turn into a newer commit, but a same-SHA rerun can race the final API
read. When detected after writing, the helper restores failure and reports
invalidation. This can briefly expose success, and a restoration API outage
requires intervention. The scope checks are not a claim of atomic enforcement.

Run the helper's focused regression checks with:

```sh
node --test docs/automation/apply-e2e-waiver.test.mjs
```

Server API regression coverage lives under
`apps/server/tests/e2e/testhistory/`; producer/parsing coverage lives in the
Cypress producer fixture and `apps/server/internal/ingest/`. Passing these checks
establishes tested contracts, not a successful deployment or live Cursor run.

## Measurements and rollout

The [baseline scripts](../scripts/baseline/README.md) measure stored-test
observations and provide an explicitly non-causal policy replay. Retry-survivors
must not be counted as failed terminal checks. A TSIO row is not evidence that a
PR was blocked: actual GitHub context/run/attempt results and separately recorded
waivers are needed to measure blocking and its reduction.

Waivers do not rewrite raw TSIO results. Applying a label or status does not erase
that raw baseline; retention, missing uploads and historical identity gaps can
limit its coverage. Preserve the evidence and decision records and report those
limitations alongside measurements. Aggregate master pass rates alone do not
establish how many PRs encounter unrelated failures.

No adjudicated false-green or false-red rate is established by these changes.
Record both separately, against independently reviewed outcomes. Do not treat
rerun-green or a pushed fix as automatic ground truth about causation. A
successful staging example is a workflow demonstration, not a calibrated
classifier evaluation.

The deployment must run the complete pending migration sequence. Catalog-only
schema additions can still wait for locks. Existing-row backfills and concurrent
index builds run outside schema migrations; consult the migration and `tsioctl`
commands for the exact deployed revision. Production table size, lock duration
and backfill duration must be measured rather than inferred from unit tests.

For the requested demo, deploy the reviewed TSIO revision to staging, run PR
#38356 against it, obtain a real completed failure and a truthful Cursor diagnosis,
then deliberately approve and verify the scoped existing workflow on the assessed
SHA and contexts. Preserve the diagnosis, approval and status receipt.

No Cursor token is required. Missing evidence or unavailable reproduction must
remain visible to the approving maintainer; they cannot be silently converted to
certainty. A diagnosis without a completed verification workflow demonstrates only
the first half of the requested flow. Do not claim a green transition that was
not observed.

## Deferred work

A master fix loop, tracker integration, quarantine and server-side verdict queue
remain future work. If pursued, fixes need reproducible validation and product
bugs should be routed to an owner without weakening tests. Quarantine needs
coverage and removal accounting; deleting expired tests automatically can improve
reported pass rates while reducing coverage and is not an anti-gaming guarantee.
