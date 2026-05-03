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

export interface CypressUnitResult {
  invocation: InvocationRecord;
  results: SpecResult[];
  // Absolute paths of failure screenshots Cypress wrote, grouped by the
  // spec_path they belong to. main.ts uploads these to
  // /api/v1/orchestration/screenshots and attaches the returned keys to
  // each spec's failing test_cases before POSTing /complete.
  screenshotsBySpec: Record<string, string[]>;
}

export function runUnit(
  cfg: RunUnitConfig,
  iterationSeq: number,
  specPaths: string[],
): CypressUnitResult {
  const iterDir = path.join(cfg.workerArtifacts, `iter-${iterationSeq}`);
  fs.mkdirSync(iterDir, { recursive: true });

  // Wipe the previous run's mochawesome output to avoid stale per-spec
  // files from a prior lease confusing the post-run lookup.
  const reportRoot = path.join(cfg.cypressDir, "results", "mochawesome-report");
  fs.rmSync(reportRoot, { recursive: true, force: true });

  // Same idea for the per-spec screenshot dirs of the leased specs:
  // wiping prevents a retest's failure screenshots from accumulating
  // alongside the prior lease's `(failed) (attempt N).png` set.
  const screenshotsRoot = path.join(cfg.cypressDir, "tests", "screenshots");
  for (const sp of specPaths) {
    const dir = path.join(screenshotsRoot, path.basename(sp));
    fs.rmSync(dir, { recursive: true, force: true });
  }

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
  //
  // Crucially, the next iteration WIPES results/mochawesome-report at its
  // start — so the live `jsonPath` is short-lived. Archive each parsed
  // file to iterDir IMMEDIATELY and track the archived path for the
  // queue-empty `uploadShard` step. Storing the live path here would
  // silently drop every shard's earlier specs by the time the worker
  // finishes draining (uploadShard skips paths whose file no longer
  // exists).
  const results: SpecResult[] = [];
  let firstArchivedPath: string | null = null;
  for (const sp of specPaths) {
    const baseName = path.basename(sp).replace(/\.(ts|js)$/, "");
    const jsonPath = path.join(reportRoot, "json", "tests", `${baseName}.json`);

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

    // Persist a copy outside the soon-to-be-wiped live results tree.
    const archived = path.join(iterDir, `${baseName}.json`);
    fs.cpSync(jsonPath, archived);
    if (!firstArchivedPath) firstArchivedPath = archived;
  }

  // Collect Cypress failure screenshots and stage them for both upload
  // pipelines:
  //   - main.ts will upload these absolute paths to
  //     /api/v1/orchestration/screenshots and attach the returned keys
  //     to the matching test_cases (Dispatch tab rendering).
  //   - copying them under <iterDir>/output/<spec-basename>/ lets
  //     upload.ts pick them up at queue-empty for the
  //     /reports/upload/.../screenshots multipart (Reports tab).
  const screenshotsBySpec: Record<string, string[]> = {};
  const outputRoot = path.join(iterDir, "output");
  for (const sp of specPaths) {
    const baseName = path.basename(sp);
    const srcDir = path.join(screenshotsRoot, baseName);
    if (!fs.existsSync(srcDir)) continue;
    const absPaths: string[] = [];
    walkPng(srcDir, absPaths);
    if (absPaths.length === 0) continue;
    screenshotsBySpec[sp] = absPaths;
    const dstDir = path.join(outputRoot, baseName);
    fs.mkdirSync(dstDir, { recursive: true });
    for (const src of absPaths) {
      fs.cpSync(src, path.join(dstDir, path.basename(src)));
    }
  }

  // The InvocationRecord wants ONE json path; pick the first archived
  // file (so it survives subsequent iterations that wipe the live
  // results dir), falling back to a synthetic path that will fail the
  // existence check upstream and be skipped from the upload set
  // (matching playwright's semantics when its results.json is missing).
  const jsonForUpload = firstArchivedPath ?? path.join(iterDir, "missing.json");
  return {
    invocation: { specPath: specPaths[0]!, iterDir, playwrightJsonPath: jsonForUpload },
    results,
    screenshotsBySpec,
  };
}

function walkPng(dir: string, out: string[]): void {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) walkPng(full, out);
    else if (ent.isFile() && /\.(png|jpe?g)$/i.test(ent.name)) out.push(full);
  }
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
