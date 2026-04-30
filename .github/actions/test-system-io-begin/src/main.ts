/**
 * test-system-io-begin: discover Mattermost Playwright spec files and
 * call POST /api/v1/orchestration/begin so the worker action can drain
 * the dispatch queue. Also calls POST /api/v1/reports/begin so the
 * report group exists when shards start uploading.
 */

import * as fs from "node:fs";
import * as path from "node:path";
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

interface ReportsBeginResponse {
  report_id: string;
}

interface DispatchUnit {
  spec_path: string;
}

const PRODUCTION_URL = "https://test-io.test.mattermost.com";
const STAGING_URL = "https://staging-test-io.test.mattermost.com";

export async function run(): Promise<void> {
  const baseURL = resolveBaseURL();
  const audience = core.getInput("oidc-audience") || "mattermost-test-system-io";
  const compositeIdentityRaw = core.getInput("composite-identity", { required: true });
  const repoDir = core.getInput("repo-dir", { required: true });
  const retestOnFail = core.getInput("retest-on-fail") === "true";
  const retestBudget = intInput("retest-budget", 1);
  const idleTimeoutMs = intInput("idle-timeout-ms", 600_000);
  const leaseTimeoutMs = intInput("lease-timeout-ms", 600_000);
  const playwrightProject = core.getInput("playwright-project") || "chrome";
  const playwrightDirInput = core.getInput("playwright-dir") || "e2e-tests/playwright";

  let compositeIdentity: CompositeIdentity;
  try {
    compositeIdentity = JSON.parse(compositeIdentityRaw) as CompositeIdentity;
  } catch (e) {
    throw new Error(`composite-identity is not valid JSON: ${(e as Error).message}`);
  }

  const playwrightDir = path.resolve(repoDir, playwrightDirInput);
  const specs = discoverSpecs(playwrightDir);
  if (specs.length === 0) {
    throw new Error(`no specs found under ${playwrightDir}`);
  }
  const dispatchUnits: DispatchUnit[] = specs.map((p) => ({ spec_path: p }));
  core.info(`discovered ${dispatchUnits.length} spec file(s)`);

  const bearer = await core.getIDToken(audience);

  const beginBody = {
    ...compositeIdentity,
    framework: "playwright",
    playwright_project: playwrightProject,
    lease_timeout_ms: leaseTimeoutMs,
    idle_timeout_ms: idleTimeoutMs,
    retest_on_fail: retestOnFail,
    retest_budget: retestBudget,
    dispatch_units: dispatchUnits,
  };

  const beginRes = await fetch(`${baseURL}/api/v1/orchestration/begin`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
    body: JSON.stringify(beginBody),
  });
  if (beginRes.status !== 200 && beginRes.status !== 201) {
    const t = await beginRes.text().catch(() => "");
    throw new Error(`orchestration/begin failed: ${beginRes.status} ${t}`);
  }
  let runId = "";
  try {
    const body = (await beginRes.json()) as { run_id?: string };
    runId = body.run_id ?? "";
  } catch {
    // tolerate non-JSON body — still treat the call as successful
  }
  core.info(`orchestration begun (${beginRes.status})`);
  core.setOutput("run-id", runId);
  core.setOutput("total-units", String(dispatchUnits.length));

  const reportsRes = await fetch(`${baseURL}/api/v1/reports/begin`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
    body: JSON.stringify(identityForReports(compositeIdentity)),
  });
  if (reportsRes.status !== 200) {
    const t = await reportsRes.text().catch(() => "");
    throw new Error(`reports/begin failed: ${reportsRes.status} ${t}`);
  }
  const { report_id } = (await reportsRes.json()) as ReportsBeginResponse;
  core.info(`report group ready: ${report_id}`);
}

/**
 * Walk `<playwrightDir>/specs/` for `*.spec.ts`. Excludes:
 *   - `specs/visual/**`  — covered by the worker's `--grep-invert @visual`
 *   - `test_setup.ts`    — runs as a Playwright project dependency, not as a dispatched unit
 *
 * Skips `playwright test --list` so the controller doesn't have to install
 * the whole webapp workspace to compile playwright-lib.
 */
export function discoverSpecs(playwrightDir: string): string[] {
  const specsDir = path.join(playwrightDir, "specs");
  const out: string[] = [];
  function rec(dir: string): void {
    let entries: fs.Dirent[];
    try {
      entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const ent of entries) {
      const full = path.join(dir, ent.name);
      if (ent.isDirectory()) rec(full);
      else if (ent.isFile() && ent.name.endsWith(".spec.ts")) out.push(full);
    }
  }
  rec(specsDir);
  return out
    .map((abs) => path.relative(playwrightDir, abs).split(path.sep).join("/"))
    .filter((p) => !p.endsWith("test_setup.ts"))
    .filter((p) => !p.startsWith("specs/visual/"))
    .sort();
}

function identityForReports(c: CompositeIdentity): Record<string, unknown> {
  const body: Record<string, unknown> = {
    repository: c.repository,
    commit: c.commit_sha,
    gh_run_id: c.gh_run_id,
    gh_run_attempt: c.gh_run_attempt,
    framework: "playwright",
    name: c.name,
    branch: c.branch,
  };
  if (c.gh_pr_number != null) body.gh_pr_number = c.gh_pr_number;
  return body;
}

function resolveBaseURL(): string {
  const useStaging = core.getInput("use-staging").trim().toLowerCase() === "true";
  return useStaging ? STAGING_URL : PRODUCTION_URL;
}

function intInput(name: string, fallback: number): number {
  const raw = core.getInput(name);
  if (raw === "") return fallback;
  const n = Number.parseInt(raw, 10);
  if (!Number.isFinite(n)) {
    throw new Error(`input ${name}=${raw} is not an integer`);
  }
  return n;
}
