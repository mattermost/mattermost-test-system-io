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
import { runUnit as runDetoxUnit } from "./detox";
import { runUnit as runMaestroUnit, parseMaestroEnv } from "./maestro";
import { screenshotContentType } from "./mime";
import { uploadShard, type UploadConfig } from "./upload";
import type {
  CheckoutResponseBody,
  CompleteResponseBody,
  CompositeIdentity,
  DbPoolStats,
  InvocationRecord,
  QueueCounts,
  SpecResult,
  WorkerCounts,
} from "./types";

const PRODUCTION_URL = "https://test-io.test.mattermost.com";
const STAGING_URL = "https://staging-test-io.test.mattermost.com";

export async function run(): Promise<void> {
  const baseURL = resolveBaseURL();
  const audience = core.getInput("oidc-audience") || "mattermost-test-system-io";
  const environmentMetadataRaw = core.getInput("environment-metadata").trim();
  if (environmentMetadataRaw) {
    try {
      JSON.parse(environmentMetadataRaw);
    } catch (err) {
      throw new Error(
        `environment-metadata must be valid JSON: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }
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
  const playwrightRetries = intInput("playwright-retries", 1);
  const playwrightProject = core.getInput("playwright-project") || "chrome";
  const playwrightDirInput = core.getInput("playwright-dir") || "e2e-tests/playwright";
  const resultsDirInput = core.getInput("results-dir") || "results";
  const cypressDirInput = core.getInput("cypress-dir") || "e2e-tests/cypress";
  const detoxDirInput = core.getInput("detox-dir") || "detox";
  const detoxConfig = core.getInput("detox-config") || "ios.sim.debug";
  const batchSize = intInput("batch-size", 1);
  if (batchSize < 1) {
    throw new Error(`batch-size must be >= 1, got ${batchSize}`);
  }
  if (framework === "maestro" && batchSize > 1) {
    core.warning(`batch-size=${batchSize} ignored for maestro; checkout uses batch_size=1`);
  }
  // detox-max-workers is Detox-only (action.yml); ignore for other frameworks
  // so a stale/invalid value cannot fail Playwright/Cypress/Maestro runs.
  let detoxMaxWorkers = 1;
  if (framework === "detox") {
    detoxMaxWorkers = intInput("detox-max-workers", 1);
    if (detoxMaxWorkers < 1) {
      throw new Error(`detox-max-workers must be >= 1, got ${detoxMaxWorkers}`);
    }
  }
  const maestroDirInput = core.getInput("maestro-dir") || "detox/maestro";
  const maestroDevice = core.getInput("maestro-device");
  const maestroPlatform = core.getInput("maestro-platform");
  const maestroEnv = parseMaestroEnv(core.getInput("maestro-env"));
  // Default 9m — keep below begin's lease-timeout-ms default (10m).
  const maestroTimeoutMs = intInput("maestro-timeout-ms", 540_000);
  if (maestroTimeoutMs <= 0) {
    throw new Error(`maestro-timeout-ms must be > 0, got ${maestroTimeoutMs}`);
  }
  // 0 disables the cap; see drain()'s idlePolls.
  const maxIdlePolls = intInput("max-idle-polls", 5);
  // Longer than the server's retry_after_ms ceiling (~7s); see drain().
  const postFailureDelayMs = intInput("post-failure-delay-ms", 10000);

  let compositeIdentity: CompositeIdentity;
  try {
    compositeIdentity = JSON.parse(compositeIdentityRaw) as CompositeIdentity;
  } catch (e) {
    throw new Error(`composite-identity is not valid JSON: ${(e as Error).message}`);
  }
  normalizeCompositeIdentity(compositeIdentity);

  // resolved.name is the GitHub Jobs API's composed display name (may
  // differ from ghJobName for nested workflow_call chains) — persist it,
  // not the raw input.
  const resolved = await resolveJobId(githubToken, ghJobName);
  const ghJobId = resolved.id;
  const resolvedJobName = resolved.name;
  core.info(`resolved gh_job_id=${ghJobId} gh_job_name=${resolvedJobName} (input=${ghJobName})`);

  const playwrightDir = path.resolve(repoDir, playwrightDirInput);
  const resultsDir = path.resolve(playwrightDir, resultsDirInput);
  const cypressDir = path.resolve(repoDir, cypressDirInput);
  const detoxDir = path.resolve(repoDir, detoxDirInput);
  const maestroDir = path.resolve(repoDir, maestroDirInput);
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
      detoxDir,
      detoxConfig,
      detoxMaxWorkers,
      batchSize,
      maestroDir,
      maestroDevice,
      maestroPlatform,
      maestroEnv,
      maestroTimeoutMs,
      workerArtifacts,
      playwrightRetries,
      playwrightProject,
      maxIdlePolls,
      postFailureDelayMs,
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
      framework,
      compositeIdentity,
      environmentMetadata: environmentMetadataRaw || undefined,
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

/**
 * Renders the optional counts/db_pool/workers fields (present on both
 * /checkout and /complete) as a compact log suffix. All best-effort —
 * degrades to "" if absent.
 */
function formatDiagnostics(body: {
  counts?: QueueCounts;
  db_pool?: DbPoolStats;
  workers?: WorkerCounts;
}): string {
  const parts: string[] = [];
  const c = body.counts;
  if (c) {
    parts.push(
      `queue: pending=${c.pending} leased=${c.leased} pass=${c.completed_pass} ` +
        `fail=${c.completed_fail} skip=${c.completed_skipped} retest_eligible=${c.retest_eligible} ` +
        `total=${c.total}`,
    );
  }
  const w = body.workers;
  if (w) {
    parts.push(`workers: active=${w.active} seen_total=${w.seen_total}`);
  }
  const p = body.db_pool;
  if (p) {
    parts.push(
      `db_pool: acquired=${p.acquired_conns} idle=${p.idle_conns} total=${p.total_conns}/${p.max_conns} ` +
        `empty_acquires=${p.empty_acquire_count}`,
    );
  }
  return parts.length > 0 ? ` [${parts.join(" | ")}]` : "";
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
  detoxDir: string;
  detoxConfig: string;
  detoxMaxWorkers: number;
  batchSize: number;
  maestroDir: string;
  maestroDevice: string;
  maestroPlatform: string;
  maestroEnv: Record<string, string>;
  maestroTimeoutMs: number;
  workerArtifacts: string;
  playwrightRetries: number;
  playwrightProject: string;
  // 0 disables the cap. See idlePolls in drain().
  maxIdlePolls: number;
  // Extra sleep after reporting a retest-eligible failure, before this
  // worker's next /checkout.
  postFailureDelayMs: number;
  invocations: InvocationRecord[];
  nextIterationSeq: () => number;
}

async function drain(cfg: DrainConfig): Promise<void> {
  let leasesHeld = 0;
  // Consecutive empty-poll count; reset to 0 whenever a unit is leased.
  let idlePolls = 0;
  while (true) {
    // Maestro runs one flow per invocation (runMaestroUnit only executes
    // specPaths[0]); never lease more than one unit for that framework.
    const checkoutBatchSize = cfg.framework === "maestro" ? 1 : cfg.batchSize;
    const checkout = await postJSON<CheckoutResponseBody>(cfg, "/api/v1/orchestration/checkout", {
      ...(cfg.compositeIdentity as unknown as Record<string, unknown>),
      gh_job_name: cfg.ghJobName,
      gh_job_id: cfg.ghJobId,
      batch_size: checkoutBatchSize,
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
    const diag = formatDiagnostics(body);
    if (body.queue_empty) {
      const retryAfterMs = Number(body.retry_after_ms);
      if (Number.isFinite(retryAfterMs) && retryAfterMs > 0) {
        idlePolls += 1;
        if (cfg.maxIdlePolls > 0 && idlePolls >= cfg.maxIdlePolls) {
          // Self-imposed: server would still have us poll, but we've waited
          // long enough. Other active workers can cover what remains.
          core.info(
            `queue empty after ${leasesHeld} unit(s); giving up after ${idlePolls} consecutive idle poll(s) ` +
              `(max-idle-polls=${cfg.maxIdlePolls})${diag}`,
          );
          break;
        }
        core.info(
          `queue empty; sleeping ${retryAfterMs}ms before re-polling ` +
            `(idle poll ${idlePolls}${cfg.maxIdlePolls > 0 ? `/${cfg.maxIdlePolls}` : ""})${diag}`,
        );
        await sleep(retryAfterMs);
        continue;
      }
      core.info(`queue empty after ${leasesHeld} unit(s); exiting cleanly${diag}`);
      break;
    }

    leasesHeld += 1;
    idlePolls = 0; // got real work; no longer in an idle stretch
    const isRetest = !!body.is_retest;
    const specPaths = (body.units || []).map((u) => u.spec_path);
    // Prefix with dispatch_seq (the run's FIFO order key) for log visibility.
    const specLabels = (body.units || []).map((u) => `${u.dispatch_seq} ${u.spec_path}`);
    core.info(`leased (${isRetest ? "retest" : "fresh"}): ${specLabels.join(", ")}${diag}`);

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
        // Cypress only writes a bare (title-derived) filename to disk, with
        // no link back to the test that produced it, so failure screenshots
        // have to be matched by filename and uploaded out-of-band before
        // /complete sees them.
        await attachCypressScreenshots(cfg, results, out.screenshotsBySpec);
      } else if (cfg.framework === "detox") {
        const out = runDetoxUnit(
          {
            detoxDir: cfg.detoxDir,
            detoxConfig: cfg.detoxConfig,
            workerArtifacts: cfg.workerArtifacts,
            maxWorkers: cfg.detoxMaxWorkers,
          },
          cfg.nextIterationSeq(),
          specPaths,
        );
        cfg.invocations.push(out.invocation);
        results = out.results;
        // Detox screenshots live under a folder named after the test's
        // fullName, so match on that instead of a filename substring.
        await attachDetoxScreenshots(cfg, results, out.screenshotsBySpec);
      } else if (cfg.framework === "maestro") {
        const out = await runMaestroUnit(
          {
            maestroDir: cfg.maestroDir,
            maestroDevice: cfg.maestroDevice,
            maestroPlatform: cfg.maestroPlatform,
            maestroEnv: cfg.maestroEnv,
            maestroTimeoutMs: cfg.maestroTimeoutMs,
            workerArtifacts: cfg.workerArtifacts,
          },
          cfg.nextIterationSeq(),
          specPaths,
        );
        cfg.invocations.push(out.invocation);
        results = out.results;
        // batch_size is always 1 for Maestro, so every screenshot belongs to
        // the single spec/test_case this invocation ran — no matching needed.
        await attachMaestroScreenshots(cfg, results, out.screenshotsBySpec);
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
        // Playwright's JSON reporter links each screenshot directly to the
        // test result that produced it, so no filename matching is needed —
        // just upload and attach to the (spec_path, ordinal) it names.
        await attachPlaywrightScreenshots(cfg, results, out.screenshots);
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
      `reported (${results.map((r) => r.status).join(",")}) → ${transitions || "(no transition)"}` +
        formatDiagnostics(completeRes.body || {}),
    );

    // Client-side only: delay this worker's next /checkout so another
    // worker's poll gets a chance at the retest first. Only once pending
    // fresh units are 0, since the server gates all retest dispatch on that.
    const pendingElsewhere = completeRes.body?.counts?.pending;
    if (results.some((r) => RETEST_ELIGIBLE_STATUSES.has(r.status)) && pendingElsewhere === 0) {
      core.info(
        `failed result reported; waiting ${cfg.postFailureDelayMs}ms before next checkout ` +
          "so another worker's poll can pick up the retest",
      );
      await sleep(cfg.postFailureDelayMs);
    }
  }
}

// Statuses the server's mapStatusesToUnitState (complete.go) treats as a
// unit failure — any of these makes the unit retest-eligible.
const RETEST_ELIGIBLE_STATUSES: ReadonlySet<string> = new Set([
  "failed",
  "timedOut",
  "interrupted",
]);

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
 * stripping filesystem-invalid characters (`/\:*?"<>|`) from the title in
 * the process, so we strip the same characters from `tc.title` before
 * checking whether a screenshot's basename includes it. When multiple
 * tests in a spec share a title prefix, the longest matching title wins
 * (so "MM-T4417_1 ..." doesn't accidentally claim a "MM-T4417_10 ..."
 * shot).
 *
 * Best-effort: a screenshot upload error logs a warning and drops the
 * single file. The /complete payload still goes out — better to lose a
 * screenshot than to fail the orchestration step.
 */
const CYPRESS_INVALID_FILENAME_CHARS_RE = /[/\\:*?"<>|]/g;

function stripCypressInvalidFilenameChars(title: string): string {
  return title.replace(CYPRESS_INVALID_FILENAME_CHARS_RE, "");
}

async function attachCypressScreenshots(
  cfg: {
    baseURL: string;
    audience: string;
    compositeIdentity: CompositeIdentity;
    ghJobId: string;
    ghJobName: string;
    framework: string;
  },
  results: SpecResult[],
  screenshotsBySpec: Record<string, string[]>,
): Promise<void> {
  for (const spec of results) {
    const files = screenshotsBySpec[spec.spec_path];
    if (!files || files.length === 0) continue;

    // Sort failing-eligible test_cases by descending sanitized-title length
    // so longer titles match before shorter prefixes of theirs.
    const candidates = spec.test_cases
      .filter(
        (tc) => tc.status === "failed" || tc.status === "timedOut" || tc.status === "interrupted",
      )
      .map((tc) => ({ tc, sanitizedTitle: stripCypressInvalidFilenameChars(tc.title) }))
      .sort((a, b) => b.sanitizedTitle.length - a.sanitizedTitle.length);
    if (candidates.length === 0) continue;

    for (const absPath of files) {
      const base = path.basename(absPath);
      const match = candidates.find((c) => c.sanitizedTitle && base.includes(c.sanitizedTitle));
      const tc = match?.tc;
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

/**
 * Upload each spec's Playwright failure screenshots and attach the
 * returned keys to the (spec_path, ordinal)-identified test_case —
 * Playwright's reporter links attachments to results directly, so no
 * filename-matching heuristic is needed here (unlike Cypress).
 */
async function attachPlaywrightScreenshots(
  cfg: {
    baseURL: string;
    audience: string;
    compositeIdentity: CompositeIdentity;
    ghJobId: string;
    ghJobName: string;
    framework: string;
  },
  results: SpecResult[],
  screenshots: { specPath: string; ordinal: number; absPath: string }[],
): Promise<void> {
  const bySpecPath = new Map(results.map((r) => [r.spec_path, r]));
  for (const shot of screenshots) {
    const spec = bySpecPath.get(shot.specPath);
    const tc = spec?.test_cases.find((c) => c.ordinal === shot.ordinal);
    if (!tc) {
      core.warning(`no test_case match for screenshot ${path.basename(shot.absPath)}; skipping`);
      continue;
    }
    const uploaded = await uploadOrchScreenshot(cfg, shot.specPath, shot.absPath);
    if (!uploaded) continue;
    tc.attachments ??= { screenshots: [] };
    tc.attachments.screenshots.push(uploaded);
  }
}

/** Matches each screenshot's parent folder name against a test_case's full_title (Detox's fullName). */
async function attachDetoxScreenshots(
  cfg: {
    baseURL: string;
    audience: string;
    compositeIdentity: CompositeIdentity;
    ghJobId: string;
    ghJobName: string;
    framework: string;
  },
  results: SpecResult[],
  screenshotsBySpec: Record<string, string[]>,
): Promise<void> {
  for (const spec of results) {
    const files = screenshotsBySpec[spec.spec_path];
    if (!files || files.length === 0) continue;

    for (const absPath of files) {
      const folderName = path.basename(path.dirname(absPath));
      // Hook failures (beforeAll/afterAll) write directly under the
      // session folder, not a per-test folder, so there's no full_title
      // to match — fall back to the spec's first failed test_case rather
      // than dropping a screenshot that's still useful for debugging.
      const tc =
        spec.test_cases.find((c) => c.full_title === folderName) ??
        spec.test_cases.find((c) => c.status === "failed");
      if (!tc) {
        core.warning(`no test_case match for screenshot folder ${folderName}; skipping`);
        continue;
      }
      const uploaded = await uploadOrchScreenshot(cfg, spec.spec_path, absPath);
      if (!uploaded) continue;
      tc.attachments ??= { screenshots: [] };
      tc.attachments.screenshots.push(uploaded);
    }
  }
}

/**
 * Attaches every screenshot from one Maestro invocation to the spec's sole
 * test_case. batch_size is always 1 for Maestro (one flow file per
 * invocation), so unlike Detox/Cypress there's no folder-name/full_title
 * ambiguity to resolve — the single test_case is the unambiguous owner.
 */
async function attachMaestroScreenshots(
  cfg: {
    baseURL: string;
    audience: string;
    compositeIdentity: CompositeIdentity;
    ghJobId: string;
    ghJobName: string;
    framework: string;
  },
  results: SpecResult[],
  screenshotsBySpec: Record<string, string[]>,
): Promise<void> {
  for (const spec of results) {
    const files = screenshotsBySpec[spec.spec_path];
    if (!files || files.length === 0) continue;
    const tc = spec.test_cases[0];
    if (!tc) {
      core.warning(`no test_case to attach screenshots to for ${spec.spec_path}; skipping`);
      continue;
    }
    for (const absPath of files) {
      const uploaded = await uploadOrchScreenshot(cfg, spec.spec_path, absPath);
      if (!uploaded) continue;
      tc.attachments ??= { screenshots: [] };
      tc.attachments.screenshots.push(uploaded);
    }
  }
}

async function uploadOrchScreenshot(
  cfg: {
    baseURL: string;
    audience: string;
    compositeIdentity: CompositeIdentity;
    ghJobId: string;
    ghJobName: string;
    framework: string;
  },
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
  form.append("gh_job_name", cfg.ghJobName);
  form.append("framework", cfg.framework);
  form.append("spec_path", specPath);
  form.append("relative_path", relPath);
  // Wrap Node's Buffer in Uint8Array — same trick upload.ts uses to bridge
  // node:buffer to DOM Blob's BlobPart type.
  form.append(
    "file",
    new Blob([new Uint8Array(buf)], { type: screenshotContentType(absPath) }),
    relPath,
  );

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
  if (!res.ok) {
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
  // Reject decimals / trailing junk ("2.5", "2abc") — parseInt would truncate.
  const n = Number(raw);
  if (!Number.isInteger(n) || n < 0) {
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
