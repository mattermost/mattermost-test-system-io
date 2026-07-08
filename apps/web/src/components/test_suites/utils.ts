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
 * (report_name, file_path) pair, keeping the latest (last in the input
 * order). A report group can mix genuine retries of one shard — same
 * report_name, later row supersedes the earlier one — with independent
 * parallel shards that happen to run the identical spec file (the same
 * suite on linux/macos/windows, or several CMT server versions). Deduping
 * on file_path alone collapses those together, silently discarding a real
 * per-platform failure whenever a different platform's later-uploaded run
 * of the same file passed. Keying on report_name too keeps unrelated
 * shards independent while still letting true retries dedupe as intended.
 */
export function dedupeSuitesByReportAndPath<T extends { report_name?: string; file_path?: string }>(
  sortedByTime: readonly T[],
): T[] {
  const keyOf = (item: T) => `${item.report_name ?? ''}::${item.file_path ?? ''}`;
  const lastIndexByKey = new Map<string, number>();
  for (let i = sortedByTime.length - 1; i >= 0; i--) {
    const item = sortedByTime[i]!;
    if (item.file_path && !lastIndexByKey.has(keyOf(item))) {
      lastIndexByKey.set(keyOf(item), i);
    }
  }
  return sortedByTime.filter((item, i) => {
    if (!item.file_path) return true;
    return lastIndexByKey.get(keyOf(item)) === i;
  });
}
