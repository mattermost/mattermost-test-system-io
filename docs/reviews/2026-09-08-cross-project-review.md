# Independent cross-project review — 8 September 2026

Reviewed source revisions: Impact Gate `ca6db76` (2.4.0), TSIO `5d112ab`, Mattermost demo worktree `5ed5f7d29ae67d6ecf7864022e37c66792280a45`. The Fable prompt pack is a proposed plan, not evidence of implementation. Source files were not modified by this reviewer.

## Decision

Impact Gate has useful deterministic diff-to-test planning components, but it is **not integrated with TSIO or Mattermost CI and is not safe to use as an automatic clearance or master repair authority today**. The previously demonstrated TSIO/Cursor/maintainer workflow is real, but it proves a narrower capability than Fable A, and does not establish B or C.

There is no evidence for 100% integration ease or 100% expected-result accuracy. Passing the existing tests establishes those test cases, not causal correctness on arbitrary PRs. Unknown outcomes must remain expressible; human-adjudicated evaluation and an explicit conservative fallback are required before reducing blocking test coverage.

## Evidence-backed findings

### P1 — Impact Gate passes its gate when it cannot assess the diff

`src/cli/commands/gate.ts:41` ignores the `getChangedFiles` error and exits 0 when the returned source list is empty (43–45). It also exits 0 for zero impacted features (54–57), without considering `impact.unboundFiles` or warnings. `src/agent/git.ts:7` excludes all `.github`, `e2e-tests`, and scripts paths, and lines21–57 exclude package manifests/locks, Go dependencies, build configuration, YAML and shell files. Those changes can affect both test execution and product behavior.

The parent independently ran the compiled reviewed revision against real Mattermost `5ed5f7d` versus merge base `502cf7e3e5379ce054bee24e279ec1779911aa38`: review printed “No changed files detected. Nothing to review.” and `gate --threshold 100` exited 0. An invalid git reference also exited 0 with “No changed files detected. Gate passes.” Logs: `/private/tmp/impact-mattermost-review.log`, `/private/tmp/impact-mattermost-gate.log`, `/private/tmp/impact-invalid-ref.log`.

This is acceptable only as an explicitly limited advisory result; it must not become authority to waive E2E failures or skip a full suite. Preserve the complete diff and errors; distinguish no changes, unsupported changes and unknown mapping. Unknown/unsupported/risky changes should select the existing full suite in any future selection caller.

### P1 — The healing path explicitly masks product bugs

`src/prompts/heal.ts:84` instructs the model to mark `test.fixme` when server behavior is broken. Line86 repeats that instruction. `src/pipeline/stage4_heal.ts:180` implements a whole-file rewrite from `test(` to `test.fixme(`; lines310–316 invoke it after two failed verification cycles. That can remove correctly failing tests from execution, directly opposing Fable C.

`src/agentic/fix_loop.ts:59` permits changing assertion logic. Its `applyFix` validator (69–87) only requires a `test(` substring and an import; a compiled pure-helper probe confirmed that `test('same test name', async () => {});` is accepted. Prompt wording to preserve intent is not a mechanical assertion-preservation guarantee.

Do not connect these healing paths to a master repair job. Remove automatic skip/fixme on unresolved product behavior, preserve the original file, return a visible unresolved/product-suspect outcome, and require the eventual repair PR to preserve assertions and receive human review.

### P1 — “Verified” does not mean the intended test ran cleanly on the failing binary

`src/pipeline/stage4_heal.ts:151` treats a missing Playwright binary as successful verification; line159 runs with one retry. `src/pipeline/spec_verifier.ts:94` smoke-runs with two retries. `src/agentic/runner.ts:188` treats `failed === 0 && compiled` as success, without requiring an executed passing test or excluding flaky/skipped-only results. `src/agentic/playwright_runner.ts:121` does not disable retries; its `baseUrl` option only adds a configuration filename at127–129 and does not actually set the requested server URL. No immutable image/run identity enters this API.

Compiled pure-helper probes confirmed both an empty report and a retry survivor satisfy the agentic success guard. The empty fixture returns passed0/failed0/compiledtrue; the retry fixture returns passed1/failed0/flaky1/compiledtrue. `test/playwright_runner.test.ts:75` already covers an empty parser result, but does not cover downstream rejection. These probes exercise helper logic, not a browser or real Mattermost reproduction.

A future Guardian needs exact recorded digest/configuration, explicit missing-runner failure, nonempty expected test identities, zero retries, and retained outcomes for each independent verification run. A successful command or parse alone is insufficient.

### P1 — The primary generation command bypasses documented safeguards

`src/cli/commands/review.ts:315` invokes `runAgenticGeneration` directly. That path writes generated code at `src/agentic/runner.ts:172` before a method validation/quarantine gate, then invokes a Playwright subprocess with the full inherited environment at `src/agentic/playwright_runner.ts:137`. The method hallucination check is in a different path (`src/pipeline/stage3_generation.ts:173`); the restricted environment is also in another path (`src/pipeline/spec_verifier.ts:20`).

Therefore README claims that generated suspicious specs are blocked from the main spec directory and execution is protected by those checks do not universally apply to the recommended `review --generate` workflow. Consolidate its validation/execution into the already existing guarded path instead of adding another orchestration layer. Run generated code only in a disposable environment with the required test credentials, not a privileged status writer.

### P2 — Mapping coverage and observed co-occurrence are represented more strongly than the evidence supports

`src/engine/impact_engine.ts:171` labels a feature covered because a Playwright spec file exists, or partial because a Cypress file exists. It does not demonstrate that assertions cover the changed behavior. `src/agent/traceability_capture.ts:364` maps each executed spec to all changed source files when no real coverage-map entry exists; the output at367–371 does not attach provenance distinguishing measured execution coverage from fallback co-occurrence. Standard Playwright JSON does not provide source coverage for every executed test.

`src/training/validator.ts:167` filters many cross-cutting changes, then calculates whether the remaining filenames match families (177–195). This is mapping completeness, not semantic mapping accuracy or escaped-defect detection. `src/agent/feedback.ts:311` calls failed/executed rates “flakeRate”, unlike TSIO's distinction between outright failure and retry survival, and derives a separate unbounded quarantine suggestion (315–332) without TSIO run identity, master restriction or Fable's cap/expiry/ticket semantics.

Preserve provenance and call these “mapped/spec present/co-changed” observations. Do not import these rates or quarantine flags into TSIO as if they were equivalent. Reuse TSIO run-level observations instead of maintaining two definitions of flakiness.

### P2 — Framework and identity integration is not complete

Impact Gate's adapter uses `**/*.cy.{ts,js,tsx,jsx}` (`src/adapters/cypress.ts:16`), whereas Mattermost's current Cypress tests use `*_spec.js`. The route-family engine separately scans `.js` and `.ts`, so support varies by command; an adapter's existence is not proof that the primary workflow discovers/runs Mattermost's suite. The `heal` CLI only reads a Playwright report (`src/cli/commands/heal.ts:44`).

No TSIO API caller, canonical `stable_key` contract, GH run-attempt binding or immutable image integration was found in the reviewed Impact Gate source paths, and no Impact Gate consumer was found in the reviewed Mattermost or TSIO workflows. Consequently an installation is not an end-to-end integration.

TSIO itself still merges different tests that reuse an external ID: migration `000028_test_cases_stable_key.up.sql:111` prefers that ID before file/title, as does compatibility migration30. `docs/automation/staging-demo-evidence.md:48` documents live MM-T1 collisions. The new Cursor prompt correctly discloses this (`docs/automation/cursor-e2e-diagnosis.md:78`); a prompt guard alone cannot provide a server-side guarantee for future automatic attribution. Reject ambiguous keys or add an explicit canonical identity/mapping before using them for decisions. A renamed PR test matching an existing master's external ID must not inherit clearance merely because the ID exists on master.

### P2 — Accuracy claims are not demonstrated accuracy

`src/prediction/model.ts:20` labels hardcoded weights “pre-trained” and claims about65% then75–80% accuracy. `src/prediction/calibration.ts:456` displays “ApacheJIT, ~65% accuracy” before local labels exist. No reproducible training artifact or held-out result supporting those exact weights/claims was found in the searched code/docs/test tree. The actual calibration evaluation at360–380 uses the training set, which the code does honestly disclose. External research performance does not establish this implementation's Mattermost performance.

`src/qa-agent/phase3/verdict.ts:35` can return go with no findings. A compiled pure-helper probe with no flows and no spec results returned `go` and “No issues found across all tested flows.” The code downgrades known untested P0/P1 flows later, but cannot do so when discovery supplies none. Require scope/execution completeness for any release decision and label heuristic scores as uncalibrated until evaluated on independent cases.

## Correct the Fable plan before implementing it

1. **Prior failure is not causal exoneration.** A test that previously failed on master can also catch a newly introduced PR defect. Rename `not_pr_caused` to an observational outcome such as `observed_on_master`; history alone must not set `can_unblock=true`. The current Cursor prompt already makes this distinction at125–153. Preserve it when building deterministic APIs.
2. **Baseline-only string lookup is not sufficient identity protection.** PR-controlled titles/IDs can equal existing baseline names. Trusted canonical identity and ambiguity handling are necessary, as demonstrated by MM-T1.
3. **The plan's quarantine expiry is unsafe.** Automatically deleting a test because it was not fixed within a window is unsupported and conflicts with bug protection. Expiry should escalate/return to blocking under an explicit policy or require reviewed retirement. Quarantine does not reduce the raw failure rate when raw outcomes stay in its denominator; report blocking interruption separately from raw quality.
4. **Author actions are not ground-truth labels.** A rerun that passes may hide a PR race; a new push may be unrelated to the failure. Those are observable behavior proxies, not independent labels for wrong-green/false-red accuracy. Preserve unknown cases and record separately adjudicated labels, ideally with paired/reproducible evidence where feasible.
5. **The seven prompts contain stale premises.** Current ingestion, project keys, catalog-only migrations, baseline CLI, tests and manual waiver code make several a–g assertions obsolete. Recheck each against the assessed revision instead of reapplying old fixes. The broad goal remains incomplete despite the narrow demo.
6. **An E2E-caught product defect is not automatically an escaped defect.** C can produce useful discovered-defect events, but escaped defects per release needs release/deployment and escape evidence. The Jira list alone cannot establish that metric.
7. **A defect query and creation can race.** A live unresolved-label search correctly avoids stale mirrored resolution state, but simultaneous agents can both create a ticket. Atomic queue ownership/scope and a stored submission/idempotency strategy are still required. This is a proposed design concern, not a claim that current code has a Jira bug; the integration is not implemented.

## Minimal boundaries that avoid duplication

- **Mattermost CI** executes the existing suites and owns its status-writing privilege. Use its proven scoped maintainer workflow for current demos.
- **TSIO** stores complete raw observations, retry/run identities, evidence and baseline queries. It should be the only definition of failure/flake metrics. It does not currently own a complete attribution/verdict ledger, master queue or Jira workflow.
- **Cursor** publishes an evidence-linked diagnosis; its present conclusion is observational. It does not need a label token for the proven manual flow.
- **Impact Gate, proposed pilot** emits an advisory JSON plan explaining changed files, matched specs, unmatched files and mapping provenance. Feed that into the existing test dispatch only in shadow mode first, compare against full-suite outcomes and independently review missed failures. It must not write statuses, heal tests, quarantine tests or invent test correctness.

Reuse one existing deterministic planner and one existing execution path. Do not connect crew orchestration, prediction/calibration, browser release verdicts, autonomous healing and quarantine merely because those commands exist. They add independent assumptions and overlapping decision authorities before the basic integration contract is measured.

## What was verified and what was not

- This reviewer read the named source/acceptance paths and ran the bounded compiled helper probes described above. The parent ran Impact Gate's full existing test suite:469passed, plus the real-Mattermost/invalid-ref CLI probes.
- The TSIO staging execution and maintainer status transition are documented in `docs/automation/staging-demo-evidence.md`, including exact commit/run/digest, completeness, limitations, approval-before-write times and receipt. This reviewer did not rerun that live workflow.
- No source edits, new test execution against Mattermost, external communications, deployment, status changes, queue automation or ticket creation were performed by this reviewer.
- No dataset proves the under5% PR-block target, automatic wrong-green rate, reduced master flake rate, integration effort or guaranteed expected outcomes. Those goals remain to be measured, not declared achieved.
