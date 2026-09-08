# Implementation prompt: make Impact Gate useful to Mattermost

Use this in the Impact Gate project. The reviewed starting revision was
`ca6db766fa035c4527af331ff5319c41f3d50435`; first inspect the current diff and preserve
other work. Treat this as one bounded integration task, not permission to build
Fable's entire triage/Guardian/Jira system.

---

Make Impact Gate a trustworthy **advisory planner for Mattermost's existing
Playwright and Cypress suites**. It recommends existing specs and explains the
mapping. Mattermost continues to run the full suite during the pilot; TSIO
remains the source of execution evidence and failure/flake definitions.

Repositories:

- Impact Gate: `/Users/yasserkhan/Desktop/yas/impact-gate`
- TSIO: `/Users/yasserkhan/Documents/mattermost/mattermost-test-system-io`
- Mattermost: resolve the actual saved checkout and reviewed PR branch. Do not
  assume a temporary demo checkout is the long-term source.

Read each repository's instructions. Distinguish existing code, tested behavior,
live verification and proposed work. Cite exact files/functions for claims. Do
not invent API capabilities, test identities, coverage links or measurements.

## First PR: make the existing planning path honest

Work through the current `getChangedFiles` → deterministic impact/plan pipeline.
Use existing CLI entry points and schemas. Add only the fields needed by this
advisory caller, plus regression tests. Do not create a new service, database,
agent orchestrator, prediction model or alternative execution framework.

1. Preserve the complete Git diff and errors. Distinguish a valid empty diff
   from an invalid ref/Git error, unsupported files and unmapped files. Do not
   discard workflow, dependency, configuration or E2E changes before the caller
   can make its conservative selection. Cross-cutting and unknown changes
   recommend the existing full suite, with the exact reason.

2. Repair the current gate's false success paths. An invalid ref must exit
   nonzero. Zero matched features with unassessed files must not say covered or
   safe. In advisory mode, an explicit unknown/full-suite recommendation may
   complete successfully as a report, but must never be a passing coverage or
   release assertion. Partial coverage is not 100% coverage.

3. Support Mattermost's actual source/test layout through a small reviewed
   configuration and the existing adapters. Resolve exact repository-relative
   Cypress `*_spec.js`/`*_spec.ts` and Playwright spec paths; inspect actual
   configuration rather than assuming `*.cy.ts`. Reject missing paths, absolute
   paths, traversal and symlinks escaping the checkout. Separate browser/project
   and enterprise/FIPS suite identity. Unknown mappings stay unknown.

4. Keep mapping provenance. Distinguish human-reviewed manifest mappings,
   measured per-test source coverage, static dependency inference and co-change
   heuristics. Finding a spec file proves presence, not behavior coverage. Do not
   write every changed file as measured coverage for every executed test when a
   real coverage map is absent. Heuristic risk scores must be labeled as such;
   remove unsupported numerical accuracy or universal-correctness claims from
   this path's output.

5. The pilot must run without an LLM key or MCP/browser service and must not
   generate, execute, heal, skip, quarantine or rewrite tests. Keep optional
   generative commands outside this caller. Do not expose the existing product-
   bug-to-fixme behavior as a Mattermost repair feature. Do not add duplicate
   flake metrics, ticket state or a status writer.

6. Emit one machine-readable report. Bind it to repository, full base/head SHA,
   changed-file set, suite/project, exact selected spec paths, mapping provenance
   and the full-suite fallback reason. Reuse existing fields where possible.
   `--json` output must contain parseable JSON only; progress belongs on stderr.
   Report unavailable evidence explicitly rather than inventing a pass.

Required regression cases:

- Nonexistent Git ref returns an error and cannot pass the gate.
- Valid genuinely empty diff is distinct from all-filtered/unsupported changes.
- Mattermost PR #38356 at `5ed5f7d29ae67d6ecf7864022e37c66792280a45` versus
  `502cf7e3e5379ce054bee24e279ec1779911aa38` retains its 11 changed files and
  recommends full-suite execution because they affect E2E/CI.
- A dependency-only change and an unmapped product file recommend the full suite.
- Exact Mattermost Cypress/Playwright paths resolve; invalid paths are rejected.
- Missing per-test coverage does not create measured coverage edges.
- No API key, model call, test write or status write occurs in advisory planning.
- Identical inputs produce identical substantive plans; output is valid JSON.

Run the existing suite, build and lint, then the new regressions. Existing tests
passed 469 cases at the reviewed revision but missed the false-pass behavior;
test count alone is not acceptance. Address relevant dependency audit findings
with compatible locked updates and rerun checks; do not blindly upgrade all
dependencies or rewrite unrelated modules.

## Second PR: prove a thin shadow integration

Only after the first PR passes, add one advisory consumer to Mattermost's existing
workflow using the reviewed immutable Impact Gate revision. The job uploads the
JSON plan as an artifact and leaves the current full-suite dispatch intact. It
must not modify E2E statuses or supply a privileged token to PR/generated code.
Use an isolated checkout without ambient provider/status-writing credentials.

Consume TSIO's existing evidence/history/orchestration reads only where they
answer a concrete comparison. No new TSIO endpoint is required for this pilot.
Bind comparisons to exact repository, commit, CI run ID, attempt, suite/project
and complete expected worker reports. Join specs using validated file and full
title, preserving TSIO's key; a reused external ID cannot prove a match by itself.
Do not count a retry survivor as a final failed spec.

Save the exact report and full-suite outcome from one real Mattermost run. Show
selected/total specs, unmapped files, fallback reason, report/worker coverage and
which observed final failures the proposed selection would have included or
missed. Label this as observed failure-selection recall, not causal regression
accuracy or guaranteed future coverage. A full-suite fallback is a correct safe
result; it is not evidence of test-time savings.

Do not reduce executed coverage until an independently reviewed shadow dataset
supports an agreed decision. Keep confirmed missed regressions and unnecessary
selection/blocking as separate measures; retain unknown labels. Do not use a
rerun going green as proof that a failure was unrelated to a PR.

Finish with the exact files changed, commands/results, real artifact/run links,
remaining limitations and the smallest next step. No automatic merges. No claim
of 100% integration ease, 100% accuracy or achieved Fable A/B/C without evidence.
