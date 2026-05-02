# test-system-io-dispatch-run

GitHub composite-bundled JavaScript action that drains a single matrix entry's slice of the orchestration dispatch queue.

For each lease, the action:

1. `POST /api/v1/orchestration/checkout` to lease a spec (or sleep on a non-empty retest pool).
2. Shells out to `npx playwright test --project=<playwright-project> --grep-invert @visual --no-deps <specs>` inside `<repo-dir>/<playwright-dir>` (default `e2e-tests/playwright`), and reads results from `<playwright-dir>/<results-dir>` (default `results`).
3. Archives the per-iteration `results/` dir and parses the Playwright reporter JSON, applying flaky-aware aggregation (a test that passes after a `--retries` recovery is `flaky`, not `failed`).
4. `POST /api/v1/orchestration/complete` with the per-spec outcome.

At queue-empty, the accumulated invocations are uploaded as one shard:

- `POST /api/v1/reports/register` to register the JSON + screenshot manifest. The dispatch-begin action created the report group earlier in the run, so the worker doesn't call `/reports/begin`; the register response carries the report-group UUID needed for the upload URLs.
- Multipart `POST /api/v1/reports/upload/<group>/<upload>/{json,screenshots}`.

OIDC tokens are minted on demand and cached for 5 minutes; HTTP 401 invalidates the cache and retries once. Transient network errors back off exponentially.

The calling workflow MUST grant `permissions: id-token: write`.

## Inputs

| name | required | default | description |
|---|---|---|---|
| `use-staging` | no | `false` | When `true`, target staging (`https://staging-test-io.test.mattermost.com`) instead of production (`https://test-io.test.mattermost.com`). |
| `oidc-audience` | no | `mattermost-test-system-io` | OIDC audience claim. |
| `composite-identity` | yes | — | Same JSON the dispatch-begin action received. |
| `repo-dir` | yes | — | Path to the checked-out repo whose Playwright suite this run covers (e.g. `mattermost/mattermost`), with the e2e stack already up. |
| `playwright-dir` | no | `e2e-tests/playwright` | Path to the Playwright project, relative to `repo-dir`. Override for repos that don't use the mattermost/mattermost layout. |
| `results-dir` | no | `results` | Path to Playwright's results output, relative to `playwright-dir`. Must match the `outputDir` configured in the consumer repo's `playwright.config.ts`. |
| `artifacts-root` | yes | — | Writable directory for per-iteration archived results. |
| `github-token` | yes | — | Token with `actions:read` — used to look up `gh_job_id` from the rendered job name. |
| `gh-job-name` | yes | — | Rendered matrix job name (e.g. `orch-worker-3`). MUST match the calling job's `name:` field. |
| `playwright-retries` | no | `1` | Value passed to `npx playwright test --retries=N`. Set to `0` to disable Playwright-internal retries. |

## Why `gh-job-name`

`GITHUB_JOB` carries the workflow-file job key, not the matrix-rendered name. The orchestrator keys leases on the unique numeric `gh_job_id`, so the action resolves it by matching `gh-job-name` against `listJobsForWorkflowRunAttempt`.

## Usage

```yaml
# Production (default)
- uses: ./.github/actions/test-system-io-dispatch-run
  with:
    composite-identity: ${{ needs.begin.outputs.composite-identity }}
    repo-dir: ./mattermost
    artifacts-root: ${{ runner.temp }}/test-system-io
    github-token: ${{ secrets.GITHUB_TOKEN }}
    gh-job-name: ${{ matrix.name }}

# Staging — set use-staging: "true"
- uses: ./.github/actions/test-system-io-dispatch-run
  with:
    use-staging: "true"
    composite-identity: ${{ needs.begin.outputs.composite-identity }}
    repo-dir: ./mattermost
    artifacts-root: ${{ runner.temp }}/test-system-io
    github-token: ${{ secrets.GITHUB_TOKEN }}
    gh-job-name: ${{ matrix.name }}
```

## Develop

```sh
npm install
npm run lint     # oxlint
npm run tsc      # type check (no emit)
npm run build    # tsup → dist/index.js (committed)
npm run format   # oxfmt
```

The `dist/` bundle is committed because GitHub Actions executes the artifact directly.
