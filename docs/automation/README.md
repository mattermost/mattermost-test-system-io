# Cursor E2E diagnosis and maintainer verification

The [replacement prompt](cursor-e2e-diagnosis.md) uses staging and publishes a
Cursor diagnosis. **It needs no `GH_LABEL_TOKEN` or administrator change.** Cursor
reads evidence and posts its normal PR review; it does not change statuses.

A maintainer reviews that durable diagnosis and manually dispatches Mattermost's
existing `.github/workflows/e2e-tests-override-status.yml`. The scoped workflow
uses its repository `GITHUB_TOKEN` to write statuses. Its inputs bind the assessed
SHA, PR, Cursor review/comment ID and body hash, exact context/status IDs, source
run/attempts, and the maintainer's reason. It stores the approval and a copy of
the diagnosis before mutation, then verifies the resulting exact statuses.

## Staging setup and demo

1. Deploy the reviewed TSIO revision to staging and verify the JSON API contract.
2. Replace the Cursor automation prompt with `cursor-e2e-diagnosis.md` in full.
   Remove the old automatic label command and token requirement.
3. Run PR #38356 against staging using the reviewed action revision. Cursor
   diagnoses the completed run and posts a review with its machine-readable
   `TSIO_E2E_DIAGNOSIS_V1` binding footer.
4. Inspect the actual diagnosis and unresolved limitations. Obtain its immutable
   review/comment ID and SHA-256 of its full body. Select only the exact failed
   full-test statuses the maintainer chooses to approve.
5. Dispatch the existing `E2E Tests - Override Status` workflow from the reviewed
   branch with those values and an explicit approval reason. The workflow file
   already exists on the default branch; GitHub permits selecting a dispatch ref.
   Changed branch input acceptance is established only when dispatch succeeds.
6. Verify the workflow's durable approval comment and status receipt on the exact
   assessed SHA. Describe the demo as **Cursor diagnosis followed by maintainer
   verification**, not an autonomous flake classifier turning the PR green.

The existing `E2E Tests/verified` label remains a legacy manual maintainer action
with broader behavior. The scoped dispatch avoids applying it. No new Cursor
secret or extra personal token is needed for this flow.

The [GitHub manual workflow documentation](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow)
requires a workflow file on the default branch and supports choosing another
branch. The existing override workflow is registered as workflow ID `229416453`;
its default-branch dispatch is present. Do not merge code just to try the demo.

## What the approval means

The workflow validates authorship and scope, not the causal correctness of the
Cursor assessment. Incomplete evidence and unavailable reproductions remain in
the copied diagnosis. The maintainer's explicit reason records why they accepted
that uncertainty. No numerical confidence or unperformed reproduction is needed
for deliberate human approval. Raw CI outcomes and TSIO test observations remain
unchanged and available for review.

GitHub's [commit-status API](https://docs.github.com/en/rest/commits/statuses#create-a-commit-status)
does not provide an atomic conditional write against a workflow run attempt. The
helper rechecks head, original status ID, source run/attempt and diagnosis body
before writing to the immutable SHA. If it detects a same-SHA rerun after a write,
it restores failure and reports invalidation. A brief write race or restoration
API outage remains possible; it is not an atomic transaction across GitHub objects.

## Optional future autonomous mode

`apply-e2e-waiver.mjs` and its tests are retained as a separate strict automatic
prototype. The current Cursor prompt does **not** invoke it. It requires an
isolated privileged token, complete evidence, fresh trusted baseline, paired
immutable-image reproduction for every failing key, and no diff suspicion.
Do not enable it by merely exposing a token to the PR runner.

Run its local regression checks with:

```sh
node --test docs/automation/apply-e2e-waiver.test.mjs
```

These tests establish mocked API behavior, not deployment, token isolation,
classifier accuracy or a live automatic waiver. A future autonomous rollout needs
its own reviewed activation and observed success receipt.
