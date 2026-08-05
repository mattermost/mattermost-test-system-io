/**
 * Per-shard upload at queue-empty: walk every archived per-iteration
 * results dir, register the JSON + screenshot manifest with
 * /reports/register, then stream multipart uploads to /reports/upload/.
 *
 * The dispatch-begin action created the report group earlier in the run,
 * so the worker doesn't call /reports/begin — /reports/register's
 * response carries the report_group UUID we need for the upload URLs.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import * as core from "@actions/core";
import { fetchWithAuthRetry, getBearer } from "./auth";
import { imageContentType } from "./mime";
import type {
  CompositeIdentity,
  InvocationRecord,
  ReportsRegisterResponseBody,
  UploadPart,
} from "./types";

export interface UploadConfig {
  baseURL: string;
  audience: string;
  ghJobId: string;
  ghJobName: string;
  framework: string;
  compositeIdentity: CompositeIdentity;
}

export async function uploadShard(
  cfg: UploadConfig,
  invocations: InvocationRecord[],
): Promise<void> {
  if (invocations.length === 0) {
    core.info("no invocations; nothing to upload");
    return;
  }

  const jsonParts: UploadPart[] = [];
  const screenshotParts: UploadPart[] = [];
  for (let i = 0; i < invocations.length; i++) {
    const inv = invocations[i]!;
    if (fs.existsSync(inv.playwrightJsonPath)) {
      const stat = fs.statSync(inv.playwrightJsonPath);
      const rel =
        invocations.length > 1 ? `playwright-results-${i}.json` : "playwright-results.json";
      jsonParts.push({ absPath: inv.playwrightJsonPath, relPath: rel, size: stat.size });
    }
    const outputRoot = path.join(inv.iterDir, "output");
    if (fs.existsSync(outputRoot)) {
      for (const img of listImages(outputRoot)) {
        // Prefix with iter index so multi-spec workers cannot collide on relative path.
        const prefixed = invocations.length > 1 ? `iter-${i}/${img.relPath}` : img.relPath;
        screenshotParts.push({ ...img, relPath: prefixed });
      }
    }
  }

  if (jsonParts.length === 0) {
    core.info("no playwright json to upload");
    return;
  }

  const regBody: Record<string, unknown> = {
    ...identityFields(cfg.compositeIdentity, cfg.framework),
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
  const reportGroupID = regRes.body!.report_id;
  const uploadID = regRes.body!.upload_id;

  await uploadMultipart(
    cfg,
    `/api/v1/reports/upload/${reportGroupID}/${uploadID}/json`,
    jsonParts,
    "application/json",
  );
  if (screenshotParts.length > 0) {
    await uploadMultipart(
      cfg,
      `/api/v1/reports/upload/${reportGroupID}/${uploadID}/screenshots`,
      screenshotParts,
    );
  }

  core.info(
    `shard uploaded: ${jsonParts.length} json + ${screenshotParts.length} screenshot(s) (group=${reportGroupID}, upload=${uploadID})`,
  );
}

async function uploadMultipart(
  cfg: UploadConfig,
  urlPath: string,
  parts: UploadPart[],
  defaultType?: string,
): Promise<void> {
  const res = await fetchWithAuthRetry(async () => {
    // Rebuild the FormData on each retry — a Blob/FormData body can't
    // be safely replayed once consumed by the first fetch attempt.
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
    const ct = imageContentType(abs);
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

function identityFields(c: CompositeIdentity, framework: string): Record<string, unknown> {
  const body: Record<string, unknown> = {
    repository: c.repository,
    commit: c.commit_sha,
    gh_run_id: c.gh_run_id,
    gh_run_attempt: c.gh_run_attempt,
    framework,
    name: c.name,
    branch: c.branch,
  };
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
