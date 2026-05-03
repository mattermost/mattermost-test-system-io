/**
 * Per-spec Cypress invocation + Mochawesome aggregation.
 *
 * Mirror of playwright.ts's `runUnit` interface so main.ts can dispatch
 * on the action's `framework` input without other code paths needing to
 * change. Differences from the Playwright adapter:
 *
 *   - Spawns `npx cypress run --reporter cypress-multi-reporters
 *     --reporter-options configFile=reporter-config.json --spec <paths>`,
 *     matching the upstream Mattermost Cypress project's reporter setup.
 *   - Locates the per-spec Mochawesome JSON at
 *     <cypressDir>/results/mochawesome-report/json/tests/<basename>.json
 *     (the path layout reporter-config.json prescribes via reportDir +
 *     reportFilename: 'json/tests/[name]').
 *   - Walks Mochawesome's results[].suites[].tests[] tree to populate the
 *     orchestration `test_cases[]` shape. Status mapping matches the
 *     scripts/lib/cypress-mochawesome-parser.js parser used by the local
 *     demo: pending → skipped, failed → failed, passed-with-attempts > 1
 *     → flaky, passed → passed.
 *   - Reuses the InvocationRecord.playwrightJsonPath field for the
 *     Mochawesome JSON path. The field name is historical (the wire is
 *     framework-agnostic — upload.ts uploads it verbatim to
 *     /api/v1/reports/upload/{rid}/{uid}/json), and the server's
 *     report-ingest pipeline detects framework via the report_groups
 *     row's framework column.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import { spawnSync } from "node:child_process";
import * as core from "@actions/core";
import type { InvocationRecord, SpecResult, TestCaseResult, TestStatus } from "./types";

export interface RunUnitConfig {
  cypressDir: string;
  resultsDir: string;
  workerArtifacts: string;
}

export function runUnit(
  cfg: RunUnitConfig,
  iterationSeq: number,
  specPaths: string[],
): { invocation: InvocationRecord; results: SpecResult[] } {
  const iterDir = path.join(cfg.workerArtifacts, `iter-${iterationSeq}`);
  fs.mkdirSync(iterDir, { recursive: true });

  // Wipe the previous run's mochawesome output to avoid stale per-spec
  // files from a prior lease confusing the post-run lookup.
  const reportRoot = path.join(cfg.cypressDir, "results", "mochawesome-report");
  fs.rmSync(reportRoot, { recursive: true, force: true });

  // The reporter is configured project-side via the consumer's
  // reporter-config.json; the dispatcher merely names the multi-reporter
  // bridge so cypress wires both junit + mochawesome correctly. Specs
  // are joined with commas (cypress's documented multi-spec form).
  const args = [
    "cypress",
    "run",
    "--reporter",
    "cypress-multi-reporters",
    "--reporter-options",
    "configFile=reporter-config.json",
    "--spec",
    specPaths.join(","),
  ];

  const startedAt = Date.now();
  const child = spawnSync("npx", args, {
    cwd: cfg.cypressDir,
    env: { ...process.env, FORCE_COLOR: "0" },
    stdio: "inherit",
  });
  const durationMs = Date.now() - startedAt;
  core.info(`cypress exit ${child.status} in ${Math.round(durationMs / 1000)}s`);

  // Cypress writes one JSON per spec under results/mochawesome-report/
  // json/tests/[name].json by reporter-config.json convention. Aggregate
  // every leased spec independently so a missing-file case for one spec
  // doesn't poison the rest.
  const results: SpecResult[] = [];
  let firstJsonPath: string | null = null;
  for (const sp of specPaths) {
    const baseName = path.basename(sp).replace(/\.(ts|js)$/, "");
    const jsonPath = path.join(reportRoot, "json", "tests", `${baseName}.json`);
    if (!firstJsonPath && fs.existsSync(jsonPath)) firstJsonPath = jsonPath;

    if (!fs.existsSync(jsonPath)) {
      core.warning(`mochawesome json missing for ${sp}: ${jsonPath}`);
      results.push({
        spec_path: sp,
        status: "interrupted",
        actual_duration_ms: 0,
        test_cases: [],
      });
      continue;
    }

    let parsed: MochawesomeJson;
    try {
      parsed = JSON.parse(fs.readFileSync(jsonPath, "utf8")) as MochawesomeJson;
    } catch (e) {
      core.warning(`mochawesome json parse failure for ${sp}: ${(e as Error).message}`);
      results.push({
        spec_path: sp,
        status: "interrupted",
        actual_duration_ms: 0,
        test_cases: [],
      });
      continue;
    }
    results.push(aggregateSpec(parsed, sp));

    // Archive a copy under the per-iteration dir so multiple workers'
    // artifacts don't fight over the same path on disk.
    const archived = path.join(iterDir, `${baseName}.json`);
    fs.cpSync(jsonPath, archived);
  }

  // The InvocationRecord wants ONE json path; pick the first valid one,
  // falling back to a synthetic path that will fail the existence check
  // upstream and be skipped from the upload set (matching playwright's
  // semantics when its results.json is missing).
  const jsonForUpload = firstJsonPath ?? path.join(iterDir, "missing.json");
  return {
    invocation: { specPath: specPaths[0]!, iterDir, playwrightJsonPath: jsonForUpload },
    results,
  };
}

interface MochawesomeJson {
  results?: MochawesomeSuite[];
}
interface MochawesomeSuite {
  title?: string;
  fullFile?: string;
  tests?: MochawesomeTest[];
  suites?: MochawesomeSuite[];
}
interface MochawesomeTest {
  title?: string;
  fullTitle?: string;
  state?: string;
  pending?: boolean;
  duration?: number;
  attempts?: unknown[];
  err?: { message?: string; stack?: string; estack?: string };
}

const RANKS: Record<TestStatus, number> = {
  skipped: 0,
  passed: 1,
  flaky: 2,
  interrupted: 3,
  timedOut: 4,
  failed: 5,
};

export function aggregateSpec(json: MochawesomeJson, specPath: string): SpecResult {
  const cases: TestCaseResult[] = [];
  let totalMs = 0;
  let worst: TestStatus = "skipped";
  let ordinal = 0;

  function visit(suite: MochawesomeSuite): void {
    for (const t of suite.tests || []) {
      const attempts = Array.isArray(t.attempts) ? t.attempts.length : 0;
      let status: TestStatus;
      if (t.pending === true) status = "skipped";
      else if (t.state === "failed") status = "failed";
      else if (t.state === "passed" && attempts > 1) status = "flaky";
      else if (t.state === "passed") status = "passed";
      else status = "interrupted";

      const tc: TestCaseResult = {
        title: t.title || "",
        full_title: t.fullTitle || t.title || "",
        status,
        retry_count: Math.max(0, attempts - 1),
        duration_ms: typeof t.duration === "number" ? t.duration : 0,
        ordinal: ordinal++,
      };
      if (t.err?.message) tc.error_message = t.err.message;
      const stack = t.err?.estack ?? t.err?.stack;
      if (stack) tc.error_stack = stack;
      cases.push(tc);
      totalMs += tc.duration_ms;
      if (RANKS[status] > RANKS[worst]) worst = status;
    }
    for (const inner of suite.suites || []) visit(inner);
  }
  for (const top of json.results || []) visit(top);

  if (cases.length === 0) {
    return { spec_path: specPath, status: "skipped", actual_duration_ms: 0, test_cases: [] };
  }
  const out: SpecResult = {
    spec_path: specPath,
    status: worst,
    actual_duration_ms: totalMs,
    test_cases: cases,
  };
  const firstFail = cases.find(
    (c) => c.status === "failed" || c.status === "timedOut" || c.status === "interrupted",
  );
  if (firstFail?.error_message) out.error_message = firstFail.error_message;
  if (firstFail?.error_stack) out.error_stack = firstFail.error_stack;
  return out;
}
