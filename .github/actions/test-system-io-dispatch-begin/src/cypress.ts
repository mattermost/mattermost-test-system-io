/**
 * Cypress spec discovery + filter pipeline for dispatch-begin.
 *
 * Mattermost cypress specs annotate themselves with two header tags:
 *
 *   // Stage: @prod
 *   // Group: @channels @bot_accounts
 *
 * `discoverCypressSpecs` reads the consumer's cypress.config to locate
 * spec files (specPattern + excludeSpecPattern under `e2e: { ... }`),
 * parses each file's metadata, then applies the caller-supplied
 * filters in a fixed order: stage drop → include-group drop →
 * exclude-group drop → sort-first/sort-last partition. The result is
 * a list of `spec_path` strings (relative to cypressDir,
 * forward-slash) ready to feed `/api/v1/orchestration/begin`.
 */

import * as fs from "node:fs";
import * as path from "node:path";

export interface CypressFilters {
  /** Specs kept only if their `// Stage:` line shares any tag. Empty array = no filter. */
  stage: string[];
  /** Specs kept only if their `// Group:` shares at least one tag. Empty array = no filter. */
  includeGroup: string[];
  /** Specs dropped if their `// Group:` shares any tag. Empty array = no filter. */
  excludeGroup: string[];
  /** Surviving specs whose `// Group:` shares any tag dispatch first. */
  sortFirst: string[];
  /** Mirror of sortFirst — dispatched last. A spec matching both goes to sortFirst. */
  sortLast: string[];
}

export interface SpecMetadata {
  stages: string[];
  groups: string[];
}

interface AnnotatedSpec {
  path: string;
  meta: SpecMetadata;
}

/** Cypress's documented default specPattern for e2e mode. */
const CYPRESS_DEFAULT_SPEC_PATTERN = ["**/*.cy.{js,jsx,ts,tsx}"];

/**
 * Spec patterns parsed from the consumer's cypress.config; honors
 * specPattern and excludeSpecPattern from the `e2e: { ... }` block.
 */
export interface CypressSpecConfig {
  /** specPattern entries — string globs relative to the cypress project dir. */
  include: string[];
  /** excludeSpecPattern entries — applied as a follow-up filter after the include glob. */
  exclude: string[];
}

/**
 * Walk the cypress project for spec files matching the consumer's
 * `specPattern`, parse each file's header tags, and apply the filter
 * pipeline. Returns paths relative to cypressDir with `/` separators.
 */
export function discoverCypressSpecs(cypressDir: string, filters: CypressFilters): string[] {
  const specCfg = readCypressSpecConfig(cypressDir);
  const seen = new Set<string>();
  const annotated: AnnotatedSpec[] = [];

  for (const pattern of specCfg.include) {
    for (const match of fs.globSync(pattern, { cwd: cypressDir, exclude: specCfg.exclude })) {
      const rel = match.split(path.sep).join("/");
      // Defensive dedup — guards against patterns that overlap (e.g.
      // a custom config listing both `tests/**/*_spec.ts` and `**/*_spec.ts`).
      if (seen.has(rel)) continue;
      const abs = path.join(cypressDir, rel);
      try {
        if (!fs.statSync(abs).isFile()) continue;
      } catch {
        continue;
      }
      seen.add(rel);
      annotated.push({ path: rel, meta: parseCypressMetadata(abs) });
    }
  }

  const filtered = annotated.filter((s) => passesFilters(s.meta, filters));
  return partitionBySort(filtered, filters).map((s) => s.path);
}

/**
 * Read `cypress.config.{ts,js,mjs,cjs}` from the consumer repo and
 * extract the `specPattern` / `excludeSpecPattern` entries from the
 * `e2e: { ... }` block. Falls back to Cypress's documented default
 * (`**\/*.cy.{js,jsx,ts,tsx}`) when no config is found or no pattern is
 * declared inside the e2e block.
 *
 * Static text parsing (not module evaluation) so we don't have to load
 * the consumer's full dependency graph at action time. Handles single-
 * string and array-of-strings forms — anything more complex (computed
 * values, spreads, env-driven expressions) falls through to the default.
 */
export function readCypressSpecConfig(cypressDir: string): CypressSpecConfig {
  for (const name of [
    "cypress.config.ts",
    "cypress.config.js",
    "cypress.config.mjs",
    "cypress.config.cjs",
  ]) {
    const cfgPath = path.join(cypressDir, name);
    if (!fs.existsSync(cfgPath)) continue;
    const text = fs.readFileSync(cfgPath, "utf8");
    const e2eBlock = extractObjectBlock(text, "e2e");
    if (e2eBlock === null) continue;
    const include = extractStringOrArrayProp(e2eBlock, "specPattern");
    const exclude = extractStringOrArrayProp(e2eBlock, "excludeSpecPattern");
    if (include.length > 0) return { include, exclude };
  }
  return { include: [...CYPRESS_DEFAULT_SPEC_PATTERN], exclude: [] };
}

/**
 * Extract the contents of an `<key>: { ... }` block from source text.
 * Handles nested braces. Returns null when the key isn't present.
 */
function extractObjectBlock(text: string, key: string): string | null {
  const re = new RegExp(`\\b${key}\\s*:\\s*\\{`);
  const m = re.exec(text);
  if (!m) return null;
  let depth = 1;
  let i = m.index + m[0].length;
  const start = i;
  while (i < text.length && depth > 0) {
    const ch = text[i];
    if (ch === "{") depth++;
    else if (ch === "}") depth--;
    if (depth === 0) return text.slice(start, i);
    i++;
  }
  return null;
}

/**
 * Read a property whose value is a string literal or an array of
 * string literals. Tolerates single, double, and backtick quotes.
 * Returns [] when the property is missing or its value is anything
 * else (computed expression, identifier, function call).
 */
function extractStringOrArrayProp(block: string, key: string): string[] {
  const arrayRe = new RegExp(`\\b${key}\\s*:\\s*\\[([^\\]]*)\\]`);
  const arr = arrayRe.exec(block);
  if (arr) {
    const inner = arr[1] ?? "";
    return [...inner.matchAll(/['"`]([^'"`]+)['"`]/g)].map((m) => m[1]!);
  }
  const stringRe = new RegExp(`\\b${key}\\s*:\\s*['"\`]([^'"\`]+)['"\`]`);
  const str = stringRe.exec(block);
  if (str) return [str[1]!];
  return [];
}

/**
 * Read the spec file header and extract `Stage:` / `Group:` tag lists.
 * Tolerant of: missing tags, malformed input, mixed-case keyword, blank
 * lines between header comments. Reads at most a 4 KB prefix — header
 * metadata always lives in the first few lines.
 */
export function parseCypressMetadata(absPath: string): SpecMetadata {
  let raw: string;
  try {
    const fd = fs.openSync(absPath, "r");
    const buf = Buffer.alloc(4096);
    const n = fs.readSync(fd, buf, 0, buf.length, 0);
    fs.closeSync(fd);
    raw = buf.subarray(0, n).toString("utf8");
  } catch {
    return { stages: [], groups: [] };
  }

  const stages: string[] = [];
  const groups: string[] = [];
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim();
    // Walk leading comment block; bail at the first non-comment, non-blank line.
    if (trimmed === "") continue;
    if (!trimmed.startsWith("//")) break;

    const stageMatch = /^\s*\/\/\s*stage:\s*(.+)$/i.exec(trimmed);
    if (stageMatch) {
      for (const tok of stageMatch[1]!.split(/\s+/)) {
        if (/^@\S+$/.test(tok)) stages.push(tok);
      }
      continue;
    }
    const groupMatch = /^\s*\/\/\s*group:\s*(.+)$/i.exec(trimmed);
    if (groupMatch) {
      for (const tok of groupMatch[1]!.split(/\s+/)) {
        if (/^@\S+$/.test(tok)) groups.push(tok);
      }
    }
  }
  return { stages, groups };
}

/**
 * Returns true when every active filter (non-empty array) admits the spec.
 * Empty filter array short-circuits to "no constraint."
 */
export function passesFilters(meta: SpecMetadata, filters: CypressFilters): boolean {
  if (filters.stage.length > 0 && !shareAny(meta.stages, filters.stage)) return false;
  if (filters.includeGroup.length > 0 && !shareAny(meta.groups, filters.includeGroup)) return false;
  if (filters.excludeGroup.length > 0 && shareAny(meta.groups, filters.excludeGroup)) return false;
  return true;
}

/**
 * Order specs into [sort-first matches] | [neither] | [sort-last matches].
 * A spec matching both lists goes to sort-first only — early surfacing of
 * a flagged failure is more useful than queue-tail demotion.
 */
export function partitionBySort(specs: AnnotatedSpec[], filters: CypressFilters): AnnotatedSpec[] {
  const first: AnnotatedSpec[] = [];
  const middle: AnnotatedSpec[] = [];
  const last: AnnotatedSpec[] = [];
  for (const s of specs) {
    if (filters.sortFirst.length > 0 && shareAny(s.meta.groups, filters.sortFirst)) {
      first.push(s);
    } else if (filters.sortLast.length > 0 && shareAny(s.meta.groups, filters.sortLast)) {
      last.push(s);
    } else {
      middle.push(s);
    }
  }
  return [...first, ...middle, ...last];
}

/** True when a and b share at least one element. */
function shareAny(a: string[], b: string[]): boolean {
  if (a.length === 0 || b.length === 0) return false;
  const setB = new Set(b);
  for (const x of a) if (setB.has(x)) return true;
  return false;
}

/**
 * Parse a comma-separated action input into a tag list. Trims each token
 * and drops empties so trailing commas / extra whitespace are tolerated.
 */
export function parseTagList(raw: string): string[] {
  if (!raw) return [];
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}
