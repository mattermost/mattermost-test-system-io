/**
 * Detox spec discovery for dispatch-begin. Specs come from a directory walk, an explicit list,
 * or `detox test --listTests`; every source yields paths relative to detoxDir, which is what
 * dispatch-run passes back to `detox test` from that directory.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import { spawnSync } from "node:child_process";

const DETOX_SPEC_RE = /\.e2e\.ts$/;
const DETOX_CONFIG_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
// dispatch-run hands specs to Detox, which runs Jest through /bin/sh unquoted.
const SAFE_SPEC_PATH_RE = /^[A-Za-z0-9_][A-Za-z0-9._/-]*$/;
const LIST_TESTS_MAX_BUFFER = 16 * 1024 * 1024;
const MAX_LISTED_SPEC_ERRORS = 10;

export interface DetoxDiscoveryOptions {
  /** Relative to detoxDir. May point at a directory (walked recursively) or a single spec file. */
  searchPath: string;
  /**
   * Specs kept only when their `// Tags:` line shares at least one tag with this list.
   * Empty array = no include filter (discover everything under searchPath).
   */
  includeTags: string[];
  /**
   * Specs dropped when their `// Tags:` line shares any tag with this list.
   * Applied after the include filter. Empty array = no exclude filter.
   */
  excludeTags: string[];
}

/** Returns sorted spec paths relative to detoxDir. A file searchPath returns just that file. */
export function discoverDetoxSpecs(detoxDir: string, opts: DetoxDiscoveryOptions): string[] {
  const target = path.join(detoxDir, opts.searchPath);
  let stat: fs.Stats;
  try {
    stat = fs.statSync(target);
  } catch (e) {
    throw new Error(
      `detox spec discovery could not find "${target}" — check detoxDir/detox-search-path ` +
        `(detoxDir="${detoxDir}", detox-search-path="${opts.searchPath}"): ${(e as Error).message}`,
    );
  }

  if (stat.isFile()) {
    if (!DETOX_SPEC_RE.test(target)) {
      throw new Error(
        `detox-search-path "${opts.searchPath}" is a file but doesn't match *.e2e.ts`,
      );
    }
    const rel = toRelative(detoxDir, target);
    return passesDetoxTagFilters(readDetoxSpecTags(target), opts) ? [rel] : [];
  }

  const out: string[] = [];
  walk(target, opts, detoxDir, out);
  return out.sort();
}

function walk(dir: string, opts: DetoxDiscoveryOptions, detoxDir: string, out: string[]): void {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) {
      walk(full, opts, detoxDir, out);
    } else if (ent.isFile() && DETOX_SPEC_RE.test(ent.name)) {
      if (!passesDetoxTagFilters(readDetoxSpecTags(full), opts)) continue;
      out.push(toRelative(detoxDir, full));
    }
  }
}

/**
 * Read `// Tags: @ios_pr @smoke` annotations from the file preamble
 * (leading blank/comment lines only). Same `@token` shape as Cypress
 * Stage/Group so callers can pass comma-separated action inputs.
 */
export function readDetoxSpecTags(absPath: string): string[] {
  let text: string;
  try {
    text = fs.readFileSync(absPath, "utf8");
  } catch {
    return [];
  }
  return parseDetoxSpecTags(text);
}

export function parseDetoxSpecTags(text: string): string[] {
  // Only scan the preamble (blank + // comments) so a later // Tags: line
  // after any TypeScript statement cannot match.
  const preamble = takeDetoxPreamble(text);
  const tags: string[] = [];
  for (const m of preamble.matchAll(/^\s*\/\/\s*tags:\s*(.+)$/gim)) {
    for (const tok of m[1]!.split(/\s+/)) {
      if (/^@\S+$/.test(tok)) tags.push(tok);
    }
  }
  return tags;
}

/** Leading blank lines and `//` comments; stops at the first code line. */
export function takeDetoxPreamble(text: string): string {
  const lines = text.split(/\r?\n/);
  const kept: string[] = [];
  for (const line of lines) {
    if (/^\s*$/.test(line) || /^\s*\/\//.test(line)) {
      kept.push(line);
      continue;
    }
    break;
  }
  return kept.join("\n");
}

export function passesDetoxTagFilters(
  tags: string[],
  opts: Pick<DetoxDiscoveryOptions, "includeTags" | "excludeTags">,
): boolean {
  if (opts.includeTags.length > 0 && !shareAny(tags, opts.includeTags)) return false;
  if (opts.excludeTags.length > 0 && shareAny(tags, opts.excludeTags)) return false;
  return true;
}

/** True when a and b share at least one element. */
function shareAny(a: string[], b: string[]): boolean {
  if (a.length === 0 || b.length === 0) return false;
  const setB = new Set(b);
  for (const x of a) if (setB.has(x)) return true;
  return false;
}

function toRelative(detoxDir: string, full: string): string {
  return path.relative(detoxDir, full).split(path.sep).join("/");
}

/**
 * Resolves spec entries (relative to detoxDir, or absolute inside it) to sorted, de-duplicated
 * paths relative to detoxDir. Symlinks are resolved on both sides so `/private/var` and
 * `/var` checkouts compare equal. Throws when any entry is missing, outside detoxDir, not a
 * file, or not `*.e2e.ts`, so a bad list fails begin instead of leasing a unit no worker can run.
 * The error names the first MAX_LISTED_SPEC_ERRORS bad entries and the total.
 */
export function resolveDetoxSpecList(detoxDir: string, entries: string[]): string[] {
  const root = fs.realpathSync(detoxDir);
  const out = new Set<string>();
  const errors: string[] = [];
  for (const raw of entries) {
    const entry = raw.trim();
    if (entry === "") continue;
    const resolved = resolveDetoxSpecEntry(root, entry);
    if ("error" in resolved) errors.push(`"${entry}" ${resolved.error}`);
    else out.add(resolved.rel);
  }
  if (errors.length > 0) {
    const shown = errors.slice(0, MAX_LISTED_SPEC_ERRORS).map((e) => `  ${e}`);
    if (errors.length > shown.length) shown.push(`  ...and ${errors.length - shown.length} more`);
    throw new Error(
      `${errors.length} invalid detox spec(s) under ${detoxDir}:\n${shown.join("\n")}`,
    );
  }
  return [...out].sort();
}

function resolveDetoxSpecEntry(root: string, entry: string): { rel: string } | { error: string } {
  let real: string;
  try {
    real = fs.realpathSync(path.resolve(root, entry));
  } catch {
    return { error: "does not exist" };
  }
  if (!real.startsWith(root + path.sep)) return { error: "is outside detox-dir" };
  if (!fs.statSync(real).isFile() || !DETOX_SPEC_RE.test(real)) {
    return { error: "is not a *.e2e.ts file" };
  }
  const rel = toRelative(root, real);
  if (!SAFE_SPEC_PATH_RE.test(rel)) return { error: "has characters outside [A-Za-z0-9._/-]" };
  return { rel };
}

/** Splits the `detox-specs` input on whitespace (newlines, spaces). Paths with spaces are not supported. */
export function parseDetoxSpecList(input: string): string[] {
  return input.split(/\s+/).filter(Boolean);
}

/**
 * Runs `npx detox test -c <config> --listTests` in detoxDir and returns the listed specs
 * relative to detoxDir. The Detox configuration decides the files (its testRunner args, Jest
 * testMatch and ignore patterns), so the queue matches what `detox test -c <config>` would run.
 * No device is booted. Needs the Detox project's node_modules installed.
 */
export function listDetoxSpecs(detoxDir: string, detoxConfig: string): string[] {
  if (!DETOX_CONFIG_RE.test(detoxConfig)) {
    throw new Error(`detox-config "${detoxConfig}" is not a valid configuration name`);
  }
  const child = spawnSync("npx", ["detox", "test", "-c", detoxConfig, "--listTests"], {
    cwd: detoxDir,
    encoding: "utf8",
    maxBuffer: LIST_TESTS_MAX_BUFFER,
  });
  if (child.error) {
    throw new Error(`detox --listTests could not start: ${child.error.message}`);
  }
  if (child.status !== 0) {
    const tail = (child.stderr ?? "").trim().split(/\r?\n/).slice(-10).join("\n");
    throw new Error(`detox --listTests exited ${child.status} for -c ${detoxConfig}:\n${tail}`);
  }
  return resolveDetoxSpecList(detoxDir, listedSpecPaths(child.stdout ?? ""));
}

/**
 * The spec paths in `--listTests` stdout: one per line, or one JSON array line when the
 * configuration passes Jest `json`. Detox logs go to stderr; anything else is dropped.
 */
export function listedSpecPaths(stdout: string): string[] {
  return stdout
    .split(/\r?\n/)
    .map((line) => line.trim())
    .flatMap((line) => jsonStringArray(line) ?? [line])
    .filter((p) => path.isAbsolute(p) && DETOX_SPEC_RE.test(p));
}

function jsonStringArray(line: string): string[] | undefined {
  if (!line.startsWith("[")) return undefined;
  try {
    const parsed: unknown = JSON.parse(line);
    if (!Array.isArray(parsed)) return undefined;
    return parsed.filter((p): p is string => typeof p === "string");
  } catch {
    return undefined;
  }
}

/** Keeps the specs that pass the include/exclude tag filters, in their given order. */
export function filterDetoxSpecsByTags(
  detoxDir: string,
  specs: string[],
  opts: Pick<DetoxDiscoveryOptions, "includeTags" | "excludeTags">,
): string[] {
  if (opts.includeTags.length === 0 && opts.excludeTags.length === 0) return specs;
  return specs.filter((s) =>
    passesDetoxTagFilters(readDetoxSpecTags(path.join(detoxDir, s)), opts),
  );
}

/** Moves specs whose `// Tags:` share any tag with sortLast to the end, keeping each group's order. */
export function sortDetoxSpecsLast(
  detoxDir: string,
  specs: string[],
  sortLast: string[],
): string[] {
  if (sortLast.length === 0) return specs;
  const last = new Set(sortLast);
  const head: string[] = [];
  const tail: string[] = [];
  for (const s of specs) {
    const tags = readDetoxSpecTags(path.join(detoxDir, s));
    (tags.some((t) => last.has(t)) ? tail : head).push(s);
  }
  return [...head, ...tail];
}
