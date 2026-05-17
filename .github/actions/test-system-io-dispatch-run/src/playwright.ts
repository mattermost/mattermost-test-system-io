/**
 * Per-spec Playwright invocation + JSON aggregation.
 *
 * `runUnit` shells out to `npx playwright test ...`, archives the raw
 * results dir to a per-iteration folder under `worker-artifacts/`,
 * parses the reporter JSON, and returns one Test System IO SpecResult
 * per spec_path the lease covered.
 *
 * `aggregateSpec` walks the suite tree and applies Playwright's flaky
 * semantics at the per-test level: a test that fails then passes via
 * `--retries` is "flaky" (counts as passed), not a hard failure. The
 * spec's overall status is the worst per-test outcome — so a spec
 * that recovers across retries reports `flaky` to the orchestrator
 * instead of `failed`, keeping it out of the retest queue.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import { spawnSync } from "node:child_process";
import * as core from "@actions/core";
import type { InvocationRecord, SpecResult, TestCaseResult, TestStatus } from "./types";

export interface RunUnitConfig {
  playwrightDir: string;
  resultsDir: string;
  workerArtifacts: string;
  playwrightRetries: number;
  playwrightProject: string;
}

export function runUnit(
  cfg: RunUnitConfig,
  iterationSeq: number,
  specPaths: string[],
): { invocation: InvocationRecord; results: SpecResult[] } {
  // Empty specPaths would silently degrade to `npx playwright test` (i.e. run
  // the whole suite) and leave invocation.specPath undefined. Fail fast.
  if (specPaths.length === 0) {
    throw new Error("runUnit requires at least one spec path");
  }

  const iterDir = path.join(cfg.workerArtifacts, `iter-${iterationSeq}`);
  fs.mkdirSync(iterDir, { recursive: true });

  // ci/prepare-playwright runs `npm ci` + `npm run build` once per worker
  // job; the per-spec loop just dispatches Playwright directly.
  // --no-deps skips the `setup` project that the active project declares
  // as a dependency. ci/prepare-playwright already ran setup once at job
  // start, and its side effects (plugins loaded, server deployed) persist
  // on the long-running mattermost server, so every spec's worth of setup
  // re-runs is wasted time. Visual specs are filtered out at dispatch-begin
  // time via the testIgnore-style excludePaths, so no runtime --grep-invert
  // is needed.
  const args = ["playwright", "test", `--project=${cfg.playwrightProject}`, "--no-deps"];
  if (cfg.playwrightRetries > 0) args.push(`--retries=${cfg.playwrightRetries}`);
  args.push(...specPaths);

  // Clear the shared results dir so a Playwright crash before it writes
  // fresh reporter output doesn't let us archive the previous lease's
  // results for the current spec. Mirrors the cypress adapter's wipe of
  // results/mochawesome-report at the top of runUnit.
  fs.rmSync(cfg.resultsDir, { recursive: true, force: true });

  const startedAt = Date.now();
  const child = spawnSync("npx", args, {
    cwd: cfg.playwrightDir,
    env: { ...process.env, PW_SNAPSHOT_ENABLE: "true" },
    stdio: "inherit",
  });
  // spawnSync doesn't throw on launch failure — it returns child.error
  // (e.g. ENOENT, EACCES). A null status means the process was killed by a
  // signal rather than exiting cleanly; surface both as hard failures so the
  // caller doesn't proceed to read a results dir that was never written.
  if (child.error) {
    throw child.error;
  }
  if (child.status === null) {
    throw new Error(`playwright terminated by signal: ${child.signal ?? "unknown"}`);
  }
  const durationMs = Date.now() - startedAt;
  core.info(`playwright exit ${child.status} in ${Math.round(durationMs / 1000)}s`);

  if (!fs.existsSync(cfg.resultsDir)) {
    throw new Error(`results dir missing after playwright run: ${cfg.resultsDir}`);
  }
  const archivedResults = path.join(iterDir, "results");
  fs.cpSync(cfg.resultsDir, archivedResults, { recursive: true });

  const playwrightJsonPath = path.join(archivedResults, "reporter", "results.json");
  if (!fs.existsSync(playwrightJsonPath)) {
    throw new Error(`playwright results.json missing: ${playwrightJsonPath}`);
  }

  const json = JSON.parse(fs.readFileSync(playwrightJsonPath, "utf8")) as PlaywrightJson;
  const results = specPaths.map((p) => aggregateSpec(json, p, durationMs));
  return {
    invocation: {
      specPath: specPaths[0]!,
      iterDir: archivedResults,
      jsonPaths: [playwrightJsonPath],
    },
    results,
  };
}

interface PlaywrightSuite {
  title?: string;
  file?: string;
  specs?: PlaywrightSpec[];
  suites?: PlaywrightSuite[];
}
interface PlaywrightSpec {
  title: string;
  tests?: PlaywrightTest[];
}
interface PlaywrightTest {
  results?: PlaywrightResult[];
}
interface PlaywrightResult {
  status?: string;
  retry?: number;
  duration?: number;
  errors?: { message?: string; stack?: string }[];
  error?: { message?: string; stack?: string };
}
interface PlaywrightJson {
  suites?: PlaywrightSuite[];
}

const RANKS: Record<TestStatus, number> = {
  skipped: 0,
  passed: 1,
  flaky: 2,
  interrupted: 3,
  timedOut: 4,
  failed: 5,
};

export function aggregateSpec(
  json: PlaywrightJson,
  specPath: string,
  fallbackDurationMs: number,
): SpecResult {
  const cases: TestCaseResult[] = [];
  let totalMs = 0;
  let worst: TestStatus = "skipped";

  function fileMatches(file: string | undefined): boolean {
    if (!file) return false;
    if (file === specPath) return true;
    if (file.endsWith("/" + specPath)) return true;
    if (specPath.endsWith("/" + file)) return true;
    return false;
  }

  function visit(suite: PlaywrightSuite, ancestors: string[], currentFile: string): void {
    const here = suite.title ? [...ancestors, suite.title] : ancestors;
    const suiteFile = suite.file || currentFile;
    if (fileMatches(suiteFile)) {
      for (const s of suite.specs || []) {
        const specTitle = [...here, s.title];
        for (const t of s.tests || []) {
          // Aggregate per-test: any pass after a failure is "flaky"
          // (== passed) at the test level. Per-result rows still capture
          // every Playwright execution so the dashboard can show the full
          // retry history.
          let everPassed = false;
          let everFailed = false;
          let everTimedOut = false;
          let everInterrupted = false;
          let everSkipped = false;
          for (const r of t.results || []) {
            const status = mapStatus(r.status);
            if (status === "passed" || status === "flaky") everPassed = true;
            else if (status === "failed") everFailed = true;
            else if (status === "timedOut") everTimedOut = true;
            else if (status === "interrupted") everInterrupted = true;
            else if (status === "skipped") everSkipped = true;

            const tc: TestCaseResult = {
              title: s.title,
              full_title: specTitle.join(" > "),
              status,
              retry_count: r.retry || 0,
              duration_ms: r.duration || 0,
              ordinal: cases.length,
            };
            const err = (r.errors && r.errors[0]) || r.error;
            if (err?.message) tc.error_message = err.message;
            if (err?.stack) tc.error_stack = err.stack;
            cases.push(tc);
            totalMs += tc.duration_ms;
          }

          let testOutcome: TestStatus | null = null;
          if (everPassed && (everFailed || everTimedOut || everInterrupted)) testOutcome = "flaky";
          else if (everPassed) testOutcome = "passed";
          else if (everInterrupted) testOutcome = "interrupted";
          else if (everTimedOut) testOutcome = "timedOut";
          else if (everFailed) testOutcome = "failed";
          else if (everSkipped) testOutcome = "skipped";
          if (testOutcome == null) continue;
          if (RANKS[testOutcome] > RANKS[worst]) worst = testOutcome;
        }
      }
    }
    for (const sub of suite.suites || []) visit(sub, here, suiteFile);
  }
  for (const s of json.suites || []) visit(s, [], "");

  if (cases.length === 0) {
    return { spec_path: specPath, status: "skipped", actual_duration_ms: 0, test_cases: [] };
  }

  // When every case in the spec is "skipped", totalMs is legitimately 0
  // — falling back to the whole-run wall clock would attribute the entire
  // playwright invocation to this one spec. Only use the fallback when
  // totalMs is missing AND the spec actually ran something.
  const actualDurationMs = totalMs > 0 || worst === "skipped" ? totalMs : fallbackDurationMs;
  const out: SpecResult = {
    spec_path: specPath,
    status: worst,
    actual_duration_ms: actualDurationMs,
    test_cases: cases,
  };
  const firstFail = cases.find(
    (c) => c.status === "failed" || c.status === "timedOut" || c.status === "interrupted",
  );
  if (firstFail?.error_message) out.error_message = firstFail.error_message;
  if (firstFail?.error_stack) out.error_stack = firstFail.error_stack;
  return out;
}

function mapStatus(s: string | undefined): TestStatus {
  switch (s) {
    case "expected":
    case "passed":
      return "passed";
    case "unexpected":
    case "failed":
      return "failed";
    case "flaky":
      return "flaky";
    case "skipped":
      return "skipped";
    case "timedOut":
      return "timedOut";
    case "interrupted":
      return "interrupted";
    default:
      return "failed";
  }
}
