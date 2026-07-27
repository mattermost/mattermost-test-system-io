# CI replay

Replays real historical CI artifacts through the real orchestration server's
`/begin`, `/checkout`, `/complete` endpoints — no synthetic data, no actual
Cypress/Playwright install. Used to stress-test dispatch/retest/lease logic
under realistic worker concurrency and spec durations.

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

Downloaded elsewhere? Point at it: `CI_RUNS_ROOT=.local/some-dir GROUP=... node scripts/ci-replay/replay.js`.

`.local/` is gitignored scratch data — safe to re-download or delete anytime.

## How it works

Each `<group>-debug-N/` directory is one historical worker's session. This
tool pools every recorded `(spec_path -> outcome)` sample across all
debug-dirs in a group, then replays: whichever spec the server leases to a
simulated worker, that worker sleeps for the sample's real
`actual_duration_ms` (scaled by `SPEED`) instead of running a test framework.

One simulated worker runs per debug-dir found (40/39/14/15 for
cypress-full/cypress-full-fips/playwright-full/playwright-full-fips).

**Known limitation**: pooling discards real inter-spec timing/ordering
correlation, since replay-time dispatch order won't match history's.

## Corpus layout

`corpus.js` expects, under `CI_RUNS_ROOT`:

```
<group>-debug-<N>/worker-artifacts/<gh_job_id>/iter-0/...
                                               iter-1/...
```

`<group>` is one of `cypress-full`, `cypress-full-fips`, `playwright-full`,
`playwright-full-fips`. `gh run download` already produces this layout, so no
reorganizing is needed in practice. Only `iter-N/`'s contents matter:

- **Cypress**: one Mochawesome JSON per spec, `iter-N/<spec_basename>.json`.
- **Playwright**: `iter-N/results/reporter/results.json`.

## Env vars

| Var | Default | Meaning |
|---|---|---|
| `GROUP` | (required) | Which corpus to replay |
| `CI_RUNS_ROOT` | `.local/mattermost-ci` | Where the downloaded corpus lives |
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
- **Both**: at drain end, registers + uploads the worker's JSON/screenshots
  as a report shard.
- After all workers finish, polls the reports API until ingest converges and
  prints a PASS/FAIL/WARN summary.

**Known limitation**: a pooled Playwright sample's JSON often batches sibling
specs, so a Playwright group's ingested suite count can exceed its dispatched
spec count — verification treats this as a floor, not equality.

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

Shared parsers live under `scripts/lib/`: `cypress-mochawesome-parser.js` and
`playwright-json-reporter-parser.js`.
