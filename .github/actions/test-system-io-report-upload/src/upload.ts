/**
 * Single-shard upload pipeline: /reports/begin (idempotent) →
 * /reports/register → multipart /reports/upload/.../screenshots (chunked)
 * → .../json. Screenshots go first so a mid-batch
 * `screenshots_upload_status=completed` cannot auto-finalize the report
 * before the JSON lands (server finalize requires json completed +
 * screenshots completed-or-null).
 */

import * as fs from "node:fs";
import * as path from "node:path";
import * as core from "@actions/core";
import { fetchWithAuthRetry, getBearer } from "./auth";
import type {
  CompositeIdentity,
  ReportsBeginBody,
  ReportsBeginResponseBody,
  ReportsRegisterResponseBody,
  UploadPart,
} from "./types";

/** Parse the captured run config; null on absent or malformed input. */
function parsedEnvironmentMetadata(raw?: string): Record<string, unknown> | null {
  if (!raw || raw.trim() === "") return null;
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
    return null;
  } catch {
    return null;
  }
}

export interface UploadConfig {
  baseURL: string;
  audience: string;
  ghJobId: string;
  ghJobName: string;
  framework: string;
  totalReportsExpected: number;
  environmentMetadata?: string;
  compositeIdentity: CompositeIdentity;
}

/** Soft caps so one gateway timeout does not redo a huge multipart body. */
const SCREENSHOT_BATCH_MAX_FILES = 25;
const SCREENSHOT_BATCH_MAX_BYTES = 15 * 1024 * 1024; // 15 MiB

export async function uploadShard(
  cfg: UploadConfig,
  jsonPath: string,
  screenshotsDir: string | null,
): Promise<void> {
  if (!fs.existsSync(jsonPath)) {
    throw new Error(`json-path does not exist: ${jsonPath}`);
  }
  const jsonStat = fs.statSync(jsonPath);
  if (!jsonStat.isFile()) {
    throw new Error(`json-path is not a file: ${jsonPath}`);
  }

  const reportsIdent: ReportsBeginBody = identityForReports(
    cfg.compositeIdentity,
    cfg.framework,
    cfg.totalReportsExpected,
  );

  const beginRes = await postJSON<ReportsBeginResponseBody>(
    cfg,
    "/api/v1/reports/begin",
    reportsIdent as unknown as Record<string, unknown>,
  );
  if (beginRes.status !== 200 && beginRes.status !== 201) {
    throw new Error(`reports/begin failed: ${beginRes.status} ${JSON.stringify(beginRes.body)}`);
  }
  const reportGroupID = beginRes.body!.report_id;

  const jsonRelName = path.basename(jsonPath);
  const jsonParts: UploadPart[] = [
    { absPath: jsonPath, relPath: jsonRelName, size: jsonStat.size },
  ];
  const screenshotParts: UploadPart[] = screenshotsDir ? listImages(screenshotsDir) : [];

  // /reports/begin is the sole writer of total_reports_expected; strip it
  // from the register body so the wire shape reflects intent.
  const { total_reports_expected: _ignored, ...registerIdent } = reportsIdent as unknown as Record<
    string,
    unknown
  >;
  const regBody: Record<string, unknown> = {
    ...registerIdent,
    // W9 — malformed captured config must never fail the upload; drop it
    // with the degradation already documented (no config evidence → the
    // config-delta pre-tag simply never fires).
    ...parsedEnvironmentMetadata(cfg.environmentMetadata),
    gh_job_id: cfg.ghJobId,
    gh_job_name: cfg.ghJobName,
    json_files: jsonParts.map((p) => ({ path: p.relPath, size: p.size })),
    screenshots: screenshotParts.map((s) => ({ path: s.relPath, size: s.size })),
  };
  const regRes = await postJSON<ReportsRegisterResponseBody>(
    cfg,
    "/api/v1/reports/register",
    regBody,
  );
  if (regRes.status !== 200) {
    throw new Error(`reports/register failed: ${regRes.status} ${JSON.stringify(regRes.body)}`);
  }
  const uploadID = regRes.body!.upload_id;

  // Screenshots before JSON: each successful screenshots POST marks
  // screenshots_upload_status=completed server-side. Doing that before JSON
  // completes means tryAutoFinalize cannot flip the report until JSON lands,
  // so chunked screenshot uploads stay safe without a server API change.
  if (screenshotParts.length > 0) {
    const batches = chunkScreenshotParts(screenshotParts);
    core.info(`uploading ${screenshotParts.length} screenshot(s) in ${batches.length} batch(es)`);
    for (let i = 0; i < batches.length; i++) {
      const batch = batches[i]!;
      core.info(
        `screenshot batch ${i + 1}/${batches.length}: ${batch.length} file(s), ` +
          `${batch.reduce((n, p) => n + p.size, 0)} bytes`,
      );
      await uploadMultipart(
        cfg,
        `/api/v1/reports/upload/${reportGroupID}/${uploadID}/screenshots`,
        batch,
      );
    }
  }

  await uploadMultipart(
    cfg,
    `/api/v1/reports/upload/${reportGroupID}/${uploadID}/json`,
    jsonParts,
    "application/json",
  );

  core.info(
    `shard uploaded: 1 json + ${screenshotParts.length} screenshot(s) (group=${reportGroupID}, upload=${uploadID})`,
  );
}

/**
 * Split screenshots into batches capped by file count and total bytes.
 * A single oversized file still goes alone (server MaxArtifactBytes applies).
 */
export function chunkScreenshotParts(
  parts: UploadPart[],
  maxFiles = SCREENSHOT_BATCH_MAX_FILES,
  maxBytes = SCREENSHOT_BATCH_MAX_BYTES,
): UploadPart[][] {
  const batches: UploadPart[][] = [];
  let current: UploadPart[] = [];
  let currentBytes = 0;

  for (const part of parts) {
    const wouldExceedFiles = current.length >= maxFiles;
    const wouldExceedBytes = current.length > 0 && currentBytes + part.size > maxBytes;
    if (wouldExceedFiles || wouldExceedBytes) {
      batches.push(current);
      current = [];
      currentBytes = 0;
    }
    current.push(part);
    currentBytes += part.size;
  }
  if (current.length > 0) {
    batches.push(current);
  }
  return batches;
}

async function uploadMultipart(
  cfg: UploadConfig,
  urlPath: string,
  parts: UploadPart[],
  defaultType?: string,
): Promise<void> {
  const res = await fetchWithAuthRetry(async () => {
    // Rebuild the FormData on each retry — a Blob/FormData body can't be
    // safely replayed once consumed by the first fetch attempt.
    const form = new FormData();
    for (const p of parts) {
      const buf = fs.readFileSync(p.absPath);
      const type = p.contentType || defaultType || "application/octet-stream";
      // Wrap Node's Buffer in Uint8Array — Blob's BlobPart expects an
      // ArrayBuffer-backed view, and Node Buffer's typings declare
      // ArrayBufferLike which TS won't widen automatically.
      form.append("files", new Blob([new Uint8Array(buf)], { type }), p.relPath);
    }
    const bearer = await getBearer(cfg.audience);
    return fetch(`${cfg.baseURL}${urlPath}`, {
      method: "POST",
      headers: { Authorization: `Bearer ${bearer}` },
      body: form,
    });
  });
  if (res.status !== 200) {
    const t = await res.text().catch(() => "");
    throw new Error(`POST ${urlPath} failed: ${res.status} ${t}`);
  }
}

function listImages(root: string): UploadPart[] {
  const out: UploadPart[] = [];
  let entries: fs.Dirent[];
  try {
    entries = fs.readdirSync(root, { recursive: true, withFileTypes: true }) as fs.Dirent[];
  } catch {
    return out;
  }
  for (const ent of entries) {
    if (!ent.isFile()) continue;
    const dir = ent.parentPath || (ent as unknown as { path?: string }).path || root;
    const abs = path.join(dir, ent.name);
    const ext = path.extname(abs).toLowerCase();
    const ct =
      ext === ".png" ? "image/png" : ext === ".jpg" || ext === ".jpeg" ? "image/jpeg" : null;
    if (!ct) continue;
    let stat: fs.Stats;
    try {
      stat = fs.statSync(abs);
    } catch {
      continue;
    }
    out.push({
      absPath: abs,
      relPath: path.relative(root, abs).split(path.sep).join("/"),
      size: stat.size,
      contentType: ct,
    });
  }
  return out;
}

function identityForReports(
  c: CompositeIdentity,
  framework: string,
  totalReportsExpected: number,
): ReportsBeginBody {
  const body: ReportsBeginBody = {
    repository: c.repository,
    commit: c.commit_sha,
    gh_run_id: c.gh_run_id,
    gh_run_attempt: c.gh_run_attempt,
    framework,
    name: c.name,
    branch: c.branch,
    total_reports_expected: totalReportsExpected,
  };
  if (c.run_group) body.run_group = c.run_group;
  if (c.gh_pr_number != null) body.gh_pr_number = c.gh_pr_number;
  return body;
}

interface PostResponse<T> {
  status: number;
  body: T | null;
}

async function postJSON<T>(
  cfg: UploadConfig,
  urlPath: string,
  body: Record<string, unknown>,
): Promise<PostResponse<T>> {
  const res = await fetchWithAuthRetry(async () => {
    const bearer = await getBearer(cfg.audience);
    return fetch(`${cfg.baseURL}${urlPath}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
      body: JSON.stringify(body),
    });
  });
  const text = await res.text();
  let parsed: T | null = null;
  if (text.length) {
    try {
      parsed = JSON.parse(text) as T;
    } catch {
      // tolerate non-JSON body
    }
  }
  return { status: res.status, body: parsed };
}
