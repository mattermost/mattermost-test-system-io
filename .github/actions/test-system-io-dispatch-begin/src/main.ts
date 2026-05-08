/**
 * test-system-io-dispatch-begin: discover Playwright spec files and call
 * POST /api/v1/orchestration/begin so the dispatch-run action can drain
 * the dispatch queue. Also calls POST /api/v1/reports/begin so the
 * report group exists when shards start uploading.
 */

import * as path from "node:path";
import * as core from "@actions/core";
import { discoverCypressSpecs, parseTagList, type CypressFilters } from "./cypress";
import { discoverPlaywrightSpecs } from "./playwright";
import { postOrUpdatePRComment } from "./pr-comment";

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
  const framework = (core.getInput("framework") || "playwright").trim().toLowerCase();
  if (framework !== "playwright" && framework !== "cypress") {
    throw new Error(`framework must be "playwright" or "cypress", got "${framework}"`);
  }
  const playwrightProject = core.getInput("playwright-project") || "chrome";
  const playwrightDirInput = core.getInput("playwright-dir") || "e2e-tests/playwright";
  const cypressDirInput = core.getInput("cypress-dir") || "e2e-tests/cypress";
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

  let specs: string[];
  if (framework === "cypress") {
    const cypressDir = path.resolve(repoDir, cypressDirInput);
    const filters: CypressFilters = {
      stage: parseTagList(core.getInput("cypress-stage")),
      includeGroup: parseTagList(core.getInput("cypress-include-group")),
      excludeGroup: parseTagList(core.getInput("cypress-exclude-group")),
      skipOn: parseTagList(core.getInput("cypress-skip-on")),
      sortFirst: parseTagList(core.getInput("cypress-sort-first")),
      sortLast: parseTagList(core.getInput("cypress-sort-last")),
    };
    specs = discoverCypressSpecs(cypressDir, filters);
    if (specs.length === 0) {
      throw new Error(
        `no Cypress specs survived the filter under ${cypressDir} ` +
          `(stage=${filters.stage.join(",") || "*"}, ` +
          `include=${filters.includeGroup.join(",") || "*"}, ` +
          `exclude=${filters.excludeGroup.join(",") || "none"}, ` +
          `skip-on=${filters.skipOn.join(",") || "none"})`,
      );
    }
  } else {
    const playwrightDir = path.resolve(repoDir, playwrightDirInput);
    // Mattermost convention: test_setup.ts runs as a `setup` project
    // dependency (executed once at job start by ci/prepare-playwright),
    // and specs/visual/** is run by a separate visual-regression
    // workflow rather than the dispatch flow. Both excluded here so
    // the consumer's playwright.config doesn't have to encode
    // dispatch-runtime concerns.
    specs = discoverPlaywrightSpecs(playwrightDir, ["test_setup.ts", "specs/visual/"]);
    if (specs.length === 0) {
      throw new Error(`no Playwright specs found under ${playwrightDir}`);
    }
  }
  const dispatchUnits: DispatchUnit[] = specs.map((p) => ({ spec_path: p }));
  core.info(`discovered ${dispatchUnits.length} ${framework} spec file(s)`);

  const bearer = await core.getIDToken(audience);
  // Mark the JWT for the runner's output filter so subsequent `core.info`,
  // error messages, or stack traces involving it print as `***`.
  core.setSecret(bearer);

  const beginBody: Record<string, unknown> = {
    ...compositeIdentity,
    framework,
    lease_timeout_ms: leaseTimeoutMs,
    idle_timeout_ms: idleTimeoutMs,
    retest_on_fail: retestOnFail,
    retest_budget: retestBudget,
    total_reports_expected: totalReportsExpected,
    dispatch_units: dispatchUnits,
  };
  // The server contract types gh_pr_number as integer, but jq's --arg in
  // shell-built composite-identity payloads emits it as a string. Coerce
  // at the action boundary so a string-of-digits survives the round-trip
  // without forcing every caller to switch to --argjson immediately.
  if (typeof beginBody.gh_pr_number === "string") {
    const n = Number.parseInt(beginBody.gh_pr_number, 10);
    if (Number.isFinite(n)) {
      beginBody.gh_pr_number = n;
    } else {
      delete beginBody.gh_pr_number;
    }
  }
  // playwright_project is a Playwright-only field; Cypress runs do not
  // carry it. Including it for cypress would be harmless on the wire
  // (server ignores unknown fields) but is misleading in the logs.
  if (framework === "playwright") {
    beginBody.playwright_project = playwrightProject;
  }

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
    body: JSON.stringify(identityForReports(compositeIdentity, framework, totalReportsExpected)),
  });
  if (reportsRes.status !== 200) {
    const t = await reportsRes.text().catch(() => "");
    throw new Error(`reports/begin failed: ${reportsRes.status} ${t}`);
  }
  const { report_id } = (await reportsRes.json()) as ReportsBeginResponse;
  core.info(`report group ready: ${report_id}`);

  // PR comment — best-effort, opt-in. Skips silently for non-PR runs.
  if (core.getInput("post-pr-comment") === "true") {
    await postBeginComment(compositeIdentity, framework, baseURL);
  }
}

async function postBeginComment(
  c: CompositeIdentity,
  framework: string,
  baseURL: string,
): Promise<void> {
  if (c.gh_pr_number == null || c.gh_pr_number === "") return;
  const prNumber = Number.parseInt(String(c.gh_pr_number), 10);
  if (!Number.isFinite(prNumber)) return;

  const token = core.getInput("github-token");
  const testType = core.getInput("test-type");
  const serverEdition = core.getInput("server-edition");
  const [owner, repo] = (c.repository || "").split("/");
  if (!owner || !repo) return;

  const shortSha = (c.commit_sha || "").slice(0, 7);
  const reportURL = buildReportURL(baseURL, c);
  const heading = formatHeading(framework, testType, serverEdition, shortSha, "started", reportURL);
  const marker = `<!-- test-system-io:${c.name}@${shortSha} -->`;
  const body = [
    heading,
    "",
    "Tests dispatched. Will be updated when the run finishes.",
    "",
    marker,
    "",
  ].join("\n");

  await postOrUpdatePRComment({ token, owner, repo, prNumber, marker, body });
}

function buildReportURL(baseURL: string, c: CompositeIdentity): string {
  const repoTrailing = (c.repository || "").split("/").pop() || c.repository;
  const repo = encodeURIComponent(repoTrailing);
  const branch = encodeURIComponent(c.branch || "main");
  const shortSha = (c.commit_sha || "").slice(0, 7);
  const name = encodeURIComponent(c.name);
  return `${baseURL}/reports/${repo}/${branch}/${shortSha}/${name}?gh_run_id=${encodeURIComponent(c.gh_run_id)}`;
}

function formatHeading(
  framework: string,
  testType: string,
  edition: string,
  shortSha: string,
  linkText: string,
  url: string,
): string {
  const fwCap = capitalize(framework);
  const ttCap = testType ? ` ${capitalize(testType)}` : "";
  const edPart = edition ? ` (${edition})` : "";
  return `**E2E — ${fwCap}${ttCap}${edPart} - \`${shortSha}\`, [${linkText}](${url})**`;
}

function capitalize(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function identityForReports(
  c: CompositeIdentity,
  framework: string,
  totalReportsExpected: number,
): Record<string, unknown> {
  const body: Record<string, unknown> = {
    repository: c.repository,
    commit: c.commit_sha,
    gh_run_id: c.gh_run_id,
    gh_run_attempt: c.gh_run_attempt,
    framework,
    name: c.name,
    branch: c.branch,
    total_reports_expected: totalReportsExpected,
  };
  if (c.gh_pr_number != null) {
    // The server contract types gh_pr_number as integer; coerce
    // string-shaped values from shell-built composite-identity payloads.
    if (typeof c.gh_pr_number === "string") {
      const n = Number.parseInt(c.gh_pr_number, 10);
      if (Number.isFinite(n)) body.gh_pr_number = n;
    } else {
      body.gh_pr_number = c.gh_pr_number;
    }
  }
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
