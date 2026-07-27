// One simulated worker's drain loop, replaying real historical spec
// outcomes instead of invoking Cypress/Playwright. Mirrors main.ts's
// drain(), including retest-pool polling.
//
// Opt-in (cfg.uploadShards): also does the Cypress inline screenshot
// attach and end-of-drain shard upload — see reports_client.js.

'use strict';

const path = require('path');
const { request, sleep } = require('./client');
const { uploadShardForWorker, uploadOrchScreenshot } = require('./reports_client');

// Mirrors the server's failure classification (complete.go's
// mapStatusesToUnitState) — statuses that make a unit retest-eligible.
const RETEST_ELIGIBLE_STATUSES = new Set(['failed', 'timedOut', 'interrupted']);

// Ported from main.ts's formatDiagnostics: renders counts/workers/db_pool
// as a bracketed log suffix. Returns '' if none are present.
function formatDiagnostics(body) {
  const parts = [];
  const c = body && body.counts;
  if (c) {
    parts.push(
      `queue: pending=${c.pending} leased=${c.leased} pass=${c.completed_pass} ` +
        `fail=${c.completed_fail} skip=${c.completed_skipped} retest_eligible=${c.retest_eligible} ` +
        `total=${c.total}`,
    );
  }
  const w = body && body.workers;
  if (w) {
    parts.push(`workers: active=${w.active} seen_total=${w.seen_total}`);
  }
  const p = body && body.db_pool;
  if (p) {
    parts.push(
      `db_pool: acquired=${p.acquired_conns} idle=${p.idle_conns} total=${p.total_conns}/${p.max_conns} ` +
        `empty_acquires=${p.empty_acquire_count}`,
    );
  }
  return parts.length > 0 ? ` [${parts.join(' | ')}]` : '';
}

// pickSample chooses a recorded sample to replay for a leased unit. On a
// retest, prefers a different sample than last time (mimics flake-then-pass)
// when more than one exists, else falls back to with-replacement reuse.
function pickSample(samples, isRetest, lastSample) {
  if (samples.length === 1) return samples[0];
  if (isRetest && lastSample) {
    const others = samples.filter((s) => s !== lastSample);
    if (others.length > 0) {
      return others[Math.floor(Math.random() * others.length)];
    }
  }
  return samples[Math.floor(Math.random() * samples.length)];
}

async function checkoutOnce(cfg, identity) {
  const resp = await request(cfg.apiBase, cfg.apiKey, 'POST', '/api/v1/orchestration/checkout', {
    ...identity,
    gh_job_name: cfg.workerName,
    gh_job_id: cfg.workerId,
    batch_size: 1,
  });
  if (resp.status === 409 && resp.body && resp.body.error === 'WORKER_HAS_ACTIVE_LEASE') {
    return { conflict: true };
  }
  if (resp.status === 409 && resp.body && resp.body.error === 'RUN_NOT_IN_PROGRESS') {
    return { queue_empty: true, terminated: true };
  }
  if (resp.status !== 200) {
    throw new Error(`checkout failed (${cfg.workerName}): ${resp.status} ${JSON.stringify(resp.body)}`);
  }
  return resp.body;
}

async function completeOnce(cfg, identity, results) {
  const resp = await request(cfg.apiBase, cfg.apiKey, 'POST', '/api/v1/orchestration/complete', {
    ...identity,
    gh_job_name: cfg.workerName,
    gh_job_id: cfg.workerId,
    results,
  });
  if (resp.status !== 200) {
    throw new Error(`complete failed (${cfg.workerName}): ${resp.status} ${JSON.stringify(resp.body)}`);
  }
  return resp.body;
}

// attachCypressScreenshots uploads each recorded screenshot for this
// leased batch (lease must still be held — server 400s otherwise) and
// attaches the result onto the longest-title-matching failed test_case.
async function attachCypressScreenshots(cfg, picks) {
  for (const { unit, sample, result } of picks) {
    const files = sample && sample.screenshotFiles;
    if (!files || files.length === 0 || !result.test_cases) continue;

    const candidates = result.test_cases
      .filter((tc) => tc.status === 'failed' || tc.status === 'timedOut' || tc.status === 'interrupted')
      .slice()
      .sort((a, b) => b.title.length - a.title.length);
    if (candidates.length === 0) continue;

    for (const absPath of files) {
      const base = path.basename(absPath);
      const tc = candidates.find((c) => c.title && base.includes(c.title));
      if (!tc) {
        cfg.log(`no test_case match for screenshot ${base}; skipping`);
        continue;
      }
      const uploaded = await uploadOrchScreenshot(
        cfg.apiBase,
        cfg.apiKey,
        cfg.identity,
        cfg.workerId,
        cfg.workerName,
        unit.spec_path,
        absPath,
        cfg.log,
      );
      if (!uploaded) continue;
      tc.attachments = tc.attachments || {};
      tc.attachments.screenshots = tc.attachments.screenshots || [];
      tc.attachments.screenshots.push(uploaded);
    }
  }
}

// runWorker drives one simulated worker until nothing's left: checkout ->
// replay a recorded sample (sleep, scaled by cfg.speed) -> complete -> repeat.
//
// cfg: { apiBase, apiKey, identity, workerName, workerId, samplesBySpec,
//        speed, maxIdlePolls, postFailureDelayMs, injectLeaseTimeoutRate,
//        uploadShards, framework, log }
//
// Resolves {workerName, leasesHeld, shard} — shard is null when
// uploadShards is off or nothing was leased.
async function runWorker(cfg) {
  const { identity, samplesBySpec, log } = cfg;
  const lastSampleByUnit = new Map();
  const invocations = [];
  let idlePolls = 0;
  let leasesHeld = 0;
  let exitReason = null;

  for (;;) {
    const co = await checkoutOnce(cfg, identity);

    if (co.conflict) {
      // A prior lease is still active; wait for it to expire and retry.
      log('active-lease conflict; sleeping 2000ms');
      await sleep(2000);
      continue;
    }

    if (co.queue_empty) {
      const diag = formatDiagnostics(co);
      if (co.retry_after_ms && !co.terminated) {
        idlePolls += 1;
        if (cfg.maxIdlePolls > 0 && idlePolls >= cfg.maxIdlePolls) {
          log(
            `queue empty after ${leasesHeld} unit(s); giving up after ${idlePolls} consecutive idle ` +
              `poll(s) (max-idle-polls=${cfg.maxIdlePolls})${diag}`,
          );
          exitReason = 'idle-give-up';
          break;
        }
        log(
          `queue empty; sleeping ${co.retry_after_ms}ms before re-polling ` +
            `(idle poll ${idlePolls}${cfg.maxIdlePolls > 0 ? `/${cfg.maxIdlePolls}` : ''})${diag}`,
        );
        await sleep(co.retry_after_ms);
        continue;
      }
      log(`queue empty after ${leasesHeld} unit(s); exiting cleanly${diag}`);
      exitReason = 'clean';
      break;
    }

    idlePolls = 0;
    leasesHeld += 1;

    const units = co.units || [];
    const isRetest = Boolean(co.is_retest);
    const picks = units.map((u) => {
      const samples = samplesBySpec.get(u.spec_path);
      if (!samples || samples.length === 0) {
        // Should not happen — fail safe rather than crash the worker.
        log(`no recorded sample for ${u.spec_path}; replaying a synthetic pass`);
        return { unit: u, sample: null, result: { spec_path: u.spec_path, status: 'passed', actual_duration_ms: 0 } };
      }
      const sample = pickSample(samples, isRetest, lastSampleByUnit.get(u.unit_id));
      lastSampleByUnit.set(u.unit_id, sample);
      return {
        unit: u,
        sample,
        result: {
          spec_path: u.spec_path,
          status: sample.status,
          actual_duration_ms: sample.actual_duration_ms,
          // Deep-copy: samples are pooled and shared across workers/retests,
          // and attachCypressScreenshots mutates test_cases in place.
          test_cases: structuredClone(sample.test_cases),
        },
      };
    });

    if (cfg.uploadShards) {
      for (const { unit, sample } of picks) {
        if (sample && sample.sourcePath && sample.iterDir) {
          invocations.push({
            specPath: unit.spec_path,
            iterDir: sample.iterDir,
            sourcePath: sample.sourcePath,
            screenshotFiles: sample.screenshotFiles,
          });
        }
      }
      if (cfg.framework === 'cypress') {
        await attachCypressScreenshots(cfg, picks);
      }
    }

    const results = picks.map((p) => p.result);
    const label = units.map((u) => `${u.dispatch_seq} ${u.spec_path}`).join(', ');
    log(`leased (${isRetest ? 'retest' : 'fresh'}): ${label}${formatDiagnostics(co)}`);

    const durationMs = Math.max(0, ...results.map((r) => r.actual_duration_ms || 0));
    const sleepMs = durationMs / cfg.speed;

    if (cfg.injectLeaseTimeoutRate > 0 && Math.random() < cfg.injectLeaseTimeoutRate) {
      log(`injecting lease timeout (skipping /complete) for: ${label}`);
      await sleep(sleepMs);
      continue;
    }
    await sleep(sleepMs);

    const complete = await completeOnce(cfg, identity, results);
    const transitions = ((complete && complete.unit_states_changed) || [])
      .map((c) => c.new_state)
      .join(',');
    log(
      `reported (${results.map((r) => r.status).join(',')}) → ${transitions || '(no transition)'}` +
        formatDiagnostics(complete || {}),
    );

    const anyRetestEligible = results.some((r) => RETEST_ELIGIBLE_STATUSES.has(r.status));
    const pendingElsewhere = complete && complete.counts ? complete.counts.pending : undefined;
    if (anyRetestEligible && pendingElsewhere === 0 && cfg.postFailureDelayMs > 0) {
      log(
        `retest-eligible result reported; waiting ${cfg.postFailureDelayMs}ms before next ` +
          "checkout so another worker's poll can pick up the retest",
      );
      await sleep(cfg.postFailureDelayMs);
    }
  }

  let shard = null;
  if (cfg.uploadShards && invocations.length > 0) {
    try {
      shard = await uploadShardForWorker(cfg.apiBase, cfg.apiKey, identity, cfg.workerId, cfg.workerName, invocations, log);
    } catch (err) {
      // A shard-upload failure is logged but doesn't crash the worker.
      log(`shard upload failed: ${err.message}`);
    }
  }

  return { workerName: cfg.workerName, leasesHeld, exitReason, shard };
}

module.exports = { runWorker, pickSample, formatDiagnostics, RETEST_ELIGIBLE_STATUSES };
