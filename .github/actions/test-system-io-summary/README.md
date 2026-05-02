# test-system-io-summary

GitHub composite-bundled JavaScript action that reads the orchestration status for a run and renders a Markdown summary to `$GITHUB_STEP_SUMMARY`. Also gates the workflow's exit code on the run's outcome.

This is the controller-after-workers step in an orchestrated CI matrix. The report group itself auto-finalizes server-side (count-based predicate against `total_reports_expected`); this action's job is purely:

1. `GET /api/v1/orchestration/status` — read counts (pass/fail/skipped/pending/leased) plus the run's terminal status.
2. Write a Markdown table + dashboard deep-link to `$GITHUB_STEP_SUMMARY`.
3. Exit non-zero if any unit ended in `completed_fail` or the run did not reach `completed`, unless `fail-on-test-failures: false` is set.

The calling workflow MUST grant `permissions: id-token: write`.

## Inputs

| name | required | default | description |
|---|---|---|---|
| `use-staging` | no | `false` | When `true`, target staging (`https://staging-test-io.test.mattermost.com`) instead of production (`https://test-io.test.mattermost.com`). |
| `oidc-audience` | no | `mattermost-test-system-io` | OIDC audience claim. |
| `composite-identity` | yes | — | Same JSON the begin action received. |
| `framework` | yes | — | Label rendered in the summary header (e.g. `playwright`, `cypress`). |
| `fail-on-test-failures` | no | `true` | When `true`, exit non-zero if any unit ended in `completed_fail` or the run did not reach `completed`. |

## Pinning a version

When using this action from another repository, prefer pinning to a full commit SHA over `@main`:

```yaml
uses: mattermost/mattermost-test-system-io/.github/actions/test-system-io-summary@<40-char-sha>  # vX.Y.Z
```

`@main` tracks whatever lands on this branch, so a refactor here can change behavior in your CI without warning. Pinning a SHA freezes the action's source until you choose to update; Dependabot's `package-ecosystem: github-actions` opens a PR when a newer version is available — the same flow you'd use for any third-party action.

## Usage

```yaml
# Production (default)
- uses: ./.github/actions/test-system-io-summary
  if: always()
  with:
    composite-identity: ${{ needs.begin.outputs.composite-identity }}
    framework: playwright

# Staging
- uses: ./.github/actions/test-system-io-summary
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
