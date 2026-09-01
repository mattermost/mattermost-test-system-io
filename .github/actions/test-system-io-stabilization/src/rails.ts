/**
 * W14 rails — the loop's blast-radius limits. Every write passes through
 * guardEditable; every PR passes the attempt caps.
 */
import * as fs from "node:fs";
import * as path from "node:path";

/**
 * Only e2e-tests/** may be written — LEXICALLY and PHYSICALLY (M13).
 *
 * Lexical: the spelling must resolve inside the workspace and under
 * e2e-tests/ (catches ../, absolute paths, sibling prefixes like
 * e2e-tests-evil/).
 *
 * Physical: the deepest EXISTING ancestor of the target is realpath'd and
 * must ALSO sit under e2e-tests/ of the real workspace root — a symlink
 * placed inside e2e-tests/ that points at ../secret passes the lexical
 * check and is caught here.
 */
export function guardEditable(workspace: string, target: string): string {
  const rootLexical = path.resolve(workspace);
  const resolved = path.resolve(workspace, target);

  if (resolved !== rootLexical && !resolved.startsWith(rootLexical + path.sep)) {
    throw new Error(`path escapes workspace: ${target}`);
  }
  const rel = path.relative(rootLexical, resolved);
  if (!rel.startsWith("e2e-tests/")) {
    throw new Error(`stabilization loop may only edit e2e-tests/** — rejected: ${rel}`);
  }

  const rootReal = fs.realpathSync(workspace);
  // R2-3: lstat, NOT existsSync — a DANGLING symlink stats as nonexistent
  // through the link, so the old walk skipped past it to a legit parent and
  // the write went through the link. A dangling link must count as EXISTING
  // here so realpath throws on it.
  let anc = resolved;
  while (!fs.lstatSync(anc, { throwIfNoEntry: false })) {
    anc = path.dirname(anc);
  }
  let ancReal: string;
  try {
    ancReal = fs.realpathSync(anc);
  } catch {
    // realpath through a dangling symlink — the exact R2-3 attack.
    throw new Error(`dangling symlink on the write path: ${target}`);
  }
  if (ancReal !== rootReal && !ancReal.startsWith(rootReal + path.sep)) {
    throw new Error(`path escapes workspace (symlink?): ${target}`);
  }
  const relReal = path.relative(rootReal, ancReal);
  // The editable root itself ("e2e-tests") is fine — new files land in it.
  if (relReal !== "" && relReal !== "e2e-tests" && !relReal.startsWith("e2e-tests" + path.sep)) {
    throw new Error(`path resolves outside e2e-tests/ (symlink?): ${target}`);
  }
  // The final component itself must not be a symlink (dangling or not).
  const finalLstat = fs.lstatSync(resolved, { throwIfNoEntry: false });
  if (finalLstat?.isSymbolicLink()) {
    throw new Error(`refusing to write through a symlink: ${target}`);
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
