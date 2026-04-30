# test-system-io-reports-complete

GitHub composite-bundled JavaScript action that finalizes the report group and writes the run's job summary.

Framework-agnostic: works for any test suite (Playwright, Cypress, …) that registered with `/reports/begin` under the same composite identity. Per-shard report uploads happen in the worker; this action runs once after every worker is done and just:

1. `POST /api/v1/reports/complete` (idempotent on composite identity) — flips the report group from `in_progress` to `completed`.
2. `GET /api/v1/orchestration/status` — reads counts (pass/fail/skipped/pending/leased) plus the run's terminal status.
3. Writes a Markdown table + deep link to `$GITHUB_STEP_SUMMARY`.

It exits non-zero when any unit ended in `completed_fail` or the run did not reach `completed`, unless `fail-on-test-failures: false` is set.

The calling workflow MUST grant `permissions: id-token: write`.

## Why this is loud on `/reports/complete` failure

The server-side per-report auto-finalize is the safety net for stuck `processing` rows, but a missed `/reports/complete` still leaves the report group at `in_progress` until every shard's upload pipeline lands. Surfacing the error in CI prevents the next investigation from having to dig through staging API to discover that this step silently dropped the ball.

## Inputs

| name | required | default | description |
|---|---|---|---|
| `use-staging` | no | `false` | When `true`, target staging (`https://staging-test-io.test.mattermost.com`) instead of production (`https://test-io.test.mattermost.com`). |
| `oidc-audience` | no | `mattermost-test-system-io` | OIDC audience claim. |
| `composite-identity` | yes | — | Same JSON the begin action received. |
| `framework` | yes | — | Test framework label (e.g. `playwright`, `cypress`). MUST match what the begin action registered with. |
| `fail-on-test-failures` | no | `true` | When `true`, exit non-zero if any unit ended in `completed_fail` or the run did not reach `completed`. |

## Usage

```yaml
# Production (default)
- uses: ./.github/actions/test-system-io-reports-complete
  if: always()
  with:
    composite-identity: ${{ needs.begin.outputs.composite-identity }}
    framework: playwright

# Staging
- uses: ./.github/actions/test-system-io-reports-complete
  if: always()
  with:
    use-staging: "true"
    composite-identity: ${{ needs.begin.outputs.composite-identity }}
    framework: playwright
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
