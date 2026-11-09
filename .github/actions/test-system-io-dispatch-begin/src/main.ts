/**
 * test-system-io-dispatch-begin: discover Playwright spec files and call
 * POST /api/v1/orchestration/begin so the dispatch-run action can drain
 * the dispatch queue. Also calls POST /api/v1/reports/begin so the
 * report group exists when shards start uploading.
 *
 * Optionally pushes a `pending` GitHub commit status whose `target_url`
 * deep-links into the Test System IO report page, so reviewers land on
 * the live dashboard from the commit-status row instead of needing a
 * separate PR comment.
 */

import * as path from "node:path";
import * as core from "@actions/core";
import { setCommitStatus } from "./commit-status";
import { discoverCypressSpecs, parseTagList, type CypressFilters } from "./cypress";
import { discoverDetoxSpecs } from "./detox";
import { durationOf, fetchDurationHistory, orderLongestFirst } from "./durations";
import { discoverMaestroSpecs } from "./maestro";
import { discoverPlaywrightSpecs } from "./playwright";
import { buildReportURL } from "./report_url";
import { retryFetch, safeText } from "./retry-fetch";

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

// Bounds the duration-hint read so a slow API cannot hold up the whole run.
const DURATION_LOOKUP_TIMEOUT_MS = 10_000;
const PRODUCTION_URL = "https://test-io.test.mattermost.com";
const STAGING_URL = "https://staging-test-io.test.mattermost.com";

/**
 * Entry point: reads the inputs, discovers the lane's specs, orders them (longest first when
 * duration hints load), and begins the orchestration run and the report group in test-system-io.
 */
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
  if (
    framework !== "playwright" &&
    framework !== "cypress" &&
    framework !== "detox" &&
    framework !== "maestro"
  ) {
    throw new Error(
      `framework must be "playwright", "cypress", "detox", or "maestro", got "${framework}"`,
    );
  }
  const playwrightProject = core.getInput("playwright-project") || "chrome";
  const playwrightDirInput = core.getInput("playwright-dir") || "e2e-tests/playwright";
  const cypressDirInput = core.getInput("cypress-dir") || "e2e-tests/cypress";
  const detoxDirInput = core.getInput("detox-dir") || "detox";
  const detoxSearchPath = core.getInput("detox-search-path") || "e2e/test";
  const detoxIncludeTags = parseTagList(core.getInput("detox-include-tags"));
  const detoxExcludeTags = parseTagList(core.getInput("detox-exclude-tags"));
  const maestroDirInput = core.getInput("maestro-dir") || "detox/maestro";
  const maestroFlowPath = core.getInput("maestro-flow-path") || "flows";
  const maestroExcludeTags = parseTagList(core.getInput("maestro-exclude-tags"));
  const totalReportsExpected = intInput("total-reports-expected", 0);
  if (totalReportsExpected <= 0) {
    throw new Error("total-reports-expected is required and must be > 0");
  }
  // Base branch whose last complete run of this lane supplies per-spec
  // durations for longest-first dispatch. Empty disables it.
  const durationBaseBranch = core.getInput("duration-base-branch").trim();

  let compositeIdentity: CompositeIdentity;
  try {
    compositeIdentity = JSON.parse(compositeIdentityRaw) as CompositeIdentity;
  } catch (e) {
    throw new Error(`composite-identity is not valid JSON: ${(e as Error).message}`);
  }
  normalizeCompositeIdentity(compositeIdentity);

  const durations =
    durationBaseBranch && (framework === "playwright" || framework === "cypress")
      ? await loadDurationHints({
          baseURL,
          repository: compositeIdentity.repository,
          name: compositeIdentity.name,
          framework,
          branch: durationBaseBranch,
        })
      : new Map<string, number>();

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
    specs = discoverCypressSpecs(cypressDir, filters, (bucket) =>
      orderLongestFirst(bucket, (s) => s.path, durations),
    );
    if (specs.length === 0) {
      throw new Error(
        `no Cypress specs survived the filter under ${cypressDir} ` +
          `(stage=${filters.stage.join(",") || "*"}, ` +
          `include=${filters.includeGroup.join(",") || "*"}, ` +
          `exclude=${filters.excludeGroup.join(",") || "none"}, ` +
          `skip-on=${filters.skipOn.join(",") || "none"})`,
      );
    }
  } else if (framework === "detox") {
    const detoxDir = path.resolve(repoDir, detoxDirInput);
    specs = discoverDetoxSpecs(detoxDir, {
      searchPath: detoxSearchPath,
      includeTags: detoxIncludeTags,
      excludeTags: detoxExcludeTags,
    });
    if (specs.length === 0) {
      throw new Error(
        `no Detox specs found under ${path.join(detoxDir, detoxSearchPath)} ` +
          `(include-tags=${detoxIncludeTags.join(",") || "*"}, ` +
          `exclude-tags=${detoxExcludeTags.join(",") || "none"})`,
      );
    }
  } else if (framework === "maestro") {
    const maestroDir = path.resolve(repoDir, maestroDirInput);
    specs = discoverMaestroSpecs(maestroDir, {
      searchPath: maestroFlowPath,
      excludeTags: maestroExcludeTags,
    });
    if (specs.length === 0) {
      throw new Error(
        `no Maestro flows found under ${path.join(maestroDir, maestroFlowPath)} ` +
          `(exclude-tags=${maestroExcludeTags.join(",") || "none"})`,
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
    specs = orderLongestFirst(specs, (s) => s, durations);
  }
  if (durations.size > 0) {
    const withHistory = specs.filter((p) => durationOf(p, durations) !== undefined).length;
    core.info(
      `longest-first: ${withHistory}/${specs.length} ${framework} spec(s) have base-branch durations; ` +
        "the rest follow alphabetically",
    );
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
  // playwright_project is a Playwright-only field; Cypress runs do not
  // carry it. Including it for cypress would be harmless on the wire
  // (server ignores unknown fields) but is misleading in the logs.
  if (framework === "playwright") {
    beginBody.playwright_project = playwrightProject;
  }

  const beginRes = await retryFetch(
    `${baseURL}/api/v1/orchestration/begin`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
      body: JSON.stringify(beginBody),
    },
    "orchestration/begin",
  );
  if (beginRes.status !== 200 && beginRes.status !== 201) {
    throw new Error(`orchestration/begin failed: ${beginRes.status} ${await safeText(beginRes)}`);
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

  const reportsRes = await retryFetch(
    `${baseURL}/api/v1/reports/begin`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
      body: JSON.stringify(identityForReports(compositeIdentity, framework, totalReportsExpected)),
    },
    "reports/begin",
  );
  if (reportsRes.status !== 200) {
    throw new Error(`reports/begin failed: ${reportsRes.status} ${await safeText(reportsRes)}`);
  }
  const { report_id } = (await reportsRes.json()) as ReportsBeginResponse;
  core.info(`report group ready: ${report_id}`);

  const reportURL = buildReportURL(baseURL, compositeIdentity);
  core.setOutput("report-url", reportURL);

  // Commit status — best-effort, opt-in. The target URL deep-links into
  // the Test System IO report page so reviewers click the commit-status
  // row and land on the live dashboard.
  if (core.getInput("post-pending-commit-status") === "true") {
    await pushPendingCommitStatus(compositeIdentity, reportURL);
  }
}

/**
 * Posts a pending commit status on the tested commit under commit-status-context, linking to the
 * report page; with no context it warns and skips.
 */
async function pushPendingCommitStatus(c: CompositeIdentity, reportURL: string): Promise<void> {
  const context = core.getInput("commit-status-context");
  if (!context) {
    core.warning(
      "post-pending-commit-status is true but commit-status-context is empty; skipping.",
    );
    return;
  }
  const token = core.getInput("github-token");
  const [owner, repo] = (c.repository || "").split("/");
  if (!owner || !repo || !c.commit_sha) {
    core.warning("post-pending-commit-status: missing repository or commit_sha; skipping.");
    return;
  }
  await setCommitStatus({
    token,
    owner,
    repo,
    sha: c.commit_sha,
    state: "pending",
    context,
    description: formatPendingDescription(),
    targetURL: reportURL,
  });
}

/** The pending commit status text, naming the server image tag and its aliases when they are given. */
function formatPendingDescription(): string {
  const imageTag = core.getInput("image-tag");
  const imageAliases = core.getInput("image-aliases");
  if (!imageTag) return "tests running";
  const aliases = imageAliases ? ` (${imageAliases})` : "";
  return `tests running, image_tag:${imageTag}${aliases}`;
}

/**
 * Duration hints for longest-first dispatch. A failed lookup logs a warning and returns no hints,
 * so dispatch keeps its default order.
 */
async function loadDurationHints(opts: {
  baseURL: string;
  repository: string;
  name: string;
  framework: string;
  branch: string;
}): Promise<Map<string, number>> {
  try {
    const h = await fetchDurationHistory({ ...opts, timeoutMs: DURATION_LOOKUP_TIMEOUT_MS });
    core.info(
      `duration hints: ${opts.framework} run ${h.runId} (${h.commit.slice(0, 8)}) of "${h.name}" on ${opts.branch} ` +
        `has durations for ${h.durations.size} file(s)`,
    );
    return h.durations;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    core.warning(`duration hints unavailable (${msg}); keeping the default dispatch order`);
    return new Map<string, number>();
  }
}

/**
 * The composite identity in the shape test-system-io's report endpoints take, with the framework
 * and the expected report count.
 */
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
  if (c.gh_pr_number != null) body.gh_pr_number = c.gh_pr_number;
  return body;
}

/** The test-system-io base URL: staging when use-staging is "true", else production. */
function resolveBaseURL(): string {
  const useStaging = core.getInput("use-staging").trim().toLowerCase() === "true";
  return useStaging ? STAGING_URL : PRODUCTION_URL;
}

/** An integer action input; fallback when it is empty. Throws when it is not an integer. */
function intInput(name: string, fallback: number): number {
  const raw = core.getInput(name);
  if (raw === "") return fallback;
  const n = Number.parseInt(raw, 10);
  if (!Number.isFinite(n)) {
    throw new Error(`input ${name}=${raw} is not an integer`);
  }
  return n;
}

/**
 * normalizeCompositeIdentity coerces gh_pr_number to a number when it
 * arrived as a string. Shell-built composite-identity payloads (jq
 * `--arg pr "${PR_NUMBER}"`) emit it as a string, but the server's
 * identityFields.GHPRNumber is *int and json.Decode rejects the string
 * form, surfacing as a 400 BAD_REQUEST "invalid JSON body" on every
 * orchestration endpoint. Normalizing once here keeps the rest of the
 * action body-agnostic.
 */
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
