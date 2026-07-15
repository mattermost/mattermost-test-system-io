# test-system-io-report-upload

GitHub composite-bundled JavaScript action that uploads one shard's test report to Test System IO without using the orchestration queue.

For workflows that produce test results some other way — their own `--shard=N/M` partitioning, an unrelated test runner, ad-hoc test suites, etc. — and just want artifacts to land on the dashboard.

This action does only the upload pipeline:

1. `POST /api/v1/reports/begin` (idempotent on composite identity).
2. `POST /api/v1/reports/register` declaring the JSON + screenshot manifest for this shard.
3. Multipart `POST /api/v1/reports/upload/<group>/<upload>/json`.
4. Multipart `POST /api/v1/reports/upload/<group>/<upload>/screenshots` (only when `screenshots-dir` is provided).

The report group auto-finalizes server-side once `total_reports_expected` shards have completed their uploads.

The calling workflow MUST grant `permissions: id-token: write`.

## When to use this vs. `test-system-io-dispatch-run`

Use this action when **you already have results** and just want them on the dashboard. Each shard runs this action independently after it finishes its own tests.

Use `test-system-io-dispatch-run` when you want the orchestrator to **assign work to you** — it leases specs from a shared queue, runs Playwright, and reports per-spec outcomes back. That action also includes this one's upload tail, so don't run both for the same shard.

## Inputs

| name | required | default | description |
|---|---|---|---|
| `use-staging` | no | `false` | When `true`, target staging (`https://staging-test-io.test.mattermost.com`) instead of production (`https://test-io.test.mattermost.com`). |
| `oidc-audience` | no | `mattermost-test-system-io` | OIDC audience claim. |
| `composite-identity` | yes | — | JSON: `repository`, `commit_sha`, `gh_run_id`, `gh_run_attempt`, `name`, optional `branch` / `gh_pr_number`. MUST match across every shard's call. |
| `total-reports-expected` | yes | — | Number of shards in the matrix. MUST match across every shard's call (mismatch returns 409 `EXPECTED_REPORTS_MISMATCH`). |
| `framework` | yes | — | Framework label (e.g. `playwright`, `cypress`). Selects the JSON parser. |
| `github-token` | yes | — | Token with `actions:read` — used to look up the matrix entry's `gh_job_id`. |
| `gh-job-name` | yes | — | Rendered matrix job name. MUST match the calling job's `name:` field. |
| `json-path` | yes | — | Path to the framework results JSON file produced by this shard. |
| `screenshots-dir` | no | `""` | Directory walked recursively for `.png` / `.jpg` / `.jpeg` files. Omit to skip screenshot upload. |

## Pinning a version

The examples below use `@main` for readability. For ongoing use, prefer pinning to a full commit SHA:

```yaml
uses: mattermost/mattermost-test-system-io/.github/actions/test-system-io-report-upload@<40-char-sha>  # vX.Y.Z
```

`@main` tracks whatever lands on this branch, so a refactor here can change behavior in your CI without warning. Pinning a SHA freezes the action's source until you choose to update; Dependabot's `package-ecosystem: github-actions` opens a PR when a newer version is available — the same flow you'd use for any third-party action.

## Usage

```yaml
jobs:
  shards:
    name: shard-${{ matrix.shard }}
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read
    strategy:
      fail-fast: false
      matrix:
        shard: [1, 2, 3, 4]
    steps:
      - uses: actions/checkout@v6

      # ... your test runner produces results.json + screenshots ...
      - name: Run tests
        run: npx playwright test --shard=${{ matrix.shard }}/4

      - name: Upload report
        if: always()
        uses: mattermost/mattermost-test-system-io/.github/actions/test-system-io-report-upload@main
        with:
          composite-identity: ${{ needs.compute-identity.outputs.json }}
          total-reports-expected: 4
          framework: playwright
          github-token: ${{ secrets.GITHUB_TOKEN }}
          gh-job-name: shard-${{ matrix.shard }}
          json-path: ./test-results/reporter/results.json
          screenshots-dir: ./test-results/output

  summary:
    needs: shards
    if: always()
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read
    steps:
      - uses: mattermost/mattermost-test-system-io/.github/actions/test-system-io-summary@main
        with:
          composite-identity: ${{ needs.compute-identity.outputs.json }}
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
