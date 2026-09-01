/**
 * W14 rails — the loop's blast-radius limits. Every write passes through
 * guardEditable; every PR passes the attempt caps.
 */
import * as path from "node:path";

/** Only e2e-tests/** may be written. Everything else — including product
 * code, CI config, and this action itself — is rejected. */
export function guardEditable(workspace: string, target: string): string {
  const resolved = path.resolve(workspace, target);
  const root = path.resolve(workspace);
  if (resolved !== root && !resolved.startsWith(root + path.sep)) {
    throw new Error(`path escapes workspace: ${target}`);
  }
  const rel = path.relative(root, resolved);
  if (!rel.startsWith("e2e-tests/")) {
    throw new Error(`stabilization loop may only edit e2e-tests/** — rejected: ${rel}`);
  }
  return resolved;
}

export interface RailsConfig {
  maxAttemptsPerTest: number;
  concurrency: number;
}

export const HARD_CONCURRENCY_CAP = 5;

export function clampConcurrency(requested: number): number {
  if (!Number.isFinite(requested) || requested < 1) return 1;
  return Math.min(Math.floor(requested), HARD_CONCURRENCY_CAP);
}

/** Attempts used by prior loop PRs for this test (from PR search). */
export function attemptsExhausted(
  used: number,
  maxAttemptsPerTest: number,
): boolean {
  return used >= maxAttemptsPerTest;
}
