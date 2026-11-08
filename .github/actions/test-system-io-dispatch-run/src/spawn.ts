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
import type { SpecResult } from "./types";

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

    const finish = (outcome: SpawnOutcome) => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      resolve(outcome);
    };

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
    error_message:
      "killed at worker deadline (deadline-minutes); spec did not finish in this worker",
  }));
}
