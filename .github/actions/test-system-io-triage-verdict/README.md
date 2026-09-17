# Test System IO triage verdict

Node 24 action for `/api/v1/triage/verdicts`. TSIO receives only an audience-bound
GitHub OIDC token. The GitHub token stays in the action and is used only against
GitHub's API. Build output is committed in `dist/index.js`.

```yaml
permissions:
  contents: read
  actions: read
  id-token: write
  statuses: write
  checks: write
  pull-requests: write
  issues: write
steps:
  - uses: mattermost/mattermost-test-system-io/.github/actions/test-system-io-triage-verdict@main
    id: triage
    with:
      composite-identity: ${{ needs.prepare.outputs.composite-identity }}
      context: e2e-test/playwright-full/enterprise
      github-token: ${{ github.token }}
      base-ref: master
      base-sha: ${{ needs.prepare.outputs.base-sha }}
      changed-files: ${{ needs.prepare.outputs.changed-files }}
      lane: enterprise
      # override-label: only for blanket-bypass labels (e.g. E2E/Override)
      anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }} # optional AI second judge
```

Use a released commit SHA in production after this action has landed. Every
reusable workflow caller must grant the permissions requested by its callee.

## Policy and results

The server controls the mode per repository/context/lane. `off` returns outputs
without making GitHub API calls. `shadow` creates only diagnostic artifacts and
never changes the required status, even if `post-commit-status` is explicitly
`true`. In `enforce`, `auto` (default) or `true` posts the required status;
`false` leaves it to the producer. SUCCESS and NEUTRAL are successful statuses;
all other verdicts fail. A blocking verdict exits nonzero only when status
posting is enabled. After a verdict is received, publication and override-audit errors are warnings;
the resolved verdict controls the exit. Verdict-fetch/configuration failures fail
the step; producers must not interpret missing outputs as an enforced success.

The action honors a live E2E/Override label (a blanket bypass the mobile label
manager re-applies on every push), the optional `override-label` (which must be
another blanket-bypass label, never a per-SHA verification label), and existing
successful `(verified)` manual status messages on this head SHA. E2E/Verified
and "E2E Tests/verified" are deliberately not read as live labels: they persist
on the PR after later pushes and would waive every subsequent commit.
Native mobile reset markers are preserved.
An explicit pending reset revokes older workflow-dispatch waivers. Overrides are
recorded through the authenticated override endpoint on a best-effort basis. An override
keeps the original `verdict` output for auditing but sets `status-state=success`
and `human-override=true`. Producers must check human override first.

A fresh workflow-run read rejects older `gh_run_attempt` publishers, including
another read immediately before the required-status mutation. GitHub status
writes have no compare-and-swap API: callers should keep their existing
per-PR concurrency/cancellation controls. Sticky comments also reject newer run
IDs/attempts found in their marker. No GitHub token is sent in any TSIO payload.

## Inputs and outputs

`action.yml` documents every input. The total wait defaults to 600000 ms; each
request asks for at most 60000 ms of long polling and refreshes OIDC. A 202 retries
within the total budget; 409 is a terminal INCOMPLETE verdict. Requests have timeouts
and refuse redirects.

The check is `e2e-triage/<context>` with failure annotations for blocking tests,
notice for exonerated tests, and warning for INFRA/INSUFFICIENT_DATA. Annotations
are batched in groups of 50. Check reruns reuse the run/attempt/context external
ID. PR comments are upserted by `<!-- tsio-triage:<context> -->` and only bot-owned
comments are changed. `post-check-run` and `post-pr-comment` default to `true`.

Outputs: `verdict`, `engine-verdict`, `adjudicated`, `mode`, `confidence`,
`verdict-url`, `status-state`, `status-description`, `blocking-count`,
`exonerated-count`, `human-override`.

## AI second judge

With `anthropic-api-key` set and a PR number in the composite identity, the
action fetches the per-finding evidence packs from TSIO
(`GET /triage/verdicts/{id}/evidence`, OIDC), adds what only GitHub knows (PR
title, changed files, diff hunks of the files named in the error, via
`compareCommitsWithBasehead` from `base-sha` to the head), and asks the model
(`adjudicator-model`, default `claude-haiku-4-5`, structured JSON output, four
findings in flight) what caused each borderline failure. The decision matrix in
`docs/triage.md` is applied in `src/adjudicate.ts`; the result is recorded on the
verdict (`POST /triage/verdicts/{id}/adjudication`) and the final verdict drives
the status, check run, PR comment and outputs. `INFRA`, `NEW_TEST` and
`OWNED_BY_PR` on the failing spec are never adjudicated. An outage, a timeout or
an invalid answer leaves the engine verdict in place (`adjudicated=false`).

Secret boundaries: the Anthropic key is sent only to `api.anthropic.com`; the
GitHub token is used only against GitHub; TSIO sees only the OIDC token. The
evidence sent to the model includes test error text and diff hunks of the PR.
The Messages API call goes through `src/anthropic.ts`, a small fetch client
(structured output, prompt caching, retries on 429/5xx), not the SDK, so the
committed bundle stays small.

## Backtest harness (`src/backtest/`)

TypeScript scripts run with `tsx`; they import the same `adjudicate.ts` the
action ships, so what the backtest scores is what runs in CI. See
`docs/triage.md` ("Backtesting" and "Second judge").

```sh
npm run backtest -- --repository mattermost/mattermost-mobile --since 2026-08-01T00:00:00Z --out backtest.md
npm run refine-truth -- backtest.json backtest.refined.json
npm run adjudicate-offline -- --backtest backtest.refined.json --gh-cache .triage-backtest-cache.json --out adjudicated.json
npm run adjudicate-worker -- packs.jsonl responses.jsonl   # where ANTHROPIC_API_KEY lives
```

```sh
npm ci
npm run tsc
npm test
npm run lint
npm run format:check
npm run build
```
