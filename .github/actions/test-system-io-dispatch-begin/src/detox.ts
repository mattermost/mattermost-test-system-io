/** Detox spec discovery for dispatch-begin: walks *.e2e.ts files under a directory. */

import * as fs from "node:fs";
import * as path from "node:path";

const DETOX_SPEC_RE = /\.e2e\.ts$/;

export interface DetoxDiscoveryOptions {
  /** Relative to detoxDir. May point at a directory (walked recursively) or a single spec file. */
  searchPath: string;
  /** Directory name to skip during the walk. Empty string disables the exclusion. */
  excludeDir: string;
}

/** Returns sorted spec paths relative to detoxDir. A file searchPath returns just that file. */
export function discoverDetoxSpecs(detoxDir: string, opts: DetoxDiscoveryOptions): string[] {
  const target = path.join(detoxDir, opts.searchPath);
  const stat = fs.statSync(target);

  if (stat.isFile()) {
    if (!DETOX_SPEC_RE.test(target)) {
      throw new Error(
        `detox-search-path "${opts.searchPath}" is a file but doesn't match *.e2e.ts`,
      );
    }
    return [toRelative(detoxDir, target)];
  }

  const out: string[] = [];
  walk(target, opts.excludeDir, detoxDir, out);
  return out.sort();
}

function walk(dir: string, excludeDir: string, detoxDir: string, out: string[]): void {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) {
      if (excludeDir && ent.name === excludeDir) continue;
      walk(full, excludeDir, detoxDir, out);
    } else if (ent.isFile() && DETOX_SPEC_RE.test(ent.name)) {
      out.push(toRelative(detoxDir, full));
    }
  }
}

function toRelative(detoxDir: string, full: string): string {
  return path.relative(detoxDir, full).split(path.sep).join("/");
}
