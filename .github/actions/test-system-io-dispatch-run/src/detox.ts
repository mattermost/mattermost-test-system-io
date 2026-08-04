/** Per-spec Detox invocation + Jest JSON aggregation. Mirrors cypress.ts's runUnit shape. */

import * as fs from "node:fs";
import * as path from "node:path";
import { spawnSync } from "node:child_process";
import * as core from "@actions/core";
import type { InvocationRecord, SpecResult, TestCaseResult, TestStatus } from "./types";

export interface RunUnitConfig {
  detoxDir: string;
  detoxConfig: string;
  workerArtifacts: string;
}

export interface DetoxUnitResult {
  invocation: InvocationRecord;
  results: SpecResult[];
  // Screenshot absolute paths, grouped by spec_path.
  screenshotsBySpec: Record<string, string[]>;
}

const DETOX_NODE_OPTIONS = "--max_old_space_size=4096";

export function runUnit(
  cfg: RunUnitConfig,
  iterationSeq: number,
  specPaths: string[],
): DetoxUnitResult {
  const iterDir = path.join(cfg.workerArtifacts, `iter-${iterationSeq}`);
  fs.mkdirSync(iterDir, { recursive: true });

  const artifactsDir = path.join(iterDir, "artifacts");
  const jestOutputPath = path.join(iterDir, "jest-results.json");

  const args = [
    "detox",
    "test",
    ...specPaths,
    "-c",
    cfg.detoxConfig,
    "--record-logs",
    "failing",
    "--take-screenshots",
    "failing",
    "--artifacts-location",
    artifactsDir,
    "--",
    "--json",
    "--outputFile",
    jestOutputPath,
  ];

  const nodeOptions = [process.env.NODE_OPTIONS, DETOX_NODE_OPTIONS].filter(Boolean).join(" ");

  const startedAt = Date.now();
  const child = spawnSync("npx", args, {
    cwd: cfg.detoxDir,
    env: { ...process.env, NODE_OPTIONS: nodeOptions },
    stdio: "inherit",
  });
  const durationMs = Date.now() - startedAt;
  core.info(
    `detox exit ${child.status} in ${Math.round(durationMs / 1000)}s` +
      (child.error ? ` error=${child.error.message}` : "") +
      (child.signal ? ` signal=${child.signal}` : ""),
  );

  let parsed: JestReport | null = null;
  if (!fs.existsSync(jestOutputPath)) {
    core.warning(`detox jest json missing: ${jestOutputPath}`);
  } else {
    try {
      parsed = JSON.parse(fs.readFileSync(jestOutputPath, "utf8")) as JestReport;
    } catch (e) {
      core.warning(`detox jest json parse failure: ${(e as Error).message}`);
    }
  }

  const results: SpecResult[] = [];
  for (const sp of specPaths) {
    const baseName = path.basename(sp);
    const fileEntry = parsed?.testResults?.find((f) => path.basename(f.name ?? "") === baseName);
    if (!fileEntry) {
      results.push({ spec_path: sp, status: "interrupted", actual_duration_ms: 0, test_cases: [] });
      continue;
    }
    results.push(aggregateDetoxFile(fileEntry, sp));
  }

  const screenshotsBySpec = collectDetoxScreenshots(artifactsDir, results);

  return {
    invocation: { specPath: specPaths[0]!, iterDir, playwrightJsonPath: jestOutputPath },
    results,
    screenshotsBySpec,
  };
}

/**
 * Buckets screenshots by parent-folder name, matched against each
 * test_case's full_title. Hook failures (beforeAll/afterAll) write their
 * screenshot directly under the session folder instead of a per-test
 * folder, so there's no full_title to match — with a single spec_path
 * per invocation (batch_size is always 1), that's unambiguous, so those
 * files still go to the one spec instead of being dropped.
 */
export function collectDetoxScreenshots(
  artifactsDir: string,
  results: SpecResult[],
): Record<string, string[]> {
  const screenshotsBySpec: Record<string, string[]> = {};
  if (!fs.existsSync(artifactsDir)) return screenshotsBySpec;

  const fullTitleToSpec = new Map<string, string>();
  for (const r of results) {
    for (const tc of r.test_cases) fullTitleToSpec.set(tc.full_title, r.spec_path);
  }
  const soleSpecPath = results.length === 1 ? results[0]!.spec_path : null;

  const byParentFolder = new Map<string, string[]>();
  walkImagesByParentFolder(artifactsDir, byParentFolder);

  for (const [folderName, absPaths] of byParentFolder) {
    const specPath = fullTitleToSpec.get(folderName) ?? soleSpecPath;
    if (!specPath) continue;
    (screenshotsBySpec[specPath] ??= []).push(...absPaths);
  }
  return screenshotsBySpec;
}

function walkImagesByParentFolder(dir: string, out: Map<string, string[]>): void {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) {
      walkImagesByParentFolder(full, out);
    } else if (ent.isFile() && /\.(png|jpe?g)$/i.test(ent.name)) {
      const parent = path.basename(path.dirname(full));
      const arr = out.get(parent) ?? [];
      arr.push(full);
      out.set(parent, arr);
    }
  }
}

interface JestReport {
  testResults?: JestTestFile[];
}
interface JestTestFile {
  name?: string;
  assertionResults?: JestTestResult[];
}
interface JestTestResult {
  ancestorTitles?: string[];
  duration?: number | null;
  failureMessages?: string[];
  fullName?: string;
  status?: string;
  title?: string;
}

const RANKS: Record<TestStatus, number> = {
  skipped: 0,
  passed: 1,
  flaky: 2,
  interrupted: 3,
  timedOut: 4,
  failed: 5,
};

/** Maps Jest's status vocabulary to the TestStatus union. Ports ingest/detox.go's detoxStatus. */
export function detoxStatus(s: string | undefined): TestStatus {
  switch (s) {
    case "passed":
      return "passed";
    case "failed":
      return "failed";
    case "pending":
    case "skipped":
    case "todo":
      return "skipped";
    default:
      return "skipped";
  }
}

export function aggregateDetoxFile(file: JestTestFile, specPath: string): SpecResult {
  const cases: TestCaseResult[] = [];
  let totalMs = 0;
  let worst: TestStatus = "skipped";
  let ordinal = 0;

  for (const t of file.assertionResults ?? []) {
    const status = detoxStatus(t.status);
    const durationMs = typeof t.duration === "number" ? t.duration : 0;
    const tc: TestCaseResult = {
      title: t.title ?? "",
      full_title: t.fullName ?? t.title ?? "",
      status,
      retry_count: 0,
      duration_ms: durationMs,
      ordinal: ordinal++,
    };
    if (t.failureMessages && t.failureMessages.length > 0) {
      tc.error_message = t.failureMessages.join("\n");
      tc.error_stack = tc.error_message;
    }
    cases.push(tc);
    totalMs += durationMs;
    if (RANKS[status] > RANKS[worst]) worst = status;
  }

  if (cases.length === 0) {
    return { spec_path: specPath, status: "skipped", actual_duration_ms: 0, test_cases: [] };
  }
  const out: SpecResult = {
    spec_path: specPath,
    status: worst,
    actual_duration_ms: totalMs,
    test_cases: cases,
  };
  const firstFail = cases.find((c) => c.status === "failed");
  if (firstFail?.error_message) out.error_message = firstFail.error_message;
  if (firstFail?.error_stack) out.error_stack = firstFail.error_stack;
  return out;
}
