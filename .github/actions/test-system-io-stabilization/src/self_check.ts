/**
 * W10 self-check — the loop runs the mechanical ban checker over its OWN diff
 * in ENFORCING mode before pushing. The loop must never fight its own rules;
 * a ban hit stops the attempt (the violation is the diagnosis, not a
 * surprise for the reviewer).
 *
 * B6 fix: the checker is IMPORTED, not located at runtime. The shipped
 * artifact is `dist/index.js` (tsup bundle) — a `new URL(..., import.meta.url)`
 * lookup cannot resolve anything there, and the CJS shim has no
 * `import.meta.url` at all. Importing means tsup bundles the checker INTO
 * dist and this runs identically in src (tests) and dist (CI).
 */
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
// Vendored CJS checker (copy of scripts/lib/stabilization-ban-checker.js).
// Imported so tsup BUNDLES it into dist (B6: a runtime file lookup cannot
// resolve inside the bundle). allowJs types it from its own source.
import banChecker from "./vendor/ban-checker.js";
const checkStabilizationDiff = banChecker.checkStabilizationDiff;

export interface BanResult {
  passed: boolean;
  violations: Array<{ rule: string; file: string; message: string }>;
}

export function checkOwnDiff(workspace: string): BanResult {
  let diff: string;
  try {
    // M14 fix: check the STAGED diff. `git add` happens before this runs in
    // the loop flow, so untracked files (which plain `git diff` never sees)
    // are included exactly as they will be committed.
    diff = execFileSync("git", ["-C", workspace, "diff", "--cached", "--unified=3"], {
      encoding: "utf8",
    });
  } catch (err) {
    throw new Error(`self-check git diff failed: ${(err as Error).message}`);
  }
  if (diff.trim() === "") return { passed: true, violations: [] };
  const result = checkStabilizationDiff(diff);
  return { passed: result.passed, violations: result.violations };
}

/** Smoke guard for the bundling invariant: dist must contain the checker. */
export function assertBundledChecker(distDir: string): void {
  const dist = fs.readFileSync(`${distDir}/index.js`, "utf8");
  if (!dist.includes("ban-bare-wait")) {
    throw new Error("dist/index.js does not contain the ban checker — B6 regression");
  }
}
