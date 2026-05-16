#!/usr/bin/env node
/**
 * Orchestration demo (Cypress) — drives the begin/checkout/complete loop
 * against a local dev server using the in-repo examples/cypress-test/
 * fixture. Sibling to scripts/orchestration-demo-playwright.js, which targets
 * Playwright and is intentionally NOT modified by this script.
 *
 * Each worker shells out to `cypress run --spec <leased>` for the leased
 * spec files. The worker reads the Mochawesome JSON the Cypress run
 * produced (via the fixture's reporter-config.json) and forwards a
 * per-test test_cases array on /complete, so the live Orchestration tab
 * shows real per-test outcomes during the run.
 *
 * Authentication is the X-API-Key header — same convention as the
 * Playwright demo. `make seed` prints a complete TSIO_API_KEY=... line;
 * paste it into the shell prefixed with `export`, or `eval` it.
 *
 * Prerequisites:
 *
 *   make docker-up && make db-reset && make dev          # one terminal (orchestration server)
 *   make seed                                            # another, captures the key
 *   cd examples/cypress-test && npm ci                   # one-time, installs cypress + reporters
 *   npx cypress install                                  # downloads Cypress's browser binary
 *
 * The fixture targets https://example.cypress.io as its baseUrl, so
 * Cypress needs internet access to drive the kitchen-sink demo site.
 * The orchestration server itself listens on https://localhost:8443
 * — the two are separate concerns; baseUrl is for cy.visit, the API
 * target is for /api/v1/orchestration/*.
 *
 *   eval "$(make seed | grep '^TSIO_API_KEY=')"
 *   node scripts/orchestration-demo-cypress.js
 *
 * Variations (env vars):
 *
 *   NUM_WORKERS=3                          # 3 parallel workers (default 1)
 *   RETEST=1 RETEST_BUDGET=2               # opt into retest of failed specs
 *   PR=4321                                # simulate a PR run (branch=pr-4321, gh_pr_number=4321)
 *   API_BASE=https://localhost:9443        # custom server
 *   TSIO_COMMIT_SHA=<40-char>              # pin the demo's commit SHA
 *
 * After the loop starts, open the per-group page printed on stdout to
 * watch live progress.
 */

'use strict';

const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const https = require('https');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');
const {
  parseMochawesomeJson,
} = require('./lib/cypress-mochawesome-parser');

// Local-dev helper: accept the mkcert-issued self-signed cert that tsio
// serves at https://localhost:8443. Node uses its bundled CA list (not the
// OS keychain), so mkcert -install on the host doesn't reach this process.
// Setting NODE_TLS_REJECT_UNAUTHORIZED=0 is process-local and only affects
// this script. Override in the environment if you point API_BASE at a host
// whose cert chains to a public CA.
if (!process.env.NODE_TLS_REJECT_UNAUTHORIZED) {
  process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
}

const API_BASE = process.env.API_BASE || 'https://localhost:8443';
const API_KEY = process.env.TSIO_API_KEY;
const NUM_WORKERS = Math.max(1, parseInt(process.env.NUM_WORKERS || '1', 10));

if (!API_KEY) {
  console.error('TSIO_API_KEY is required.');
  console.error('');
  console.error('`make seed` prints a complete `TSIO_API_KEY=...` line — paste');
  console.error('it into your shell prefixed with `export` (or `eval` it).');
  console.error('');
  console.error('  eval "$(make seed | grep \'^TSIO_API_KEY=\')"');
  console.error('  node scripts/orchestration-demo-cypress.js');
  process.exit(2);
}

if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(API_KEY)) {
  console.error('TSIO_API_KEY looks like a UUID (api_key id), not an API key.');
  console.error('Re-run `make seed` and copy the entire `TSIO_API_KEY=...` line.');
  process.exit(2);
}

const RETEST = process.env.RETEST === '1' || process.env.RETEST === 'true';
const RETEST_BUDGET = Math.max(0, parseInt(process.env.RETEST_BUDGET || '1', 10));

const CYPRESS_DIR = path.resolve(__dirname, '..', 'examples', 'cypress-test');
const SEED_TESTS_DIR = path.join(CYPRESS_DIR, 'tests', 'integration');
const MOCHAWESOME_DIR = path.join(CYPRESS_DIR, 'results', 'mochawesome-report', 'json', 'tests');

// Accumulator directory for the latest Mochawesome JSON per spec across
// all leases this demo run produces. Each lease wipes the cypress
// project's mochawesome-report dir before running, so we copy each
// lease's output here keyed by spec basename. A retest's JSON
// overwrites the lease-1 entry, leaving the most-recent attempt's JSON
// for the queue-empty shard upload.
const ACCUMULATOR_DIR = fs.mkdtempSync(path.join(os.tmpdir(), 'tsio-cypress-demo-'));

const NOW_MS = Date.now();
const COMMIT_SHA = (process.env.TSIO_COMMIT_SHA || crypto.randomBytes(20).toString('hex')).toLowerCase();

const PR_NUMBER = (() => {
  const raw = process.env.PR;
  if (!raw) return null;
  const n = parseInt(raw, 10);
  if (!Number.isFinite(n) || n <= 0) {
    console.error(`PR=${raw} is not a positive integer`);
    process.exit(2);
  }
  return n;
})();

const IDENTITY = {
  repository: 'repo-name',
  commit_sha: COMMIT_SHA,
  gh_run_id: `demo-${NOW_MS}`,
  name: 'orchestration-demo-cypress',
  gh_run_attempt: '1',
  framework: 'cypress',
  branch: PR_NUMBER ? `pr-${PR_NUMBER}` : 'main',
  ...(PR_NUMBER ? { gh_pr_number: PR_NUMBER } : {}),
};

// ─── HTTP helpers ──────────────────────────────────────────────────────────

function request(method, urlPath, body) {
  return new Promise((resolve, reject) => {
    const url = new URL(urlPath, API_BASE);
    const data = body == null ? null : Buffer.from(JSON.stringify(body));
    const client = url.protocol === 'https:' ? https : http;
    const req = client.request(
      {
        method,
        hostname: url.hostname,
        port: url.port || (url.protocol === 'https:' ? 443 : 80),
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

// ─── Spec discovery ────────────────────────────────────────────────────────

function listSpecFiles() {
  return fs
    .readdirSync(SEED_TESTS_DIR)
    .filter((f) => f.endsWith('_spec.ts') || f.endsWith('_spec.js'))
    .sort();
}

function buildDispatchUnits(specFiles) {
  // Spec-paths are reported relative to the cypress project root so they
  // match what `cypress run --spec <path>` accepts as input. The demo
  // submits in lexicographic order; the orchestrator preserves that order
  // (FIFO) on dispatch.
  return specFiles.map((f) => ({ spec_path: `tests/integration/${f}` }));
}

// Locate the Mochawesome JSON file the cypress run wrote for one spec.
// Returns the absolute path or null if it cannot be found. The fixture's
// reporter-config.json sets reportFilename: 'json/tests/[name]', so a
// spec at tests/integration/foo_spec.ts produces foo_spec.json under
// MOCHAWESOME_DIR.
function locateMochawesomeJson(specPath) {
  const baseName = path.basename(specPath).replace(/\.(ts|js)$/, '');
  const candidate = path.join(MOCHAWESOME_DIR, `${baseName}.json`);
  return fs.existsSync(candidate) ? candidate : null;
}

// ─── Cypress execution ─────────────────────────────────────────────────────

function runCypress(specPaths) {
  // Mochawesome's `overwrite: false` (per reporter-config.json) appends a
  // numeric suffix when a spec re-runs in the same dir, which would
  // confuse the per-spec JSON locator. Wiping the report tree before
  // each invocation matches the dispatcher action's behavior and means
  // a retest's JSON cleanly overwrites the lease-1 output for the
  // shard-upload step at queue-empty.
  const reportRoot = path.join(CYPRESS_DIR, 'results', 'mochawesome-report');
  fs.rmSync(reportRoot, { recursive: true, force: true });

  // Wipe the leased specs' screenshot dirs so a retest's failure
  // screenshot replaces the lease-1 output instead of accumulating
  // alongside it as `(attempt 2).png`. The orchestration-screenshots
  // upload below picks up whatever ends up there post-run.
  for (const sp of specPaths) {
    const dir = path.join(CYPRESS_DIR, 'tests', 'screenshots', path.basename(sp));
    fs.rmSync(dir, { recursive: true, force: true });
  }

  return new Promise((resolve) => {
    const args = [
      'cypress',
      'run',
      '--reporter',
      'cypress-multi-reporters',
      '--reporter-options',
      'configFile=reporter-config.json',
      '--spec',
      specPaths.join(','),
    ];
    const child = spawn('npx', args, {
      cwd: CYPRESS_DIR,
      env: {
        ...process.env,
        FORCE_COLOR: '0',
        // Cypress emits an ASCII box-drawing summary by default. Off keeps
        // the demo's parallel-worker stdout legible.
        CYPRESS_QUIET: 'true',
      },
      stdio: 'inherit',
    });
    child.on('error', (err) => {
      // Resolve rather than reject so the worker can still emit a /complete
      // for the lease (otherwise the lease times out and the spec gets
      // re-dispatched which is rarely what the demo wants).
      resolve({ exitCode: -1, spawnError: err });
    });
    child.on('close', (code) => {
      resolve({ exitCode: code });
    });
  });
}

// Build a /complete results[] item per leased spec. If the Mochawesome
// JSON for a spec is missing/malformed (cypress crashed before writing,
// or the spec had no tests), emit a synthetic interrupted entry — the
// orchestrator accepts it and surfaces the issue on the dashboard.
function buildSpecResults(leasedSpecPaths) {
  return leasedSpecPaths.map((specPath) => {
    const jsonPath = locateMochawesomeJson(specPath);
    if (!jsonPath) {
      return {
        spec_path: specPath,
        status: 'interrupted',
        actual_duration_ms: 0,
      };
    }
    let parsed;
    try {
      parsed = JSON.parse(fs.readFileSync(jsonPath, 'utf8'));
    } catch (err) {
      console.warn(`  parse failure for ${specPath}: ${err.message}`);
      return {
        spec_path: specPath,
        status: 'interrupted',
        actual_duration_ms: 0,
      };
    }
    const { aggregateStatus, testCases } = parseMochawesomeJson(parsed);
    const totalDurationMs = testCases.reduce((acc, c) => acc + (c.duration_ms || 0), 0);

    // Stash this lease's JSON for the queue-empty shard upload. Keying
    // by basename means a retest's pass overwrites the lease-1 fail —
    // the report-group view only needs the most-recent shape per spec.
    const baseName = path.basename(specPath).replace(/\.(ts|js)$/, '');
    fs.copyFileSync(jsonPath, path.join(ACCUMULATOR_DIR, `${baseName}.json`));

    return {
      spec_path: specPath,
      status: aggregateStatus,
      actual_duration_ms: totalDurationMs,
      test_cases: testCases,
    };
  });
}

// ─── Orchestration drivers ─────────────────────────────────────────────────

async function beginRun(dispatchUnits) {
  // total_reports_expected = NUM_WORKERS: each worker uploads one shard
  // at queue-empty (matching the playwright demo's contract). The server
  // seeds the report_group with this count so it can auto-finalize once
  // that many shards reach `complete`. Required field on the openapi
  // BeginRunRequest schema; omitting it returns 400.
  const body = {
    ...IDENTITY,
    lease_timeout_ms: 60_000,
    idle_timeout_ms: 600_000,
    retest_on_fail: RETEST,
    retest_budget: RETEST_BUDGET,
    total_reports_expected: NUM_WORKERS,
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

// ─── Worker loop ───────────────────────────────────────────────────────────

async function runWorker(index) {
  const workerName = `cypress-worker-${index}`;
  const workerId = `${NOW_MS}-${index}`;

  while (true) {
    const co = await checkoutOnce(workerName, workerId);
    if (co.conflict) {
      // Earlier complete was lost; the server still has the worker's
      // previous lease. Wait for it to time out and try again.
      console.log(`[${workerName}] active-lease conflict; sleeping`);
      await sleep(2000);
      continue;
    }
    if (co.queue_empty) {
      console.log(`[${workerName}] queue empty; exiting`);
      return;
    }
    const leasedSpecs = (co.units || []).map((u) => u.spec_path);
    if (leasedSpecs.length === 0) {
      console.log(`[${workerName}] checkout returned no units; exiting`);
      return;
    }
    console.log(`[${workerName}] leased: ${leasedSpecs.join(', ')}`);

    const cypressResult = await runCypress(leasedSpecs);
    if (cypressResult.spawnError) {
      console.warn(`[${workerName}] cypress spawn failed: ${cypressResult.spawnError.message}`);
    } else {
      console.log(`[${workerName}] cypress exit ${cypressResult.exitCode}`);
    }

    const results = buildSpecResults(leasedSpecs);

    // Upload any per-spec failure screenshots Cypress wrote during this
    // lease and attach the returned keys to the failing test_cases.
    // Done before /complete so the attempt row's attachments field
    // carries the keys when it lands server-side.
    try {
      await attachOrchScreenshotsToResults(workerName, workerId, results);
    } catch (err) {
      console.warn(`[${workerName}] screenshot attach failed (non-fatal): ${err.message}`);
    }

    const aggregateLine = results
      .map((r) => `${path.basename(r.spec_path)}=${r.status}`)
      .join(' ');
    console.log(`[${workerName}] complete: ${aggregateLine}`);
    await completeOnce(workerName, workerId, results);
  }
}

// ─── Orchestration-screenshots upload (per-lease, attached to attempts) ───

// Walks tests/screenshots/<spec-basename>/ and returns absolute paths of
// every PNG/JPG Cypress wrote there during this lease's run. Cypress
// writes failure screenshots only — a passing test produces nothing.
function listSpecScreenshots(specPath) {
  const root = path.join(CYPRESS_DIR, 'tests', 'screenshots', path.basename(specPath));
  if (!fs.existsSync(root)) return [];
  const out = [];
  for (const ent of fs.readdirSync(root, { withFileTypes: true })) {
    if (ent.isFile() && /\.(png|jpe?g)$/i.test(ent.name)) {
      out.push(path.join(root, ent.name));
    }
  }
  return out;
}

// Streams one screenshot file to /api/v1/orchestration/screenshots and
// returns the server-assigned storage key plus the relative_path the
// orchestration tab will render. On failure (network error, non-201
// response, missing key) returns null and the screenshot is dropped
// silently — the orchestration outcome is still reported correctly.
async function uploadOrchScreenshot(workerName, workerId, specPath, absPath) {
  let body;
  try {
    body = fs.readFileSync(absPath);
  } catch (err) {
    console.warn(`[${workerName}] failed to read screenshot ${absPath}: ${err.message}`);
    return null;
  }
  const ext = path.extname(absPath).toLowerCase();
  const contentType = ext === '.png' ? 'image/png' : 'image/jpeg';
  const relativePath = path.basename(absPath);

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
  form.set('file', new Blob([body], { type: contentType }), relativePath);

  let resp;
  try {
    resp = await fetch(`${API_BASE}/api/v1/orchestration/screenshots`, {
      method: 'POST',
      headers: { 'X-API-Key': API_KEY },
      body: form,
    });
  } catch (err) {
    console.warn(`[${workerName}] orch screenshot upload error: ${err.message}`);
    return null;
  }
  if (resp.status !== 201) {
    const text = await resp.text().catch(() => '');
    console.warn(
      `[${workerName}] orch screenshot upload failed: ${resp.status} ${text}`,
    );
    return null;
  }
  const parsed = await resp.json().catch(() => null);
  if (!parsed || typeof parsed.key !== 'string') return null;
  return { key: parsed.key, relative_path: relativePath };
}

// For each per-spec result that has at least one failing test_case,
// uploads every screenshot Cypress wrote for that spec via the
// orchestration screenshots endpoint and attaches the returned keys to
// the failing test_cases via the schema's attachments wrapper. Passing
// test_cases keep attachments unset so the orchestration tab only shows
// inline screenshots where there's a failure to investigate.
async function attachOrchScreenshotsToResults(workerName, workerId, results) {
  for (const r of results) {
    const screenshots = listSpecScreenshots(r.spec_path);
    if (screenshots.length === 0) continue;

    const uploaded = [];
    for (const absPath of screenshots) {
      const out = await uploadOrchScreenshot(workerName, workerId, r.spec_path, absPath);
      if (out) uploaded.push(out);
    }
    if (uploaded.length === 0) continue;

    const failingCases = (r.test_cases || []).filter(
      (tc) =>
        tc.status === 'failed' || tc.status === 'timedOut' || tc.status === 'interrupted',
    );
    // If none of the test_cases is failing but a screenshot exists, attach
    // to the first case so the screenshot is still discoverable from the
    // orchestration tab.
    const targets = failingCases.length > 0 ? failingCases : (r.test_cases || []).slice(0, 1);
    for (const tc of targets) {
      tc.attachments = { screenshots: uploaded };
    }
  }
}

// ─── Reports-flow upload (per-shard at queue-empty) ────────────────────────

// reportsIdentityBody returns the body shape /reports/begin and
// /reports/register expect. NOTE: the field name is `commit`, not
// `commit_sha` — the reports endpoints differ from orchestration here.
function reportsIdentityBody() {
  const body = {
    repository: IDENTITY.repository,
    commit: IDENTITY.commit_sha,
    gh_run_id: IDENTITY.gh_run_id,
    gh_run_attempt: IDENTITY.gh_run_attempt,
    framework: 'cypress',
    name: IDENTITY.name,
    branch: IDENTITY.branch,
  };
  if (IDENTITY.gh_pr_number != null) body.gh_pr_number = IDENTITY.gh_pr_number;
  return body;
}

// listScreenshotFiles walks tests/screenshots/ recursively, collecting
// every .png/.jpg image Cypress wrote there during the demo's lease
// loop. Returns objects shaped for the manifest + multipart upload:
// each entry's relPath is the path relative to the screenshotsFolder
// root (e.g. "screenshot_demo_spec.ts/screenshot demo -- captures a
// screenshot when an assertion fails on the actions page (failed).png")
// so the server can match it against attempt records by filename.
function listScreenshotFiles() {
  const root = path.join(CYPRESS_DIR, 'tests', 'screenshots');
  const out = [];
  if (!fs.existsSync(root)) return out;
  function walk(dir) {
    for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, ent.name);
      if (ent.isDirectory()) walk(full);
      else if (ent.isFile() && /\.(png|jpe?g)$/i.test(ent.name)) {
        const stat = fs.statSync(full);
        const rel = path.relative(root, full).split(path.sep).join('/');
        const ext = path.extname(ent.name).toLowerCase();
        const ct = ext === '.png' ? 'image/png' : 'image/jpeg';
        out.push({ absPath: full, relPath: rel, size: stat.size, contentType: ct });
      }
    }
  }
  walk(root);
  return out;
}

// uploadShard mirrors how the dispatcher action's upload tail lands a
// CI matrix worker's per-shard artifacts. Steps:
//
//   1. POST /api/v1/reports/begin (idempotent on composite identity).
//   2. POST /api/v1/reports/register declaring the JSON + screenshot
//      manifest.
//   3. POST /api/v1/reports/upload/<group>/<upload>/json as multipart
//      with one part per Mochawesome JSON file.
//   4. POST /api/v1/reports/upload/<group>/<upload>/screenshots as
//      multipart with one part per image (only when at least one
//      screenshot exists, e.g. from a failing test).
//
// The local demo is a single virtual worker (NUM_WORKERS=1 by default;
// even with NUM_WORKERS>1 we still upload one combined shard for
// simplicity).
async function uploadShard() {
  const jsonFiles = fs
    .readdirSync(ACCUMULATOR_DIR)
    .filter((f) => f.endsWith('.json'))
    .map((f) => ({
      absPath: path.join(ACCUMULATOR_DIR, f),
      relPath: f,
      size: fs.statSync(path.join(ACCUMULATOR_DIR, f)).size,
      contentType: 'application/json',
    }));

  if (jsonFiles.length === 0) {
    console.log('[upload] no Mochawesome JSON accumulated; skipping reports upload');
    return;
  }

  const screenshots = listScreenshotFiles();

  // Begin (idempotent on composite identity; returns the same
  // report_group_id every time within this demo run).
  const beginBody = {
    ...reportsIdentityBody(),
    total_reports_expected: NUM_WORKERS,
  };
  const beginRes = await post('/api/v1/reports/begin', beginBody);
  if (beginRes.status !== 200 && beginRes.status !== 201) {
    throw new Error(`reports/begin failed: ${beginRes.status} ${JSON.stringify(beginRes.body)}`);
  }
  const reportGroupId = beginRes.body.report_id;
  console.log(`[upload] reports/begin → group ${reportGroupId}`);

  // Register the manifest. The first worker's identity is sufficient
  // for the demo's single-shard upload.
  const workerName = 'cypress-worker-0';
  const workerId = `${NOW_MS}-0`;
  const registerBody = {
    ...reportsIdentityBody(),
    gh_job_id: workerId,
    gh_job_name: workerName,
    json_files: jsonFiles.map((f) => ({ path: f.relPath, size: f.size })),
    screenshots: screenshots.map((f) => ({ path: f.relPath, size: f.size })),
  };
  const regRes = await post('/api/v1/reports/register', registerBody);
  if (regRes.status !== 200 && regRes.status !== 201) {
    throw new Error(`reports/register failed: ${regRes.status} ${JSON.stringify(regRes.body)}`);
  }
  const uploadId = regRes.body.upload_id;
  console.log(
    `[upload] reports/register → upload ${uploadId} ` +
      `(${jsonFiles.length} json, ${screenshots.length} screenshot(s))`,
  );

  await uploadMultipart(
    `${API_BASE}/api/v1/reports/upload/${reportGroupId}/${uploadId}/json`,
    jsonFiles,
  );
  console.log(`[upload] uploaded ${jsonFiles.length} JSON files`);

  if (screenshots.length > 0) {
    await uploadMultipart(
      `${API_BASE}/api/v1/reports/upload/${reportGroupId}/${uploadId}/screenshots`,
      screenshots,
    );
    console.log(`[upload] uploaded ${screenshots.length} screenshot(s)`);
  }
}

// Streams a multipart upload via Node's built-in fetch + FormData.
// Filenames in the form match the manifest's relPath so the server
// can correlate uploaded blobs to declared entries.
async function uploadMultipart(url, files) {
  const form = new FormData();
  for (const f of files) {
    const buf = fs.readFileSync(f.absPath);
    form.append('files', new Blob([buf], { type: f.contentType }), f.relPath);
  }
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'X-API-Key': API_KEY },
    body: form,
  });
  if (res.status !== 200) {
    const text = await res.text().catch(() => '');
    throw new Error(`upload to ${url} failed: ${res.status} ${text}`);
  }
}

// ─── Final status + page hint ──────────────────────────────────────────────

async function pollFinalStatus() {
  const params = new URLSearchParams({
    repository: IDENTITY.repository,
    commit_sha: IDENTITY.commit_sha,
    gh_run_id: IDENTITY.gh_run_id,
    name: IDENTITY.name,
    gh_run_attempt: IDENTITY.gh_run_attempt,
  });
  const resp = await get(`/api/v1/orchestration/status?${params.toString()}`);
  return resp.body;
}

function printPageHint() {
  const repo = encodeURIComponent(IDENTITY.repository);
  const branch = encodeURIComponent(IDENTITY.branch);
  const shortSha = IDENTITY.commit_sha.slice(0, 7);
  const name = encodeURIComponent(IDENTITY.name);
  const url = `https://localhost:3000/reports/${repo}/${branch}/${shortSha}/${name}?gh_run_id=${encodeURIComponent(IDENTITY.gh_run_id)}&tab=orchestration`;
  console.log('');
  console.log('Orchestration tab:');
  console.log(`  ${url}`);
  console.log('');
}

// ─── Main ──────────────────────────────────────────────────────────────────

async function main() {
  console.log(`Orchestration demo (cypress) against ${API_BASE}`);
  console.log(
    `  workers: ${NUM_WORKERS}, retest: ${RETEST} (budget=${RETEST_BUDGET})`,
  );
  console.log(
    `  identity: ${IDENTITY.repository} @ ${IDENTITY.commit_sha.slice(0, 7)} / ${IDENTITY.name} (run ${IDENTITY.gh_run_id})`,
  );
  console.log(
    `  branch: ${IDENTITY.branch}${IDENTITY.gh_pr_number != null ? ` (PR #${IDENTITY.gh_pr_number})` : ''}`,
  );
  console.log(`  fixture: ${CYPRESS_DIR}`);
  console.log('');

  const specFiles = listSpecFiles();
  if (specFiles.length === 0) {
    throw new Error(`No *_spec.{ts,js} files found under ${SEED_TESTS_DIR}`);
  }
  const dispatchUnits = buildDispatchUnits(specFiles);
  console.log('Dispatch order:');
  for (const [i, u] of dispatchUnits.entries()) {
    console.log(`  [${i}] ${u.spec_path}`);
  }
  console.log('');

  // Wipe any previous Mochawesome output and Cypress screenshots so
  // per-spec lookups (and the queue-empty screenshot upload) don't
  // pick up stale files from a prior demo invocation.
  fs.rmSync(path.join(CYPRESS_DIR, 'results'), { recursive: true, force: true });
  fs.rmSync(path.join(CYPRESS_DIR, 'tests', 'screenshots'), { recursive: true, force: true });

  await beginRun(dispatchUnits);
  printPageHint();

  const workers = Array.from({ length: NUM_WORKERS }, (_, i) => runWorker(i));
  await Promise.all(workers);

  // Land the per-shard Mochawesome bundle on the report-group view so
  // the dashboard's Reports tab populates alongside the Orchestration
  // tab. Wrapped in try/catch — a failed upload shouldn't mask the
  // orchestration outcome the user is here to see.
  try {
    await uploadShard();
  } catch (err) {
    console.warn(`[upload] shard upload failed (non-fatal): ${err.message}`);
  }

  console.log('');
  console.log('All workers exited. Final run status:');
  const final = await pollFinalStatus();
  console.log(JSON.stringify(final, null, 2));
}

main().catch((err) => {
  console.error('orchestration-demo-cypress failed:', err.message);
  process.exit(1);
});
