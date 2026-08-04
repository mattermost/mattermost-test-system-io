#!/usr/bin/env node
/**
 * CI replay — drives the real orchestration server's begin/checkout/complete
 * endpoints using real historical CI artifacts (see README.md). One
 * simulated worker per historical `<group>-debug-N/` directory; each replays
 * a recorded spec outcome/duration (scaled by SPEED) instead of running a
 * test framework.
 *
 * Prerequisites:
 *
 *   make docker-up && make db-migrate && make dev-server   # one terminal
 *   eval "$(make seed | grep '^TSIO_API_KEY=')"            # another
 *   node scripts/ci-replay/replay.js
 *
 * Env vars:
 *
 *   GROUP=cypress-full-fips     # required: cypress-full | cypress-full-fips |
 *                               #           playwright-full | playwright-full-fips |
 *                               #           detox-ios | detox-android | detox-ipad |
 *                               #           maestro-ios | maestro-android
 *   CI_RUNS_ROOT=.local/mattermost-ci  # where the corpus was downloaded (see README.md);
 *                               # defaults to .local/mattermost-mobile-ci for detox-* / maestro-* groups
 *   SPEED=1                     # duration scale-down multiplier (10 = 10x faster than real time)
 *   RETEST=1 RETEST_BUDGET=1    # retest-on-fail config for the simulated run (default on)
 *   MAX_IDLE_POLLS=5            # matches the real dispatch-run action's default
 *   POST_FAILURE_DELAY_MS=10000 # matches the real dispatch-run action's default
 *   INJECT_LEASE_TIMEOUT_RATE=0 # 0..1, synthetic: probability a worker skips /complete
 *   API_BASE=http://localhost:8080
 *   TSIO_API_KEY=...            # required (make seed prints a full export line)
 *   TSIO_COMMIT_SHA=...         # pin an exact commit_sha; default is the current minute,
 *                               # shared automatically across terminals started together
 *                               # (e.g. one per group, replicating 4 parallel CI jobs on one commit)
 *   UPLOAD_SHARDS=0             # opt-in: also do the Cypress inline screenshot attach and
 *                               # each worker's end-of-drain shard upload — see reports_client.js
 *   VERIFY_TIMEOUT_MS=120000    # UPLOAD_SHARDS=1 only: poll timeout for ingest to converge.
 *                               # Not scaled by SPEED — real server-side latency.
 */

'use strict';

const { loadCorpus, percentileDurationMs } = require('./corpus');
const { request, sleep } = require('./client');
const { runWorker } = require('./worker');

const API_BASE = process.env.API_BASE || 'http://localhost:8080';
const WEB_BASE = process.env.WEB_BASE || 'http://localhost:3000';
const API_KEY = process.env.TSIO_API_KEY;

if (!API_KEY) {
  console.error('TSIO_API_KEY is required.');
  console.error('');
  console.error('`make seed` prints a complete `TSIO_API_KEY=...` line — paste');
  console.error('it into your shell prefixed with `export` (or `eval` it).');
  console.error('');
  console.error('  eval "$(make seed | grep \'^TSIO_API_KEY=\')"');
  console.error('  GROUP=cypress-full-fips node scripts/ci-replay/replay.js');
  process.exit(2);
}
if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(API_KEY)) {
  console.error('TSIO_API_KEY looks like a UUID (api_key id), not an API key.');
  console.error('Re-run `make seed` and copy the entire `TSIO_API_KEY=...` line.');
  process.exit(2);
}

const GROUP = process.env.GROUP;
if (!GROUP) {
  console.error(
    'GROUP is required: one of cypress-full, cypress-full-fips, playwright-full, ' +
      'playwright-full-fips, detox-ios, detox-android, detox-ipad, maestro-ios, maestro-android',
  );
  process.exit(2);
}

// Coerces an env var to a finite number, falling back on empty/non-numeric
// input instead of propagating NaN (e.g. a typo'd SPEED=fast would otherwise
// make every replayed spec finish instantly via durationMs / NaN).
const num = (raw, fallback) => {
  const v = Number(raw);
  return Number.isFinite(v) ? v : fallback;
};

const SPEED = Math.max(0.001, num(process.env.SPEED, 1));
const RETEST = process.env.RETEST !== '0' && process.env.RETEST !== 'false';
const RETEST_BUDGET = Math.max(0, Math.trunc(num(process.env.RETEST_BUDGET, 1)));
const MAX_IDLE_POLLS = Math.max(0, Math.trunc(num(process.env.MAX_IDLE_POLLS, 5)));
const POST_FAILURE_DELAY_MS = Math.max(0, Math.trunc(num(process.env.POST_FAILURE_DELAY_MS, 10000)));
const INJECT_LEASE_TIMEOUT_RATE = Math.min(1, Math.max(0, num(process.env.INJECT_LEASE_TIMEOUT_RATE, 0)));
const UPLOAD_SHARDS = process.env.UPLOAD_SHARDS === '1' || process.env.UPLOAD_SHARDS === 'true';
const VERIFY_TIMEOUT_MS = Math.max(0, Math.trunc(num(process.env.VERIFY_TIMEOUT_MS, 120000)));

const post = (p, body) => request(API_BASE, API_KEY, 'POST', p, body);
const get = (p) => request(API_BASE, API_KEY, 'GET', p, null);

const NOW_MS = Date.now();
// Rounded down to the minute so terminals started around the same time
// (e.g. one per group) share a commit_sha with no coordination needed.
// Override with TSIO_COMMIT_SHA to pin an exact value.
const COMMIT_MINUTE_MS = Math.floor(NOW_MS / 60_000) * 60_000;

function buildIdentity(framework) {
  return {
    repository: framework === 'detox' ? 'mattermost/mattermost-mobile' : 'mattermost/mattermost',
    // Must be pure hex — the web dashboard's URL router only treats
    // /reports/:repo/:branch/:commit/:name as a single-run page when the
    // commit segment matches /^[0-9a-f]{7,40}$/i.
    commit_sha: (
      process.env.TSIO_COMMIT_SHA || COMMIT_MINUTE_MS.toString(16).padEnd(40, '0')
    ).toLowerCase(),
    gh_run_id: `replay-${GROUP}-${NOW_MS}`,
    name: GROUP,
    gh_run_attempt: '1',
    framework,
    branch: framework === 'detox' ? 'main' : 'master',
  };
}

async function beginRun(identity, dispatchUnits, leaseTimeoutMs, workerCount) {
  const body = {
    ...identity,
    lease_timeout_ms: leaseTimeoutMs,
    idle_timeout_ms: 600_000,
    retest_on_fail: RETEST,
    retest_budget: RETEST_BUDGET,
    total_reports_expected: workerCount,
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

// printPageHint prints the dashboard URL for this run. Only prints the
// Reports tab link when UPLOAD_SHARDS actually populated it.
function printPageHint(identity) {
  const repo = encodeURIComponent(identity.repository.split('/').pop());
  const branch = encodeURIComponent(identity.branch);
  const shortSha = identity.commit_sha.slice(0, 7);
  const name = encodeURIComponent(identity.name);
  const dispatchParams = new URLSearchParams({ gh_run_id: identity.gh_run_id, tab: 'dispatch' });
  console.log(
    `Orchestration tab: ${WEB_BASE}/reports/${repo}/${branch}/${shortSha}/${name}?${dispatchParams.toString()}`,
  );
  if (UPLOAD_SHARDS) {
    const reportsParams = new URLSearchParams({ gh_run_id: identity.gh_run_id, tab: 'reports' });
    console.log(
      `Reports tab: ${WEB_BASE}/reports/${repo}/${branch}/${shortSha}/${name}?${reportsParams.toString()}`,
    );
  }
}

async function pollFinalStatus(identity) {
  const params = new URLSearchParams({
    repository: identity.repository,
    commit_sha: identity.commit_sha,
    gh_run_id: identity.gh_run_id,
    name: identity.name,
    gh_run_attempt: identity.gh_run_attempt,
  });
  const resp = await get(`/api/v1/orchestration/status?${params.toString()}`);
  if (resp.status !== 200) {
    throw new Error(`status failed: ${resp.status} ${JSON.stringify(resp.body)}`);
  }
  return resp.body;
}

// verifyShardUpload polls GET /reports/{id} and /reports/{id}/suites until
// ingest converges, then prints a PASS/FAIL/WARN summary. expectedReports
// counts workers that actually produced a shard, not corpus.workerCount.
async function verifyShardUpload(reportGroupId, workerResults, corpus) {
  const expectedReports = workerResults.filter((w) => w.shard).length;
  if (expectedReports === 0) {
    console.log('');
    console.log('Shard verification: SKIP (no worker produced a shard — nothing to verify)');
    return true;
  }

  console.log('');
  console.log(`Verifying shard upload (group=${reportGroupId}, expecting ${expectedReports} report(s))...`);

  const deadline = Date.now() + VERIFY_TIMEOUT_MS;
  let detail = null;
  for (;;) {
    const resp = await get(`/api/v1/reports/${reportGroupId}`);
    if (resp.status !== 200) {
      throw new Error(`reports detail failed: ${resp.status} ${JSON.stringify(resp.body)}`);
    }
    detail = resp.body;
    const reports = detail.reports || [];
    const anyFailed = reports.some((r) => r.status === 'failed');
    const allComplete = reports.length >= expectedReports && reports.every((r) => r.status === 'complete');
    if (anyFailed || allComplete || Date.now() >= deadline) break;
    await sleep(1500);
  }

  const reports = detail.reports || [];
  const completedCount = reports.filter((r) => r.status === 'complete').length;
  const failedCount = reports.filter((r) => r.status === 'failed').length;
  const groupCompleted = detail.status === 'completed';

  console.log('');
  console.log('Shard verification:');
  console.log(
    `  ${failedCount === 0 ? 'PASS' : 'FAIL'}: 0 failed reports (${failedCount} failed / ${reports.length} total)`,
  );
  console.log(
    `  ${completedCount >= expectedReports ? 'PASS' : 'FAIL'}: all expected reports finalized ` +
      `(${completedCount}/${expectedReports} complete)`,
  );
  console.log(
    `  ${groupCompleted ? 'PASS' : 'WARN'}: report_groups reached status=completed (actual: ${detail.status})` +
      (groupCompleted ? '' : ' — best-effort, see README'),
  );

  let suiteCheckPassed = true;
  if (corpus.framework === 'detox') {
    // Not a floor check — expected to ingest zero suites, full stop.
    // ingest/detox.go's extractDetox expects the shape mattermost-mobile's
    // own merge-jest-results-for-tsio.js produces (testFilePath / nested
    // testResults), not the native Jest JSON this tool uploads as-is (see
    // DETOX_ORCHESTRATION_PLAN.md's evidence section). This is a pre-existing
    // report-upload-path gap, unrelated to replay/dispatch fidelity, so it's
    // never scored as a failure here.
    console.log(
      '  SKIP: suite ingestion check (native Jest JSON isn\'t the shape ingest/detox.go\'s ' +
        'extractDetox expects — a known report-upload-path gap, not a replay defect)',
    );
  } else {
    try {
      const suitesResp = await get(`/api/v1/reports/${reportGroupId}/suites`);
      if (suitesResp.status === 200) {
        const suites = (suitesResp.body && suitesResp.body.suites) || [];
        const withFile = suites.filter((s) => s.file_path != null).length;
        // Floor, not equality: Playwright samples can over-ingest sibling
        // specs from a shared source file (see reports_client.js).
        const floor = corpus.framework === 'cypress' ? corpus.specPaths.length * 0.9 : 1;
        suiteCheckPassed = withFile >= floor;
        console.log(
          `  ${suiteCheckPassed ? 'PASS' : 'FAIL'}: ingested suite count vs. corpus spec count ` +
            `(${withFile} suites w/ file_path vs. ${corpus.specPaths.length} dispatched specs` +
            `${corpus.framework === 'playwright' ? ' — over-ingestion expected here, this is a floor check' : ''})`,
        );
      } else {
        console.log(`  WARN: could not fetch suites for comparison (${suitesResp.status})`);
      }
    } catch (err) {
      console.log(`  WARN: suites comparison failed: ${err.message}`);
    }
  }

  const hardFail = failedCount > 0 || completedCount < expectedReports || !suiteCheckPassed;
  console.log('');
  console.log(hardFail ? 'Shard verification: FAIL' : 'Shard verification: PASS');
  return !hardFail;
}

async function main() {
  console.log(`CI replay against ${API_BASE}`);
  console.log(`  group: ${GROUP}, speed: ${SPEED}x, retest: ${RETEST} (budget=${RETEST_BUDGET})`);
  console.log(`  max-idle-polls: ${MAX_IDLE_POLLS}, post-failure-delay-ms: ${POST_FAILURE_DELAY_MS}`);
  if (INJECT_LEASE_TIMEOUT_RATE > 0) {
    console.log(`  inject-lease-timeout-rate: ${INJECT_LEASE_TIMEOUT_RATE} (synthetic — not a replay-from-history behavior)`);
  }
  console.log('');

  const corpus = loadCorpus(GROUP);
  console.log(
    `Corpus: ${corpus.specPaths.length} distinct spec(s), ` +
      `${[...corpus.samplesBySpec.values()].reduce((a, arr) => a + arr.length, 0)} recorded sample(s), ` +
      `${corpus.workerCount} historical worker(s) (debug-dirs)`,
  );
  if (corpus.singleSampleSpecPaths.length > 0) {
    console.log(
      `  note: ${corpus.singleSampleSpecPaths.length}/${corpus.specPaths.length} spec(s) have only one ` +
        'recorded sample — retests for those will reuse it (with-replacement)',
    );
  }

  // Matches the real dispatch-begin action's default, scaled by SPEED and
  // floored at 5s for very high speeds.
  const GH_ACTIONS_DEFAULT_LEASE_TIMEOUT_MS = 600_000;
  const leaseTimeoutMs = Math.max(5000, Math.round(GH_ACTIONS_DEFAULT_LEASE_TIMEOUT_MS / SPEED));
  const p90Ms = percentileDurationMs(corpus.samplesBySpec, 90);
  console.log(
    `lease_timeout_ms=${leaseTimeoutMs} (GH Actions default 600000ms, scaled by speed; ` +
      `corpus's own p90 duration is ${p90Ms}ms for reference)`,
  );
  console.log('');

  const identity = buildIdentity(corpus.framework);
  const dispatchUnits = corpus.specPaths.map((p) => ({ spec_path: p }));

  await beginRun(identity, dispatchUnits, leaseTimeoutMs, corpus.workerCount);
  console.log(
    `Identity: ${identity.repository} / ${identity.name} (run ${identity.gh_run_id}), framework=${identity.framework}`,
  );
  printPageHint(identity);
  console.log('');

  const workers = [];
  for (let i = 0; i < corpus.workerCount; i++) {
    const workerName = `replay-${GROUP}-${i}`;
    const workerId = `${NOW_MS}-${i}`;
    const startupJitterMs = Math.round(Math.random() * 2000);
    workers.push(
      (async () => {
        await sleep(startupJitterMs);
        return runWorker({
          apiBase: API_BASE,
          apiKey: API_KEY,
          identity,
          workerName,
          workerId,
          samplesBySpec: corpus.samplesBySpec,
          speed: SPEED,
          maxIdlePolls: MAX_IDLE_POLLS,
          postFailureDelayMs: POST_FAILURE_DELAY_MS,
          injectLeaseTimeoutRate: INJECT_LEASE_TIMEOUT_RATE,
          uploadShards: UPLOAD_SHARDS,
          framework: corpus.framework,
          log: (msg) => console.log(`[${workerName}] ${msg}`),
        });
      })(),
    );
  }

  const workerResults = await Promise.all(workers);

  console.log('');
  console.log('All workers exited. Final run status:');
  const final = await pollFinalStatus(identity);
  // Drop `units` — hundreds of KB of per-test detail, not useful here.
  const { units, ...summary } = final;
  console.log(JSON.stringify(summary, null, 2));

  if (UPLOAD_SHARDS) {
    const reportGroupId = workerResults.find((w) => w.shard)?.shard.reportId;
    if (!reportGroupId) {
      console.log('');
      console.log('Shard verification: SKIP (no worker produced a shard — nothing to verify)');
      return;
    }
    const ok = await verifyShardUpload(reportGroupId, workerResults, corpus);
    if (!ok) process.exitCode = 1;
  }
}

main().catch((err) => {
  console.error('ci-replay failed:', err.message);
  process.exit(1);
});
