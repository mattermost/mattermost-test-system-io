/**
 * Playwright spec discovery driven by the consumer's playwright.config.
 *
 * Reads `testDir` and `testMatch` from `playwright.config.{ts,js,mjs,cjs}`
 * via static text parsing (no module evaluation, so we don't need to
 * resolve the consumer's full dep graph). Falls back to Playwright's
 * documented defaults when a value is missing or expressed as a runtime
 * computation.
 */

import * as fs from "node:fs";
import * as path from "node:path";

// Playwright's documented default testMatch is `**/*.@(spec|test).?(c|m)[jt]s?(x)`.
// Node's fs.globSync only supports the standard glob alphabet (no extended
// `@(...)` / `?(...)`), so we expand it into brace form.
const PLAYWRIGHT_DEFAULT_TEST_MATCH = ["**/*.{spec,test}.{ts,tsx,js,jsx,mjs,cjs,mts,cts}"];
const PLAYWRIGHT_DEFAULT_TEST_DIR = ".";

export interface PlaywrightSpecConfig {
  /** Resolved absolute path of the test root directory. */
  testDir: string;
  /** testMatch glob entries relative to testDir. */
  testMatch: string[];
}

/**
 * Walk the Playwright test root for files matching `testMatch` and
 * return paths relative to playwrightDir with `/` separators.
 *
 * `excludePaths` is applied as a final pass — used by the controller
 * to drop framework-internal files (e.g. test_setup.ts that runs as a
 * project dependency, not as a dispatched unit) without forcing the
 * consumer to encode that in their config.
 */
export function discoverPlaywrightSpecs(
  playwrightDir: string,
  excludePaths: string[] = [],
): string[] {
  const cfg = readPlaywrightSpecConfig(playwrightDir);
  const seen = new Set<string>();

  for (const pattern of cfg.testMatch) {
    for (const match of fs.globSync(pattern, { cwd: cfg.testDir })) {
      const abs = path.resolve(cfg.testDir, match);
      try {
        if (!fs.statSync(abs).isFile()) continue;
      } catch {
        continue;
      }
      const rel = path.relative(playwrightDir, abs).split(path.sep).join("/");
      if (seen.has(rel)) continue;
      seen.add(rel);
    }
  }

  const isExcluded = (p: string): boolean =>
    excludePaths.some((ex) => p === ex || p.endsWith("/" + ex) || p.startsWith(ex));
  return [...seen].filter((p) => !isExcluded(p)).sort();
}

/**
 * Parse playwright.config for top-level `testDir` (default `"."`) and
 * `testMatch` (default Playwright's `**\/*.@(spec|test).@(c|m)?[jt]s?(x)`).
 *
 * Per-project `testMatch` overrides aren't read — those gate which specs
 * a `--project=name` invocation runs, not which files exist on disk.
 * Workers pass `--project=` themselves at run time.
 *
 * Regex `testMatch` (e.g. `/test_setup\.ts/`) is unsupported by the glob
 * matcher; falls back to the default. Callers handle file-name exclusions
 * via the `excludePaths` arg to discoverPlaywrightSpecs instead.
 */
export function readPlaywrightSpecConfig(playwrightDir: string): PlaywrightSpecConfig {
  let testDirRel = PLAYWRIGHT_DEFAULT_TEST_DIR;
  let testMatch: string[] = [...PLAYWRIGHT_DEFAULT_TEST_MATCH];

  for (const name of [
    "playwright.config.ts",
    "playwright.config.js",
    "playwright.config.mjs",
    "playwright.config.cjs",
  ]) {
    const cfgPath = path.join(playwrightDir, name);
    if (!fs.existsSync(cfgPath)) continue;
    const raw = fs.readFileSync(cfgPath, "utf8");
    // Strip nested blocks that carry their own testMatch (per-project
    // overrides under `projects: [...]`) or noise that would mislead the
    // top-level scan (`use: { ... }`).
    const text = stripBlocks(raw, [
      [/\bprojects\s*:\s*\[/, "[", "]"],
      [/\buse\s*:\s*\{/, "{", "}"],
    ]);
    const dirVal = extractStringProp(text, "testDir");
    if (dirVal) testDirRel = dirVal;
    const matchVal = extractStringOrArrayProp(text, "testMatch");
    if (matchVal.length > 0) testMatch = matchVal;
    break;
  }

  return {
    testDir: path.resolve(playwrightDir, testDirRel),
    testMatch,
  };
}

/**
 * Replace each matching nested block (header regex + opener char +
 * matching closer at the same nesting level) with a single space, so
 * subsequent property scans don't see values that belong to the inner
 * block. Multiple passes are run for each opener/closer pair.
 */
function stripBlocks(
  text: string,
  rules: ReadonlyArray<readonly [RegExp, "{" | "[", "}" | "]"]>,
): string {
  let out = text;
  for (const [headerRe, opener, closer] of rules) {
    while (true) {
      const m = headerRe.exec(out);
      if (!m) break;
      const start = m.index + m[0].length - 1; // index of opener
      let depth = 1;
      let i = start + 1;
      while (i < out.length && depth > 0) {
        const ch = out[i];
        if (ch === opener) depth++;
        else if (ch === closer) depth--;
        i++;
      }
      if (depth !== 0) break; // unbalanced — bail to avoid infinite loop
      out = out.slice(0, m.index) + " " + out.slice(i);
    }
  }
  return out;
}

function extractStringProp(text: string, key: string): string | null {
  const re = new RegExp(`\\b${key}\\s*:\\s*['"\`]([^'"\`]+)['"\`]`);
  const m = re.exec(text);
  return m ? m[1]! : null;
}

function extractStringOrArrayProp(text: string, key: string): string[] {
  const arrayRe = new RegExp(`\\b${key}\\s*:\\s*\\[([^\\]]*)\\]`);
  const arr = arrayRe.exec(text);
  if (arr) {
    const inner = arr[1] ?? "";
    return [...inner.matchAll(/['"`]([^'"`]+)['"`]/g)].map((m) => m[1]!);
  }
  const stringRe = new RegExp(`\\b${key}\\s*:\\s*['"\`]([^'"\`]+)['"\`]`);
  const str = stringRe.exec(text);
  if (str) return [str[1]!];
  return [];
}
