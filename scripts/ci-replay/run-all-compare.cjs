#!/usr/bin/env node
'use strict';

const { spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const { randomCommitSha, commitShaForGroup, isReplayGhRunId } = require('./identity');

const GROUPS = [
  'maestro-ios',
  'maestro-android',
  'detox-ipad',
  'playwright-full',
  'playwright-full-fips',
  'detox-ios',
  'detox-android',
  'cypress-full-fips',
  'cypress-full',
];

const API_BASE = process.env.API_BASE || 'http://localhost:8080';
const SPEED = process.env.SPEED || '60';
const API_KEY = process.env.TSIO_API_KEY;

const batchCommits = {
  mattermost: (process.env.TSIO_COMMIT_SHA_MATTERMOST || randomCommitSha()).toLowerCase(),
  mobile: (process.env.TSIO_COMMIT_SHA_MOBILE || randomCommitSha()).toLowerCase(),
};

if (!API_KEY) {
  console.error('TSIO_API_KEY is required');
  process.exit(2);
}

const replayJs = path.join(__dirname, 'replay.js');
const results = [];
const batchStartedMs = Date.now();

console.log(
  `Batch CI replay — mattermost ${batchCommits.mattermost.slice(0, 7)} / mobile ${batchCommits.mobile.slice(0, 7)}… speed ${SPEED}x`,
);
console.log('');

for (const group of GROUPS) {
  console.log(`=== ${group} ===`);
  const started = Date.now();
  const groupCommit = commitShaForGroup(group, batchCommits);
  const proc = spawnSync(process.execPath, [replayJs], {
    cwd: path.join(__dirname, '..', '..'),
    env: {
      ...process.env,
      GROUP: group,
      SPEED,
      TSIO_COMMIT_SHA: groupCommit,
      API_BASE,
      TSIO_API_KEY: API_KEY,
    },
    encoding: 'utf8',
    maxBuffer: 20 * 1024 * 1024,
  });
  const elapsed = ((Date.now() - started) / 1000).toFixed(1);
  const out = (proc.stdout || '') + (proc.stderr || '');
  process.stdout.write(proc.stdout || '');
  process.stderr.write(proc.stderr || '');

  let status = 'unknown';
  let runStatus = null;
  const statusMatch = out.match(/"status"\s*:\s*"([^"]+)"/g);
  if (statusMatch) {
    const last = statusMatch[statusMatch.length - 1];
    const m = last.match(/"status"\s*:\s*"([^"]+)"/);
    runStatus = m ? m[1] : null;
  }
  if (proc.status !== 0) {
    status = 'FAIL';
  } else if (runStatus === 'completed' || runStatus === 'failed') {
    status = runStatus === 'completed' ? 'PASS' : 'FAIL';
  } else {
    status = proc.status === 0 ? 'PASS' : 'FAIL';
  }

  results.push({ group, status, runStatus, elapsed, exitCode: proc.status });
  console.log(`--- ${group}: ${status} (${elapsed}s, orchestration=${runStatus ?? 'n/a'}) ---\n`);
}

console.log('\n=== Replay summary ===');
for (const r of results) {
  console.log(`${r.status.padEnd(5)} ${r.group.padEnd(22)} ${r.elapsed}s  status=${r.runStatus ?? 'n/a'}`);
}

const failed = results.filter((r) => r.status !== 'PASS');
if (failed.length) {
  console.log(`\n${failed.length} group(s) failed replay`);
  process.exitCode = 1;
}

(async () => {
  console.log('\n=== SSR vs API comparison ===');
  const groupedResp = await fetch(`${API_BASE}/api/v1/reports/grouped?limit=50&offset=0`);
  const grouped = await groupedResp.json();
  const htmlResp = await fetch(`${API_BASE}/`);
  const html = await htmlResp.text();

  const apiRuns = grouped.groups
    .flatMap((g) => g.runs)
    .filter((r) => {
      const gh = String(r.gh_run_id || '');
      if (!isReplayGhRunId(gh)) return false;
      const ts = Number(/^gh_run_(\d+)$/.exec(gh)?.[1]);
      return Number.isFinite(ts) && ts >= batchStartedMs - 5000;
    });

  const htmlFailed = (html.match(/class="run-row run-row-failed"/g) || []).length;
  const htmlRows = (html.match(/class="run-row/g) || []).length;

  const apiFailed = apiRuns.filter((r) => {
    const stats = r.orchestration?.tests || r.test_stats;
    return stats && stats.failed > 0;
  }).length;

  console.log(`API replay runs on page 1: ${apiRuns.length}`);
  console.log(`SSR run rows on page 1: ${htmlRows}`);
  console.log(`API replay runs with failures: ${apiFailed}`);
  console.log(`SSR failed row wrappers: ${htmlFailed}`);

  const issues = [];
  for (const run of apiRuns) {
    const name = run.name || '';
    const gh = run.gh_run_id || '';
    const inHtml = html.includes(`run-name">${name}<`) && html.includes(gh);
    if (!inHtml) issues.push(`missing in SSR HTML: ${name} (${gh})`);
    const stats = run.orchestration?.tests || run.test_stats;
    const expectFailed = stats && stats.failed > 0;
    const ghIdx = html.indexOf(gh);
    if (expectFailed && ghIdx >= 0) {
      const slice = html.slice(Math.max(0, ghIdx - 500), ghIdx + 200);
      if (!slice.includes('run-row-failed')) {
        issues.push(`SSR missing run-row-failed for ${name} (${stats.failed} failed)`);
      }
    }
  }

  if (apiRuns.length < GROUPS.length) {
    issues.push(`expected ${GROUPS.length} replay runs on page 1, found ${apiRuns.length}`);
  }

  const liveResp = await fetch(`${API_BASE}/reports/fragment/home-live`);
  const live = await liveResp.json();
  console.log(`Live fragment: status=${liveResp.status} count=${live.count ?? 'n/a'} htmlLen=${(live.html || '').length}`);

  console.log('\n=== Repo SSR vs API comparison ===');
  const repoChecks = [
    { slug: 'mattermost', label: 'mattermost/mattermost' },
    { slug: 'mattermost-mobile', label: 'mattermost/mattermost-mobile' },
  ];
  for (const { slug, label } of repoChecks) {
    const branchFilter = 'branch%3Amain-master';
    const repoGroupedResp = await fetch(
      `${API_BASE}/api/v1/reports/grouped?limit=50&repository=${slug}&branch_filter=${branchFilter}`,
    );
    const repoGrouped = await repoGroupedResp.json();
    const repoHtmlResp = await fetch(`${API_BASE}/reports/${slug}?branch_filter=${branchFilter}`);
    const repoHtml = await repoHtmlResp.text();

    const repoApiRuns = repoGrouped.groups
      .flatMap((g) => g.runs)
      .filter((r) => {
        const gh = String(r.gh_run_id || '');
        if (!isReplayGhRunId(gh)) return false;
        const ts = Number(/^gh_run_(\d+)$/.exec(gh)?.[1]);
        return Number.isFinite(ts) && ts >= batchStartedMs - 5000;
      });

    const hasStatic = repoHtml.includes('id="run-card-static"');
    const hasLiveCard = repoHtml.includes('id="run-card-live"');
    const staticRows = (repoHtml.match(/data-tsio-mode="static"/g) || []).length;
    const liveRows = (repoHtml.match(/data-tsio-mode="live"/g) || []).length;

    console.log(
      `${label}: apiReplay=${repoApiRuns.length} staticCard=${hasStatic} liveCard=${hasLiveCard} staticRows=${staticRows} liveRows=${liveRows}`,
    );

    if (!hasStatic) {
      issues.push(`repo ${slug}: missing static section`);
    }
    if (repoApiRuns.length === 0) {
      issues.push(`repo ${slug}: no batch replay runs in grouped API`);
    }
    for (const run of repoApiRuns) {
      const name = run.name || '';
      const gh = run.gh_run_id || '';
      const inHtml = repoHtml.includes(`run-name">${name}<`) && repoHtml.includes(gh);
      if (!inHtml) issues.push(`repo ${slug}: missing in SSR HTML: ${name} (${gh})`);
    }

    const repoLiveResp = await fetch(
      `${API_BASE}/reports/fragment/home-live?repository=${slug}&branch_filter=${branchFilter}`,
    );
    const repoLive = await repoLiveResp.json();
    if (repoLiveResp.status !== 200) {
      issues.push(`repo ${slug}: live fragment HTTP ${repoLiveResp.status}`);
    } else if (hasLiveCard && (repoLive.count ?? 0) !== liveRows) {
      issues.push(
        `repo ${slug}: live fragment count ${repoLive.count ?? 'n/a'} != SSR live rows ${liveRows}`,
      );
    } else if (!hasLiveCard && (repoLive.count ?? 0) !== 0) {
      issues.push(`repo ${slug}: live fragment count ${repoLive.count} but no live card on page`);
    }
    console.log(
      `  live fragment: status=${repoLiveResp.status} count=${repoLive.count ?? 'n/a'} htmlLen=${(repoLive.html || '').length}`,
    );
  }

  try {
    const viteGrouped = await fetch('http://localhost:3000/api/v1/reports/grouped?limit=50&offset=0');
    if (viteGrouped.status === 200) {
      const vg = await viteGrouped.json();
      const viteTotal = vg.total;
      if (viteTotal !== grouped.total) {
        issues.push(`Vite grouped total ${viteTotal} != SSR API total ${grouped.total}`);
      }
    }
  } catch {
    console.log('Vite (:3000) not reachable — skipping Vite comparison');
  }

  if (issues.length) {
    console.log('\nComparison issues:');
    for (const i of issues) console.log(`  - ${i}`);
    process.exitCode = 1;
  } else {
    console.log('\nComparison: PASS (home + repo pages aligned with API)');
  }

  const outPath = path.join(__dirname, '..', '..', '.local', 'ci-replay-batch-results.json');
  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  fs.writeFileSync(
    outPath,
    JSON.stringify(
      {
        commitShaMattermost: batchCommits.mattermost,
        commitShaMobile: batchCommits.mobile,
        speed: SPEED,
        results,
        apiRunCount: apiRuns.length,
        issues,
      },
      null,
      2,
    ),
  );
  console.log(`\nWrote ${outPath}`);
})().catch((err) => {
  console.error('Comparison failed:', err.message);
  process.exitCode = 1;
});
