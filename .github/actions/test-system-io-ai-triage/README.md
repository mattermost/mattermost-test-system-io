# test-system-io-ai-triage

Classify a run's E2E failures as flakes or real regressions **without rerunning tests**.

When every failure is a flake (or already failing on the baseline), **`mode: gate` flips the original `e2e-test/*` commit status to success** so the PR check goes green. Bugs stay red.

Reruns are expensive. Three hundred identical failures must not become three hundred reruns or three hundred model calls. Cost scales with **distinct error signatures**, not failure count.

```
GET /api/v1/triage/evidence
        │
        ├─ cluster by normalized error (UUIDs/hex/numbers stripped)
        │     300 "element not visible" → 1 cluster
        │
        ├─ in-run recovery (status=flaky)     → FLAKY_TEST, measured, no AI
        ├─ failing on baseline + other PRs    → MAIN_REGRESSION, not this PR, no AI
        ├─ historically flipping              → FLAKY_TEST candidate
        ├─ clean on baseline, isolated        → PR_REGRESSION candidate
        └─ residue (≤8 clusters)              → agent with TSIO tools
                │
                └─ policy decides waiver (fail closed)
                        │
                        ├─ all waived + mode: gate → original e2e-test/* = success
                        └─ anything unwaived       → original check stays red
```

If the verdict is a bug and exactly one non-merge commit landed between the last pass and the first failure, the action names that commit and author. Flakes never get an author.

This also posts `e2e-test/ai-triage`. Run it **after** `test-system-io-summary` so the original check exists to flip.

## Invariants

- **No rerun.** History, screenshots, and the PR diff are the evidence.
- **Cluster first.** Identical causes share one investigation.
- **Fail closed.** Missing evidence, a history error, no Anthropic key, or low confidence all stay red.
- **0.85 to waive.** A false red costs a glance; a false green ships a bug.
- **Two independent citations**, or in-run recovery (measurement).
- **PR diff overlap cannot be waived as a flake** — attribution is ambiguous.
- **RELEASE / `release-*` never auto-waive** (CMT stays fail-closed).
- **MAIN may auto-waive** confirmed flakes (same 0.85 / citations / amnesty bar as PR) so required `e2e-test/*` checks on `main` go green — needed for Create Release Branches, which pushes `release-*` from a main commit with no PR labels.
- **Amnesty is server-side.** A test that has already been waived too often stays red.
- **Blame is GitHub compare, not git bisect.** Name an author only when the suspect range is a single non-merge commit.
- **Green only what was classified.** `mode: shadow` never flips the merge-blocking row. `mode: gate` flips it only when every failure was waived.

## Inputs

| name | required | default | description |
|---|---|---|---|
| `composite-identity` | yes | — | Same JSON the begin action received. |
| `group-id` | no | — | Report-group UUID; composite identity is enough without it. |
| `baseline-branch` | no | `main` | Branch history is measured on. |
| `run-type` | no | `PR` | `PR` \| `MAIN` \| `RELEASE`. |
| `mode` | no | `shadow` | `shadow` observes; `gate` greens the original check on a flake and fails this job on a bug. |
| `original-commit-status-contexts` | no | — | Original `e2e-test/*` context(s) to flip. Empty = discover red `e2e-test/*` rows. |
| `use-staging` | no | `false` | Target staging. |
| `anthropic-api-key` | no | — | When unset, residue stays on the history suggestion (usually red). |
| `claude-model` | no | `claude-sonnet-4-6` | |
| `github-token` | no | `${{ github.token }}` | Status writes, PR file list, and blame compare. |
| `commit-status-context` | no | `e2e-test/ai-triage` | |

The calling job MUST grant `permissions: id-token: write` (ledger write) and `permissions: statuses: write` (commit status).

## Usage

```yaml
- uses: mattermost/mattermost-test-system-io/.github/actions/test-system-io-ai-triage@<sha>
  if: always()
  with:
    composite-identity: ${{ needs.begin.outputs.composite-identity }}
    original-commit-status-contexts: e2e-test/ios
    run-type: PR
    mode: gate
    github-token: ${{ secrets.GITHUB_TOKEN }}
    anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

Pin to a SHA. This action needs the `/api/v1/triage/evidence` endpoint from the AI-triage API (mattermost-test-system-io#101).

## Develop

```sh
npm install
npm test
npm run lint
npm run tsc
npm run build   # tsup → dist/index.js (committed)
```
