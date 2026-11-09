/**
 * Runs a spec runner with an optional kill deadline. The child gets its own
 * process group so the kill reaches the framework's workers too (`npx` alone
 * would leave `playwright test` running after the signal).
 *
 * With no timeout this behaves like spawnSync(stdio: inherit) and stays in the
 * worker's own process group, so a runner cancel still reaches it. With a
 * timeout the child gets its own group, and when the timer fires the whole
 * group gets SIGKILL and `timedOut` is set. The caller can then report the unit
 * as interrupted and still reach its upload step.
 */

import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import type { InvocationRecord, SpecResult } from "./types";

export interface SpawnOutcome {
  status: number | null;
  signal: NodeJS.Signals | null;
  error?: Error;
  timedOut: boolean;
}

export interface SpawnWithDeadlineOptions {
  cwd: string;
  env?: NodeJS.ProcessEnv;
  /** Kill the process group after this many ms. Undefined = no kill timer. */
  timeoutMs?: number;
}

/**
 * Spawns command with inherited stdio and resolves with its exit status, its signal, and whether
 * the kill timer (opts.timeoutMs) ended it. It never rejects: a spawn failure comes back as
 * outcome.error.
 */
export function spawnWithDeadline(
  command: string,
  args: string[],
  opts: SpawnWithDeadlineOptions,
): Promise<SpawnOutcome> {
  return new Promise((resolve) => {
    const child = spawn(command, args, {
      cwd: opts.cwd,
      env: opts.env,
      stdio: "inherit",
      detached: opts.timeoutMs !== undefined && process.platform !== "win32",
    });

    let settled = false;
    let timedOut = false;
    let timer: NodeJS.Timeout | undefined;

    /** Settles the promise once, clearing the kill timer. */
    const finish = (outcome: SpawnOutcome) => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      resolve(outcome);
    };

    /**
     * SIGKILLs the child's whole process group (the child alone on Windows), so the framework's own
     * workers stop too.
     */
    const killGroup = () => {
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

    if (opts.timeoutMs !== undefined) {
      timer = setTimeout(
        () => {
          timedOut = true;
          killGroup();
        },
        Math.max(0, opts.timeoutMs),
      );
    }

    child.on("error", (error) => finish({ status: null, signal: null, error, timedOut }));
    child.on("close", (status, signal) => finish({ status, signal, timedOut }));
  });
}

/** Why a killed lease's specs did not finish. E2E triage reads a lease with this message as unfinished, not failed. */
export const DEADLINE_MESSAGE =
  "killed at worker deadline (deadline-minutes); spec did not finish in this worker";

/**
 * The result for every spec of a lease whose run was killed at the worker
 * deadline. Each spec is interrupted: the server accepts a /complete only when
 * it covers every spec in the lease, and interrupted is a retest-eligible
 * failure, so another worker re-runs the spec.
 */
export function deadlineInterruptedResults(specPaths: string[], durationMs: number): SpecResult[] {
  return specPaths.map((spec_path) => ({
    spec_path,
    status: "interrupted",
    actual_duration_ms: durationMs,
    test_cases: [],
    error_message: DEADLINE_MESSAGE,
  }));
}

/**
 * Writes the report a lease killed at the worker deadline leaves behind: one in the framework's own format
 * (Playwright's JSON reporter, or a mochawesome report for Cypress) that holds no test, so the worker still
 * uploads its shard and the report group, which waits for every worker's shard, can complete.
 *
 * It holds no test on purpose. The killed specs' interrupted results go to the orchestration, which hands
 * them to another worker; that worker's shard carries their real results. An interrupted placeholder here
 * would stay in this shard's report after the retest passed, and read as a failure. Test System IO ingests a
 * report without suites as complete (apps/server/internal/api/reports/stateless.go, finalizeResults).
 */
export function writeDeadlineReport(opts: {
  framework: "playwright" | "cypress";
  jsonPath: string;
  specPaths: string[];
  startedAt: number;
  endedAt: number;
}): void {
  const start = new Date(opts.startedAt).toISOString();
  const end = new Date(opts.endedAt).toISOString();
  const duration = Math.max(0, opts.endedAt - opts.startedAt);
  // Not read by the server: says why the report is empty to a person who opens it.
  const killedAtDeadline = { reason: DEADLINE_MESSAGE, specs: opts.specPaths };
  const report =
    opts.framework === "playwright"
      ? {
          config: {},
          suites: [],
          errors: [],
          stats: { startTime: start, duration, expected: 0, unexpected: 0, flaky: 0, skipped: 0 },
          killedAtDeadline,
        }
      : {
          stats: { suites: 0, tests: 0, passes: 0, pending: 0, failures: 0, start, end, duration },
          results: [],
          killedAtDeadline,
        };
  fs.mkdirSync(path.dirname(opts.jsonPath), { recursive: true });
  fs.writeFileSync(opts.jsonPath, JSON.stringify(report));
}

/**
 * What a lease killed at the worker deadline hands back: every spec interrupted (deadlineInterruptedResults),
 * and an invocation whose report is the empty one writeDeadlineReport leaves in the lease's archive dir, so the
 * worker's shard uploads even when this was its only lease.
 */
export function deadlineLease(opts: {
  framework: "playwright" | "cypress";
  iterDir: string;
  specPaths: string[];
  startedAt: number;
  endedAt: number;
}): { invocation: InvocationRecord; results: SpecResult[] } {
  const jsonPath =
    opts.framework === "playwright"
      ? path.join(opts.iterDir, "results", "reporter", "results.json")
      : path.join(opts.iterDir, "killed-at-deadline.json");
  writeDeadlineReport({ ...opts, jsonPath });
  return {
    invocation: {
      specPath: opts.specPaths[0]!,
      iterDir: opts.iterDir,
      playwrightJsonPath: jsonPath,
    },
    results: deadlineInterruptedResults(opts.specPaths, Math.max(0, opts.endedAt - opts.startedAt)),
  };
}
