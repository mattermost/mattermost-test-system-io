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
