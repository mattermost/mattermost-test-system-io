/**
 * W10 self-check — the loop runs the mechanical ban checker over its OWN diff
 * in ENFORCING mode before pushing. The loop must never fight its own rules;
 * a ban hit stops the attempt (the violation is the diagnosis, not a
 * surprise for the reviewer).
 */
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import { fileURLToPath } from "node:url";

export interface BanResult {
  passed: boolean;
  violations: Array<{ rule: string; file: string; message: string }>;
}

/** Vendored copy of scripts/lib/stabilization-ban-checker.js (self-containment:
 * an action directory is downloaded without the rest of the repo). */
function vendoredChecker(): string {
  return fileURLToPath(new URL("./vendor/ban-checker.js", import.meta.url));
}

export function checkOwnDiff(workspace: string): BanResult {
  let diff: string;
  try {
    diff = execFileSync("git", ["-C", workspace, "diff", "--unified=3"], { encoding: "utf8" });
  } catch (err) {
    throw new Error(`self-check git diff failed: ${(err as Error).message}`);
  }
  if (diff.trim() === "") return { passed: true, violations: [] };
  const checker = vendoredChecker();
  if (!fs.existsSync(checker)) {
    throw new Error("vendored ban checker missing — self-containment invariant broken");
  }
  const raw = execFileSync(process.execPath, [checker, "-", "--report-only"], {
    input: diff,
    encoding: "utf8",
  });
  // The CLI prints human lines; parse the rule names out of them. The checker
  // is the source of truth; this parse only surfaces what it found.
  const violations: BanResult["violations"] = [];
  for (const line of raw.split("\n")) {
    const m = /^  (.+): \[(.+)\] (.+)$/.exec(line);
    if (m) violations.push({ file: m[1]!, rule: m[2]!, message: m[3]! });
  }
  // Enforcing for our own diff regardless of the CLI's report-only mode above:
  // we asked for report-only only so the CLI never exits non-zero before we
  // can print the full list ourselves.
  return { passed: violations.length === 0, violations };
}
