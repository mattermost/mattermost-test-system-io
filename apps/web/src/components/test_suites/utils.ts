import type { ReportStats } from '@/types';

export { formatDuration } from '@/components/report_card_parts/utils';

export function calcPassRate(stats: ReportStats): string {
  // Pass rate excludes skipped tests: (passed + flaky) / (passed + flaky + failed)
  const countedTotal = stats.expected + stats.flaky + stats.unexpected;
  if (countedTotal === 0) return '0';
  return (((stats.expected + stats.flaky) / countedTotal) * 100).toFixed(1);
}

/** Splits "<base>-<N>" into its base prefix and trailing digits, or null if there's no numeric suffix. */
export function splitTrailingNumber(name: string): { base: string; digits: string } | null {
  const m = name.match(/^(.*)-(\d+)$/);
  return m ? { base: m[1]!, digits: m[2]! } : null;
}

/**
 * Pick the worker's matrix slot from a `gh_job_name` / `report_name` /
 * `display_name` that ends in a numeric suffix (e.g. "orch-worker-3" → 3),
 * but only when at least one other name in `siblingNames` shares the same
 * prefix before the number — that's the actual "these are numbered shards
 * of the same job" signal. A lone name that happens to end in digits for
 * an unrelated reason (e.g. "e2e-on-macos-26", "e2e-on-windows-2022" — OS
 * version numbers, not shard indices) falls back instead of misreporting
 * its version number as a slot.
 *
 * Returns `fallback` when the name doesn't carry a shared numeric suffix —
 * legacy uploads, single-report groups, or ad-hoc names. The fallback is
 * typically the server-assigned `report_number`, which is a chronological
 * per-group ordinal that won't necessarily line up with the matrix index
 * but is better than rendering nothing.
 */
export function workerSlot(
  name: string | null | undefined,
  fallback: number,
  siblingNames: ReadonlyArray<string | null | undefined> = [],
): number {
  if (!name) return fallback;
  const split = splitTrailingNumber(name);
  if (!split) return fallback;

  const hasSharedBase = siblingNames.some((other) => {
    if (!other || other === name) return false;
    const otherSplit = splitTrailingNumber(other);
    return otherSplit !== null && otherSplit.base === split.base;
  });
  if (!hasSharedBase) return fallback;

  const n = Number(split.digits);
  return Number.isFinite(n) ? n : fallback;
}

/**
 * Collapse a chronologically-sorted suite list to one entry per
 * (report_name, file_path) pair by MERGING suites that share that pair,
 * rather than keeping only the latest.
 *
 * Why merge (not keep-latest): a spec file whose Playwright suite tree has a
 * top-level `describe` block is emitted as TWO suite rows sharing the same
 * file_path — the file-level suite and the describe-level suite — with
 * distinct test sets. A keep-latest strategy let a later all-skipped describe
 * clobber an earlier file-level failure, silently hiding it on the
 * dashboard (e.g. popout_windows.test.ts showing 0 failures while the file
 * actually failed). Merging sums the per-suite counts (and duration) and
 * keeps the earliest member's identity/start_time so the merged per-file row
 * reflects every test in the file and sorts where the file first ran.
 *
 * Why (report_name, file_path) and not file_path alone: independent shards
 * (linux/macos/windows, or several CMT server versions) that ran the same
 * file must NOT be merged — each shard keeps its own row so a per-platform
 * failure is never hidden by another platform's passing run.
 *
 * Why summing never double-counts: the server collapses same-(file,title)
 * retries into one suite before the client sees them, so by the time
 * multiple rows share a (report_name, file_path) here they are always
 * distinct-title file+describe rows with disjoint test sets.
 */
export function dedupeSuitesByReportAndPath<T extends { report_name?: string; file_path?: string }>(
  sortedByTime: readonly T[],
): T[] {
  // Count + duration fields aggregated across suites that share a
  // (report_name, file_path). Each is only set on the merged row when at
  // least one member defines it, so minimal shapes (e.g. test fixtures with
  // only failed_count) pass through without inventing other fields.
  const SUM_FIELDS = [
    'tests_count',
    'passed_count',
    'failed_count',
    'flaky_count',
    'skipped_count',
    'duration_ms',
  ] as const;

  const keyOf = (item: T) => `${item.report_name ?? ''}::${item.file_path ?? ''}`;

  // Pre-index every group (preserving first-seen order) so the merged row
  // can be emitted at the position of the group's earliest member.
  const groups = new Map<string, T[]>();
  for (const item of sortedByTime) {
    if (!item.file_path) continue;
    const k = keyOf(item);
    let g = groups.get(k);
    if (!g) {
      g = [] as T[];
      groups.set(k, g);
    }
    g.push(item);
  }

  const seen = new Set<string>();
  const out: T[] = [];
  for (const item of sortedByTime) {
    if (!item.file_path) {
      out.push(item); // no merge key — pass through unchanged
      continue;
    }
    const k = keyOf(item);
    if (seen.has(k)) continue; // a later member of an already-emitted group
    seen.add(k);
    const g = groups.get(k)!;
    if (g.length === 1) {
      out.push(item); // nothing to merge
      continue;
    }
    // Merge: base = earliest member (input is sorted ascending by time),
    // with count/duration fields summed across the whole group.
    const merged = { ...g[0]! } as Record<string, unknown> as T;
    const record = merged as unknown as Record<string, unknown>;
    for (const f of SUM_FIELDS) {
      let sum = 0;
      let any = false;
      for (const m of g) {
        const v = (m as Record<string, unknown>)[f];
        if (typeof v === 'number') {
          sum += v;
          any = true;
        }
      }
      if (any) record[f] = sum;
    }
    out.push(merged);
  }
  return out;
}
