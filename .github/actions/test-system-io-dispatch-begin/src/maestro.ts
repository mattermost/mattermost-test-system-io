/** Maestro flow discovery for dispatch-begin: walks *.yml/*.yaml flow files under a directory. */

import * as fs from "node:fs";
import * as path from "node:path";

const MAESTRO_FLOW_RE = /\.ya?ml$/;
// Helper flows (e.g. `_connect_check.yml`) and chooser flows consumed via
// `runFlow:` rather than dispatched directly — not real test cases.
const MAESTRO_HELPER_RE = /^_/;
const MAESTRO_PICKER_RE = /_picker\.ya?ml$/;

export interface MaestroDiscoveryOptions {
  /** Relative to maestroDir. May point at a directory (walked recursively) or a single flow file. */
  searchPath: string;
  /** Directory name to skip during the walk. Empty string disables the exclusion. */
  excludeDir: string;
  /** Flow dropped if its `tags:` list shares any tag with this list. Empty array = no filter. */
  excludeTags: string[];
}

/** Returns sorted flow paths relative to maestroDir. A file searchPath returns just that file. */
export function discoverMaestroSpecs(maestroDir: string, opts: MaestroDiscoveryOptions): string[] {
  const target = path.join(maestroDir, opts.searchPath);
  let stat: fs.Stats;
  try {
    stat = fs.statSync(target);
  } catch (e) {
    throw new Error(
      `maestro flow discovery could not find "${target}" — check maestro-dir/maestro-flow-path ` +
        `(maestro-dir="${maestroDir}", maestro-flow-path="${opts.searchPath}"): ${(e as Error).message}`,
    );
  }

  if (stat.isFile()) {
    if (!MAESTRO_FLOW_RE.test(target)) {
      throw new Error(
        `maestro-flow-path "${opts.searchPath}" is a file but doesn't match *.yml/*.yaml`,
      );
    }
    if (!isEligibleFlowFile(target, path.basename(target), opts.excludeTags)) {
      return [];
    }
    return [toRelative(maestroDir, target)];
  }

  // Reject an excluded directory when it is the search root, matching child skips.
  if (opts.excludeDir && path.basename(target) === opts.excludeDir) {
    return [];
  }

  const out: string[] = [];
  walk(target, opts.excludeDir, maestroDir, opts.excludeTags, out);
  return out.sort();
}

function isEligibleFlowFile(absPath: string, baseName: string, excludeTags: string[]): boolean {
  if (MAESTRO_HELPER_RE.test(baseName) || MAESTRO_PICKER_RE.test(baseName)) {
    return false;
  }
  if (excludeTags.length > 0 && shareAny(readMaestroFlowTags(absPath), excludeTags)) {
    return false;
  }
  return true;
}

function walk(
  dir: string,
  excludeDir: string,
  maestroDir: string,
  excludeTags: string[],
  out: string[],
): void {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) {
      if (excludeDir && ent.name === excludeDir) continue;
      walk(full, excludeDir, maestroDir, excludeTags, out);
    } else if (ent.isFile() && MAESTRO_FLOW_RE.test(ent.name)) {
      if (!isEligibleFlowFile(full, ent.name, excludeTags)) continue;
      out.push(toRelative(maestroDir, full));
    }
  }
}

/**
 * Reads a flow file's `tags:` YAML block (list form, e.g.
 * `tags:\n  - MM-T1325\n  - @known_issue`) or inline flow form
 * (`tags: [MM-T1325, "@known_issue"]`). Tolerant, narrow string
 * extraction rather than a full YAML parser — flow headers are a small,
 * well-known shape (see mattermost-mobile's detox/maestro/flows/*.yml).
 * Returns [] when absent or unparseable.
 */
export function readMaestroFlowTags(absPath: string): string[] {
  let text: string;
  try {
    text = fs.readFileSync(absPath, "utf8");
  } catch {
    return [];
  }
  return parseMaestroFlowTags(text);
}

export function parseMaestroFlowTags(text: string): string[] {
  const inline = /^tags:\s*\[([^\]]*)\]\s*$/m.exec(text);
  if (inline) {
    return [...inline[1]!.matchAll(/['"]?([^,'"\s]+)['"]?/g)]
      .map((m) => m[1]!)
      .filter((s) => s.length > 0);
  }
  const block = /^tags:[ \t]*\n((?:[ \t]+-[ \t]*.+\n?)+)/m.exec(text);
  if (!block) return [];
  return [...block[1]!.matchAll(/^[ \t]+-[ \t]*['"]?([^'"\n]+?)['"]?[ \t]*$/gm)].map((m) =>
    m[1]!.trim(),
  );
}

/** True when a and b share at least one element. */
function shareAny(a: string[], b: string[]): boolean {
  if (a.length === 0 || b.length === 0) return false;
  const setB = new Set(b);
  for (const x of a) if (setB.has(x)) return true;
  return false;
}

function toRelative(maestroDir: string, full: string): string {
  return path.relative(maestroDir, full).split(path.sep).join("/");
}
