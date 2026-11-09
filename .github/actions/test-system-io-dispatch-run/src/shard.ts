/**
 * Which of a worker's archived JSON reports its shard uploads. Kept free of runtime imports from sibling
 * modules so `node --test` loads it directly; upload.ts registers and uploads what this returns.
 */

import * as fs from "node:fs";
import type { InvocationRecord, UploadPart } from "./types";

/**
 * The JSON reports that exist on disk, across every invocation, named for the upload: one file is
 * `playwright-results.json`, several are `playwright-results-<n>.json`. A report listed but missing on disk is
 * left out, so the names reflect what actually uploads.
 */
export function shardJsonParts(invocations: InvocationRecord[]): UploadPart[] {
  const existing: string[] = [];
  for (const inv of invocations) {
    for (const jsonPath of [inv.playwrightJsonPath, ...(inv.additionalJsonPaths ?? [])]) {
      if (fs.existsSync(jsonPath)) existing.push(jsonPath);
    }
  }
  const multi = existing.length > 1;
  return existing.map((absPath, j) => ({
    absPath,
    relPath: multi ? `playwright-results-${j}.json` : "playwright-results.json",
    size: fs.statSync(absPath).size,
  }));
}
