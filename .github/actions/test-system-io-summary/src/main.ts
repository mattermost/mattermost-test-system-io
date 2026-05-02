/**
 * test-system-io-summary: read /api/v1/orchestration/status and write a
 * Markdown job summary to $GITHUB_STEP_SUMMARY linking to the per-group
 * dashboard page. Per-shard report uploads already happened inside each
 * worker; the report_group auto-finalizes once total_reports_expected
 * uploads have landed (server-side count-based predicate).
 *
 * Framework-agnostic — the framework input is purely a UI label for the
 * summary header.
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
  // Mark the JWT for the runner's output filter so subsequent `core.info`,
  // error messages, or stack traces involving it print as `***`.
  core.setSecret(bearer);

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
  // Whitelist only the fields we render — guards against accidental leakage
  // if the API ever evolves to include sensitive fields (signed URLs, debug
  // tokens, etc.) since this runs in consumer CI logs which are public.
  if (status) {
    const counts = status.counts || {};
    core.info(
      `orchestration status: status=${status.status ?? "unknown"} total=${status.total_units ?? "?"} ` +
        `pass=${counts.completed_pass ?? 0} fail=${counts.completed_fail ?? 0} ` +
        `skip=${counts.completed_skipped ?? 0} pending=${counts.pending ?? 0} leased=${counts.leased ?? 0}`,
    );
  }

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
