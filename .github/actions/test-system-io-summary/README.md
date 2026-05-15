# test-system-io-summary

GitHub composite-bundled JavaScript action that reads the orchestration status for a run and renders a Markdown summary to `$GITHUB_STEP_SUMMARY`. Also gates the workflow's exit code on the run's outcome.

This is the controller-after-workers step in an orchestrated CI matrix. The report group itself auto-finalizes server-side (count-based predicate against `total_reports_expected`); this action's job is purely:

1. `GET /api/v1/orchestration/status` — read counts (pass/fail/skipped/pending/leased) plus the run's terminal status.
2. Write a Markdown table + dashboard deep-link to `$GITHUB_STEP_SUMMARY`.
3. Exit non-zero if any unit ended in `completed_fail` or the run did not reach `completed`, unless `fail-on-test-failures: false` is set.

The calling workflow MUST grant `permissions: id-token: write`. When `update-commit-status: 'true'` is set, the calling job MUST also grant `permissions: statuses: write`.

Optionally flips the `pending` GitHub commit status the begin action pushed to `success`/`failure`/`error` on the same commit + context, with `target_url` pointing at the Test System IO report page. Set `update-commit-status: 'true'` plus `commit-status-context` + `github-token`.

## Inputs

| name | required | default | description |
|---|---|---|---|
| `use-staging` | no | `false` | When `true`, target staging (`https://staging-test-io.test.mattermost.com`) instead of production (`https://test-io.test.mattermost.com`). |
| `oidc-audience` | no | `mattermost-test-system-io` | OIDC audience claim. |
| `composite-identity` | yes | — | Same JSON the begin action received. |
| `framework` | yes | — | Label rendered in the summary header (e.g. `playwright`, `cypress`). |
| `commit-status-context` | yes | — | Slash-separated context label used as the commit-status `context` and in rendered summaries/webhooks. Must match the begin action value. |
| `fail-on-test-failures` | no | `true` | When `true`, exit non-zero if any unit ended in `completed_fail` or the run did not reach `completed`. |
| `update-commit-status` | no | `true` | When `true` (default), flip the begin action's `pending` commit status to terminal state (`success`/`failure`/`error`). Requires `commit-status-context` + `github-token` and `permissions: statuses: write` on the job. Set `false` to opt out. |
| `github-token` | no | `""` | GitHub token with `statuses: write` scope. Required only when `update-commit-status` is `true`. |

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
