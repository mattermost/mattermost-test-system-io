# Playwright seed

A small Playwright project used as input to the orchestration demo. The
`tests/` directory holds spec files annotated with `// Group: @<tag>`
headers; a CI controller can read those headers to derive a dispatch order
before calling `POST /api/v1/orchestration/begin`.

## Layout

```
tests/
  login.spec.ts            # Group: @auth @smoke @sort-first
  search.spec.ts           # Group: @search @smoke
  group_messaging.spec.ts  # Group: @messaging @group @featureA
  dm.spec.ts               # Group: @messaging @dm @featureA
  example.spec.ts          # the original sample (no Group: header)
  messaging.spec.ts        # Group: @messaging @featureA
  admin.spec.ts            # Group: @admin @slow @sort-last
  random.spec.ts           # Group: @flaky @random — coin-flip pass/fail for exercising retests
```

Each spec file is its own dispatch unit. The orchestrator does not bundle
specs — every unit addresses exactly one spec file.

`random.spec.ts` deliberately passes or fails at 50/50 (override with
`RANDOM_PASS_PROB`). With `RETEST=1` it makes the retest dispatch path
easy to see — failing units get re-leased once their fail_count is still
within the run's retest_budget.

## Run the orchestration demo locally

The demo script at the repo root drives the full begin/checkout/complete
loop against a local dev server, simulating multiple parallel workers so the
**Orchestration** tab on the per-group page populates in real time.

### 1. Boot the server stack

From the repo root:

```bash
make docker-up    # Postgres 18.3 + MinIO + Adminer
make db-reset     # apply schema (includes the orchestration tables)
make dev          # API on :8080, web on :3000
```

Leave `make dev` running in its own terminal.

### 2. Issue a dev API key

In a second terminal:

```bash
make seed
# → prints two lines:
#   seeded: api_key id=018f-...                        ← NOT the key (this is a UUID)
#   TSIO_API_KEY=AaBbCcDd.eFgHiJkLmNoPqRsTuVwXyZ012345 ← the actual key
```

The second line is a complete `KEY=value` pair — paste it directly into
your shell prefixed with `export`:

```bash
export TSIO_API_KEY=AaBbCcDd.eFgHiJkLmNoPqRsTuVwXyZ012345
```

Or use `eval` to capture and export it in one step:

```bash
eval "$(make seed | grep '^TSIO_API_KEY=')"
```

(The `api_key id` UUID on the first line is NOT the key — the demo
script will refuse a UUID-shaped value with a 401 hint if you copy the
wrong line.)

> The repo's `TSIO_ADMIN_KEY` is a separate credential (`X-Admin-Key`)
> for privileged setup endpoints only — the orchestration endpoints
> reject it. Always use the `TSIO_API_KEY` from `make seed`.

### 3. Install Playwright browsers (one-time)

The demo actually shells out to Playwright on each leased dispatch unit,
so the chromium browser must be available. From the repo root:

```bash
cd examples/playwright-test
npx playwright install --with-deps chromium
```

(Replace `chromium` with `firefox` or `webkit` if you intend to run the
demo with `PLAYWRIGHT_PROJECT=firefox` etc.)

### 4. Drive the demo

```bash
node scripts/orchestration-demo-playwright.js
```

The script:

1. Reads `examples/playwright-test/tests/`.
2. Parses each spec's `// Group:` header and applies a small
   tag-weight table to derive a sort order (sort-first / smoke / default /
   slow / sort-last). Each spec becomes its own dispatch unit.
3. Calls `POST /api/v1/orchestration/begin` with the ordered list.
4. Spawns N async workers (default 1; override with `NUM_WORKERS=`) that
   loop `checkout` → run `npx playwright test` against the leased spec
   (using a custom reporter — see below) → forward the parsed results to
   `complete` until the queue is empty.
5. Prints the per-group page URL on stdout — open it to watch live
   progress.

#### Reporter, per-spec output dirs, and screenshot uploads

The demo invokes Playwright with a custom reporter at
`examples/playwright-test/reporters/tsio-reporter.ts`. Unlike the built-in
`--reporter=json` output, the custom reporter records per-test-case
attachment paths (failure screenshots, traces, videos), retry counts,
durations, and per-attempt errors in a single JSON file the demo can
consume directly.

Each spec invocation gets its own output directory under
`$TMPDIR/tsio-demo/<run-id>/unit-<n>-<ts>/`, containing:

- `tsio-results.json` — the custom reporter's emitted JSON.
- `test-results/` — Playwright's own per-spec output (screenshots, traces,
  videos).
- `playwright-report/` — the standard HTML report (still emitted via
  `--reporter=html` layered alongside the custom reporter).

These artifacts are preserved until the demo process exits so they can be
inspected (open the HTML report) and later uploaded to TSIO as a report
bundle. The demo prints the artifacts root on startup and again on exit.

When a test fails and produces an image attachment (configured via
`use: { screenshot: 'only-on-failure' }` in `playwright.config.ts`), the
worker uploads each image to `POST /api/v1/orchestration/screenshots`,
receives back a storage `key`, and references that key inside the
matching `/complete` payload as
`results[i].test_cases[j].attachments = { screenshots: [{ key, relative_path }, ...] }`.
This makes failure screenshots visible inline on the Orchestration tab
without going through the separate report-upload flow.

#### Report bundle upload

After all workers exit, the demo POSTs each per-unit Playwright JSON file
plus its screenshots through the canonical `/api/v1/reports/*` chain
(`begin` → `register` → `upload/{rid}/{uid}/json` →
`upload/{rid}/{uid}/screenshots` → `complete`). This populates the
**Report Group** tab on the dashboard with the same data CI uploads
produce. Each dispatch unit becomes its own "shard" report
(`gh_job_id = unit-<dispatch_seq>`); retest dispatches register as
separate shards (`unit-<dispatch_seq>-retest`). Image-only artifacts
(`.png` / `.jpg` / `.jpeg`) are uploaded for now; traces (`.zip`) and
videos (`.webm`) are excluded. To make the built-in Playwright JSON
reporter produce the per-unit results file, the orchestration demo sets
`PLAYWRIGHT_JSON_OUTPUT_NAME` per Playwright invocation and the
`json` reporter is layered last in `playwright.config.ts`.

The Orchestration tab and Report Group tab now show the same run from
two angles: the orchestration view shows dispatch / lease / attempt
mechanics, while the Report Group view shows the canonical
Playwright-derived suite and test-case tree. Per-spec disagreement
between the two views is visually flagged on the per-group page.

### 5. Watch live progress

Open the URL the script printed (it looks like
`http://localhost:3000/reports/orchestration-demo/main/0123456/orchestration-demo?gh_run_id=...&tab=orchestration`).
You'll see:

- One row per spec file, starting in `pending` (gray).
- Rows transition to `leased` (blue spinner) as the worker checks them
  out and runs Playwright, then to `completed_pass` (green) /
  `completed_fail` (red) / `completed_skipped` (gray dash) as
  Playwright finishes and the worker reports.
- Expanding a row reveals the per-attempt history, the worker name,
  durations, and any error message returned by Playwright.

### 6. Optional flags

```bash
# Run with multiple parallel workers (each one runs Playwright in its own
# subprocess against its own leased units).
NUM_WORKERS=3 node scripts/orchestration-demo-playwright.js

# Real Playwright runs can flake against the live https://playwright.dev/
# pages, and random.spec.ts fails roughly half the time. Turn on
# retest-on-fail to see failing units get re-leased.
RETEST=1 RETEST_BUDGET=2 node scripts/orchestration-demo-playwright.js

# Use a different Playwright project (firefox or webkit) — install the
# matching browser first via `npx playwright install <name>`.
PLAYWRIGHT_PROJECT=firefox node scripts/orchestration-demo-playwright.js

# Hit a non-default server or use a custom API key.
API_BASE=http://localhost:9090 \
TSIO_API_KEY=my-rotated-key \
  node scripts/orchestration-demo-playwright.js
```

Note: the seed specs hit `https://playwright.dev/` and assert against its
public docs. They're meant as recognizable demo content rather than as a
representative production suite — flakes against the live site are
expected when the docs change. With `RETEST=1`, those flakes will visibly
exercise the retest dispatch path.

## Run the test suite directly (without orchestration)

To run the seed Playwright suite outside the orchestration loop — useful
for iterating on the spec files themselves:

```bash
cd examples/playwright-test
npx playwright test                  # all specs, all projects
npx playwright test tests/login.spec.ts  # one spec
npx playwright test --project=chromium   # one browser
```

## Compare orchestration vs uploaded artifacts

If you want to see the **divergence-flagging** flow (orchestration view vs
canonical Report Group view co-located on the same page), run the
orchestration demo first, then upload Playwright reports under the same
composite identity using the existing seed-upload pipeline. The two tabs
on the per-group page will populate independently and any per-spec
disagreement is visually flagged.
