# test-system-io-ai-triage

Classify a run's E2E failures as flakes or real regressions **without rerunning tests**.

Reruns are expensive (emulators, servers, wall clock). The history APIs already answer "was this failing on main?". Screenshots, the error, and the PR diff are what a human looks at. This action uses those, then optionally asks Claude to read the screenshots for the residue history cannot decide.

```
GET /api/v1/triage/evidence
        │
        ├─ in-run recovery (status=flaky)     → FLAKY_TEST, measured
        ├─ failing on baseline + other PRs    → MAIN_REGRESSION, not this PR
        ├─ historically flipping              → FLAKY_TEST, confirm with screenshots
        ├─ clean on baseline, isolated        → PR_REGRESSION candidate
        └─ everything else                    → Claude + screenshots + diff
                │
                └─ policy engine decides waiver (fail closed)
```

The original `e2e-test/*` contexts are untouched. This posts `e2e-test/ai-triage`.

## Invariants

- **Fail closed.** Missing evidence, a history error, no Anthropic key, or low confidence all stay red.
- **0.85 to waive.** A false red costs a glance; a false green ships a bug.
- **Two independent citations**, or in-run recovery (measurement).
- **PR diff overlap cannot be waived as a flake** — attribution is ambiguous.
- **MAIN / RELEASE never auto-waive.**
- **Amnesty is server-side.** A test that has already been waived too often stays red.

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
| `github-token` | no | `${{ github.token }}` | Status writes + PR file list. |
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
