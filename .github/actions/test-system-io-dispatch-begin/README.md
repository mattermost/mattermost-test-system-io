# test-system-io-dispatch-begin

GitHub composite-bundled JavaScript action that creates the orchestration queue for a Test System IO run.

Supports Playwright (default) and Cypress via the `framework` input. Walks the consumer repo's spec directory:

- `framework: playwright` — `<repo-dir>/<playwright-dir>/specs` (default `e2e-tests/playwright/specs`); collects every `*.spec.ts` (excluding `specs/visual/**` and `test_setup.ts`).
- `framework: cypress` — `<repo-dir>/<cypress-dir>/tests/integration` (default `e2e-tests/cypress/tests/integration`); collects every `*_spec.{ts,js}`.

Then posts the dispatch units to:

- `POST /api/v1/orchestration/begin` — registers the run + dispatch units keyed by composite identity.
- `POST /api/v1/reports/begin` — provisions the report group so per-shard uploads have somewhere to land.

Authentication is the calling workflow's GitHub Actions OIDC token. The workflow MUST grant `permissions: id-token: write`.

## Inputs

| name | required | default | description |
|---|---|---|---|
| `use-staging` | no | `false` | When `true`, target staging (`https://staging-test-io.test.mattermost.com`) instead of production (`https://test-io.test.mattermost.com`). |
| `oidc-audience` | no | `mattermost-test-system-io` | OIDC audience claim the orchestrator expects. |
| `composite-identity` | yes | — | JSON: `repository`, `commit_sha`, `gh_run_id`, `gh_run_attempt`, `name`, optional `branch` / `gh_pr_number`. |
| `framework` | no | `playwright` | Test framework this run dispatches. `playwright` or `cypress`. |
| `repo-dir` | yes | — | Path to the checked-out repo whose test suite this run covers (e.g. `mattermost/mattermost`). |
| `total-reports-expected` | yes | — | Number of per-shard reports the run will produce (== the worker matrix size). Frozen on first `/reports/begin`; the report group auto-finalizes once that many child reports reach `complete`. |
| `playwright-dir` | no | `e2e-tests/playwright` | Path to the Playwright project, relative to `repo-dir`. Ignored when `framework: cypress`. |
| `playwright-project` | no | `chrome` | Playwright project name passed through to workers. Ignored when `framework: cypress`. |
| `cypress-dir` | no | `e2e-tests/cypress` | Path to the Cypress project, relative to `repo-dir`. Spec discovery walks `<cypress-dir>/tests/integration/`. Only consulted when `framework: cypress`. |
| `retest-on-fail` | no | `false` | Whether the orchestrator should re-dispatch failed units once. |
| `retest-budget` | no | `1` | Max number of retest passes when `retest-on-fail` is true. |
| `idle-timeout-ms` | no | `600000` | Inactivity window before the orchestrator transitions an idle run to `timed_out`. Bumped on every checkout/complete. |
| `lease-timeout-ms` | no | `600000` | Per-lease ceiling before a stuck worker's units are reclaimed. |

## Outputs

| name | description |
|---|---|
| `run-id` | Server-assigned `uuidv7` for the run. |
| `total-units` | Number of dispatch units created. |

## Pinning a version

When using this action from another repository, prefer pinning to a full commit SHA over `@main`:

```yaml
uses: mattermost/mattermost-test-system-io/.github/actions/test-system-io-dispatch-begin@<40-char-sha>  # vX.Y.Z
```

`@main` tracks whatever lands on this branch, so a refactor here can change behavior in your CI without warning. Pinning a SHA freezes the action's source until you choose to update; Dependabot's `package-ecosystem: github-actions` opens a PR when a newer version is available — the same flow you'd use for any third-party action.

## Usage

```yaml
# Production (default)
- uses: ./.github/actions/test-system-io-dispatch-begin
  with:
    composite-identity: ${{ steps.identity.outputs.json }}
    repo-dir: ./mattermost

# Staging
- uses: ./.github/actions/test-system-io-dispatch-begin
  with:
    use-staging: "true"
    composite-identity: ${{ steps.identity.outputs.json }}
    repo-dir: ./mattermost
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
