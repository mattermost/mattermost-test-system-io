# Review: testing Mattermost's changed behavior

The review found and fixed defects in selecting CI work and in trusting its evidence.
It also found product-assertion gaps that running the full E2E suite cannot close.
Impact Gate remains advisory; no result here authorizes reduced dispatch, automatic
clearance, or a claim of 100% changed-behavior coverage.

## What was reviewed

The main product repository is **mattermost/mattermost**. The captured inventory
contains 40 open PRs targeting master and 40 recently updated merged PRs targeting
master since 2026-08-15. This is a bounded sample, not every repository PR.
Eight product/test PRs received exact-revision assertion review: merged #38312,
#38296, #38229, #38330; open #38345, #38383, #38328, #38391. The [detailed audit](2026-09-08-mattermost-product-coverage.md)
records full heads, comparison bases, actual merge commits, source assertions and
explicit search limits. No tests from those eight PRs were executed by that audit.

The implementation review also covered TSIO #114, Mattermost #38356, Impact Gate
#1 and Mattermost #38392. TSIO #107/#108/#110 were marked merged into the experimental
`claude/e2e-ai-triage-api` branch; its parent #101 was closed unmerged. A merged
badge on those children is insufficient evidence of a shipped default-branch feature.
All three captured merge commits were verified not to be ancestors of current
TSIO main `aa0d692f255d98be1621b500e59387ca0debfb3d`.

## Fixed selection and evidence defects

1. **Relevant changes could be skipped.** Mattermost's previous extension filter
   returned `should_run=false` for isolated CSS, lockfile, SQL migration, Go checksum
   and local build-action changes. The replacement permits an automatic skip only
   for an explicit documentation-only set. Unknown, dependency, configuration,
   asset and harness paths retain the full suite. Manual requests and empty diffs
   also retain it. Go module/checksum/workspace and FIPS build changes request FIPS.
2. **The selected PR and diff could be stale or incomplete.** The resolver now
   resolves full SHAs, pages associated PRs, requires an open exact-head match and
   rejects ambiguous candidates. The workflow freezes the PR head, comparison base
   and base branch. Trusted workflow code reads an immutable Git diff, retaining
   deletions, both rename paths, submodule revisions, and filenames with newlines.
   Multiple merge bases are rejected rather than arbitrarily choosing one that
   could hide a runtime change behind a documentation-only result.
3. **Planner provenance could be overstated.** Impact Gate now re-derives the
   complete diff at its advisory API boundary, checks hidden tracked bytes, honors
   explicit custom/hidden spec patterns, rejects misleading patterns and binds plan
   IDs to both bases and the head. A caller's `human-reviewed-manifest` label is
   unverified provenance; it cannot turn a candidate relationship into approved
   coverage or targeted selection. The pilot still has no approved mappings.
4. **A dispatched master run was conflated with its tested input.** TSIO now
   distinguishes the tested `commit_sha` from unanimous verified OIDC
   `source_workflow_sha`. Guardian checks the latter against GitHub's actual run
   head. Wrong run/attempt, mixed shard revisions, mismatched Begin receipts and
   old queue items without source provenance still fail closed.
5. **A repair could publish against newer, unverified master.** Guardian now checks
   the entire current master revision before publication and requires the repair
   commit's sole parent to be the verified tested commit. Any intervening master
   change requires fresh evidence/revalidation, even when the target spec is
   byte-identical. This covers helper/fixture changes and upstream fixes such as
   #38330's deleted image dependency. Normal PR CI and human review remain required.

## How Impact Gate and TSIO fit together

The existing Mattermost workflow owns execution. The pinned Impact Gate job reads
the exact tested source as data, emits complete static inventories and selection
reasons, and stamps artifact hashes with the planner, base, tested and workflow
revisions plus run/attempt. Enterprise suites and enabled FIPS suites remain
distinct. The E2E dispatch jobs do not depend on the advisory job.

A read-only job after E2E compares those plans to GitHub worker jobs and public
TSIO orchestration/report evidence for the same identity. It reports static
selection, dispatch, worker completeness and final failed specs separately.
Incomplete/mismatched evidence produces unavailable recall. Retry survivors do
not become final failures; no failures means an empty denominator, not 100%.
The artifact cannot write labels, commit statuses, issues or test-selection inputs.

This answers “did the recommended tests actually run, and were observed final
failures included?” It does not answer “does a test assert every behavior changed
by this PR?” That requires assertion review and, where missing, a regression.

## Product regressions still needed

These are gaps at the captured public PR revisions, not findings that every
private/enterprise/manual test is absent. Full citations and case details are in
the accompanying audit. They are not silently added to unrelated integration PRs.

| Main-repository change | Assertion evidence and remaining gap |
| --- | --- |
| #38345 phone autolinking | Adds no tests for plain numbers becoming links in posts. Existing explicit `tel:` and profile-phone tests assert different behavior. Add option/normalization/exclusion cases and an actual post-rendering assertion. |
| #38383 SAML rotation | New mocked helper tests assert reload calls. They do not prove a same-filename upload rotates the running SP or reconfigures peers. Add upload-to-reload and peer-handler integration, then a real certificate-rotation case. |
| #38328 attachment ABAC | Useful API allow/deny tests and positive browser upload exist. PATCH/scheduled update and retained/removal-only file branches need direct assertions, including no persisted mutation on denial. |
| #38296 expired-post pinning | Strong server regressions exist. Existing E2E “older message” means older in a fresh list, not past the edit limit. Add expired-post pin/unpin versus edit-blocking UI coverage. |
| #38312 / #38391 React default/bootstrap | Concurrent-mode behavior tests exist, but forced configuration can mask the product default; non-root CSP/bootstrap assertions are incomplete. Keep full UI execution and add direct bootstrap/default assertions. |
| #38229 plugin request body limit | Direct server tests cover the intended cookie/CSRF-body path and preserve header bypass. Boundary/read-error cases belong at the server layer; an arbitrary plugin UI spec is not a suitable replacement. |
| #38330 image fixture repair | A real test-data dependency fix, now included by refreshing the demo branch from master. It is not evidence of new product behavior coverage. |

## Acceptance boundary

Selection correctness, execution completeness, assertion coverage and measured
accuracy are separate acceptance criteria. Full fallback preserves the existing
suite; it cannot invent missing assertions. No time saving, behavioral recall,
FIPS execution success, autonomous repair or Jira submission is claimed by static
planning or local protocol tests. Guardian/provider/Jira live prerequisites remain
in [the operations guide](../automation/triage-operations.md).

Local validation includes 507 planner tests, 37 comparison tests and the focused
PostgreSQL/race triage and OIDC suites. Stored comparisons reproduce the complete
historical Cypress 2/2 observed failed-spec result and the incomplete 39/40-worker
run's unavailable recall; neither measures changed-behavior coverage. The full
local TSIO CI attempt hit a Docker startup timeout in an unrelated orchestration
test; its serialized retry later hit a lint timeout while the host was under
heavy CPU load. These attempts are not recorded as successful full CI. Use the
existing PRs' checks for final published-head validation and deployment receipts.
