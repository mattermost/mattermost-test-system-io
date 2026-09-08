# Fable goals: implementation and integration audit

The demonstrated Cursor diagnosis plus maintainer waiver is working. **The full
seven-session plan and goals A, B and C are not complete.** The earlier demo GO
did not claim automatic attribution, an autonomous master repair loop, defect
filing, a measured reduction in PR blocks, or measured classifier accuracy.

This review treats the supplied Fable prompts as a proposed design, not as
instructions to implement unsafe behavior or evidence that a capability exists.
Audited source revisions were TSIO `5d112ab`, Mattermost `5ed5f7d`, and Impact Gate
`ca6db76` (package version 2.4.0). The independent review is recorded in
[cross-project findings](reviews/2026-09-08-cross-project-review.md).

## What is achieved

| Fable phase | Verified state | Remaining acceptance gap |
| --- | --- | --- |
| 1. Data foundations | Per-attempt ingestion, project separation, complete evidence, safer migrations and summary tests exist. Real Cypress retry evidence survived the full staging pipeline. | Reused external IDs still combine different tests. Historical missing retries/projects cannot be reconstructed from final-only rows. Production-scale lock/backfill measurements are not recorded. |
| 2. Before measurement | A repeatable, read-only baseline CLI and SQL exist, with PostgreSQL regression tests. This audit fixes two additional query defects. | No committed 60–90-day production measurement covering the requested suites and GitHub block denominator. No independently adjudicated causal numerator. |
| 3. Deterministic attribution | Not implemented. There is no `/triage/attribution` route or server verdict ledger. | The plan's master-failed-implies-not-PR-caused rule is unsound. Trusted identity, ambiguity handling, conservative outcomes and durable decisions must precede any automatic clearance. |
| 4. PR Diagnoser | A real Cursor review consumed staging evidence on the exact commit; a separate maintainer workflow recorded approval before changing the selected status. | This is not the proposed deterministic endpoint/template automation. No repository implementation proves all four trigger outcomes, a reconciliation sweep, the status feature flag or an adjudicated shadow report. |
| 5. Quarantine and repair queue | Not implemented. Existing orchestration leases dispatch test execution, not repair ownership. | Cap/owner/ticket/expiry policy, raw-metric invariance and atomic repair claims are missing. Automatic test deletion on expiry must be removed from the design. |
| 6. Guardian master repair | Not implemented or demonstrated. PR #38356 repairs reporting/CI; it is not a Guardian-generated test fix. | Exact-image controlled reproduction, retries disabled, mechanical repair checks, three-strike history, and a real fix PR reviewed by a human are missing. |
| 7. Product defect call-out | Not implemented. | Product-suspect/no-edit outcome, Jira integration, live unresolved-ticket deduplication, concurrency handling and a real defect handoff remain to be built. |

Evidence for phase 1 includes
`apps/server/tests/e2e/testhistory/retry_parity_e2e_test.go::TestRetryParity_PlaywrightAndCypressStoreTheSameRun`,
`project_disambiguation_e2e_test.go::TestStableKey_DisambiguatesIdenticalTitlesAcrossProjects`
in the same directory, and the `TestSummarize_*` functions in
`apps/server/internal/api/testhistory/summarize_test.go`. Migration/backfill
coverage is
`apps/server/cmd/tsioctl/db/data_e2e_test.go::TestStableKeyBackfill`.
The [live demo record](automation/staging-demo-evidence.md) separately records
deployment, 40 Cypress plus 20 Playwright reports, the Cursor review and the
verified maintainer receipt. Unit tests alone are not deployment proof.

Several Fable ground-truth statements are now stale: the parsers no longer have
the described retry defects; project keys and catalog-only migrations exist;
waiver and baseline code exist. Server attribution, quarantine, repair and Jira
capabilities remain absent. Do not repeat the old fixes or assert zero code hits.

## Corrections made during this audit

The baseline replay compared a PR suite name directly with its master suite
name. In Mattermost, `cypress-full-enterprise` and
`cypress-full-enterprise-master` are different names; the same applies to the
other three full suites. That comparison silently lost relevant master history.
The query now applies the four explicit mappings only for repository
`mattermost/mattermost` and trusted branch `master`. Other suites/branches retain
exact-name matching; framework and edition remain separate.

The query also prioritized any shard's `run_failed=true` over another shard's
successful spec retest. It now agrees with `/tests/history`: failed then passed
across complete shards is flaky, and does not count as an observed failed PR run
in this replay. This is still a stored-report calculation, not a measurement of
terminal GitHub check outcomes or causation.

Both defects were reproduced before the fix by
`apps/server/cmd/tsioctl/db/data_e2e_test.go::TestBaseline_MattermostSuiteMapping`
and `TestBaseline_RetestSurvivorIsNotARedRun` in that file. The mapping regression
also covers a different trusted branch so the special master naming cannot
silently replace release-branch behavior.

## Impact Gate: useful, with a narrower role

**Use its existing deterministic planner for an advisory pilot. Do not use its
current gate, prediction score or healing loop to clear Mattermost PRs, skip
blocking tests or repair master automatically.** Installing the package is only
one integration step; repository mappings, execution identity, output semantics
and a real workflow caller are not demonstrated integrations today.

The existing test suite passed **469 tests** in an isolated checkout. Additional
probes found behavior those tests do not reject:

| Probe on the assessed Impact Gate revision | Observed result | Consequence |
| --- | --- | --- |
| Review Mattermost PR #38356 against merge base `502cf7e3e5379ce054bee24e279ec1779911aa38` | `No changed files detected. Nothing to review.` despite 11 changed files | Workflow and E2E paths are filtered out, even though these changes affect CI. |
| Gate that PR with threshold 100 | Exit 0, `Gate passes` | No assessed scope is being mistaken for coverage. |
| Gate with a nonexistent Git ref | Exit 0, `Gate passes` | A Git error is mistaken for no changes. |
| Empty Playwright result / retry survivor | Both satisfy the agentic success guard | A successful verdict does not establish a nonempty clean verification run. |
| Assertion-free replacement test | Accepted by the fix parser | Prompt instructions do not enforce preservation of test intent. |
| Product-bug healing prompt | Requests `test.fixme`; another healing path rewrites tests to `fixme` | Conflicts directly with capability C. |

The last three are compiled helper/fixture probes, not actual browser runs.
The independent review provides exact source references and further issues:
generation safeguards differ between code paths, fallback traceability
confuses co-changed files with observed coverage, the Cypress adapter's default
glob misses Mattermost's `*_spec.js` convention, and claimed prediction accuracy
has no reproduced held-out Mattermost result.

There is no honest basis for **100% integration ease** or **100% accurate
expected outcomes**. A finite test set can establish its own cases and expose
defects; it cannot prove correctness on every future change. The present probes
already refute readiness for that promise. Use explicit unknown outcomes and
measure missed regressions and unnecessary blocking separately.

## Keep ownership clear

| Component | Owns | Must not duplicate or infer |
| --- | --- | --- |
| Mattermost CI | Executes existing suites and controls status-writing permissions | Test planning must not bypass complete result accounting. |
| TSIO | Raw attempt/run evidence, canonical identity work, complete history and rates | A history match is not proof of PR innocence. |
| Cursor | Evidence-backed diagnosis and proposed investigation | No automatic status changes in the demonstrated flow. |
| Maintainer workflow | Explicit approval, durable record and scoped status change | Human approval is not classifier ground truth. |
| Impact Gate pilot, proposed | Advisory changed-file-to-existing-spec mapping with provenance | No duplicate flake rates, quarantine, Jira state, status writer or repair queue. |

Use the [bounded implementation prompt](impact-gate-mattermost-pilot-prompt.md).
It builds on existing commands and execution workflows. Adding another crew,
graph service, classifier or framework adapter is not a prerequisite for this
pilot. Unsupported changes must select the existing full suite.

## Correct the remaining plan before executing it

1. Replace `not_pr_caused` based solely on prior master failure with
   `observed_on_master`. Keep clearance separate and conservative; a known flaky
   test can also detect a new PR regression.
2. Resolve identities from trusted baseline sources and reject ambiguous,
   renamed or incompatible observations. A PR-controlled string matching an
   existing ID is not identity proof. The live MM-T1 collision demonstrates why.
3. Quarantine expiry must escalate for review or restore blocking under an
   explicit policy. Test retirement requires human review; expiry alone is not
   evidence that a test is unnecessary. Report raw quality and blocking noise
   separately because moving a test cannot reduce its raw failure rate.
4. Rerun-green and pushed-fix events are behavioral observations, not causal
   labels. Independently adjudicate evaluation cases, retain unknowns, and report
   wrong-toward-green and wrong-toward-red with their own numerators/denominators.
5. Caught E2E defects are not automatically escaped release defects. Release and
   deployment linkage plus evidence of escape are needed for the latter metric.
6. An enabled Jira path must fail clearly without credentials; an intentionally
   disabled optional feature must not break unrelated TSIO ingestion. Deduplication
   also needs serialized ownership/idempotency around creation, not only a query.

## Completion order and measurable gates

1. Finish trusted identity/ambiguity handling and capture the baseline with the
   existing CLI. Preserve raw exports, exact revision, retention window,
   producer cutover and unavailable fields. Obtain GitHub's original terminal
   run/status records and independently reviewed causal labels before claiming
   metric 1. Do not fabricate 90 days of retries that were never recorded.
2. Implement the corrected attribution caller/record together in comment-only
   mode. Replay labeled cases, keep actual statuses unchanged, and publish the
   two error directions. Agree on the acceptable wrong-green bound before any
   status flag is enabled; observing zero errors in a small sample is not 100%.
3. Run the Impact Gate advisory pilot alongside the existing full suite. Compare
   its selected specs with observed full-suite failures and separately reviewed
   regressions. Do not reduce coverage until this has evidence.
4. Build one atomic repair queue, then one immutable-image reproduction and
   proposed test fix. Prove that assertions/skips/retries cannot make verification
   look successful, and obtain human review of the real fix PR.
5. Add the mutually exclusive product-suspect/defect path, tracker ownership and
   bounded quarantine only after those earlier callers and evidence exist.

The under 5% block target, suite-wide master flake reduction and escaped-defect
contribution remain **unmeasured goals**. The next work is data/identity and a
shadow evaluation, not stronger wording or another model. This document is a
completion plan and audit; it does not claim those future gates have passed.

## Validation of this follow-up

The required `make ci` checks were executed. Unit/race tests, lint, formatting,
typechecks, contract/history/orchestration and baseline database suites passed.
The reports suite hit a local Docker startup timeout; an initial isolated retry
also overlapped a web build replacing embedded assets. After that build finished,
`go test -race -tags=e2e ./tests/e2e/reports -count=1` passed. The remaining
`make build actions-dist-check` checks passed. These are disclosed local check
retries, not product test failures that were waived.

Staging remains on the demonstrated runtime revision `6c385b8`. Use this newer
baseline CLI/query revision for subsequent baseline exports. A production
baseline export and the proposed Impact Gate integration were not executed by
this audit.
