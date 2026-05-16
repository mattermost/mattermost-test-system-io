import type { ReportStats } from '@/types';

export { formatDuration } from '@/components/report_card_parts/utils';

export function calcPassRate(stats: ReportStats): string {
  // Pass rate excludes skipped tests: (passed + flaky) / (passed + flaky + failed)
  const countedTotal = stats.expected + stats.flaky + stats.unexpected;
  if (countedTotal === 0) return '0';
  return (((stats.expected + stats.flaky) / countedTotal) * 100).toFixed(1);
}

/**
 * Pick the worker's matrix slot from a `gh_job_name` / `report_name` /
 * `display_name` that ends in a numeric suffix (e.g. "orch-worker-3" → 3).
 * Returns `fallback` when the name doesn't carry one — legacy uploads,
 * single-report groups, or ad-hoc names. The fallback is typically the
 * server-assigned `report_number`, which is a chronological per-group
 * ordinal that won't necessarily line up with the matrix index but is
 * better than rendering nothing.
 */
export function workerSlot(name: string | null | undefined, fallback: number): number {
  if (!name) return fallback;
  const m = name.match(/-(\d+)$/);
  if (!m) return fallback;
  const n = Number(m[1]);
  return Number.isFinite(n) ? n : fallback;
}
