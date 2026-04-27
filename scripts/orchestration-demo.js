#!/usr/bin/env node
/**
 * Orchestration demo: drive the begin/checkout/complete loop against a local
 * dev server so the Orchestration tab populates with live progress.
 *
 * Each worker actually shells out to Playwright on the leased spec files.
 * The Playwright JSON reporter output is parsed and forwarded to /complete
 * so the orchestration store records real per-spec status, durations, and
 * per-test-case detail. This is the primary reason `examples/playwright-test/`
 * is checked in — the demo exercises Playwright end-to-end.
 *
 * Flow:
 *   1. Read spec files from examples/playwright-test/tests/
 *   2. Order them by their `// Group:` header tags (illustrative — a real CI
 *      controller can use any signal it wants; the orchestrator is FIFO).
 *   3. Call POST /api/v1/orchestration/begin with the ordered list.
 *   4. Spawn N async workers (default 1) that loop checkout → run Playwright
 *      → complete until queue_empty.
 *   5. Poll GET /api/v1/orchestration/status when all workers exit.
 *
 * Authentication: uses the X-API-Key header with a dev API key issued by
 * `make seed`. The orchestration handlers accept the same auth chain as the
 * rest of the API (apikey | OIDC bearer | session cookie); the script picks
 * apikey because it requires no JWKS bootstrap.
 *
 * Prerequisites:
 *
 *   make docker-up && make db-reset && make dev          # one terminal
 *   make seed                                            # another, captures the key
 *   cd examples/playwright-test && npx playwright install --with-deps chromium  # one-time
 *
 * `make seed` prints a complete `TSIO_API_KEY=...` line — paste it into
 * your shell prefixed with `export`, or use `eval` to capture it:
 *
 *   export TSIO_API_KEY=tsio_key_AaBbCcDd.eFgHiJkLmNoPqRsTuVwXyZ012
 *   # or
 *   eval "$(make seed | grep '^TSIO_API_KEY=')"
 *
 *   node scripts/orchestration-demo.js
 *
 * Usage:
 *
 *   node scripts/orchestration-demo.js                   # default 1 worker
 *   NUM_WORKERS=3 node scripts/orchestration-demo.js     # 3 parallel workers
 *   RETEST=1 RETEST_BUDGET=2 node scripts/orchestration-demo.js
 *                                                       # turn on retest-on-fail
 *
 *   API_BASE=http://localhost:8080 \
 *     node scripts/orchestration-demo.js                 # custom server
 *
 *   PLAYWRIGHT_PROJECT=firefox node scripts/orchestration-demo.js
 *                                                       # alternate browser
 *
 * After it starts, open the per-group page to watch live progress:
 *   http://localhost:3000/reports/<repo-encoded>/<branch>/<short-sha>/<name>
 *   (the script prints the exact URL on stdout)
 */

'use strict';

const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');

const API_BASE = process.env.API_BASE || 'http://localhost:8080';
const API_KEY = process.env.TSIO_API_KEY;
const NUM_WORKERS = Math.max(1, parseInt(process.env.NUM_WORKERS || '1', 10));
const PLAYWRIGHT_PROJECT = process.env.PLAYWRIGHT_PROJECT || 'chromium';

if (!API_KEY) {
  console.error('TSIO_API_KEY is required.');
  console.error('');
  console.error('`make seed` prints a complete `TSIO_API_KEY=...` line — paste');
  console.error('it into your shell prefixed with `export` (or `eval` it).');
  console.error('');
  console.error('Example output:');
  console.error('  $ make seed');
  console.error('  seeded: api_key id=019dc88c-...         ← NOT the key (UUID)');
  console.error('  TSIO_API_KEY=7CRsc7fn.f1mHuAqtlNF7Aj... ← export THIS line');
  console.error('');
  console.error('Then:');
  console.error('  export TSIO_API_KEY=7CRsc7fn.f1mHuAqtlNF7Aj...');
  console.error('  # or, in one step:');
  console.error('  eval "$(make seed | grep \'^TSIO_API_KEY=\')"');
  console.error('');
  console.error('  node scripts/orchestration-demo.js');
  console.error('');
  console.error('Note: TSIO_ADMIN_KEY is a SEPARATE credential (X-Admin-Key)');
  console.error('used only by privileged setup endpoints. It is not accepted');
  console.error('by the orchestration endpoints. Use the TSIO_API_KEY from `make seed`.');
  process.exit(2);
}

// The api_key id from `make seed` output is a UUID and is NOT the credential —
// the credential lives on the next line as `TSIO_API_KEY=`. Detect this common
// confusion and refuse before sending a doomed request.
if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(API_KEY)) {
  console.error('TSIO_API_KEY looks like a UUID (api_key id), not an API key.');
  console.error('');
  console.error('Re-run `make seed` and copy the entire `TSIO_API_KEY=...` line,');
  console.error('NOT the api_key id printed on the line above it.');
  console.error('');
  console.error('You exported:');
  console.error(`  TSIO_API_KEY=${API_KEY}   ← the api_key id (UUID)`);
  console.error('');
  console.error('You want a value shaped like:');
  console.error('  TSIO_API_KEY=AaBbCcDd.eFgHiJkLmNoPqRsTuVwXyZ012345');
  process.exit(2);
}
const RETEST = process.env.RETEST === '1' || process.env.RETEST === 'true';
const RETEST_BUDGET = Math.max(0, parseInt(process.env.RETEST_BUDGET || '1', 10));

const PLAYWRIGHT_DIR = path.resolve(__dirname, '..', 'examples', 'playwright-test');
const SEED_TESTS_DIR = path.join(PLAYWRIGHT_DIR, 'tests');

// Composite identity for this demo run. Repo deliberately uses no slashes so
// the per-group page URL stays simple to read. gh_run_id and gh_run_attempt
// are derived from the wallclock time so each invocation registers a fresh
// run; remove the timestamp suffix to exercise begin-run idempotency.
const NOW_MS = Date.now();

// One artifacts root per demo invocation. Each spec invocation gets its own
// subdirectory under this so Playwright runs in parallel workers do not
// clobber each other's screenshots, traces, html report, or reporter JSON.
// The directory is intentionally NOT cleaned up at exit — it's printed at
// the end so the developer can inspect HTML reports / failure screenshots.
const DEMO_ARTIFACTS_ROOT = path.join(os.tmpdir(), 'tsio-demo', String(NOW_MS));
fs.mkdirSync(DEMO_ARTIFACTS_ROOT, { recursive: true });

// Monotonic counter for per-spec-invocation subdirectories. Incremented
// each time a worker dispatches Playwright.
let dispatchSeq = 0;

// Per-worker artifact buckets. One bucket per worker (= per CI job in
// production); a worker may run many specs across its checkout/complete
// loop and at queue-empty uploads ONE shard report covering everything it
// ran. Mirrors how a CI matrix job uploads its artifacts at the very end,
// not after each test. Keyed by gh_job_id.
const workerBuckets = new Map();

function ensureWorkerBucket(workerName, workerId) {
  let bucket = workerBuckets.get(workerId);
  if (!bucket) {
    bucket = {
      workerName,
      workerId,
      // {playwrightJsonPath, outputDir, specPath, isRetest} per invocation.
      invocations: [],
    };
    workerBuckets.set(workerId, bucket);
  }
  return bucket;
}

// Generate a fresh 40-char hex SHA per demo invocation so each run lands
// under its own commit on the dashboard. Override via TSIO_COMMIT_SHA when
// pinning is useful (e.g. exercising idempotency on the same commit).
const COMMIT_SHA = (process.env.TSIO_COMMIT_SHA || crypto.randomBytes(20).toString('hex')).toLowerCase();

const IDENTITY = {
  repository: 'orchestration-demo',
  commit_sha: COMMIT_SHA,
  gh_run_id: `demo-${NOW_MS}`,
  name: 'orchestration-demo',
  gh_run_attempt: '1',
  framework: 'playwright',
  branch: 'main',
};

// Tag-weight ordering. Negative weights sort first; positive weights sort
// last. Specs without any matching tag get weight 0 and remain at their
// natural read-dir order. A real caller might keep these weights in a
// dedicated config file, but for the demo a small static map illustrates
// the technique.
const TAG_WEIGHTS = {
  '@sort-first': -100,
  '@smoke': -10,
  '@featureA': 0,
  '@admin': 5,
  '@slow': 20,
  '@sort-last': 100,
};

// ─── HTTP helpers ──────────────────────────────────────────────────────────

function request(method, urlPath, body) {
  return new Promise((resolve, reject) => {
    const url = new URL(urlPath, API_BASE);
    const data = body == null ? null : Buffer.from(JSON.stringify(body));
    const req = http.request(
      {
        method,
        hostname: url.hostname,
        port: url.port,
        path: url.pathname + url.search,
        headers: {
          'Content-Type': 'application/json',
          'X-API-Key': API_KEY,
          ...(data ? { 'Content-Length': data.length } : {}),
        },
      },
      (res) => {
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8');
          let parsed = null;
          if (text.length) {
            try {
              parsed = JSON.parse(text);
            } catch {
              parsed = text;
            }
          }
          resolve({ status: res.statusCode, body: parsed });
        });
      },
    );
    req.on('error', reject);
    if (data) req.write(data);
    req.end();
  });
}

const post = (p, body) => request('POST', p, body);
const get = (p) => request('GET', p, null);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ─── Spec discovery + tag-based ordering ───────────────────────────────────

function listSpecFiles() {
  return fs
    .readdirSync(SEED_TESTS_DIR)
    .filter((f) => f.endsWith('.spec.ts') || f.endsWith('.spec.js'))
    .sort();
}

function readSpecHeaderTags(specName) {
  const filePath = path.join(SEED_TESTS_DIR, specName);
  const text = fs.readFileSync(filePath, 'utf8');
  // First contiguous comment block at top of file. Stop at the first blank
  // line after the comment ends.
  const firstNonComment = text.search(/^(?!\s*\/\/|\s*$)/m);
  const header = firstNonComment > 0 ? text.slice(0, firstNonComment) : '';
  const tagMatch = header.match(/Group:\s*(.*)$/m);
  if (!tagMatch) return [];
  return tagMatch[1].split(/\s+/).filter((t) => t.startsWith('@'));
}

function specWeight(specName) {
  const tags = readSpecHeaderTags(specName);
  let weight = 0;
  for (const t of tags) {
    if (t in TAG_WEIGHTS) weight += TAG_WEIGHTS[t];
  }
  return weight;
}

function buildDispatchUnits(specFiles) {
  // Each spec file becomes its own dispatch unit; the orchestrator does not
  // bundle specs. Sort by tag-derived weight (lighter first) so the demo
  // shows the FIFO checkout order matching the caller's intent.
  const units = specFiles.map((f) => ({ file: f, weight: specWeight(f) }));
  units.sort((a, b) => a.weight - b.weight || a.file.localeCompare(b.file));
  return units.map((u) => ({ spec_path: `tests/${u.file}` }));
}

// ─── Orchestration drivers ─────────────────────────────────────────────────

async function beginRun(dispatchUnits) {
  const body = {
    ...IDENTITY,
    lease_timeout_ms: 60_000,
    run_timeout_ms: 600_000,
    retest_on_fail: RETEST,
    retest_budget: RETEST_BUDGET,
    dispatch_units: dispatchUnits,
  };
  const resp = await post('/api/v1/orchestration/begin', body);
  if (resp.status !== 201 && resp.status !== 200) {
    throw new Error(`begin run failed: ${resp.status} ${JSON.stringify(resp.body)}`);
  }
  const verb = resp.status === 201 ? 'created' : 'idempotent-replay';
  console.log(`[controller] begin run: ${verb}, ${dispatchUnits.length} units submitted`);
  return resp.body;
}

async function checkoutOnce(workerName, workerId) {
  const resp = await post('/api/v1/orchestration/checkout', {
    ...IDENTITY,
    gh_job_name: workerName,
    gh_job_id: workerId,
    batch_size: 1,
  });
  if (resp.status === 409 && resp.body && resp.body.error === 'WORKER_HAS_ACTIVE_LEASE') {
    return { conflict: true };
  }
  if (resp.status === 409 && resp.body && resp.body.error === 'RUN_NOT_IN_PROGRESS') {
    // Race: another worker reported the final unit between this worker's
    // last complete and its next checkout, flipping the run to a terminal
    // state. Treat as queue-empty so the worker exits cleanly.
    return { queue_empty: true, run_terminated: true };
  }
  if (resp.status !== 200) {
    throw new Error(
      `checkout failed (${workerName}): ${resp.status} ${JSON.stringify(resp.body)}`,
    );
  }
  return resp.body;
}

async function completeOnce(workerName, workerId, results) {
  const resp = await post('/api/v1/orchestration/complete', {
    ...IDENTITY,
    gh_job_name: workerName,
    gh_job_id: workerId,
    results,
  });
  if (resp.status !== 200) {
    throw new Error(
      `complete failed (${workerName}): ${resp.status} ${JSON.stringify(resp.body)}`,
    );
  }
  return resp.body;
}

// ─── Real Playwright execution ─────────────────────────────────────────────

// runPlaywright shells out to `npx playwright test` for one dispatch unit's
// spec files using the custom TSIO reporter. Each invocation gets its own
// subdirectory under DEMO_ARTIFACTS_ROOT so concurrent workers do not
// clobber each other's screenshots/traces/html report. Returns the parsed
// reporter JSON (the file the custom reporter writes), the playwright exit
// code, and the on-disk paths so the demo can walk attachments and
// optionally clean up later. Resolves regardless of pass/fail; rejects only
// when the reporter file is missing/unreadable or the process can't be
// spawned.
function runPlaywright(specPaths) {
  const subdir = path.join(
    DEMO_ARTIFACTS_ROOT,
    `unit-${dispatchSeq++}-${Date.now()}`,
  );
  const outputDir = path.join(subdir, 'test-results');
  const reporterFile = path.join(subdir, 'tsio-results.json');
  // Built-in Playwright json reporter output. Used by the canonical
  // /api/v1/reports/* upload chain after all workers exit. The reporter
  // honors PLAYWRIGHT_JSON_OUTPUT_NAME automatically.
  const playwrightJsonPath = path.join(subdir, 'playwright-results.json');
  fs.mkdirSync(outputDir, { recursive: true });

  return new Promise((resolve, reject) => {
    // Do NOT pass --reporter on the CLI: it overrides the layered reporter
    // list in playwright.config.ts. The config already toggles to
    // [list, tsio-reporter, html, json] when TSIO_REPORTER_OUTPUT is set,
    // and the canonical /reports/* upload chain depends on the built-in
    // json reporter writing playwright-results.json. Letting the config
    // drive keeps every reporter (including json) in the rotation.
    const args = [
      'playwright',
      'test',
      `--project=${PLAYWRIGHT_PROJECT}`,
      `--output=${outputDir}`,
      ...specPaths, // already includes "tests/" prefix
    ];
    const child = spawn('npx', args, {
      cwd: PLAYWRIGHT_DIR,
      env: {
        ...process.env,
        FORCE_COLOR: '0',
        TSIO_REPORTER_OUTPUT: reporterFile,
        TSIO_OUTPUT_DIR: outputDir,
        PLAYWRIGHT_JSON_OUTPUT_NAME: playwrightJsonPath,
      },
      // Inherit stdout/stderr so the `list` reporter shows progress.
      stdio: 'inherit',
    });
    child.on('error', reject);
    child.on('close', (code) => {
      let report;
      try {
        const text = fs.readFileSync(reporterFile, 'utf8');
        report = JSON.parse(text);
      } catch (err) {
        reject(
          new Error(
            `tsio reporter output missing or invalid (exit ${code}, file ${reporterFile}): ${err.message}`,
          ),
        );
        return;
      }
      resolve({ report, exitCode: code, outputDir, reporterFile, playwrightJsonPath });
    });
  });
}

// Worst-case status across an iterable so a spec-level result reflects the
// most severe outcome of any test in the file. Order:
// failed/timedOut/interrupted > flaky > skipped > passed.
const STATUS_RANK = {
  passed: 0,
  skipped: 1,
  flaky: 2,
  interrupted: 3,
  timedOut: 4,
  failed: 5,
};
function worstStatus(statuses) {
  let worst = 'passed';
  for (const s of statuses) {
    if ((STATUS_RANK[s] ?? 0) > (STATUS_RANK[worst] ?? 0)) worst = s;
  }
  return worst;
}

// buildSpecResults consumes the reporter-emitted JSON ({ specs: [...] }) and
// produces one SpecResult per leased spec_path. The reporter's per-spec
// shape already matches the /complete payload almost exactly — we just need
// to handle the "lease covers a spec but reporter has no result for it"
// edge case (spec was all test.skip()'d, or the spec file had no tests
// matching the project filter) by emitting a synthetic skipped row, and
// strip absolute attachment paths before returning. Screenshot uploads are
// substituted into `test_cases[].attachments` separately by the worker
// after this function runs.
function buildSpecResults(report, leasedSpecPaths) {
  const bySpec = new Map();
  for (const s of report.specs ?? []) {
    bySpec.set(s.spec_path, s);
  }

  return leasedSpecPaths.map((spec_path) => {
    const reported = bySpec.get(spec_path);
    if (!reported || (reported.test_cases ?? []).length === 0) {
      return {
        spec_path,
        status: 'skipped',
        actual_duration_ms: 0,
        test_cases: [],
      };
    }
    return {
      spec_path: reported.spec_path,
      status: reported.status,
      actual_duration_ms: reported.actual_duration_ms,
      test_cases: reported.test_cases,
      ...(reported.error_message ? { error_message: reported.error_message } : {}),
      ...(reported.error_stack ? { error_stack: reported.error_stack } : {}),
    };
  });
}

// ─── Screenshot uploads ────────────────────────────────────────────────────

// uploadScreenshots streams every image attachment in `attachments` to the
// orchestration screenshots endpoint. Returns a list of
// `{ key, relative_path }` objects in the same order, one per successful
// upload. Failures are logged per-attachment and do NOT abort the worker —
// the corresponding /complete request just won't carry that attachment.
async function uploadScreenshots(workerName, workerId, specPath, attachments) {
  const out = [];
  if (!Array.isArray(attachments) || attachments.length === 0) return out;

  for (const att of attachments) {
    if (!att || typeof att.path !== 'string' || att.path.length === 0) continue;
    if (!att.content_type || !att.content_type.startsWith('image/')) continue;

    let body;
    try {
      body = fs.readFileSync(att.path);
    } catch (err) {
      console.error(
        `[${workerName}] failed to read attachment ${att.path}: ${err.message}`,
      );
      continue;
    }

    // Use a deterministic relative path under the per-invocation outputDir
    // when possible; fall back to the basename. This ends up in the
    // resulting storage key, so making it stable per spec is helpful.
    const relativePath = path.basename(att.path);

    const form = new FormData();
    form.set('repository', IDENTITY.repository);
    form.set('commit_sha', IDENTITY.commit_sha);
    form.set('gh_run_id', IDENTITY.gh_run_id);
    form.set('name', IDENTITY.name);
    form.set('gh_run_attempt', IDENTITY.gh_run_attempt);
    form.set('gh_job_name', workerName);
    form.set('gh_job_id', workerId);
    form.set('spec_path', specPath);
    form.set('relative_path', relativePath);
    form.set(
      'file',
      // Node 20+ supports Blob in the global scope.
      new Blob([body], { type: att.content_type }),
      relativePath,
    );

    let resp;
    try {
      resp = await fetch(`${API_BASE}/api/v1/orchestration/screenshots`, {
        method: 'POST',
        headers: { 'X-API-Key': API_KEY },
        body: form,
      });
    } catch (err) {
      console.error(
        `[${workerName}] screenshot upload error for ${relativePath}: ${err.message}`,
      );
      continue;
    }

    if (resp.status !== 201) {
      const text = await resp.text().catch(() => '');
      console.error(
        `[${workerName}] screenshot upload failed for ${relativePath}: ${resp.status} ${text}`,
      );
      continue;
    }

    let parsed;
    try {
      parsed = await resp.json();
    } catch (err) {
      console.error(
        `[${workerName}] screenshot response not JSON for ${relativePath}: ${err.message}`,
      );
      continue;
    }
    if (!parsed || typeof parsed.key !== 'string') {
      console.error(
        `[${workerName}] screenshot response missing key for ${relativePath}`,
      );
      continue;
    }
    out.push({ key: parsed.key, relative_path: relativePath });
  }

  return out;
}

// attachScreenshotsToResults walks every test_case in `results`, uploads
// any image attachments to the orchestration server, and rewrites the
// test_case's `attachments` field to the
// `{ screenshots: [{ key, relative_path }, ...] }` shape the /complete
// endpoint expects. Test cases with no successfully-uploaded screenshots
// have their `attachments` field stripped entirely.
async function attachScreenshotsToResults(workerName, workerId, results) {
  for (const r of results) {
    for (const tc of r.test_cases ?? []) {
      const uploaded = await uploadScreenshots(
        workerName,
        workerId,
        r.spec_path,
        tc.attachments ?? [],
      );
      if (uploaded.length > 0) {
        tc.attachments = { screenshots: uploaded };
      } else {
        delete tc.attachments;
      }
    }
  }
}

async function runWorker(idx) {
  const workerName = `demo-worker-${idx}`;
  const workerId = String(100_000 + idx);
  const bucket = ensureWorkerBucket(workerName, workerId);
  let leasesHeld = 0;

  // Stagger worker startup a touch so concurrent checkouts visibly race.
  await sleep(50 * idx);

  while (true) {
    const resp = await checkoutOnce(workerName, workerId);
    if (resp.conflict) {
      await sleep(500);
      continue;
    }
    if (resp.queue_empty) {
      console.log(`[${workerName}] queue empty after ${leasesHeld} unit(s); exiting.`);
      // Worker is done — upload its accumulated artifacts as ONE shard
      // report (one CI job → one report). Mirrors how a real CI matrix job
      // uploads its results at the very end of its execution.
      try {
        await uploadWorkerShard(bucket);
      } catch (err) {
        console.error(`[${workerName}] shard upload failed: ${err.message}`);
      }
      return;
    }

    const isRetest = !!resp.is_retest;
    leasesHeld += 1;

    const tag = isRetest ? 'retest' : 'fresh';
    const specPaths = resp.units.map((u) => u.spec_path);
    // Capture the orchestrator's per-unit dispatch_seq so the canonical
    // reports upload uses a stable, identifying gh_job_id per shard. Falls
    // back to the spec path if the field is missing (older servers).
    const unitDispatchSeq =
      (resp.units[0] && resp.units[0].dispatch_seq != null)
        ? resp.units[0].dispatch_seq
        : null;
    console.log(
      `[${workerName}] checked out (${tag}): ${specPaths.join(', ')} (deadline ${new Date(resp.deadline).toLocaleTimeString()})`,
    );

    const startedAt = Date.now();
    let results;
    let unitArtifact = null;
    try {
      const { report, exitCode, outputDir, playwrightJsonPath } = await runPlaywright(specPaths);
      results = buildSpecResults(report, specPaths);
      unitArtifact = { outputDir, playwrightJsonPath };
      const elapsed = Math.round((Date.now() - startedAt) / 1000);
      console.log(
        `[${workerName}] playwright finished in ${elapsed}s (exit ${exitCode}, artifacts ${outputDir}): ` +
          results.map((r) => `${path.basename(r.spec_path)}=${r.status}`).join(' '),
      );
      // Walk image attachments and upload them. On success, the test_case
      // attachments are rewritten to the storage-key shape expected by
      // /complete; on failure they're stripped.
      await attachScreenshotsToResults(workerName, workerId, results);
    } catch (err) {
      // Surface Playwright invocation failures (missing browsers, syntax
      // errors, etc.) as a synthetic failed report so the run still makes
      // progress and the dashboard shows the failure.
      console.error(`[${workerName}] playwright execution error: ${err.message}`);
      results = specPaths.map((spec_path) => ({
        spec_path,
        status: 'failed',
        actual_duration_ms: Date.now() - startedAt,
        error_message: `playwright invocation failed: ${err.message}`,
      }));
    }

    // Append to this worker's bucket. The whole bucket uploads as ONE
    // shard report when the worker exits (queue_empty). Retest dispatches
    // append a separate invocation entry under the same worker.
    if (unitArtifact) {
      bucket.invocations.push({
        dispatchSeq: unitDispatchSeq,
        specPath: specPaths[0],
        outputDir: unitArtifact.outputDir,
        playwrightJsonPath: unitArtifact.playwrightJsonPath,
        isRetest,
      });
    }

    const outcome = await completeOnce(workerName, workerId, results);
    const transitions = (outcome.unit_states_changed || [])
      .map((c) => c.new_state)
      .join(',');
    console.log(
      `[${workerName}] reported (${results.map((r) => r.status).join(',')}) → ${transitions || '(no state change — late report)'}`,
    );
  }
}

// ─── Canonical reports upload ──────────────────────────────────────────────

// extToImageContentType returns the multipart Content-Type to use for an
// image file. Returns null for non-image extensions so the caller can skip
// the file entirely.
function extToImageContentType(filePath) {
  const ext = path.extname(filePath).toLowerCase();
  if (ext === '.png') return 'image/png';
  if (ext === '.jpg' || ext === '.jpeg') return 'image/jpeg';
  return null;
}

// listImagesRecursive walks `root` recursively and returns
// `[{absPath, relPath, size, contentType}]` for every PNG/JPEG file. Traces
// (.zip) and videos (.webm) are intentionally excluded — image-only for now.
function listImagesRecursive(root) {
  const out = [];
  let entries;
  try {
    entries = fs.readdirSync(root, { recursive: true, withFileTypes: true });
  } catch {
    return out;
  }
  for (const ent of entries) {
    if (!ent.isFile()) continue;
    // Node 20+ provides parentPath; older fallback to path.
    const dir = ent.parentPath || ent.path || root;
    const absPath = path.join(dir, ent.name);
    const ct = extToImageContentType(absPath);
    if (!ct) continue;
    let size;
    try {
      size = fs.statSync(absPath).size;
    } catch {
      continue;
    }
    const relPath = path.relative(root, absPath);
    out.push({ absPath, relPath, size, contentType: ct });
  }
  return out;
}

// reportsIdentityBody returns the body shape the /reports/begin and
// /reports/complete endpoints expect. NOTE: the field name is `commit`,
// not `commit_sha` — different from the orchestration endpoints.
function reportsIdentityBody() {
  const body = {
    repository: IDENTITY.repository,
    commit: IDENTITY.commit_sha,
    gh_run_id: IDENTITY.gh_run_id,
    gh_run_attempt: IDENTITY.gh_run_attempt,
    framework: 'playwright',
    name: IDENTITY.name,
    branch: IDENTITY.branch,
  };
  if (IDENTITY.gh_pr_number != null) {
    body.gh_pr_number = IDENTITY.gh_pr_number;
  }
  return body;
}

// Lazily-resolved report_group id from /api/v1/reports/begin. Workers call
// `ensureReportGroup` from inside their own goroutine when they're ready to
// upload; the first caller drives the begin call, the rest reuse the
// memoized result. The endpoint is idempotent on the composite identity, so
// concurrent first-callers race harmlessly.
let reportGroupIDPromise = null;

async function ensureReportGroup() {
  if (reportGroupIDPromise) return reportGroupIDPromise;
  reportGroupIDPromise = (async () => {
    const resp = await post('/api/v1/reports/begin', reportsIdentityBody());
    if (resp.status !== 200 && resp.status !== 201) {
      throw new Error(
        `reports/begin failed: ${resp.status} ${JSON.stringify(resp.body)}`,
      );
    }
    const reportGroupID = resp.body && resp.body.report_id;
    if (!reportGroupID) {
      throw new Error(`reports/begin returned no report_id: ${JSON.stringify(resp.body)}`);
    }
    console.log(`[reports] begin: report_group_id=${reportGroupID}`);
    return reportGroupID;
  })();
  return reportGroupIDPromise;
}

// uploadWorkerShard uploads a single worker's accumulated artifacts as ONE
// shard report — exactly how a CI matrix job uploads its results at the end
// of its execution. The worker's gh_job_id / gh_job_name flow straight into
// the Report Group's per-shard list. Multiple Playwright JSONs (one per
// spec the worker ran) are uploaded as separate parts; the existing ingest
// pipeline appends each suite tree to the worker's report.
async function uploadWorkerShard(bucket) {
  if (!bucket || bucket.invocations.length === 0) {
    console.log(`[${bucket?.workerName ?? 'worker?'}] no artifacts to upload`);
    return;
  }
  const reportGroupID = await ensureReportGroup();

  // Collect every Playwright JSON the worker produced + every screenshot
  // across every invocation in its bucket. Each invocation's outputDir is a
  // separate subtree so screenshot relative paths cannot collide.
  const jsonParts = [];
  const screenshotParts = [];
  for (let i = 0; i < bucket.invocations.length; i++) {
    const inv = bucket.invocations[i];
    if (inv.playwrightJsonPath && fs.existsSync(inv.playwrightJsonPath)) {
      let stat;
      try {
        stat = fs.statSync(inv.playwrightJsonPath);
      } catch {
        stat = null;
      }
      if (stat) {
        // Suffix with the invocation index so multi-spec workers do not
        // overwrite each other under the same object key.
        const filename = bucket.invocations.length > 1
          ? `playwright-results-${i}.json`
          : 'playwright-results.json';
        jsonParts.push({
          absPath: inv.playwrightJsonPath,
          relPath: filename,
          size: stat.size,
        });
      }
    }
    if (inv.outputDir) {
      for (const img of listImagesRecursive(inv.outputDir)) {
        screenshotParts.push(img);
      }
    }
  }

  if (jsonParts.length === 0) {
    console.log(`[${bucket.workerName}] no playwright json on disk; skipping shard upload`);
    return;
  }

  const registerBody = {
    ...reportsIdentityBody(),
    gh_job_id: bucket.workerId,
    gh_job_name: bucket.workerName,
    json_files: jsonParts.map((p) => ({ path: p.relPath, size: p.size })),
    screenshots: screenshotParts.map((s) => ({ path: s.relPath, size: s.size })),
  };
  const regResp = await post('/api/v1/reports/register', registerBody);
  if (regResp.status !== 200 && regResp.status !== 201) {
    throw new Error(
      `reports/register failed for ${bucket.workerName}: ${regResp.status} ${JSON.stringify(regResp.body)}`,
    );
  }
  const uploadID = regResp.body && regResp.body.upload_id;
  if (!uploadID) {
    throw new Error(`reports/register returned no upload_id for ${bucket.workerName}`);
  }

  // JSON upload: one part per spec invocation.
  {
    const form = new FormData();
    for (const p of jsonParts) {
      const buf = fs.readFileSync(p.absPath);
      form.append('files', new Blob([buf], { type: 'application/json' }), p.relPath);
    }
    const resp = await fetch(
      `${API_BASE}/api/v1/reports/upload/${reportGroupID}/${uploadID}/json`,
      { method: 'POST', headers: { 'X-API-Key': API_KEY }, body: form },
    );
    if (resp.status !== 200) {
      const text = await resp.text().catch(() => '');
      throw new Error(`reports/upload/json failed for ${bucket.workerName}: ${resp.status} ${text}`);
    }
  }

  // Screenshots upload: one part per image, only when there are images.
  if (screenshotParts.length > 0) {
    const form = new FormData();
    for (const s of screenshotParts) {
      const buf = fs.readFileSync(s.absPath);
      form.append('files', new Blob([buf], { type: s.contentType }), s.relPath);
    }
    const resp = await fetch(
      `${API_BASE}/api/v1/reports/upload/${reportGroupID}/${uploadID}/screenshots`,
      { method: 'POST', headers: { 'X-API-Key': API_KEY }, body: form },
    );
    if (resp.status !== 200) {
      const text = await resp.text().catch(() => '');
      throw new Error(
        `reports/upload/screenshots failed for ${bucket.workerName}: ${resp.status} ${text}`,
      );
    }
  }

  console.log(
    `[${bucket.workerName}] shard uploaded: ${jsonParts.length} json + ${screenshotParts.length} screenshot(s)`,
  );
}

// finalizeReports POSTs /reports/complete after every worker has finished
// uploading its shard. Idempotent on composite identity. The Report Group
// view flips to a terminal state once this returns.
async function finalizeReports() {
  if (!reportGroupIDPromise) {
    // No worker uploaded anything — nothing to finalize.
    return;
  }
  const reportGroupID = await reportGroupIDPromise;
  const completeResp = await post('/api/v1/reports/complete', reportsIdentityBody());
  if (completeResp.status !== 200) {
    throw new Error(
      `reports/complete failed: ${completeResp.status} ${JSON.stringify(completeResp.body)}`,
    );
  }
  console.log(`[reports] complete: report_group_id=${reportGroupID}`);

  const repo = encodeURIComponent(IDENTITY.repository);
  const branch = encodeURIComponent(IDENTITY.branch);
  const shortSha = IDENTITY.commit_sha.slice(0, 7);
  const name = encodeURIComponent(IDENTITY.name);
  const url = `http://localhost:3000/reports/${repo}/${branch}/${shortSha}/${name}?gh_run_id=${encodeURIComponent(IDENTITY.gh_run_id)}&tab=reports`;
  console.log('');
  console.log('Report Group tab:');
  console.log(`  ${url}`);
}

async function pollFinalStatus() {
  const params = new URLSearchParams({
    repository: IDENTITY.repository,
    commit_sha: IDENTITY.commit_sha,
    gh_run_id: IDENTITY.gh_run_id,
    name: IDENTITY.name,
    gh_run_attempt: IDENTITY.gh_run_attempt,
  });
  const resp = await get(`/api/v1/orchestration/status?${params.toString()}`);
  if (resp.status !== 200) {
    throw new Error(`status failed: ${resp.status} ${JSON.stringify(resp.body)}`);
  }
  return resp.body;
}

function printPageHint() {
  // The web app's per-group route is /reports/:repo/:branch/:commit/:name.
  // Repo names with slashes URL-encode them; this seed uses a slash so the
  // hint shows the encoded form.
  const repo = encodeURIComponent(IDENTITY.repository);
  const branch = encodeURIComponent(IDENTITY.branch);
  const shortSha = IDENTITY.commit_sha.slice(0, 7);
  const name = encodeURIComponent(IDENTITY.name);
  const url = `http://localhost:3000/reports/${repo}/${branch}/${shortSha}/${name}?gh_run_id=${encodeURIComponent(IDENTITY.gh_run_id)}&tab=orchestration`;
  console.log('');
  console.log('Open the orchestration tab while this runs:');
  console.log(`  ${url}`);
  console.log('');
}

async function main() {
  console.log(`Orchestration demo against ${API_BASE}`);
  console.log(
    `  workers: ${NUM_WORKERS}, browser: ${PLAYWRIGHT_PROJECT}, retest: ${RETEST} (budget=${RETEST_BUDGET})`,
  );
  console.log(
    `  identity: ${IDENTITY.repository} @ ${IDENTITY.commit_sha.slice(0, 7)} / ${IDENTITY.name} (run ${IDENTITY.gh_run_id})`,
  );
  console.log(`  artifacts: ${DEMO_ARTIFACTS_ROOT}`);
  console.log('');

  const specs = listSpecFiles();
  if (specs.length === 0) {
    throw new Error(`No *.spec.ts files found under ${SEED_TESTS_DIR}`);
  }
  const dispatchUnits = buildDispatchUnits(specs);
  console.log('Dispatch order (after tag-weight sort):');
  for (const [i, u] of dispatchUnits.entries()) {
    console.log(`  [${i}] ${u.spec_path}`);
  }
  console.log('');

  await beginRun(dispatchUnits);
  printPageHint();

  const workers = Array.from({ length: NUM_WORKERS }, (_, i) => runWorker(i));
  await Promise.all(workers);

  console.log('');
  console.log('All workers exited. Final run status:');
  const final = await pollFinalStatus();
  console.log(JSON.stringify(final, null, 2));

  // Each worker has already uploaded its own shard (one /reports/register
  // + /upload chain per worker, called from inside runWorker right before
  // it exits — same shape as a CI matrix job uploading at the end of its
  // execution). Now finalize the run-level report group so the Report
  // Group view flips to terminal. Idempotent on composite identity.
  try {
    await finalizeReports();
  } catch (err) {
    console.error(`[reports] finalize failed (non-fatal): ${err.message}`);
  }

  console.log('');
  console.log(`artifacts preserved at ${DEMO_ARTIFACTS_ROOT}`);
}

main().catch((err) => {
  console.error('orchestration demo failed:', err.message);
  process.exit(1);
});
