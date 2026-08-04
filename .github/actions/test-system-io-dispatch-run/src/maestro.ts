/** Per-flow Maestro invocation + JUnit XML aggregation. Mirrors detox.ts's runUnit shape. */

import * as fs from "node:fs";
import * as path from "node:path";
import { spawn } from "node:child_process";
import * as core from "@actions/core";
import { XMLParser } from "fast-xml-parser";
import type { InvocationRecord, SpecResult, TestCaseResult, TestStatus } from "./types";

export interface RunUnitConfig {
  maestroDir: string;
  maestroDevice: string;
  maestroPlatform: string;
  // Forwarded as `--env KEY=VALUE` per entry. Maestro flows template
  // `${SITE_1_URL}`, `${TEST_USER_EMAIL}`, etc. — the CLI only resolves
  // those from explicit --env flags, not the invoking process's ambient
  // environment, so this is required whenever a flow references a variable.
  maestroEnv: Record<string, string>;
  // Cap on a single `maestro test` invocation. Keep below the run's
  // lease-timeout-ms so the worker can /complete before the server reclaims.
  maestroTimeoutMs: number;
  workerArtifacts: string;
}

export interface MaestroUnitResult {
  invocation: InvocationRecord;
  results: SpecResult[];
  // Screenshot absolute paths, grouped by spec_path.
  screenshotsBySpec: Record<string, string[]>;
}

interface SpawnOutcome {
  status: number | null;
  signal: NodeJS.Signals | null;
  error?: Error;
  timedOut: boolean;
}

// batch_size is always 1 for Maestro (one flow file per invocation, mirroring
// mattermost-mobile's run_ci_batches.sh loop), so specPaths has exactly one
// entry; only the first is used defensively if a caller ever leases more.
export async function runUnit(
  cfg: RunUnitConfig,
  iterationSeq: number,
  specPaths: string[],
): Promise<MaestroUnitResult> {
  const specPath = specPaths[0]!;
  if (specPaths.length > 1) {
    core.warning(
      `maestro runUnit leased ${specPaths.length} specs; only running the first (${specPath})`,
    );
  }

  const iterDir = path.join(cfg.workerArtifacts, `iter-${iterationSeq}`);
  fs.mkdirSync(iterDir, { recursive: true });

  const artifactsDir = path.join(iterDir, "artifacts");
  const junitOutputPath = path.join(iterDir, "maestro-batch.xml");

  const args = ["test"];
  if (cfg.maestroDevice) args.push("--device", cfg.maestroDevice);
  if (cfg.maestroPlatform) args.push("--platform", cfg.maestroPlatform);
  for (const [key, value] of Object.entries(cfg.maestroEnv)) {
    args.push("--env", `${key}=${value}`);
  }
  args.push(
    "--format",
    "junit",
    "--output",
    junitOutputPath,
    "--test-output-dir",
    artifactsDir,
    "--flatten-debug-output",
    specPath,
  );

  const startedAt = Date.now();
  const child = await spawnMaestro(args, cfg.maestroDir, cfg.maestroTimeoutMs);
  const durationMs = Date.now() - startedAt;
  core.info(
    `maestro exit ${child.status} in ${Math.round(durationMs / 1000)}s` +
      (child.timedOut ? " timedOut=true" : "") +
      (child.error ? ` error=${child.error.message}` : "") +
      (child.signal ? ` signal=${child.signal}` : ""),
  );

  let result: SpecResult;
  if (child.timedOut) {
    core.warning(
      `maestro timed out after ${cfg.maestroTimeoutMs}ms; returning interrupted for ${specPath}`,
    );
    result = {
      spec_path: specPath,
      status: "interrupted",
      actual_duration_ms: durationMs,
      test_cases: [],
    };
  } else if (child.error) {
    // Spawn failed to start Maestro (ENOENT, bad cwd, etc.) — not an interruption.
    result = {
      spec_path: specPath,
      status: "failed",
      actual_duration_ms: durationMs,
      test_cases: [],
      error_message: `maestro failed to start: ${child.error.message}`,
      error_stack: child.error.stack,
    };
  } else if (!fs.existsSync(junitOutputPath)) {
    core.warning(`maestro junit xml missing: ${junitOutputPath}`);
    result = { spec_path: specPath, status: "interrupted", actual_duration_ms: 0, test_cases: [] };
  } else {
    try {
      result = aggregateMaestroReport(fs.readFileSync(junitOutputPath, "utf8"), specPath);
    } catch (e) {
      core.warning(`maestro junit xml parse failure: ${(e as Error).message}`);
      result = {
        spec_path: specPath,
        status: "interrupted",
        actual_duration_ms: 0,
        test_cases: [],
      };
    }
  }

  // batch_size is always 1, so every screenshot under this invocation's
  // artifacts dir belongs to the single spec/test_case just run — unlike
  // Detox, no folder-name/full_title matching heuristic is needed.
  const screenshotsBySpec = collectMaestroScreenshots(artifactsDir, specPath);

  return {
    invocation: { specPath, iterDir, playwrightJsonPath: junitOutputPath },
    results: [result],
    screenshotsBySpec,
  };
}

/** Runs Maestro and kills its process group when maestroTimeoutMs elapses. */
function spawnMaestro(args: string[], cwd: string, timeoutMs: number): Promise<SpawnOutcome> {
  return new Promise((resolve) => {
    const child = spawn("maestro", args, {
      cwd,
      stdio: "inherit",
      // Own process group so timeout can signal Maestro and its descendants.
      detached: process.platform !== "win32",
    });

    let settled = false;
    let timedOut = false;

    const finish = (outcome: SpawnOutcome) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(outcome);
    };

    const killTree = () => {
      if (child.pid == null) return;
      try {
        if (process.platform === "win32") {
          child.kill("SIGKILL");
        } else {
          process.kill(-child.pid, "SIGKILL");
        }
      } catch {
        try {
          child.kill("SIGKILL");
        } catch {
          // already exited
        }
      }
    };

    const timer = setTimeout(() => {
      timedOut = true;
      killTree();
    }, timeoutMs);

    child.on("error", (error) => {
      finish({ status: null, signal: null, error, timedOut });
    });
    child.on("close", (status, signal) => {
      finish({ status, signal, timedOut });
    });
  });
}

/**
 * Parses a `maestro-env` action input: one `KEY=VALUE` pair per line.
 * Blank lines and lines without `=` are ignored; a value may itself
 * contain `=` (only the first splits key from value).
 */
export function parseMaestroEnv(raw: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of raw.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const eq = trimmed.indexOf("=");
    if (eq === -1) continue;
    out[trimmed.slice(0, eq)] = trimmed.slice(eq + 1);
  }
  return out;
}

export function collectMaestroScreenshots(
  artifactsDir: string,
  specPath: string,
): Record<string, string[]> {
  if (!fs.existsSync(artifactsDir)) return {};
  const files: string[] = [];
  walkImages(artifactsDir, files);
  return files.length > 0 ? { [specPath]: files } : {};
}

function walkImages(dir: string, out: string[]): void {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) {
      walkImages(full, out);
    } else if (ent.isFile() && /\.(png|jpe?g)$/i.test(ent.name)) {
      out.push(full);
    }
  }
}

const RANKS: Record<TestStatus, number> = {
  skipped: 0,
  passed: 1,
  flaky: 2,
  interrupted: 3,
  timedOut: 4,
  failed: 5,
};

/** Maps Maestro's JUnit status attribute to the TestStatus union. */
export function maestroStatus(raw: string | undefined): TestStatus {
  switch ((raw ?? "").toUpperCase()) {
    case "SUCCESS":
    case "PASSED":
      return "passed";
    case "FAILED":
    case "ERROR":
      return "failed";
    case "SKIPPED":
    case "WARNING":
      return "skipped";
    // Canceled/stopped/in-progress FlowStatus values, plus unknown/missing.
    case "CANCELED":
    case "STOPPED":
    case "PENDING":
    case "PREPARING":
    case "INSTALLING":
    case "RUNNING":
    default:
      return "interrupted";
  }
}

interface MaestroJUnitTestCase {
  id?: string;
  name?: string;
  classname?: string;
  time?: string | number;
  status?: string;
  failure?: unknown;
  error?: unknown;
}

interface MaestroJUnitTestSuite {
  testcase?: MaestroJUnitTestCase | MaestroJUnitTestCase[];
}

/** Aggregates one `maestro test --format junit` invocation's output into a SpecResult. */
export function aggregateMaestroReport(xml: string, specPath: string): SpecResult {
  const parser = new XMLParser({ ignoreAttributes: false, attributeNamePrefix: "" });
  const parsed = parser.parse(xml) as {
    testsuites?: { testsuite?: MaestroJUnitTestSuite | MaestroJUnitTestSuite[] };
    testsuite?: MaestroJUnitTestSuite | MaestroJUnitTestSuite[];
  };
  const suites = toArray(parsed?.testsuites?.testsuite ?? parsed?.testsuite);

  const cases: TestCaseResult[] = [];
  let totalMs = 0;
  let ordinal = 0;
  for (const suite of suites) {
    for (const tc of toArray(suite?.testcase)) {
      const status = maestroStatus(tc?.status);
      const durationMs = parseDurationMs(tc?.time);
      const tcResult: TestCaseResult = {
        title: String(tc?.name ?? tc?.id ?? ""),
        full_title: String(tc?.classname ?? tc?.name ?? tc?.id ?? ""),
        status,
        retry_count: 0,
        duration_ms: durationMs,
        ordinal: ordinal++,
      };
      const errorText = extractErrorText(tc?.failure ?? tc?.error);
      if (errorText) {
        tcResult.error_message = errorText;
        tcResult.error_stack = errorText;
      }
      cases.push(tcResult);
      totalMs += durationMs;
    }
  }

  if (cases.length === 0) {
    return { spec_path: specPath, status: "skipped", actual_duration_ms: 0, test_cases: [] };
  }

  let worst: TestStatus = "skipped";
  for (const c of cases) if (RANKS[c.status] > RANKS[worst]) worst = c.status;

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

function parseDurationMs(raw: string | number | undefined): number {
  const seconds = typeof raw === "number" ? raw : Number.parseFloat(raw ?? "");
  return Number.isFinite(seconds) ? Math.round(seconds * 1000) : 0;
}

/** Reads a JUnit <failure>/<error> element's message attribute or text content. */
function extractErrorText(node: unknown): string | undefined {
  if (node == null) return undefined;
  if (typeof node === "object") {
    const obj = node as { message?: unknown; "#text"?: unknown };
    const text = obj.message ?? obj["#text"];
    return text != null && String(text).length > 0 ? String(text) : undefined;
  }
  const s = String(node);
  return s.length > 0 ? s : undefined;
}

function toArray<T>(x: T | T[] | undefined): T[] {
  if (x === undefined || x === null) return [];
  return Array.isArray(x) ? x : [x];
}
