# test-system-io-dispatch-begin

GitHub composite-bundled JavaScript action that creates the orchestration queue for a Test System IO run.

Supports Playwright (default) and Cypress via the `framework` input. Walks the consumer repo's spec directory:

- `framework: playwright` — `<repo-dir>/<playwright-dir>/specs` (default `e2e-tests/playwright/specs`); collects every `*.spec.ts` (excluding `specs/visual/**` and `test_setup.ts`).
- `framework: cypress` — `<repo-dir>/<cypress-dir>/tests/integration` (default `e2e-tests/cypress/tests/integration`); collects every `*_spec.{ts,js}`. Each spec's `// Stage:` and `// Group:` header tags drive optional include / exclude / sort-first / sort-last filtering — see the cypress-* inputs below.

Then posts the dispatch units to:

- `POST /api/v1/orchestration/begin` — registers the run + dispatch units keyed by composite identity.
- `POST /api/v1/reports/begin` — provisions the report group so per-shard uploads have somewhere to land.

Authentication is the calling workflow's GitHub Actions OIDC token. The workflow MUST grant `permissions: id-token: write`.

Optionally pushes a `pending` GitHub commit status whose `target_url` deep-links to the Test System IO report page, so reviewers click the commit-status row and land on the live dashboard instead of a PR comment. Set `post-pending-commit-status: 'true'` plus `commit-status-context` + `github-token` (and add `permissions: statuses: write` to the calling job).

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
| `cypress-stage` | no | `@prod` | Comma-separated `// Stage:` tags. Spec kept only if its Stage line shares any tag. Empty disables. Cypress only. |
| `cypress-include-group` | no | `""` | Comma-separated `// Group:` tags. Spec kept only if its Group line shares at least one tag. Empty disables. Cypress only. |
| `cypress-exclude-group` | no | `""` | Comma-separated `// Group:` tags. Spec dropped if its Group line shares any tag. Applied after include. Cypress only. |
| `cypress-skip-on` | no | `""` | Comma-separated active-environment tags (e.g. `@headless`). Spec dropped if its `// Skip:` line shares any tag. Cypress only. |
| `cypress-sort-first` | no | `""` | Comma-separated `// Group:` tags. Surviving specs whose Group shares any tag dispatch first. A spec matching both sort-first and sort-last goes to sort-first. Cypress only. |
| `cypress-sort-last` | no | `@known_issue` | Comma-separated `// Group:` tags. Surviving specs whose Group shares any tag dispatch last. Cypress only. |
| `retest-on-fail` | no | `false` | Whether the orchestrator should re-dispatch failed units once. |
| `retest-budget` | no | `1` | Max number of retest passes when `retest-on-fail` is true. |
| `idle-timeout-ms` | no | `600000` | Inactivity window before the orchestrator transitions an idle run to `timed_out`. Bumped on every checkout/complete. |
| `lease-timeout-ms` | no | `600000` | Per-lease ceiling before a stuck worker's units are reclaimed. |
| `post-pending-commit-status` | no | `true` | When `true` (default), push a `pending` GitHub commit status whose `target_url` deep-links to the Test System IO report page. Requires `commit-status-context` + `github-token` and `permissions: statuses: write` on the job. Set `false` to opt out. |
| `github-token` | no | `""` | GitHub token with `statuses: write` scope. Required only when `post-pending-commit-status` is `true`. |
| `commit-status-context` | no | `""` | Commit-status context (e.g. `e2e-test/playwright-full/enterprise`). Required when `post-pending-commit-status` is `true`. Must match the value the finalizer (`test-system-io-summary`) uses. |
| `image-tag` | no | `""` | Server image tag rendered in the commit-status description (e.g. `master`, `9e955bf_3521709`). Surfaced as `tests running, image_tag:<tag>`. |
| `image-aliases` | no | `""` | Optional comma-separated alias suffix appended to `image_tag` (e.g. `release-11.4, release-11`). |

## Outputs

| name | description |
|---|---|
| `run-id` | Server-assigned `uuidv7` for the run. |
| `total-units` | Number of dispatch units created. |
| `report-url` | Test System IO report URL for this run (same value used as commit-status `target_url`). |

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
