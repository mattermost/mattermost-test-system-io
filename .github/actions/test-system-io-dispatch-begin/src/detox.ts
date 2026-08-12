/** Detox spec discovery for dispatch-begin: walks *.e2e.ts files under a directory. */

import * as fs from "node:fs";
import * as path from "node:path";

const DETOX_SPEC_RE = /\.e2e\.ts$/;

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
 * (before the first non-comment import/code line). Same `@token` shape as Cypress
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
  // Only scan the preamble so a string literal later in the file cannot match.
  const preambleEnd = text.search(/^\s*(?:import|export|const|let|var|function|class|describe)\b/m);
  const preamble = preambleEnd === -1 ? text : text.slice(0, preambleEnd);
  const tags: string[] = [];
  for (const m of preamble.matchAll(/^\s*\/\/\s*tags:\s*(.+)$/gim)) {
    for (const tok of m[1]!.split(/\s+/)) {
      if (/^@\S+$/.test(tok)) tags.push(tok);
    }
  }
  return tags;
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
