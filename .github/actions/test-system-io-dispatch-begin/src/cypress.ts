/**
 * Cypress spec discovery + filter pipeline for dispatch-begin.
 *
 * Mattermost cypress specs annotate themselves with three header tags:
 *
 *   // Stage: @prod
 *   // Group: @channels @bot_accounts
 *   // Skip:  @firefox @darwin
 *
 * `discoverCypressSpecs` reads the consumer's cypress.config to locate
 * spec files (specPattern + excludeSpecPattern under `e2e: { ... }`),
 * parses each file's metadata, then applies the caller-supplied
 * filters in a fixed order: stage drop → include-group drop →
 * exclude-group drop → skip-on drop → sort-first/sort-last partition.
 * The result is a list of `spec_path` strings (relative to cypressDir,
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
  /**
   * Active-environment tags (e.g. `["@headless"]`).
   * Specs whose `// Skip:` line shares any tag are dropped — mirrors the
   * upstream Mattermost convention where a `@headless` skip tag means
   * "skip on headless browser." Empty array = no skip filter.
   */
  skipOn: string[];
  /** Surviving specs whose `// Group:` shares any tag dispatch first. */
  sortFirst: string[];
  /** Mirror of sortFirst — dispatched last. A spec matching both goes to sortFirst. */
  sortLast: string[];
}

export interface SpecMetadata {
  stages: string[];
  groups: string[];
  skips: string[];
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
    // Returning a parsed config the moment EITHER pattern is present means
    // a config that only sets excludeSpecPattern (relying on Cypress's
    // default specPattern) still gets its excludes honored. The earlier
    // form short-circuited only on include and silently dropped the
    // exclude list.
    if (include.length > 0 || exclude.length > 0) {
      return {
        include: include.length > 0 ? include : [...CYPRESS_DEFAULT_SPEC_PATTERN],
        exclude,
      };
    }
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
 * Read the spec file preamble and extract `Stage:` / `Group:` / `Skip:`
 * tag lists. Tolerant of: missing tags, malformed input, mixed-case
 * keyword, blank lines, and intervening `import` statements between
 * comment blocks (a common Mattermost pattern: copyright comment, then
 * imports, then the metadata comments, then the describe block).
 *
 * Reads at most a 4 KB prefix, then truncates at the first
 * `describe(`/`it(`/`context(`/`test(` so per-test inline tags
 * (`it('@flaky should …', …)`) don't bleed into spec-level metadata.
 *
 * `matchAll` over the truncated preamble (one regex per tag type) is
 * ~2× faster than line-by-line iteration on a 666-spec corpus and
 * captures multiple Stage/Group lines (some specs declare both
 * `// Stage: @prod` and `// Stage: @dev` on adjacent lines).
 */
export function parseCypressMetadata(absPath: string): SpecMetadata {
  // readFileSync internally closes the fd even when decoding/throwing, so
  // there's no descriptor leak if the read fails partway through. We only
  // need the first ~4 KiB to find the preamble Stage/Group/Skip comments.
  let raw: string;
  try {
    raw = fs.readFileSync(absPath, "utf8").slice(0, 4096);
  } catch {
    return { stages: [], groups: [], skips: [] };
  }

  const opener = /^\s*(?:describe|it|context|test)\s*\.?\s*[(.]/m.exec(raw);
  const preamble = opener ? raw.slice(0, opener.index) : raw;

  const stages: string[] = [];
  for (const m of preamble.matchAll(/^\s*\/\/\s*stage:\s*(.+)$/gim)) {
    for (const tok of m[1]!.split(/\s+/)) {
      if (/^@\S+$/.test(tok)) stages.push(tok);
    }
  }
  const groups: string[] = [];
  for (const m of preamble.matchAll(/^\s*\/\/\s*group:\s*(.+)$/gim)) {
    for (const tok of m[1]!.split(/\s+/)) {
      if (/^@\S+$/.test(tok)) groups.push(tok);
    }
  }
  const skips: string[] = [];
  for (const m of preamble.matchAll(/^\s*\/\/\s*skip:\s*(.+)$/gim)) {
    for (const tok of m[1]!.split(/\s+/)) {
      if (/^@\S+$/.test(tok)) skips.push(tok);
    }
  }
  return { stages, groups, skips };
}

/**
 * Returns true when every active filter (non-empty array) admits the spec.
 * Empty filter array short-circuits to "no constraint."
 */
export function passesFilters(meta: SpecMetadata, filters: CypressFilters): boolean {
  if (filters.stage.length > 0 && !shareAny(meta.stages, filters.stage)) return false;
  if (filters.includeGroup.length > 0 && !shareAny(meta.groups, filters.includeGroup)) return false;
  if (filters.excludeGroup.length > 0 && shareAny(meta.groups, filters.excludeGroup)) return false;
  if (filters.skipOn.length > 0 && shareAny(meta.skips, filters.skipOn)) return false;
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
