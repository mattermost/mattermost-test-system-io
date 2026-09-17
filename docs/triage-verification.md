# Verification (after review fixes, 2026-09-17)

Every claim below is backed by the pasted command output. Commands ran from a
clean shell (no `TSIO_*` variables exported) against the uncommitted working tree
on `codex/flaky-test-quarantine`. Docker was available for the testcontainers
suites.

## Go server

```sh
gofmt -s -l apps/server/internal apps/server/cmd apps/server/tests
# (no output)

go vet ./... && go build ./...
# ok

go test -race -count=1 ./...          # apps/server, output filtered to non-ok lines
# (no failures; exit 0)

go test -race -tags=e2e -count=1 ./tests/e2e/triage/... ./tests/e2e/identity/...
ok  	github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/triage	17.814s
ok  	github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/identity	7.331s
```

The previously failing `TestLoad_rejectsZeroTriageWindow` now passes with the
required variables unset:

```sh
env -u TSIO_S3_BUCKET -u TSIO_SESSION_SECRET go test -count=1 -run TestLoad_rejectsZeroTriageWindow ./internal/config/
ok  	github.com/mattermost/mattermost-test-system-io/apps/server/internal/config	1.637s
```

Lint (`make lint-server`, golangci-lint v2.11.4): see the last section.

## Triage action (`.github/actions/test-system-io-triage-verdict`)

```sh
npm run lint          # Found 0 warnings and 0 errors. (5 files)
npm run format:check  # All matched files use the correct format.
npx tsc --noEmit      # (no output)
npx vitest run
 Test Files  2 passed (2)
      Tests  41 passed (41)
npm run build
CJS dist/index.js 1015.90 KB
CJS ⚡️ Build success in 148ms
grep -c "postWithRetry\|blanket" dist/index.js   # 2  (dist rebuilt from src)
```

## Web (`apps/web`)

```sh
npx tsc --noEmit -p tsconfig.json                          # (no output)
npx vitest run src/pages/__tests__/triage_pages.test.tsx  # Tests 2 passed (2)
npx oxlint src/services/triage.ts src/types/triage.ts src/pages/triage_test_page.tsx
# Found 0 warnings and 0 errors.
npx oxfmt --check src/services/triage.ts src/types/triage.ts src/pages/__tests__/triage_pages.test.tsx
# All matched files use the correct format.
```

## Producer patches (`docs/triage-producer-patches`)

Regenerated from temporary detached worktrees of the real checkouts (removed
afterwards; the real checkouts were not modified).

```sh
git -C ~/Documents/mattermost/mattermost-mobile apply --check docs/triage-producer-patches/mattermost-mobile.patch   # ok
git -C ~/Documents/mattermost/mattermost        apply --check docs/triage-producer-patches/mattermost.patch          # ok
grep -c "^+.*@main" docs/triage-producer-patches/*.patch          # 0 and 0
grep -c "^+.*issues: write" docs/triage-producer-patches/*.patch  # 0 and 0

node --test detox/utils/tsio-triage-status.test.js
✔ enforce blocks
✔ enforce exonerates
✔ override precedes enforce
✔ unknown policy blocks raw fallback
✔ verified label from an earlier commit does not waive
✔ shadow keeps the raw path with override conversion
✔ removed override revokes earlier action output
✔ old attempt never posts
✔ metadata keeps one report and platform lanes
✔ resolver enriches pre-expanded platform identity with fresh merge base
ℹ tests 10  ℹ pass 10  ℹ fail 0

node detox/utils/tsio-report-status.js --self-test   # SELF-TEST OK
bash .github/scripts/tsio-assert-results.test.sh     # triage final assertion: 9 cases passed
node --check .github/scripts/tsio-finalize-status.mjs # ok
actionlint -shellcheck= -pyflakes= <changed workflows in both repos>   # (no findings)
```

## Not run here

- The full `make ci` target in one invocation (it also builds every action bundle
  and runs the infra tests). The pieces it is composed of that this change
  touches were run individually above.
- Any GitHub workflow dispatch, deployment, or shared variable change. Producer
  behavior against live GitHub/TSIO still requires the shadow rollout described
  in `docs/triage.md`.

## Lint

```sh
make lint-server
Linting Go (golangci-lint v2.11.4)...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4 run ./...
0 issues.
```

After the goconst fix (`classBrokenOnTrunk` / `resultSuccess` constants) the
triage packages were re-run: `internal/triage`, `internal/triage/health`,
`internal/triage/verdict` all `ok` under `-race`.

# Verification round 2 (backtest tooling + engine changes, 2026-09-17)

Changes since round 1: `as_of` replay, `tsioctl db import-remote`,
`scripts/triage_backtest.py`, batched identity enrichment, quarantine
`created_at` filter, area-rule window, `FLAKY_CROSS_PR` rule (engine `triage-4`).

```sh
gofmt -s -l apps/server/internal apps/server/cmd apps/server/tests   # (no output)
go vet ./...                                                          # ok
go test -race -count=1 ./...                                          # all ok (filtered output empty, exit 0)
go test -race -tags=e2e -count=1 ./tests/e2e/triage/ ./tests/e2e/identity/
ok  	github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/triage	21.264s
ok  	github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/identity	7.442s
make lint-server                                                      # 0 issues.
```

`TestAsOfReplayUsesOnlyEvidenceThatExisted` covers: 403 without admin, replay
before/after a trunk break flips REGRESSION ↔ BROKEN_ON_TRUNK, 400 with
`wait_for_completion_ms`, and no `pr_verdicts` row is written.

## Backtest on production history

Local stack: Postgres 18.3 (docker compose `postgres` service), the server run
with `go run ./cmd/tsio`, seeded with
`tsioctl db import-remote --since 2026-08-04 --pr-failed-only --pr-green-sample 20`
for both repositories (all trunk runs, every failing PR run, 20 green PR runs;
2,756 groups, ~1.7M test cases). Verdicts replayed with `as_of` = run end + 5 min.
Ground truth from GitHub label events, later runs of the same PR, and the
compare API (merge base, changed files). Reports:
`backtest-mobile.md` / `backtest-webapp.md` (scratchpad; summarized in the
delivery message and in `docs/triage-changes.md`).

## Backtest summary (engine triage-4)


## mattermost/mattermost-mobile

| Ground truth | runs | engine: green (auto-unblock) | engine: red | engine: rerun (infra/stale) |
|---|---|---|---|---|
| FIXED_BY_AUTHOR | 11 | 1 | 9 | 1 |
| WAIVED | 50 | 19 | 17 | 14 |
| RERUN_PASSED | 1 | 0 | 1 | 0 |
| OVERRIDDEN | 26 | 16 | 7 | 3 |
| UNRESOLVED | 400 | 72 | 275 | 53 |
| GREEN | 21 | 21 | 0 | 0 |


## mattermost/mattermost

| Ground truth | runs | engine: green (auto-unblock) | engine: red | engine: rerun (infra/stale) |
|---|---|---|---|---|
| FIXED_BY_AUTHOR | 20 | 7 | 13 | 0 |
| RERUN_PASSED | 5 | 1 | 4 | 0 |
| UNRESOLVED | 97 | 19 | 78 | 0 |
| GREEN | 78 | 78 | 0 | 0 |


Full per-run tables: docs/backtests/2026-09-17-*.md (webapp = newest 200 PR runs of 755; mobile = all 509 PR runs since 2026-08-18).

Ground-truth caveat: FIXED_BY_AUTHOR is a proxy (a later commit of the same PR passed). Every webapp run in that bucket the engine let through is a test that failed on 5–22 other PRs while passing 29–30 of 30 trunk runs, i.e. the proxy mislabels environment flakes as regressions.

# Verification round 3 (AI second judge in production path, 2026-09-17)

## Go server

```sh
make lint-server        # 0 issues.
make test-server-e2e    # ok: contract, admin_cli, identity, oidc, orchestration, reports, triage (25.5s)
```

`tests/e2e/triage/TestEvidenceAndAdjudicationRoundTrip`: verdict → evidence pack
(full error, engine class, trunk window) → 400 on an out-of-range finding index →
200 on a valid adjudication → `GET` shows `adjudication` while the engine verdict
is unchanged.

## Triage action

```sh
npm run tsc && npm run lint && npm run format:check && npm test && npm run build
# Found 0 warnings and 0 errors.  Tests 55 passed (55).  CJS dist/index.js 1.64 MB
```

`src/adjudicate.test.ts` covers the decision matrix (unblock needs ≥ min
confidence plus a `cross_pr`/`hunk_N` citation or `bug_on_master`; veto needs ≥ 0.9
plus a hunk; model unavailable keeps the engine decision; spec ownership is
never asked), hunk selection and the final-verdict rules. `src/main.test.ts`
drives the full flow with a mocked TSIO, GitHub and Anthropic: the Anthropic
request carries only the Anthropic key (never the GitHub token or OIDC), the
TSIO evidence request carries only OIDC, the adjudication is recorded, the
required status flips to `success` with "AI second judge changed 1", a
low-confidence answer keeps `failure`, a model outage keeps the engine verdict
with `adjudicated=false`, and trunk runs are never adjudicated.

## Producer patches

Regenerated from pristine `mattermost@2945359dcc` and `mattermost-mobile@d943628e0f`
scratch worktrees; only the `ANTHROPIC_API_KEY` declarations and pass-throughs
changed relative to round 2. `git apply --check` clean on both;
`actionlint -shellcheck= -pyflakes=` clean on every touched workflow;
`tsio-report-status.js --self-test` OK, `tsio-triage-status.test.js` 10 pass,
`tsio-assert-results.test.sh` 9 cases passed.
