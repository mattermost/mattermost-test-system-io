// Multipart shard-upload client for the opt-in UPLOAD_SHARDS path — mirrors
// upload.ts/main.ts, using fetch/FormData/Blob (client.js is JSON-only).

'use strict';

const fs = require('fs');
const path = require('path');

function contentTypeFor(absPath) {
  const ext = path.extname(absPath).toLowerCase();
  if (ext === '.png') return 'image/png';
  if (ext === '.jpg' || ext === '.jpeg') return 'image/jpeg';
  if (ext === '.json') return 'application/json';
  return 'application/octet-stream';
}

// listImages recursively collects {absPath, relPath, size, contentType} for
// every .png/.jpg/.jpeg under rootDir. Used for Playwright's whole-invocation
// screenshot walk (Cypress's are pre-scoped per spec in corpus.js).
function listImages(rootDir) {
  let entries;
  try {
    entries = fs.readdirSync(rootDir, { recursive: true, withFileTypes: true });
  } catch {
    return [];
  }
  const out = [];
  for (const ent of entries) {
    if (!ent.isFile()) continue;
    const ext = path.extname(ent.name).toLowerCase();
    if (ext !== '.png' && ext !== '.jpg' && ext !== '.jpeg') continue;
    const entDir = ent.parentPath || ent.path || rootDir;
    const absPath = path.join(entDir, ent.name);
    out.push({
      absPath,
      relPath: path.relative(rootDir, absPath).split(path.sep).join('/'),
      size: fs.statSync(absPath).size,
      contentType: contentTypeFor(absPath),
    });
  }
  return out;
}

async function postJSON(apiBase, apiKey, urlPath, body) {
  const res = await fetch(new URL(urlPath, apiBase), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-API-Key': apiKey },
    body: JSON.stringify(body),
  });
  const text = await res.text();
  let parsed = null;
  if (text.length) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = text;
    }
  }
  return { status: res.status, body: parsed };
}

// registerReport finds-or-creates the report_groups row and the per-shard
// reports row, keyed on gh_job_id. Field is `commit`, not `commit_sha`.
// Omits total_reports_expected — /begin already set it.
async function registerReport(apiBase, apiKey, identity, ghJobId, ghJobName, jsonParts, screenshotParts) {
  const body = {
    repository: identity.repository,
    commit: identity.commit_sha,
    gh_run_id: identity.gh_run_id,
    gh_run_attempt: identity.gh_run_attempt,
    framework: identity.framework,
    name: identity.name,
    branch: identity.branch,
    gh_job_id: ghJobId,
    gh_job_name: ghJobName,
    json_files: jsonParts.map((p) => ({ path: p.relPath, size: p.size })),
    screenshots: screenshotParts.map((p) => ({ path: p.relPath, size: p.size })),
  };
  const resp = await postJSON(apiBase, apiKey, '/api/v1/reports/register', body);
  if (resp.status !== 200) {
    throw new Error(`reports/register failed: ${resp.status} ${JSON.stringify(resp.body)}`);
  }
  return resp.body;
}

// uploadMultipart streams `parts` to urlPath as repeated `files` fields.
// Fresh FormData per call. Do not set Content-Type — fetch sets the
// multipart boundary itself.
async function uploadMultipart(apiBase, apiKey, urlPath, parts) {
  const form = new FormData();
  for (const p of parts) {
    const buf = fs.readFileSync(p.absPath);
    form.append('files', new Blob([new Uint8Array(buf)], { type: p.contentType }), p.relPath);
  }
  const res = await fetch(new URL(urlPath, apiBase), {
    method: 'POST',
    headers: { 'X-API-Key': apiKey },
    body: form,
  });
  const text = await res.text();
  let parsed = null;
  if (text.length) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = text;
    }
  }
  if (res.status < 200 || res.status >= 300) {
    throw new Error(`POST ${urlPath} failed: ${res.status} ${JSON.stringify(parsed)}`);
  }
  return parsed;
}

// uploadShardForWorker: register the shard, upload its JSON file(s), then
// its screenshots (only if any). invocations: {specPath, iterDir,
// sourcePath}[], one per leased unit; deduped by resolved sourcePath since
// pooled Playwright samples can share one source file across specs.
// Returns null if no JSON files, else {reportId, uploadId, jsonCount, screenshotCount}.
async function uploadShardForWorker(apiBase, apiKey, identity, ghJobId, ghJobName, invocations, log) {
  const seenSource = new Map();
  for (const inv of invocations) {
    const resolved = path.resolve(inv.sourcePath);
    if (!seenSource.has(resolved) && fs.existsSync(inv.sourcePath)) {
      seenSource.set(resolved, inv);
    }
  }
  const distinctInvocations = [...seenSource.values()];
  if (distinctInvocations.length === 0) {
    log('no JSON files to upload; skipping shard');
    return null;
  }

  const jsonParts = distinctInvocations.map((inv, i) => ({
    absPath: inv.sourcePath,
    relPath:
      distinctInvocations.length > 1
        ? `${identity.framework}-results-${i}.json`
        : `${identity.framework}-results.json`,
    size: fs.statSync(inv.sourcePath).size,
    contentType: 'application/json',
  }));

  // Cypress: each invocation already knows its own leased spec's screenshots
  // (corpus.js pre-scopes them) — use those directly rather than walking the
  // whole iterDir/output, which can hold sibling specs this worker never leased.
  const screenshotParts = [];
  const withoutScreenshotFiles = [];
  for (const inv of distinctInvocations) {
    if (!inv.screenshotFiles || inv.screenshotFiles.length === 0) {
      withoutScreenshotFiles.push(inv);
      continue;
    }
    const outputRoot = path.join(inv.iterDir, 'output');
    for (const absPath of inv.screenshotFiles) {
      screenshotParts.push({
        absPath,
        relPath: path.relative(outputRoot, absPath).split(path.sep).join('/'),
        size: fs.statSync(absPath).size,
        contentType: contentTypeFor(absPath),
      });
    }
  }

  // Playwright: no per-sample scoping — walk each distinct iterDir/output once.
  const distinctIterDirs = [...new Set(withoutScreenshotFiles.map((inv) => inv.iterDir))];
  distinctIterDirs.forEach((iterDir, i) => {
    const images = listImages(path.join(iterDir, 'output'));
    for (const img of images) {
      screenshotParts.push({
        ...img,
        relPath: distinctIterDirs.length > 1 ? `iter-${i}/${img.relPath}` : img.relPath,
      });
    }
  });

  const reg = await registerReport(apiBase, apiKey, identity, ghJobId, ghJobName, jsonParts, screenshotParts);
  const reportGroupId = reg.report_id;
  const reportId = reg.upload_id;

  await uploadMultipart(apiBase, apiKey, `/api/v1/reports/upload/${reportGroupId}/${reportId}/json`, jsonParts);
  if (screenshotParts.length > 0) {
    await uploadMultipart(
      apiBase,
      apiKey,
      `/api/v1/reports/upload/${reportGroupId}/${reportId}/screenshots`,
      screenshotParts,
    );
  }

  log(
    `shard uploaded: ${jsonParts.length} json + ${screenshotParts.length} screenshot(s) ` +
      `(group=${reportGroupId}, report=${reportId})`,
  );
  return {
    reportId: reportGroupId,
    uploadId: reportId,
    jsonCount: jsonParts.length,
    screenshotCount: screenshotParts.length,
  };
}

// uploadOrchScreenshot: upload one failure screenshot for the leased spec,
// returning {key, relative_path} to attach to a test_case. Best-effort —
// never throws, returns null on failure.
async function uploadOrchScreenshot(apiBase, apiKey, identity, ghJobId, ghJobName, specPath, absPath, log) {
  let buf;
  try {
    buf = fs.readFileSync(absPath);
  } catch (err) {
    log(`read screenshot ${absPath} failed: ${err.message}`);
    return null;
  }
  const relPath = path.basename(absPath);
  const form = new FormData();
  form.append('repository', identity.repository);
  form.append('commit_sha', identity.commit_sha);
  form.append('gh_run_id', identity.gh_run_id);
  form.append('gh_run_attempt', identity.gh_run_attempt);
  form.append('name', identity.name);
  form.append('gh_job_id', ghJobId);
  form.append('gh_job_name', ghJobName);
  form.append('spec_path', specPath);
  form.append('relative_path', relPath);
  form.append('file', new Blob([new Uint8Array(buf)], { type: contentTypeFor(absPath) }), relPath);

  let res;
  try {
    res = await fetch(new URL('/api/v1/orchestration/screenshots', apiBase), {
      method: 'POST',
      headers: { 'X-API-Key': apiKey },
      body: form,
    });
  } catch (err) {
    log(`screenshot upload error (${relPath}): ${err.message}`);
    return null;
  }
  if (res.status < 200 || res.status >= 300) {
    const text = await res.text().catch(() => '');
    log(`screenshot upload ${relPath} failed: ${res.status} ${text}`);
    return null;
  }
  const parsed = await res.json().catch(() => null);
  if (!parsed || !parsed.key) {
    log(`screenshot upload ${relPath} returned no key`);
    return null;
  }
  return { key: parsed.key, relative_path: relPath };
}

module.exports = { registerReport, uploadMultipart, uploadShardForWorker, uploadOrchScreenshot, listImages };
