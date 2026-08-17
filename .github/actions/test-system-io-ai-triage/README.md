# test-system-io-ai-triage

Classify a run's E2E failures as flakes or real regressions **without rerunning tests**.

Reruns are expensive (emulators, servers, wall clock). Three hundred identical failures must not become three hundred reruns or three hundred model calls. Cost scales with **distinct error signatures**, not failure count.

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
                ├─ get_history / get_failing_elsewhere
                ├─ get_screenshot (look at the UI before calling a flake)
                ├─ blame_commits (last_pass … failing_since) only for bugs
                └─ policy engine decides waiver (fail closed)
```

If the verdict is a bug and exactly one non-merge commit landed between the last pass and the first failure, the action names that commit and author. Wider ranges stay unnamed. Flakes never get an author.

The original `e2e-test/*` contexts are untouched. This posts `e2e-test/ai-triage`.

## Invariants

- **No rerun.** History, screenshots, and the PR diff are the evidence.
- **Cluster first.** Identical causes share one investigation.
- **Fail closed.** Missing evidence, a history error, no Anthropic key, or low confidence all stay red.
- **0.85 to waive.** A false red costs a glance; a false green ships a bug.
- **Two independent citations**, or in-run recovery (measurement).
- **PR diff overlap cannot be waived as a flake** — attribution is ambiguous.
- **MAIN / RELEASE never auto-waive.**
- **Amnesty is server-side.** A test that has already been waived too often stays red.
- **Blame is GitHub compare, not git bisect.** Name an author only when the suspect range is a single non-merge commit (max 8 commits shown as candidates).

## Inputs

| name | required | default | description |
|---|---|---|---|
| `composite-identity` | yes | — | Same JSON the begin action received. |
| `group-id` | no | — | Report-group UUID; composite identity is enough without it. |
| `baseline-branch` | no | `main` | Branch history is measured on. |
| `run-type` | no | `PR` | `PR` \| `MAIN` \| `RELEASE`. |
| `mode` | no | `shadow` | `shadow` never fails the job; `gate` exits non-zero when anything is unwaived. |
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
    run-type: PR
    mode: shadow
    github-token: ${{ secrets.GITHUB_TOKEN }}
    anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

Pin to a SHA. This action needs the `/api/v1/triage/evidence` endpoint from the AI-triage API (mattermost-test-system-io#101 and this follow-up).

## Develop

```sh
npm install
npm test
npm run lint
npm run tsc
npm run build   # tsup → dist/index.js (committed)
```
