/** Per-flow / per-scenario Maestro invocation + JUnit XML aggregation. */

import * as fs from "node:fs";
import * as path from "node:path";
import { spawn } from "node:child_process";
import * as core from "@actions/core";
import { XMLParser } from "fast-xml-parser";
import type { InvocationRecord, SpecResult, TestCaseResult, TestStatus } from "./types";

const MAESTRO_SCENARIO_RE = /\.scenario\.ya?ml$/;

export interface RunUnitConfig {
  maestroDir: string;
  /** Consumer repo root — scenario scripts are invoked with this cwd. */
  repoDir: string;
  /** Device A UDID / emulator serial (single-device flows + scenario DEVICE_A_UDID). */
  maestroDevice: string;
  /** Device B for multi-device scenarios (DEVICE_B_UDID). Empty skips scenarios that need it. */
  maestroDeviceB: string;
  maestroPlatform: string;
  // Forwarded as `--env KEY=VALUE` per entry for single-device `maestro test`.
  // For scenarios, also exported into the script's process environment.
  maestroEnv: Record<string, string>;
  // Cap on a single `maestro test` / scenario-script invocation. Keep below
  // the run's lease-timeout-ms so the worker can /complete before reclaim.
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

// batch_size is always 1 for Maestro (one flow/scenario per invocation), so
// specPaths has exactly one entry; only the first is used defensively.
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

  if (MAESTRO_SCENARIO_RE.test(path.basename(specPath))) {
    return runScenario(cfg, iterationSeq, specPath);
  }
  return runFlow(cfg, iterationSeq, specPath);
}

async function runFlow(
  cfg: RunUnitConfig,
  iterationSeq: number,
  specPath: string,
): Promise<MaestroUnitResult> {
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
  const child = await spawnCommand("maestro", args, cfg.maestroDir, cfg.maestroTimeoutMs);
  const durationMs = Date.now() - startedAt;
  core.info(
    `maestro exit ${child.status} in ${Math.round(durationMs / 1000)}s` +
      (child.timedOut ? " timedOut=true" : "") +
      (child.error ? ` error=${child.error.message}` : "") +
      (child.signal ? ` signal=${child.signal}` : ""),
  );

  const result = outcomeToSpecResult(
    child,
    durationMs,
    junitOutputPath,
    specPath,
    cfg.maestroTimeoutMs,
  );
  const screenshotsBySpec = collectMaestroScreenshots(artifactsDir, specPath);

  return {
    invocation: { specPath, iterDir, playwrightJsonPath: junitOutputPath },
    results: [result],
    screenshotsBySpec,
  };
}

async function runScenario(
  cfg: RunUnitConfig,
  iterationSeq: number,
  specPath: string,
): Promise<MaestroUnitResult> {
  const iterDir = path.join(cfg.workerArtifacts, `iter-${iterationSeq}`);
  fs.mkdirSync(iterDir, { recursive: true });
  const artifactsDir = path.join(iterDir, "artifacts");
  fs.mkdirSync(artifactsDir, { recursive: true });
  const junitOutputPath = path.join(iterDir, "maestro-batch.xml");

  const scenarioAbs = path.join(cfg.maestroDir, specPath);
  let scriptRel: string;
  try {
    scriptRel = parseMaestroScenarioScript(fs.readFileSync(scenarioAbs, "utf8"));
  } catch (e) {
    const result: SpecResult = {
      spec_path: specPath,
      status: "failed",
      actual_duration_ms: 0,
      test_cases: [],
      error_message: `failed to parse scenario ${specPath}: ${(e as Error).message}`,
    };
    return {
      invocation: { specPath, iterDir, playwrightJsonPath: junitOutputPath },
      results: [result],
      screenshotsBySpec: {},
    };
  }

  if (!cfg.maestroDevice || !cfg.maestroDeviceB) {
    const result: SpecResult = {
      spec_path: specPath,
      status: "failed",
      actual_duration_ms: 0,
      test_cases: [],
      error_message:
        `scenario ${specPath} requires maestro-device (DEVICE_A) and maestro-device-b (DEVICE_B); ` +
        `got device=${cfg.maestroDevice || "(empty)"} device-b=${cfg.maestroDeviceB || "(empty)"}`,
    };
    return {
      invocation: { specPath, iterDir, playwrightJsonPath: junitOutputPath },
      results: [result],
      screenshotsBySpec: {},
    };
  }

  const scriptAbs = path.isAbsolute(scriptRel) ? scriptRel : path.resolve(cfg.repoDir, scriptRel);
  if (!fs.existsSync(scriptAbs)) {
    const result: SpecResult = {
      spec_path: specPath,
      status: "failed",
      actual_duration_ms: 0,
      test_cases: [],
      error_message: `scenario script not found: ${scriptAbs} (from ${specPath})`,
    };
    return {
      invocation: { specPath, iterDir, playwrightJsonPath: junitOutputPath },
      results: [result],
      screenshotsBySpec: {},
    };
  }

  const env: NodeJS.ProcessEnv = {
    ...process.env,
    ...cfg.maestroEnv,
    DEVICE_A_UDID: cfg.maestroDevice,
    DEVICE_B_UDID: cfg.maestroDeviceB,
  };
  if (cfg.maestroPlatform) {
    env.MAESTRO_PLATFORM = cfg.maestroPlatform;
  }

  core.info(
    `maestro scenario ${specPath} → ${scriptRel} ` +
      `(DEVICE_A=${cfg.maestroDevice} DEVICE_B=${cfg.maestroDeviceB})`,
  );

  // Drop leftover maestro-*.xml from prior scenarios in this worker so
  // aggregation does not attribute stale Device A/B reports to this unit.
  clearScenarioJUnitReports(cfg.repoDir);

  const startedAt = Date.now();
  const child = await spawnCommand(scriptAbs, [], cfg.repoDir, cfg.maestroTimeoutMs, env);
  const durationMs = Date.now() - startedAt;
  core.info(
    `scenario script exit ${child.status} in ${Math.round(durationMs / 1000)}s` +
      (child.timedOut ? " timedOut=true" : "") +
      (child.error ? ` error=${child.error.message}` : "") +
      (child.signal ? ` signal=${child.signal}` : ""),
  );

  // Orchestrator scripts write JUnit under repo `build/` or `detox/build/`.
  const reportXmls = collectScenarioJUnitReports(cfg.repoDir, startedAt);
  let result: SpecResult;
  if (child.timedOut) {
    core.warning(
      `scenario timed out after ${cfg.maestroTimeoutMs}ms; returning interrupted for ${specPath}`,
    );
    result = {
      spec_path: specPath,
      status: "interrupted",
      actual_duration_ms: durationMs,
      test_cases: [],
    };
  } else if (child.error) {
    result = {
      spec_path: specPath,
      status: "failed",
      actual_duration_ms: durationMs,
      test_cases: [],
      error_message: `scenario script failed to start: ${child.error.message}`,
      error_stack: child.error.stack,
    };
  } else if (reportXmls.length > 0) {
    result = aggregateScenarioReports(reportXmls, specPath, durationMs, child.status);
    // Persist a combined copy for shard upload when present.
    try {
      fs.writeFileSync(
        junitOutputPath,
        reportXmls.map((p) => fs.readFileSync(p, "utf8")).join("\n"),
      );
    } catch {
      // non-fatal — complete still has the SpecResult
    }
  } else if (child.status === 0) {
    result = {
      spec_path: specPath,
      status: "passed",
      actual_duration_ms: durationMs,
      test_cases: [
        {
          title: path.basename(specPath),
          full_title: specPath,
          status: "passed",
          retry_count: 0,
          duration_ms: durationMs,
          ordinal: 0,
        },
      ],
    };
  } else {
    result = {
      spec_path: specPath,
      status: "failed",
      actual_duration_ms: durationMs,
      test_cases: [
        {
          title: path.basename(specPath),
          full_title: specPath,
          status: "failed",
          retry_count: 0,
          duration_ms: durationMs,
          ordinal: 0,
          error_message: `scenario script exited with code ${child.status}`,
        },
      ],
      error_message: `scenario script exited with code ${child.status}`,
    };
  }

  // Prefer screenshots staged under iter artifacts; also scoop common build dirs.
  const screenshots = [
    ...Object.values(collectMaestroScreenshots(artifactsDir, specPath)).flat(),
    ...findImagesUnder(path.join(cfg.repoDir, "build")),
    ...findImagesUnder(path.join(cfg.repoDir, "detox", "build")),
  ];
  const screenshotsBySpec = screenshots.length > 0 ? { [specPath]: unique(screenshots) } : {};

  return {
    invocation: { specPath, iterDir, playwrightJsonPath: junitOutputPath },
    results: [result],
    screenshotsBySpec,
  };
}

function outcomeToSpecResult(
  child: SpawnOutcome,
  durationMs: number,
  junitOutputPath: string,
  specPath: string,
  timeoutMs: number,
): SpecResult {
  if (child.timedOut) {
    core.warning(`maestro timed out after ${timeoutMs}ms; returning interrupted for ${specPath}`);
    return {
      spec_path: specPath,
      status: "interrupted",
      actual_duration_ms: durationMs,
      test_cases: [],
    };
  }
  if (child.error) {
    return {
      spec_path: specPath,
      status: "failed",
      actual_duration_ms: durationMs,
      test_cases: [],
      error_message: `maestro failed to start: ${child.error.message}`,
      error_stack: child.error.stack,
    };
  }
  if (!fs.existsSync(junitOutputPath)) {
    core.warning(`maestro junit xml missing: ${junitOutputPath}`);
    return { spec_path: specPath, status: "interrupted", actual_duration_ms: 0, test_cases: [] };
  }
  try {
    return aggregateMaestroReport(fs.readFileSync(junitOutputPath, "utf8"), specPath);
  } catch (e) {
    core.warning(`maestro junit xml parse failure: ${(e as Error).message}`);
    return {
      spec_path: specPath,
      status: "interrupted",
      actual_duration_ms: 0,
      test_cases: [],
    };
  }
}

/** Extracts the `script:` value from a `*.scenario.yml` manifest. */
export function parseMaestroScenarioScript(text: string): string {
  const m = /^script:\s*(.+?)\s*$/m.exec(text);
  if (!m) {
    throw new Error('scenario missing required "script:" field');
  }
  let script = m[1]!.trim();
  if (
    (script.startsWith('"') && script.endsWith('"')) ||
    (script.startsWith("'") && script.endsWith("'"))
  ) {
    script = script.slice(1, -1);
  }
  if (!script) {
    throw new Error('scenario "script:" field is empty');
  }
  return script;
}

function scenarioReportDirs(repoDir: string): string[] {
  return [path.join(repoDir, "build"), path.join(repoDir, "detox", "build")];
}

function clearScenarioJUnitReports(repoDir: string): void {
  for (const dir of scenarioReportDirs(repoDir)) {
    if (!fs.existsSync(dir)) continue;
    for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
      if (ent.isFile() && /maestro.*\.xml$/i.test(ent.name)) {
        try {
          fs.unlinkSync(path.join(dir, ent.name));
        } catch {
          // best-effort
        }
      }
    }
  }
}

/** Collects maestro-*.xml reports written at or after startedAtMs. */
function collectScenarioJUnitReports(repoDir: string, startedAtMs: number): string[] {
  const out: string[] = [];
  for (const dir of scenarioReportDirs(repoDir)) {
    if (!fs.existsSync(dir)) continue;
    for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
      if (!ent.isFile() || !/maestro.*\.xml$/i.test(ent.name)) continue;
      const full = path.join(dir, ent.name);
      try {
        if (fs.statSync(full).mtimeMs + 1000 < startedAtMs) continue;
      } catch {
        continue;
      }
      out.push(full);
    }
  }
  return out.sort();
}

function aggregateScenarioReports(
  reportPaths: string[],
  specPath: string,
  wallDurationMs: number,
  exitStatus: number | null,
): SpecResult {
  const cases: TestCaseResult[] = [];
  let ordinal = 0;
  for (const reportPath of reportPaths) {
    let parsed: SpecResult;
    try {
      parsed = aggregateMaestroReport(fs.readFileSync(reportPath, "utf8"), specPath);
    } catch (e) {
      core.warning(`scenario junit parse failure (${reportPath}): ${(e as Error).message}`);
      continue;
    }
    for (const tc of parsed.test_cases) {
      cases.push({ ...tc, ordinal: ordinal++ });
    }
  }

  if (cases.length === 0) {
    if (exitStatus === 0) {
      return {
        spec_path: specPath,
        status: "passed",
        actual_duration_ms: wallDurationMs,
        test_cases: [],
      };
    }
    return {
      spec_path: specPath,
      status: "failed",
      actual_duration_ms: wallDurationMs,
      test_cases: [],
      error_message: `scenario script exited with code ${exitStatus} (no parsable junit)`,
    };
  }

  let worst: TestStatus = "skipped";
  let totalMs = 0;
  for (const c of cases) {
    if (RANKS[c.status] > RANKS[worst]) worst = c.status;
    totalMs += c.duration_ms;
  }
  // Non-zero exit overrides an all-green JUnit parse (script may fail after
  // writing partial reports).
  if (exitStatus !== 0 && exitStatus != null && worst === "passed") {
    worst = "failed";
  }

  const out: SpecResult = {
    spec_path: specPath,
    status: worst,
    actual_duration_ms: totalMs > 0 ? totalMs : wallDurationMs,
    test_cases: cases,
  };
  const firstFail = cases.find((c) => c.status === "failed");
  if (firstFail?.error_message) out.error_message = firstFail.error_message;
  if (firstFail?.error_stack) out.error_stack = firstFail.error_stack;
  else if (exitStatus !== 0 && exitStatus != null && !out.error_message) {
    out.error_message = `scenario script exited with code ${exitStatus}`;
  }
  return out;
}

/** Runs a command and kills its process group when timeoutMs elapses. */
function spawnCommand(
  command: string,
  args: string[],
  cwd: string,
  timeoutMs: number,
  env?: NodeJS.ProcessEnv,
): Promise<SpawnOutcome> {
  return new Promise((resolve) => {
    const child = spawn(command, args, {
      cwd,
      env,
      stdio: "inherit",
      // Own process group so timeout can signal the process and its descendants.
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

function findImagesUnder(dir: string): string[] {
  if (!fs.existsSync(dir)) return [];
  const files: string[] = [];
  walkImages(dir, files);
  return files;
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

function unique(paths: string[]): string[] {
  return [...new Set(paths)];
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
