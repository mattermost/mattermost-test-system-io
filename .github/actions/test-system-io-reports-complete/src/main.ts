/**
 * test-system-io-reports-complete: post /api/v1/reports/complete
 * (idempotent on composite identity), fetch /api/v1/orchestration/status,
 * and write a job summary linking to the per-group dashboard page.
 * Per-shard report uploads already happened inside each worker.
 *
 * Framework-agnostic — the caller passes the framework label so the same
 * action can finalize Playwright, Cypress, or any other suite that
 * registered with /reports/begin under the same name.
 *
 * Exits non-zero when any unit ended in completed_fail or the run did not
 * reach `completed` (unless fail-on-test-failures=false).
 */

import * as fs from "node:fs";
import * as core from "@actions/core";

interface CompositeIdentity {
  repository: string;
  commit_sha: string;
  gh_run_id: string;
  gh_run_attempt: string;
  name: string;
  branch?: string;
  gh_pr_number?: number | string;
}

interface OrchestrationStatus {
  status?: string;
  total_units?: number;
  counts?: {
    completed_pass?: number;
    completed_fail?: number;
    completed_skipped?: number;
    pending?: number;
    leased?: number;
  };
}

const PRODUCTION_URL = "https://test-io.test.mattermost.com";
const STAGING_URL = "https://staging-test-io.test.mattermost.com";

export async function run(): Promise<void> {
  const baseURL = resolveBaseURL();
  const audience = core.getInput("oidc-audience") || "mattermost-test-system-io";
  const compositeIdentityRaw = core.getInput("composite-identity", { required: true });
  const framework = core.getInput("framework", { required: true });
  const failOnTestFailures = core.getInput("fail-on-test-failures") !== "false";

  let compositeIdentity: CompositeIdentity;
  try {
    compositeIdentity = JSON.parse(compositeIdentityRaw) as CompositeIdentity;
  } catch (e) {
    throw new Error(`composite-identity is not valid JSON: ${(e as Error).message}`);
  }

  const bearer = await core.getIDToken(audience);

  const completeRes = await fetch(`${baseURL}/api/v1/reports/complete`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
    body: JSON.stringify(identityForReports(compositeIdentity, framework)),
  });
  if (completeRes.status !== 200) {
    // Loud failure — the server-side per-report auto-finalize is the safety
    // net for stuck `processing` rows, but a missed /reports/complete still
    // leaves the report_group at `in_progress` until every shard's upload
    // pipeline lands. Surface the error in CI so the next investigation
    // doesn't have to dig through staging API to discover that this step
    // silently dropped the ball.
    const t = await completeRes.text().catch(() => "");
    throw new Error(`reports/complete returned ${completeRes.status}: ${t}`);
  }
  core.info("reports/complete OK");

  const params = new URLSearchParams({
    repository: compositeIdentity.repository,
    commit_sha: compositeIdentity.commit_sha,
    gh_run_id: compositeIdentity.gh_run_id,
    name: compositeIdentity.name,
    gh_run_attempt: compositeIdentity.gh_run_attempt,
  });
  const statusRes = await fetch(`${baseURL}/api/v1/orchestration/status?${params.toString()}`, {
    headers: { Authorization: `Bearer ${bearer}` },
  });
  let status: OrchestrationStatus | null = null;
  try {
    status = (await statusRes.json()) as OrchestrationStatus;
  } catch {
    status = null;
  }
  if (status) core.info(JSON.stringify(status, null, 2));

  // Dashboard URLs use only the trailing segment of the repository slug
  // ("owner/repo" → "repo") to match the convention surfaced by the
  // /reports/consolidated and /reports/grouped endpoints. Mirroring the same
  // path shape used elsewhere in the UI keeps deep links consistent and
  // browsable.
  const repoSlug = compositeIdentity.repository || "";
  const repoTrailing = repoSlug.split("/").pop() || repoSlug;
  const repo = encodeURIComponent(repoTrailing);
  const branch = encodeURIComponent(compositeIdentity.branch || "main");
  const shortSha = (compositeIdentity.commit_sha || "").slice(0, 7);
  const name = encodeURIComponent(compositeIdentity.name);
  const reportURL = `${baseURL}/reports/${repo}/${branch}/${shortSha}/${name}?gh_run_id=${encodeURIComponent(compositeIdentity.gh_run_id)}`;

  const summaryPath = process.env.GITHUB_STEP_SUMMARY;
  if (summaryPath) {
    const counts = status?.counts || {};
    const total = status?.total_units ?? "?";
    const lines = [
      `## E2E Test Results — ${framework} (Test System IO orchestrated)`,
      "",
      `**Run status:** \`${status?.status ?? "unknown"}\``,
      "",
      "| metric | value |",
      "|---|---|",
      `| total units | ${total} |`,
      `| pass | ${counts.completed_pass ?? 0} |`,
      `| fail | ${counts.completed_fail ?? 0} |`,
      `| skipped | ${counts.completed_skipped ?? 0} |`,
      `| pending | ${counts.pending ?? 0} |`,
      `| leased | ${counts.leased ?? 0} |`,
      "",
      `[Open Report Group](${reportURL})`,
      "",
    ];
    fs.appendFileSync(summaryPath, lines.join("\n"));
  }

  if (status?.status !== "completed") {
    const msg = `run did not complete cleanly: ${status?.status}`;
    if (failOnTestFailures) throw new Error(msg);
    core.warning(msg);
  }
  const failed = status?.counts?.completed_fail ?? 0;
  if (failed > 0) {
    const msg = `${failed} unit(s) failed`;
    if (failOnTestFailures) throw new Error(msg);
    core.warning(msg);
  }
}

function resolveBaseURL(): string {
  const useStaging = core.getInput("use-staging").trim().toLowerCase() === "true";
  return useStaging ? STAGING_URL : PRODUCTION_URL;
}

function identityForReports(c: CompositeIdentity, framework: string): Record<string, unknown> {
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
