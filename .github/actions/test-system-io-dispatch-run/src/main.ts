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
import { runUnit } from "./playwright";
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
  const playwrightRetries = intInput("playwright-retries", 1);
  const playwrightDirInput = core.getInput("playwright-dir") || "e2e-tests/playwright";
  const resultsDirInput = core.getInput("results-dir") || "results";

  let compositeIdentity: CompositeIdentity;
  try {
    compositeIdentity = JSON.parse(compositeIdentityRaw) as CompositeIdentity;
  } catch (e) {
    throw new Error(`composite-identity is not valid JSON: ${(e as Error).message}`);
  }

  const ghJobId = await resolveJobId(githubToken, ghJobName);
  core.info(`resolved gh_job_id=${ghJobId} for gh_job_name=${ghJobName}`);

  const playwrightDir = path.resolve(repoDir, playwrightDirInput);
  const resultsDir = path.resolve(playwrightDir, resultsDirInput);
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
      ghJobName,
      playwrightDir,
      resultsDir,
      workerArtifacts,
      playwrightRetries,
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
      ghJobName,
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
  playwrightDir: string;
  resultsDir: string;
  workerArtifacts: string;
  playwrightRetries: number;
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
      const out = runUnit(
        {
          playwrightDir: cfg.playwrightDir,
          resultsDir: cfg.resultsDir,
          workerArtifacts: cfg.workerArtifacts,
          playwrightRetries: cfg.playwrightRetries,
        },
        cfg.nextIterationSeq(),
        specPaths,
      );
      cfg.invocations.push(out.invocation);
      results = out.results;
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
async function resolveJobId(token: string, ghJobName: string): Promise<string> {
  const octokit = github.getOctokit(token);
  const { owner, repo } = github.context.repo;
  const runId = github.context.runId;
  const attempt = Number(process.env.GITHUB_RUN_ATTEMPT || "1");

  const jobs = await octokit.paginate(octokit.rest.actions.listJobsForWorkflowRunAttempt, {
    owner,
    repo,
    run_id: runId,
    attempt_number: attempt,
    per_page: 100,
  });
  const match = jobs.find((j) => j.name === ghJobName);
  if (!match) {
    const names = jobs.map((j) => j.name).join(", ");
    throw new Error(`no job matched gh-job-name=${ghJobName}; available: ${names}`);
  }
  return String(match.id);
}

interface PostResponse<T> {
  status: number;
  body: T | null;
}

async function postJSON<T>(
  cfg: { baseURL: string; audience: string },
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

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
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
