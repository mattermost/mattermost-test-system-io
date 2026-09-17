# Triage implementation delivery

All changes remain local on `codex/flaky-test-quarantine`. No commits, pushes, deployments, GitHub variable changes, or remote status/check/comment writes were performed during implementation.

## Deliverables

1. Seven reversible migrations; shared identity normalization/enrichment; ingest and completion transaction hooks; idempotent backfill CLI; normalization/concurrency/PostgreSQL tests.
2. Rolling health worker, completion subscription, recovery/nightly refresh, audited auto-quarantine lifecycle and context-specific lane alarm.
3. Pure versioned verdict engine, ordered rules, probability/chronology/cluster/area/infra guards, confidence, deterministic audit hashing and markdown.
4. Authenticated/admin triage API, public reads, full OpenAPI contracts, long polling, four quarantine feeds and event publishing.
5. Health, identity and verdict UI routes with filters, sparklines, timeline, audit details and admin quarantine controls.
6. OIDC triage action, rebuilt distribution, metadata forwarding actions, manual override handling and unit tests.
7. Separate mobile and webapp producer patches under `triage-producer-patches/`, verified against unchanged local producer checkouts.
8. Operational runbook in `triage.md`, verification transcript and full-file delivery artifact.

## Review fixes (2026-09-17)

Applied after the independent review of this delivery:

- `internal/config/config_test.go`: `TestLoad_rejectsZeroTriageWindow` sets the required `TSIO_*` env vars, so `make test-server` passes in a clean shell and in CI.
- `internal/triage/verdict/classify.go`: flake confidence is evidence sufficiency `runs/(runs+1)` instead of `1 − r̂^m`; the 48-hour freshness decay is removed (the stale guard alone enforces freshness); a non-infrastructure blocking finding yields `FAILURE` even alongside infra stubs; PR ownership matches full paths, not basenames. `EngineVersion` is `triage-3`.
- `internal/triage/verdict/guards.go`: findings whose failure already fuzzy-matches trunk's dominant failure are never relabeled `REGRESSION_CLUSTER`.
- `internal/triage/health/classify.go`: `healthy` uses the raw rate, making auto-release attainable at the 30-run cap.
- `internal/triage/verdict/store.go`, `api/triage/handlers.go`: verdict listing is one query.
- Web: `TriageHealthRow` (identity endpoint) split from `TriageHealth` (list endpoint); verdict test fixture uses the full DTO.
- Action: only a live `E2E/Override` (or `override-label`) is honored; `E2E/Verified` / `E2E Tests/verified` count only via the per-SHA `(verified)` status marker; 502/503/504 from TSIO are retried with backoff.
- Producer patches: shadow/off required-status path is byte-for-byte the pre-triage path; enforce-only live checks; `issues` permission stays `read`; action refs use the `@TSIO_TRIAGE_RELEASE_SHA` placeholder instead of `@main`; the webapp finalizer no longer reads labels live.

## Backtest-driven engine changes (2026-09-17, engine `triage-4`)

Findings from replaying 30 days of production PR runs (`npm run backtest` in the triage action):

- Quarantine entries are now filtered by `created_at <= as_of` so replays cannot see entries opened later (they leaked into historical verdicts).
- The area rule compares against the whole evidence window instead of the last ten trunk runs; the old form contradicted `FLAKY_CONFIRMED` for tests that flaked earlier in the window.
- New `FLAKY_CROSS_PR` rule and `cross_pr_min_prs` threshold (default 3): a test that passes on trunk but fails on ≥3 other PRs in the window is flaky in the PR environment. Signatures recurring on ≥2 other PRs are not novel clusters. Without this rule the engine withheld on most human-waived mobile runs because mobile trunk runs only ~10 times per lane per fortnight and never show the PR-environment flakes.
- `identity.InferLane` now understands the real producer names: trunk/release groups are named `playwright-full-enterprise-master`, `-release`, `-release-cut` and upgrade lanes `-enterprise-upgrade-from-release-11.8`. Before this fix every webapp trunk observation landed in lane `''` while PR runs were `enterprise`, so every webapp verdict was `INSUFFICIENT_DATA`. Upgrade variants stay a separate lane. Existing rows need `tsioctl db backfill-identities` or the SQL in `docs/triage-verification.md` after deploying.
- `triage.Store.AllObservations` pages by 20,000 rows (was 1,000): the per-identity ranking CTE is re-evaluated per page and a webapp lane holds ~36k trunk observations, so one verdict cost ~36 CTE scans (about two minutes); now two.
- `identity.persistBatch`: one identity upsert per shard plus two statements per case (was five round trips per case); ~4× faster ingest under the per-repository advisory lock. Semantics unchanged; covered by the identity/triage e2e suites.

## Backtest tooling (2026-09-17)

- `POST /triage/verdicts` gains admin-only `as_of` replay (never persisted, nil UUID id, rejects `wait_for_completion_ms`). Covered by `TestAsOfReplayUsesOnlyEvidenceThatExisted`.
- `tsioctl db import-remote`: seeds a database from another TSIO's public API through the real `ingest.Consolidate` path (so identities/observations are derived exactly as for live uploads).
- Backtest harness (`.github/actions/test-system-io-triage-verdict/src/backtest/`): GitHub ground truth via `gh` + `as_of` replays → agreement report; truth refinement; offline adjudicator scoring and a CI worker. TypeScript, sharing the action's code.

## Blast radius of every modified existing file

| Existing file | Change and effect |
| --- | --- |
| `.github/actions/test-system-io-dispatch-begin/dist/index.js` | Rebuilt distributable for the metadata forwarding change; existing action consumers execute this bundle. |
| `.github/actions/test-system-io-dispatch-begin/src/main.ts` | Forwards optional branch kind, merge base and lane metadata at orchestration begin. Existing inputs remain compatible. |
| `.github/actions/test-system-io-report-upload/dist/index.js` | Rebuilt upload action bundle; affects consumers after they reference this revision. |
| `.github/actions/test-system-io-report-upload/src/types.ts` | Adds optional identity metadata fields to the upload action contract. |
| `.github/actions/test-system-io-report-upload/src/upload.ts` | Forwards optional metadata at report registration; retains report count and existing upload flow. |
| `.github/workflows/ci.yml` | Adds new action to the action checks matrix and runs optional action unit-test scripts. |
| `Makefile` | Adds new action install/typecheck/format/lint/test/build to make ci; increases local CI dependency and runtime requirements. |
| `apps/server/api/openapi.yaml` | Documents and validates triage routes/DTOs, metadata fields, and stateless begin/register schemas. Public reads stay public; writes reuse existing authentication. |
| `apps/server/cmd/tsio/main.go` | Starts and stops the in-process health worker with shared database/event hub and configured defaults. |
| `apps/server/cmd/tsioctl/db/db.go` | Registers the idempotent backfill-identities database subcommand. |
| `apps/server/internal/api/orchestration/handlers.go` | Accepts and validates additive branch/base/lane metadata on orchestration begin. |
| `apps/server/internal/api/reports/stateless.go` | Validates and persists additive metadata at begin/register; emits group completion after successful finalization so worker refreshes promptly. |
| `apps/server/internal/config/config.go` | Loads TRIAGE_* threshold defaults and validates ranges at startup. |
| `apps/server/internal/config/config_test.go` | Isolates config tests from a developer .env file using temporary working directories; also covers invalid triage defaults. |
| `apps/server/internal/ingest/consolidate.go` | Persists extracted error stack and links identities/facts inside the existing insert transaction. Enrichment errors roll back ingestion. |
| `apps/server/internal/ingest/playwright.go` | Preserves Playwright error stacks for failure-locus extraction. |
| `apps/server/internal/ingest/types.go` | Adds optional ErrorStack to the internal extracted-case DTO. |
| `apps/server/internal/orchestration/complete.go` | Enriches committed per-test completion evidence within the same lease transaction. Duplicate completion remains idempotent. |
| `apps/server/internal/orchestration/runs.go` | Persists optional metadata on the seeded report group without replacing the existing composite identity. |
| `apps/server/internal/server/server.go` | Wires triage stores/handlers and registers public/authenticated routes using existing auth. |
| `apps/server/tests/e2e/testenv/testenv.go` | Sets a test-only admin key for privileged API integration coverage. |
| `apps/web/src/app.tsx` | Adds Triage navigation and three routes; existing report routes remain available. |

## New-file blast radius

- Migrations add six triage tables and nullable metadata links to existing report/case tables. They require PostgreSQL 18 uuidv7(), matching the existing database version. Down migrations remove triage data and links; they are destructive to the new feature history.
- Identity writes add transaction work and a repository/framework advisory lock. Concurrent shard writes are serialized only for shared identity enrichment.
- Health refresh uses bounded recent group histories and per-lane advisory locks. It may move an enforced context to shadow when the configured unhealthy ratio is exceeded.
- Verdicts and observations add persistent audit storage. Existing group deletion cascades through the new tables as specified.
- New admin controls can waive test failures. Default shadow mode leaves required statuses raw; enforcement requires a per-context policy change.
- Producer patches add GitHub checks/comment/status permissions to the affected reusable-workflow call chains. They use the existing workflow token only for GitHub and keep OIDC separate for TSIO.
- Runner skipping is disabled by default. The optional Playwright helper has no effect unless its dedicated environment flag is enabled.

## Retained specification constraints

- At the default 30-run cap, the specified Laplace-smoothed rate cannot reach the healthy cutoff below 0.02. Automatic release therefore needs a larger configured sample (at least 49 all-pass runs) or a manual release. See the runbook.
- Chronology uses report creation order and an exact base-SHA pivot when available. TSIO does not establish Git ancestry independently.
- Policy keys are per context; health/quarantine keys are per identity, lane and base branch. Contexts sharing those dimensions should use compatible projection thresholds.
- Before receiving a valid verdict, malformed inputs/network errors/timeouts fail conservatively because the API pending response contains no policy mode. Optional publishing failures after a valid verdict are warnings; resolved enforced blocking decisions fail the action.
- Local validation does not replace the required two-week shadow agreement period or a deployed producer workflow smoke test.

## Verification

The final CI transcript and full file-content artifact are linked in the delivery response. Producer patch validation is documented in `triage-producer-patches/README.md`.

## Producer file blast radius (patches only)

### mattermost-mobile

| File | Effect |
| --- | --- |
| `.github/workflows/compatibility-matrix-testing.yml` | Propagates required checks/comments/actions/OIDC permissions through the reusable workflow call chain. |
| `.github/workflows/e2e-android-template.yml` | Captures merge base and changed files, forwards lane metadata, invokes triage after upload, and passes mode/override/status and notification outputs to finalizers. |
| `.github/workflows/e2e-detox-pr.yml` | Propagates required checks/comments/actions/OIDC permissions through the reusable workflow call chain. |
| `.github/workflows/e2e-detox.yml` | Propagates required checks/comments/actions/OIDC permissions through the reusable workflow call chain. |
| `.github/workflows/e2e-ios-template.yml` | Captures merge base and changed files, forwards lane metadata, invokes triage after upload, and passes mode/override/status and notification outputs to finalizers. |
| `detox/save_report.js` | Adds triage details to the existing webhook attachment using action outputs; destination stays unchanged. |
| `detox/utils/build-tsio-job-config.js` | Adds inferred branch/base/lane metadata to per-platform composite identities; preserves one report per platform. |
| `detox/utils/resolve-tsio-job-config.sh` | Applies newly computed metadata to already-expanded job configurations before returning them. |
| `detox/utils/tsio-report-status.js` | Keeps live human overrides first, then uses triage output in enforce and raw reports in shadow/off; checks latest run attempt and preserves native reset markers. |
| `detox/utils/tsio-triage-status.test.js` | New regression coverage for finalization, metadata or manual override behavior. |
### mattermost

| File | Effect |
| --- | --- |
| `.github/scripts/tsio-assert-results.test.sh` | New regression coverage for finalization, metadata or manual override behavior. |
| `.github/scripts/tsio-finalize-status.mjs` | New raw-status finalizer for off/shadow with live manual override and run-attempt protection. |
| `.github/workflows/e2e-tests-ci.yml` | Propagates required checks/comments/actions/OIDC permissions through the reusable workflow call chain. |
| `.github/workflows/e2e-tests-on-merge.yml` | Propagates required checks/comments/actions/OIDC permissions through the reusable workflow call chain. |
| `.github/workflows/e2e-tests-on-release.yml` | Propagates required checks/comments/actions/OIDC permissions through the reusable workflow call chain. |
| `.github/workflows/e2e-tests-playwright-template.yml` | Adds prepare-run metadata, triage after summary, explicit enforce/raw assertion and guarded status finalizer; enriches existing webhook after triage. |
| `.github/workflows/e2e-tests-playwright.yml` | Propagates required checks/comments/actions/OIDC permissions through the reusable workflow call chain. |
| `e2e-tests/playwright/lib/quarantine.ts` | New opt-in environment-to-grepInvert helper with invalid-regex validation. |
| `e2e-tests/playwright/playwright.config.ts` | Wires optional quarantine grep inversion only when explicitly enabled; default test selection remains unchanged. |

## AI second judge in production (2026-09-17)

- Migration `000034_pr_verdicts_adjudication`: `pr_verdicts.adjudication jsonb` (reversible).
- `internal/triage/verdict/evidence.go`: `EvidencePacks` builds one pack per adjudicable finding (full error from `test_cases`, trunk window stats, cross-PR failures within the verdict's window, other failures in the run); `Adjudication.Validate` and `RecordAdjudication`.
- API: `GET /triage/verdicts/{id}/evidence` and `POST /triage/verdicts/{id}/adjudication` (OIDC repository-bound or admin; validated against OpenAPI; emits `triage.verdict.adjudicated`). `TriageVerdict.adjudication` is returned on reads. The engine verdict is never rewritten.
- Action: `src/adjudicate.ts` carries the prompt, JSON schema and decision matrix that the offline harness scores (the harness imports it); `src/anthropic.ts` is a 90-line fetch client for the Messages API (structured output, prompt caching, retries) so the committed bundle stays the size of the other actions; `main.ts` runs the second judge for PR runs when `anthropic-api-key` is set, merges GitHub diff hunks, records the adjudication and publishes the final verdict. New inputs `anthropic-api-key`, `adjudicator-model` (`claude-haiku-4-5`), `adjudicate-min-confidence` (0.85), `adjudicate`; new outputs `engine-verdict`, `adjudicated`. No new runtime dependency.
- Producer patches: `ANTHROPIC_API_KEY` is declared as an optional `workflow_call` secret and threaded through the PR chains only (`e2e-tests-ci.yml → e2e-tests-playwright.yml → e2e-tests-playwright-template.yml`; `e2e-detox-pr.yml (inherit) → e2e-detox.yml → e2e-ios/android-template.yml`). Merge/release/matrix callers do not pass it, and the action skips adjudication without a PR number anyway.
