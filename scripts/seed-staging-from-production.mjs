#!/usr/bin/env node
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * Seed a TSIO deployment (normally staging) with recent production history
 * through the public read API and the ordinary upload API, so history-based
 * features can be demonstrated on a database that is recreated on every
 * deploy. Nothing is read from a database and no AWS access is needed.
 *
 * Groups are replayed oldest first, so relative ordering (latest trunk run,
 * counts inside a window) survives even though every replayed group is
 * stamped with the upload time. Only groups whose reports are all downloadable
 * through GET /reports/{id}/json are replayed; that endpoint serves the first
 * JSON file of the first report, so multi-report groups are skipped.
 *
 * Auth to the target: a GitHub Actions OIDC token when ACTIONS_ID_TOKEN_REQUEST_URL
 * is set (the target's bootstrap policy grants uploader to mattermost/*), or
 * TSIO_API_KEY.
 *
 *   node scripts/seed-staging-from-production.mjs \
 *     --repository mattermost/mattermost-mobile --days 14 \
 *     [--source https://test-io.test.mattermost.com] [--target https://staging-test-io.test.mattermost.com] \
 *     [--max-groups N] [--name-prefix mobile-] [--dry-run]
 */
const args = process.argv.slice(2);
const opt = (name, dflt) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : dflt;
};
const SOURCE = (opt("source", "https://test-io.test.mattermost.com")).replace(/\/$/, "");
const TARGET = (opt("target", "https://staging-test-io.test.mattermost.com")).replace(/\/$/, "");
const REPOSITORY = opt("repository", "");
const DAYS = Number(opt("days", "14"));
const MAX = Number(opt("max-groups", "0"));
const PREFIX = opt("name-prefix", "");
const DRY = args.includes("--dry-run");
if (!REPOSITORY) throw new Error("--repository is required");
if (SOURCE === TARGET) throw new Error("source and target must differ");
const AUDIENCE = process.env.TSIO_OIDC_AUDIENCE || "mattermost-test-system-io";
const log = (m) => process.stderr.write(m + "\n");

let auth = { header: "", at: 0 };
async function authHeader() {
  if (process.env.TSIO_API_KEY) return `ApiKey ${process.env.TSIO_API_KEY}`;
  const url = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
  const tok = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  if (!url || !tok) throw new Error("no TSIO_API_KEY and no Actions OIDC environment");
  if (Date.now() - auth.at < 4 * 60000) return auth.header;
  const res = await fetch(`${url}&audience=${encodeURIComponent(AUDIENCE)}`, { headers: { authorization: `bearer ${tok}` } });
  if (!res.ok) throw new Error(`OIDC token request ${res.status}`);
  auth = { header: `Bearer ${(await res.json()).value}`, at: Date.now() };
  return auth.header;
}
async function getJSON(url) {
  const res = await fetch(url, { signal: AbortSignal.timeout(60000) });
  if (!res.ok) throw new Error(`GET ${url} -> ${res.status}`);
  return res.json();
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
/** POST to the target; 429/5xx and network errors are retried with backoff. */
async function post(path, body) {
  let last;
  for (let attempt = 0; attempt < 4; attempt++) {
    if (attempt) await sleep(2000 * 2 ** (attempt - 1));
    try {
      const res = await fetch(`${TARGET}/api/v1${path}`, {
        method: "POST",
        headers: { "content-type": "application/json", authorization: await authHeader() },
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(60000),
      });
      const text = await res.text();
      if (res.ok) return JSON.parse(text);
      last = new Error(`POST ${path} -> ${res.status} ${text.slice(0, 200)}`);
      if (res.status !== 429 && res.status < 500) throw last;
    } catch (e) {
      last = e;
      if (String(e).includes("-> 4")) throw e;
    }
  }
  throw last;
}
/** True when the target already holds a completed group for this run (re-runs fill gaps only). */
async function alreadySeeded(g) {
  const q = new URLSearchParams({ repository: g.repository, commit: g.commit, name: g.name, limit: "20" });
  const page = await getJSON(`${TARGET}/api/v1/reports?${q}`);
  return (page.reports ?? []).some((t) => String(t.gh_run_id) === String(g.gh_run_id) && String(t.gh_run_attempt || "1") === String(g.gh_run_attempt || "1") && t.status === "completed");
}

// 1. Production groups in the window, oldest first.
const since = Date.now() - DAYS * 86400000;
const groups = [];
for (let offset = 0; ; offset += 200) {
  const page = await getJSON(`${SOURCE}/api/v1/reports?limit=200&offset=${offset}`);
  for (const g of page.reports) {
    if (Date.parse(g.created_at) < since) continue;
    if (g.repository === REPOSITORY && g.status === "completed" && (!PREFIX || g.name.startsWith(PREFIX))) groups.push(g);
  }
  const last = page.reports[page.reports.length - 1];
  if (!last || Date.parse(last.created_at) < since || offset + 200 >= page.total) break;
}
groups.sort((a, b) => a.created_at.localeCompare(b.created_at));
const selected = MAX ? groups.slice(-MAX) : groups;
log(`${selected.length} completed ${REPOSITORY} groups in the last ${DAYS} days (${groups[0]?.created_at ?? "-"} .. ${groups[groups.length - 1]?.created_at ?? "-"})`);

// 2. Replay each group: begin, then register + upload every report.
const stats = { replayed: 0, skipped: 0, failed: 0, tests: 0 };
for (const [i, g] of selected.entries()) {
  const tag = `[${i + 1}/${selected.length}] ${g.name} ${g.commit.slice(0, 7)}${g.gh_pr_number ? ` PR ${g.gh_pr_number}` : ""}`;
  try {
    if (!DRY && (await alreadySeeded(g))) {
      stats.skipped++;
      continue;
    }
    const detail = await getJSON(`${SOURCE}/api/v1/reports/${g.id}`);
    const reports = detail.reports ?? [];
    if (reports.length !== 1) {
      stats.skipped++;
      log(`${tag}: skipped (${reports.length} reports; only single-report groups can be fetched through /json)`);
      continue;
    }
    const res = await fetch(`${SOURCE}/api/v1/reports/${g.id}/json`, { redirect: "follow", signal: AbortSignal.timeout(120000) });
    if (!res.ok) throw new Error(`json download ${res.status}`);
    const body = Buffer.from(await res.arrayBuffer());
    if (DRY) {
      stats.replayed++;
      log(`${tag}: would upload ${body.length} bytes`);
      continue;
    }
    const identity = {
      repository: g.repository,
      commit: g.commit,
      gh_run_id: String(g.gh_run_id),
      gh_run_attempt: String(g.gh_run_attempt || "1"),
      framework: g.framework,
      name: g.name,
      ...(g.run_group ? { run_group: g.run_group } : {}),
      branch: g.branch,
      ...(g.gh_pr_number ? { gh_pr_number: g.gh_pr_number } : {}),
    };
    await post("/reports/begin", { ...identity, total_reports_expected: 1, environment_metadata: { seeded_from: `${SOURCE}/api/v1/reports/${g.id}`, source_created_at: g.created_at } });
    const r = reports[0];
    const reg = await post("/reports/register", { ...identity, gh_job_id: String(r.gh_job_id || r.id), gh_job_name: r.gh_job_name || r.display_name || g.name, json_files: [{ path: "results.json", size: body.length }], screenshots: [] });
    const form = new FormData();
    form.append("files", new Blob([body], { type: "application/json" }), "results.json");
    const up = await fetch(`${TARGET}/api/v1/reports/upload/${reg.report_id}/${reg.upload_id}/json`, { method: "POST", headers: { authorization: await authHeader() }, body: form, signal: AbortSignal.timeout(300000) });
    if (!up.ok) throw new Error(`upload ${up.status} ${(await up.text()).slice(0, 200)}`);
    // Give the target's extraction a moment so ordering by created_at stays strict.
    await sleep(150);
    stats.replayed++;
    stats.tests += g.test_stats?.total ?? 0;
    log(`${tag}: uploaded ${body.length} bytes (${g.test_stats?.failed ?? "?"} failed of ${g.test_stats?.total ?? "?"})`);
  } catch (e) {
    stats.failed++;
    log(`${tag}: FAILED ${String(e).slice(0, 200)}`);
  }
}
log(JSON.stringify(stats));
if (stats.failed) process.exit(1);
