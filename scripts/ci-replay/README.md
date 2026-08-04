# CI replay

Replays real historical CI artifacts through the real orchestration server's
`/begin`, `/checkout`, `/complete` endpoints — no synthetic data, no actual
Cypress/Playwright/Detox install. Used to stress-test dispatch/retest/lease
logic under realistic worker concurrency and spec durations.

Covers two source repos: `mattermost/mattermost` (Cypress, Playwright) and
`mattermost/mattermost-mobile` (Detox).

## Quick start

```bash
# 1. Find a run
gh run list -R mattermost/mattermost --workflow .github/workflows/e2e-tests-on-merge.yml

# 2. Download its artifacts (already laid out the way this tool expects)
gh run download <run-id> -R mattermost/mattermost --dir .local/mattermost-ci

# 3. Start the server (one terminal) + grab an API key (another)
make docker-up && make db-migrate && make dev-server
eval "$(make seed | grep '^TSIO_API_KEY=')"

# 4. Replay one of the four groups you downloaded
GROUP=cypress-full-fips node scripts/ci-replay/replay.js
```

Detox (mattermost-mobile) works the same way, just against its own repo and
default corpus root:

```bash
gh run list -R mattermost/mattermost-mobile --workflow .github/workflows/e2e-detox.yml
gh run download <run-id> -R mattermost/mattermost-mobile --dir .local/mattermost-mobile-ci
GROUP=detox-ios node scripts/ci-replay/replay.js
```

`detox-ipad` (the iPad-only leg, a narrower spec set run as its own matrix)
works the same way, sourced from the same downloaded run:

```bash
GROUP=detox-ipad node scripts/ci-replay/replay.js
```

Downloaded elsewhere? Point at it: `CI_RUNS_ROOT=.local/some-dir GROUP=... node scripts/ci-replay/replay.js`.

`.local/` is gitignored scratch data — safe to re-download or delete anytime.

## How it works

Each debug-dir is one historical worker's session — `<group>-debug-N/` for
Cypress/Playwright, `{ios,android,ipad}-results-<id>-N/` for Detox. This tool
pools every recorded `(spec_path -> outcome)` sample across all debug-dirs in
a group, then replays: whichever spec the server leases to a simulated
worker, that worker sleeps for the sample's real `actual_duration_ms` (scaled
by `SPEED`) instead of running a test framework.

One simulated worker runs per debug-dir found (40/39/14/15 for
cypress-full/cypress-full-fips/playwright-full/playwright-full-fips; 20/20
for detox-ios/detox-android; iPad's own matrix size for detox-ipad — much
smaller, since it's a narrower search path, not the full spec set).

**Known limitation**: pooling discards real inter-spec timing/ordering
correlation, since replay-time dispatch order won't match history's.

## Corpus layout

`corpus.js` expects, under `CI_RUNS_ROOT` (default `.local/mattermost-ci` for
cypress-*/playwright-* groups, `.local/mattermost-mobile-ci` for detox-*
groups):

```
# cypress-full, cypress-full-fips, playwright-full, playwright-full-fips
<group>-debug-<N>/worker-artifacts/<gh_job_id>/iter-0/...
                                               iter-1/...

# detox-ios, detox-android, detox-ipad
{ios,android,ipad}-results-<id>-<N>/jest-results.json
```

`gh run download` already produces both layouts, so no reorganizing is
needed. For cypress/playwright, only `iter-N/`'s contents matter:

- **Cypress**: one Mochawesome JSON per spec, `iter-N/<spec_basename>.json`.
- **Playwright**: `iter-N/results/reporter/results.json`.

Detox has no `iter-N`/`worker-artifacts` nesting — mattermost-mobile isn't
orchestrated via begin/checkout/complete yet, so each matrix worker's
artifact is a single flat `jest-results.json` (native Jest `--json
--outputFile` shape) covering every spec that worker statically ran. One
shard therefore yields many samples, not one — unlike Cypress/Playwright,
where one iter-dir is one spec.

## Env vars

| Var | Default | Meaning |
|---|---|---|
| `GROUP` | (required) | Which corpus to replay — `cypress-full`, `cypress-full-fips`, `playwright-full`, `playwright-full-fips`, `detox-ios`, `detox-android`, `detox-ipad` |
| `CI_RUNS_ROOT` | `.local/mattermost-ci` (`.local/mattermost-mobile-ci` for `detox-*`) | Where the downloaded corpus lives |
| `SPEED` | `1` | Divides each spec's real duration before sleeping. Does **not** scale `retry_after_ms`/`post-failure-delay-ms` — those are the real client policy under test |
| `RETEST` / `RETEST_BUDGET` | `1` / `1` | `retest_on_fail` config for the run |
| `MAX_IDLE_POLLS` | `5` | Matches the real dispatch-run action's default |
| `POST_FAILURE_DELAY_MS` | `10000` | Matches the real dispatch-run action's default |
| `INJECT_LEASE_TIMEOUT_RATE` | `0` | `0..1` — synthetic: probability a worker skips `/complete` to exercise lease reclaim |
| `API_BASE` | `http://localhost:8080` | Target server |
| `TSIO_API_KEY` | (required) | From `make seed` |
| `TSIO_COMMIT_SHA` | current minute | Shared `commit_sha` across terminals started together |
| `UPLOAD_SHARDS` | `0` | Opt-in: also do shard upload — see below |
| `VERIFY_TIMEOUT_MS` | `120000` | `UPLOAD_SHARDS=1` only: ingest-convergence poll timeout |

`lease_timeout_ms` is fixed at `600000 / SPEED` (the real dispatch-begin
action's default), not directly configurable.

## Shard upload (`UPLOAD_SHARDS=1`)

Off by default. When set, also replicates a real worker's upload half, using
real historical JSON/screenshot files already in the corpus:

- **Cypress only**: uploads recorded failure screenshots per spec before
  `/complete`, attached to the matching failed test_case.
- **All frameworks**: at drain end, registers + uploads the worker's
  JSON/screenshots as a report shard.
- After all workers finish, polls the reports API until ingest converges and
  prints a PASS/FAIL/WARN summary.

**Known limitations**:
- A pooled Playwright sample's JSON often batches sibling specs, so a
  Playwright group's ingested suite count can exceed its dispatched spec
  count — verification treats this as a floor, not equality.
- Detox's corpus has no recorded screenshots (the source CI artifacts don't
  carry them), so `UPLOAD_SHARDS=1` uploads JSON only there. More
  fundamentally, `ingest/detox.go`'s `extractDetox` expects the shape
  mattermost-mobile's own `merge-jest-results-for-tsio.js` produces
  (`testFilePath` / nested `testResults`), not the native Jest JSON this tool
  uploads as-is — so a Detox group's ingested suite count is always zero.
  Verification skips the suite-count check for `detox-*` groups entirely
  rather than scoring it a failure; this is a pre-existing report-upload-path
  gap, not a replay defect (see `.local/DETOX_ORCHESTRATION_PLAN.md`).

## Out of scope

- `total_reports_expected` is set to the worker count; without
  `UPLOAD_SHARDS` it's inert (cosmetic-only stuck upload counter).
- No GitHub API/token dependency — `gh_job_id` is a locally-unique string.

## Files

- `corpus.js` — builds the spec-path union and pooled samples for one group.
- `worker.js` — one simulated worker's drain loop (checkout → replay →
  complete), mirroring `main.ts`'s `drain()`.
- `client.js` — minimal JSON HTTP helper.
- `reports_client.js` — multipart client for the opt-in shard-upload path.
- `replay.js` — entrypoint: loads the corpus, calls `/begin`, spawns workers,
  prints a final status summary (and verification summary if
  `UPLOAD_SHARDS=1`).

Shared parsers live under `scripts/lib/`: `cypress-mochawesome-parser.js`,
`playwright-json-reporter-parser.js`, and `detox-jest-results-parser.js`.
