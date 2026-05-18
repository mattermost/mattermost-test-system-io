/**
 * test-system-io-report-upload: upload one shard's results to Test
 * System IO without using the orchestration queue. Posts /reports/begin
 * (idempotent on composite identity), /reports/register, and the
 * multipart /reports/upload/.../json (and .../screenshots when a
 * screenshots-dir is configured).
 *
 * For workflows that produce results some other way (their own
 * --shard partitioning, an unrelated test runner, etc.) and just want
 * artifacts to land on the dashboard.
 */

import * as core from "@actions/core";
import * as github from "@actions/github";
import { uploadShard, type UploadConfig } from "./upload";
import type { CompositeIdentity } from "./types";

const PRODUCTION_URL = "https://test-io.test.mattermost.com";
const STAGING_URL = "https://staging-test-io.test.mattermost.com";

export async function run(): Promise<void> {
  const baseURL = resolveBaseURL();
  const audience = core.getInput("oidc-audience") || "mattermost-test-system-io";
  const compositeIdentityRaw = core.getInput("composite-identity", { required: true });
  const framework = core.getInput("framework", { required: true });
  const githubToken = core.getInput("github-token", { required: true });
  // Mark the input value for the runner's output filter so it prints as
  // `***` even if a caller passed a non-GITHUB_TOKEN PAT (which the runner
  // wouldn't auto-mask on its own).
  core.setSecret(githubToken);
  const ghJobName = core.getInput("gh-job-name", { required: true });
  const jsonPath = core.getInput("json-path", { required: true });
  const screenshotsDirRaw = core.getInput("screenshots-dir");
  const screenshotsDir = screenshotsDirRaw.trim() === "" ? null : screenshotsDirRaw;
  const totalReportsExpected = intInput("total-reports-expected", 0);
  if (totalReportsExpected <= 0) {
    throw new Error("total-reports-expected is required and must be > 0");
  }

  let compositeIdentity: CompositeIdentity;
  try {
    compositeIdentity = JSON.parse(compositeIdentityRaw) as CompositeIdentity;
  } catch (e) {
    throw new Error(`composite-identity is not valid JSON: ${(e as Error).message}`);
  }
  normalizeCompositeIdentity(compositeIdentity);

  const ghJobId = await resolveJobId(githubToken, ghJobName);
  core.info(`resolved gh_job_id=${ghJobId} for gh_job_name=${ghJobName}`);

  const cfg: UploadConfig = {
    baseURL,
    audience,
    ghJobId,
    ghJobName,
    framework,
    totalReportsExpected,
    compositeIdentity,
  };
  await uploadShard(cfg, jsonPath, screenshotsDir);
}

/**
 * Resolve the runner's gh_job_id from the workflow's rendered job name.
 *
 * GH Actions' GITHUB_JOB env var carries the workflow-file job key, not
 * the matrix-rendered name. The orchestrator and reports backend key
 * leases / uploads on the unique numeric job id, so we look it up by
 * matching the gh-job-name input against the runtime job names returned
 * by `listJobsForWorkflowRunAttempt`.
 */
async function resolveJobId(token: string, ghJobName: string): Promise<string> {
  const octokit = github.getOctokit(token);
  const { owner, repo } = github.context.repo;
  const runId = github.context.runId;
  const attempt = Number(process.env.GITHUB_RUN_ATTEMPT || "1");
  const runnerName = process.env.RUNNER_NAME ?? "";

  // Retry transient failures (5xx, 429, network errors); gh_job_id is
  // load-bearing for the upload payload, so we'd rather wait a few
  // seconds than fail on a flaky API moment.
  const jobs = await retryGitHubCall(() =>
    octokit.paginate(octokit.rest.actions.listJobsForWorkflowRunAttempt, {
      owner,
      repo,
      run_id: runId,
      attempt_number: attempt,
      per_page: 100,
    }),
  );

  // Exact match first — flat workflow case where the caller's
  // gh-job-name equals the runtime job name.
  let matches = jobs.filter((j) => j.name === ghJobName);
  if (matches.length === 0) {
    // Nested workflow_call composes the displayed name as
    // `<parent> / <intermediate> / ... / <child>`; fall back to the
    // trailing segment so callers can pass the bare child name.
    matches = jobs.filter((j) => {
      const parts = j.name.split(" / ");
      return parts[parts.length - 1] === ghJobName;
    });
  }
  // Disambiguate identically-named children across parallel chains by
  // RUNNER_NAME (unique per GH-hosted job, equal to the calling job's).
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
  return String(matches[0]!.id);
}

function resolveBaseURL(): string {
  const useStaging = core.getInput("use-staging").trim().toLowerCase() === "true";
  return useStaging ? STAGING_URL : PRODUCTION_URL;
}

function intInput(name: string, fallback: number): number {
  const raw = core.getInput(name);
  if (raw === "") return fallback;
  // Strict-digit check mirrors normalizeCompositeIdentity below.
  // Number.parseInt's prefix behavior coerces "12abc" to 12, which would
  // accept malformed inputs (e.g. a copy-paste with a trailing token)
  // and bind the upload to an unrelated shard count.
  const trimmed = raw.trim();
  if (!/^\d+$/.test(trimmed)) {
    throw new Error(`input ${name}=${raw} is not a non-negative integer`);
  }
  return Number(trimmed);
}

// normalizeCompositeIdentity coerces gh_pr_number to a number when it
// arrived as a string. Shell-built composite-identity payloads (jq
// `--arg pr "${PR_NUMBER}"`) emit it as a string, but the server's
// identityFields.GHPRNumber is *int and json.Decode rejects the string
// form. Normalizing once here keeps every downstream payload accepted.
function normalizeCompositeIdentity(c: CompositeIdentity): void {
  if (typeof c.gh_pr_number === "string") {
    // Strict-digit check — Number.parseInt's prefix behavior would coerce
    // "123abc" to 123 and bind the run to the wrong PR. Whitespace-only
    // payloads from shell interpolation also need to drop cleanly.
    const raw = c.gh_pr_number.trim();
    if (/^\d+$/.test(raw)) {
      c.gh_pr_number = Number(raw);
    } else {
      delete c.gh_pr_number;
    }
  }
}

// retryGitHubCall wraps an Octokit/REST API call with bounded retries on
// transient failures (5xx, 429, network/abort errors). Permanent 4xx
// errors fall through immediately so the action surfaces a clear failure
// instead of pointlessly retrying.
async function retryGitHubCall<T>(fn: () => Promise<T>): Promise<T> {
  const delays = [500, 1500, 4000];
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
      await new Promise<void>((r) => setTimeout(r, ms));
    }
  }
  throw lastErr;
}

function isRetryableGitHubError(err: unknown): boolean {
  const e = err as { status?: number; name?: string; message?: string };
  if (e.status == null) return true;
  if (e.status === 408 || e.status === 429) return true;
  if (e.status >= 500 && e.status < 600) return true;
  return false;
}
