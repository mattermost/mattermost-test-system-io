/**
 * Drain this matrix entry's slice of the orchestration queue. For each
 * leased spec, dispatch Playwright via runUnit (archives per-iteration
 * results), then call /orchestration/complete. At queue-empty, upload
 * the worker's accumulated artifacts as one shard report.
 *
 * Identity comes from `composite-identity` + the resolved gh_job_id (looked
 * up via the GitHub API from `gh-job-name`). The orchestrator finds the
 * worker's lease by (run, gh_job_id) — workers never see a lease_id.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import * as core from "@actions/core";
import * as github from "@actions/github";
import { fetchWithAuthRetry, getBearer } from "./auth";
import { runUnit as runPlaywrightUnit } from "./playwright";
import { runUnit as runCypressUnit } from "./cypress";
import { uploadShard, type UploadConfig } from "./upload";
import type {
  CheckoutResponseBody,
  CompleteResponseBody,
  CompositeIdentity,
  InvocationRecord,
  SpecResult,
} from "./types";

const PRODUCTION_URL = "https://test-io.test.mattermost.com";
const STAGING_URL = "https://staging-test-io.test.mattermost.com";

export async function run(): Promise<void> {
  const baseURL = resolveBaseURL();
  const audience = core.getInput("oidc-audience") || "mattermost-test-system-io";
  const compositeIdentityRaw = core.getInput("composite-identity", { required: true });
  const repoDir = core.getInput("repo-dir", { required: true });
  const artifactsRoot = core.getInput("artifacts-root", { required: true });
  const githubToken = core.getInput("github-token", { required: true });
  // Mark the input value for the runner's output filter so it prints as
  // `***` even if a caller passed a non-GITHUB_TOKEN PAT (which the runner
  // wouldn't auto-mask on its own).
  core.setSecret(githubToken);
  const ghJobName = core.getInput("gh-job-name", { required: true });
  const framework = (core.getInput("framework") || "playwright").trim().toLowerCase();
  if (framework !== "playwright" && framework !== "cypress") {
    throw new Error(`framework must be "playwright" or "cypress", got "${framework}"`);
  }
  const playwrightRetries = intInput("playwright-retries", 1);
  const playwrightProject = core.getInput("playwright-project") || "chrome";
  const playwrightDirInput = core.getInput("playwright-dir") || "e2e-tests/playwright";
  const resultsDirInput = core.getInput("results-dir") || "results";
  const cypressDirInput = core.getInput("cypress-dir") || "e2e-tests/cypress";

  let compositeIdentity: CompositeIdentity;
  try {
    compositeIdentity = JSON.parse(compositeIdentityRaw) as CompositeIdentity;
  } catch (e) {
    throw new Error(`composite-identity is not valid JSON: ${(e as Error).message}`);
  }
  normalizeCompositeIdentity(compositeIdentity);

  // The action input is the bare child name (e.g. `dispatch-run-1`),
  // but for nested workflow_call chains the GitHub Jobs API reports the
  // composed display name (e.g. `e2e-cypress / cypress-full-v2 /
  // dispatch-run-1`). Persist the composed name on the server so the
  // dashboard / orchestration data carry the framework + intermediate
  // chain that produced the worker. The flat-workflow case (where the
  // composed name equals the input) is unchanged.
  const resolved = await resolveJobId(githubToken, ghJobName);
  const ghJobId = resolved.id;
  const resolvedJobName = resolved.name;
  core.info(`resolved gh_job_id=${ghJobId} gh_job_name=${resolvedJobName} (input=${ghJobName})`);

  const playwrightDir = path.resolve(repoDir, playwrightDirInput);
  const resultsDir = path.resolve(playwrightDir, resultsDirInput);
  const cypressDir = path.resolve(repoDir, cypressDirInput);
  const workerArtifacts = path.join(artifactsRoot, ghJobId);
  fs.mkdirSync(workerArtifacts, { recursive: true });

  const invocations: InvocationRecord[] = [];
  let iterationSeq = 0;

  // Drain → uploadShard runs in a finally-style block so accumulated artifacts
  // upload to Test System IO even when the drain loop crashes mid-run (lease 401,
  // network drop, etc.). Otherwise everything ran so far is lost from the dashboard.
  let drainErr: Error | undefined;
  try {
    await drain({
      baseURL,
      audience,
      compositeIdentity,
      ghJobId,
      ghJobName: resolvedJobName,
      framework,
      playwrightDir,
      resultsDir,
      cypressDir,
      workerArtifacts,
      playwrightRetries,
      playwrightProject,
      invocations,
      nextIterationSeq: () => iterationSeq++,
    });
  } catch (err) {
    drainErr = err instanceof Error ? err : new Error(String(err));
    core.error(`drain loop failed: ${drainErr.message}`);
  }

  if (invocations.length > 0) {
    const uploadCfg: UploadConfig = {
      baseURL,
      audience,
      ghJobId,
      ghJobName: resolvedJobName,
      compositeIdentity,
    };
    try {
      await uploadShard(uploadCfg, invocations);
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err));
      core.error(`uploadShard failed: ${e.message}`);
      if (!drainErr) drainErr = e;
    }
  }

  if (drainErr) throw drainErr;
}

interface DrainConfig {
  baseURL: string;
  audience: string;
  compositeIdentity: CompositeIdentity;
  ghJobId: string;
  ghJobName: string;
  framework: string;
  playwrightDir: string;
  resultsDir: string;
  cypressDir: string;
  workerArtifacts: string;
  playwrightRetries: number;
  playwrightProject: string;
  invocations: InvocationRecord[];
  nextIterationSeq: () => number;
}

async function drain(cfg: DrainConfig): Promise<void> {
  let leasesHeld = 0;
  while (true) {
    const checkout = await postJSON<CheckoutResponseBody>(cfg, "/api/v1/orchestration/checkout", {
      ...(cfg.compositeIdentity as unknown as Record<string, unknown>),
      gh_job_name: cfg.ghJobName,
      gh_job_id: cfg.ghJobId,
      batch_size: 1,
    });

    // The Test System IO Error envelope uses `{error, message}` — the Go `Code`
    // field is JSON-tagged as "error". Match on `body.error`, not `body.code`.
    if (checkout.status === 409 && checkout.body?.error === "WORKER_HAS_ACTIVE_LEASE") {
      core.info("active lease still recorded; waiting");
      await sleep(2000);
      continue;
    }
    if (checkout.status === 409 && checkout.body?.error === "RUN_NOT_IN_PROGRESS") {
      core.info("run no longer in_progress; exiting");
      break;
    }
    if (checkout.status !== 200) {
      throw new Error(`checkout failed: ${checkout.status} ${JSON.stringify(checkout.body)}`);
    }

    const body = checkout.body!;
    if (body.queue_empty) {
      const retryAfterMs = Number(body.retry_after_ms);
      if (Number.isFinite(retryAfterMs) && retryAfterMs > 0) {
        // Other workers in flight or retest pool non-empty — stay alive so
        // we can pick up retest units the moment another worker reports a
        // fresh-pass result. Sleeping clients add no extra load on the
        // orchestrator (single read query) and let the retest pool fan out
        // across workers instead of serializing on the slowest.
        core.info(`queue empty; sleeping ${retryAfterMs}ms before re-polling`);
        await sleep(retryAfterMs);
        continue;
      }
      core.info(`queue empty after ${leasesHeld} unit(s); exiting cleanly`);
      break;
    }

    leasesHeld += 1;
    const isRetest = !!body.is_retest;
    const specPaths = (body.units || []).map((u) => u.spec_path);
    core.info(`leased (${isRetest ? "retest" : "fresh"}): ${specPaths.join(", ")}`);

    let results: SpecResult[];
    try {
      if (cfg.framework === "cypress") {
        const out = runCypressUnit(
          {
            cypressDir: cfg.cypressDir,
            resultsDir: cfg.resultsDir,
            workerArtifacts: cfg.workerArtifacts,
          },
          cfg.nextIterationSeq(),
          specPaths,
        );
        cfg.invocations.push(out.invocation);
        results = out.results;
        // Cypress's Mochawesome JSON doesn't carry attachment paths the way
        // Playwright's reporter does, so failure screenshots have to be
        // uploaded out-of-band and stitched onto the matching test_cases
        // before /complete sees them.
        await attachCypressScreenshots(cfg, results, out.screenshotsBySpec);
      } else {
        const out = runPlaywrightUnit(
          {
            playwrightDir: cfg.playwrightDir,
            resultsDir: cfg.resultsDir,
            workerArtifacts: cfg.workerArtifacts,
            playwrightRetries: cfg.playwrightRetries,
            playwrightProject: cfg.playwrightProject,
          },
          cfg.nextIterationSeq(),
          specPaths,
        );
        cfg.invocations.push(out.invocation);
        results = out.results;
      }
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err));
      core.error(`dispatch error: ${e.message}`);
      results = specPaths.map((spec_path) => ({
        spec_path,
        status: "failed",
        actual_duration_ms: 0,
        test_cases: [],
        error_message: `worker dispatch failure: ${e.message}`,
      }));
    }

    const completeRes = await postJSON<CompleteResponseBody>(
      cfg,
      "/api/v1/orchestration/complete",
      {
        ...(cfg.compositeIdentity as unknown as Record<string, unknown>),
        gh_job_name: cfg.ghJobName,
        gh_job_id: cfg.ghJobId,
        results,
      },
    );
    if (completeRes.status !== 200) {
      throw new Error(`complete failed: ${completeRes.status} ${JSON.stringify(completeRes.body)}`);
    }
    const transitions = (completeRes.body?.unit_states_changed || [])
      .map((c) => c.new_state)
      .join(",");
    core.info(
      `reported (${results.map((r) => r.status).join(",")}) → ${transitions || "(no transition)"}`,
    );
  }
}

/**
 * Resolve the runner's gh_job_id from the workflow's rendered job name.
 *
 * GH Actions' GITHUB_JOB env var carries the *workflow-file* job key
 * (e.g. `e2e-orchestrated`), not the matrix-rendered name. The orchestrator
 * keys leases on the unique numeric job id, so we look it up by matching
 * the gh-job-name input against the runtime job names returned by
 * `listJobsForWorkflowRunAttempt`.
 */
async function resolveJobId(
  token: string,
  ghJobName: string,
): Promise<{ id: string; name: string }> {
  const octokit = github.getOctokit(token);
  const { owner, repo } = github.context.repo;
  const runId = github.context.runId;
  const attempt = Number(process.env.GITHUB_RUN_ATTEMPT || "1");
  const runnerName = process.env.RUNNER_NAME ?? "";

  // Retry transient failures (5xx, 429, network errors). gh_job_id is
  // load-bearing for orchestration leases, so we'd rather wait a few
  // seconds than fail the whole worker on a flaky API moment.
  const jobs = await retryGitHubCall(() =>
    octokit.paginate(octokit.rest.actions.listJobsForWorkflowRunAttempt, {
      owner,
      repo,
      run_id: runId,
      attempt_number: attempt,
      per_page: 100,
    }),
  );

  // Exact match first — the flat-workflow case where the caller's
  // gh-job-name is identical to the runtime job name.
  let matches = jobs.filter((j) => j.name === ghJobName);
  if (matches.length === 0) {
    // Nested workflow_call composes the displayed job name as
    // `<parent> / <intermediate> / ... / <child>` with ` / ` (space-
    // slash-space) as the separator. Fall back to matching the trailing
    // segment so callers can pass the bare child name without having
    // to know the parent chain.
    matches = jobs.filter((j) => {
      const parts = j.name.split(" / ");
      return parts[parts.length - 1] === ghJobName;
    });
  }

  // Two parallel chains can produce identically-named children
  // (e.g. cypress matrix and playwright matrix each have `dispatch-run-1`).
  // Narrow by RUNNER_NAME — GH-hosted runners assign a unique name per
  // job, and our action runs from inside the calling job, so the
  // matching job's `runner_name` equals our `process.env.RUNNER_NAME`.
  if (matches.length > 1 && runnerName) {
    const narrowed = matches.filter((j) => j.runner_name === runnerName);
    if (narrowed.length > 0) matches = narrowed;
  }

  if (matches.length === 0) {
    const names = jobs.map((j) => j.name).join(", ");
    throw new Error(`no job matched gh-job-name=${ghJobName}; available: ${names}`);
  }
  if (matches.length > 1) {
    const names = matches.map((j) => j.name).join(", ");
    throw new Error(`gh-job-name=${ghJobName} matched multiple jobs: ${names}`);
  }
  const m = matches[0]!;
  return { id: String(m.id), name: m.name };
}

interface PostResponse<T> {
  status: number;
  body: T | null;
}

// postJSON wraps fetchWithAuthRetry with a 5xx-retry policy. The
// underlying fetchWithAuthRetry handles network-layer errors and OIDC
// 401 refresh, but passes HTTP responses through verbatim — appropriate
// for upload endpoints whose callers branch on specific status codes.
// For the orchestration JSON endpoints (/checkout, /complete) a 5xx is
// always transient: the proxy timed out waiting for the backend, the
// caller can re-send safely (orchestrator detects duplicate completion
// on a lease). 4xx still passes through so business signals like 409
// `WORKER_HAS_ACTIVE_LEASE` reach the caller intact.
async function postJSON<T>(
  cfg: { baseURL: string; audience: string },
  urlPath: string,
  body: Record<string, unknown>,
): Promise<PostResponse<T>> {
  const delays = [500, 1500, 4000];
  for (let attempt = 0; attempt <= delays.length; attempt++) {
    const res = await fetchWithAuthRetry(async () => {
      const bearer = await getBearer(cfg.audience);
      return fetch(`${cfg.baseURL}${urlPath}`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
        body: JSON.stringify(body),
      });
    });
    const text = await res.text();
    if (res.status >= 500 && res.status < 600 && attempt < delays.length) {
      const ms = delays[attempt]! + Math.floor(Math.random() * 250);
      core.warning(
        `${urlPath}: HTTP ${res.status} (attempt ${attempt + 1}/${delays.length + 1}); ` +
          `retrying in ${ms}ms. body=${text.slice(0, 200)}`,
      );
      await sleep(ms);
      continue;
    }
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
  // Unreachable: the loop above either returns or continues with `attempt < delays.length`.
  return { status: 0, body: null };
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/**
 * Upload each spec's Cypress failure screenshots to /api/v1/orchestration/
 * screenshots and attach the returned keys to the matching test_cases.
 *
 * Filename → test_case match: Cypress writes
 * `<Suite chain joined by " -- "> -- <test title> (failed)[ (attempt N)].png`,
 * so a screenshot whose basename includes `tc.title` belongs to that
 * test. When multiple tests in a spec share a title prefix, the longest
 * matching title wins (so "MM-T4417_1 ..." doesn't accidentally claim a
 * "MM-T4417_10 ..." shot).
 *
 * Best-effort: a screenshot upload error logs a warning and drops the
 * single file. The /complete payload still goes out — better to lose a
 * screenshot than to fail the orchestration step.
 */
async function attachCypressScreenshots(
  cfg: { baseURL: string; audience: string; compositeIdentity: CompositeIdentity; ghJobId: string },
  results: SpecResult[],
  screenshotsBySpec: Record<string, string[]>,
): Promise<void> {
  for (const spec of results) {
    const files = screenshotsBySpec[spec.spec_path];
    if (!files || files.length === 0) continue;

    // Sort failing-eligible test_cases by descending title length so longer
    // titles match before shorter prefixes of theirs.
    const candidates = spec.test_cases
      .filter(
        (tc) => tc.status === "failed" || tc.status === "timedOut" || tc.status === "interrupted",
      )
      .slice()
      .sort((a, b) => b.title.length - a.title.length);
    if (candidates.length === 0) continue;

    for (const absPath of files) {
      const base = path.basename(absPath);
      const tc = candidates.find((c) => c.title && base.includes(c.title));
      if (!tc) {
        core.warning(`no test_case match for screenshot ${base}; skipping`);
        continue;
      }
      const uploaded = await uploadOrchScreenshot(cfg, spec.spec_path, absPath);
      if (!uploaded) continue;
      tc.attachments ??= { screenshots: [] };
      tc.attachments.screenshots.push(uploaded);
    }
  }
}

async function uploadOrchScreenshot(
  cfg: { baseURL: string; audience: string; compositeIdentity: CompositeIdentity; ghJobId: string },
  specPath: string,
  absPath: string,
): Promise<{ key: string; relative_path: string } | null> {
  let buf: Buffer;
  try {
    buf = fs.readFileSync(absPath);
  } catch (err) {
    core.warning(`read screenshot ${absPath} failed: ${(err as Error).message}`);
    return null;
  }
  const relPath = path.basename(absPath);
  const form = new FormData();
  for (const [k, v] of Object.entries(cfg.compositeIdentity)) {
    if (v !== undefined && v !== null) form.append(k, String(v));
  }
  form.append("gh_job_id", cfg.ghJobId);
  form.append("spec_path", specPath);
  form.append("relative_path", relPath);
  // Wrap Node's Buffer in Uint8Array — same trick upload.ts uses to bridge
  // node:buffer to DOM Blob's BlobPart type.
  form.append("file", new Blob([new Uint8Array(buf)], { type: "image/png" }), relPath);

  let res: Response;
  try {
    res = await fetchWithAuthRetry(async () => {
      const bearer = await getBearer(cfg.audience);
      return fetch(`${cfg.baseURL}/api/v1/orchestration/screenshots`, {
        method: "POST",
        headers: { Authorization: `Bearer ${bearer}` },
        body: form,
      });
    });
  } catch (err) {
    core.warning(`screenshot upload error (${relPath}): ${(err as Error).message}`);
    return null;
  }
  if (res.status !== 200) {
    const text = await res.text().catch(() => "");
    core.warning(`screenshot upload ${relPath} failed: ${res.status} ${text}`);
    return null;
  }
  const body = (await res.json().catch(() => null)) as { key?: string } | null;
  if (!body?.key) {
    core.warning(`screenshot upload ${relPath} returned no key`);
    return null;
  }
  return { key: body.key, relative_path: relPath };
}

function resolveBaseURL(): string {
  const useStaging = core.getInput("use-staging").trim().toLowerCase() === "true";
  return useStaging ? STAGING_URL : PRODUCTION_URL;
}

function intInput(name: string, fallback: number): number {
  const raw = core.getInput(name);
  if (raw === "") return fallback;
  const n = Number.parseInt(raw, 10);
  if (!Number.isFinite(n) || n < 0) {
    throw new Error(`input ${name}=${raw} is not a non-negative integer`);
  }
  return n;
}

// normalizeCompositeIdentity coerces gh_pr_number to a number when it
// arrived as a string. Shell-built composite-identity payloads (jq
// `--arg pr "${PR_NUMBER}"`) emit it as a string, but the server's
// identityFields.GHPRNumber is *int and json.Decode rejects the string
// form, surfacing as a 400 BAD_REQUEST "invalid JSON body" on every
// orchestration endpoint. Normalizing once here keeps the rest of the
// action body-agnostic.
function normalizeCompositeIdentity(c: CompositeIdentity): void {
  if (typeof c.gh_pr_number === "string") {
    const n = Number.parseInt(c.gh_pr_number, 10);
    if (Number.isFinite(n)) {
      c.gh_pr_number = n;
    } else {
      delete c.gh_pr_number;
    }
  }
}

// retryGitHubCall wraps an Octokit/REST API call with bounded retries on
// transient failures (5xx, 429, network/abort errors). 4xx errors that
// indicate a permanent problem — bad token, missing permissions, no
// such job — fall through immediately so the action surfaces a clear
// failure instead of pointlessly retrying.
async function retryGitHubCall<T>(fn: () => Promise<T>): Promise<T> {
  const delays = [500, 1500, 4000]; // ms; 3 attempts after the first
  let lastErr: unknown;
  for (let attempt = 0; attempt <= delays.length; attempt++) {
    try {
      return await fn();
    } catch (err) {
      lastErr = err;
      if (!isRetryableGitHubError(err) || attempt === delays.length) break;
      const ms = delays[attempt]! + Math.floor(Math.random() * 250);
      core.warning(
        `GitHub API call failed (attempt ${attempt + 1}/${delays.length + 1}): ` +
          `${(err as Error).message}; retrying in ${ms}ms`,
      );
      await sleep(ms);
    }
  }
  throw lastErr;
}

function isRetryableGitHubError(err: unknown): boolean {
  // Octokit RequestError exposes `.status`; native fetch / network
  // errors tend to lack it.
  const e = err as { status?: number; name?: string; message?: string };
  if (e.status == null) return true; // network / DNS / abort
  if (e.status === 408 || e.status === 429) return true;
  if (e.status >= 500 && e.status < 600) return true;
  return false;
}
