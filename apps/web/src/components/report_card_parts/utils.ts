export function formatDateShort(dateString: string): string {
  const date = new Date(dateString);
  return date.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
  });
}

export function formatDateFull(dateString: string): string {
  const date = new Date(dateString);
  return date.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function formatDuration(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);

  if (hours > 0) {
    const remainingMinutes = minutes % 60;
    return remainingMinutes > 0 ? `${hours}h ${remainingMinutes}m` : `${hours}h`;
  }
  if (minutes > 0) {
    const remainingSeconds = seconds % 60;
    return remainingSeconds > 0 ? `${minutes}m ${remainingSeconds}s` : `${minutes}m`;
  }
  return `${seconds}s`;
}

export function calculateRawPassRate(stats: {
  passed: number;
  failed: number;
  flaky: number;
}): number | null {
  const passed = stats.passed + stats.flaky;
  const failed = stats.failed;
  const total = passed + failed;
  if (total === 0) return null;
  const rate = (passed * 100) / total;
  return rate === 100 ? 100 : Math.floor(rate * 10) / 10;
}

export function getPassRateColorClass(passRate: number | null): string {
  if (passRate === null) return '';
  return passRate === 100
    ? 'bg-green-100 text-green-700 dark:bg-green-900/50 dark:text-green-300'
    : 'bg-red-100 text-red-700 dark:bg-red-900/50 dark:text-red-300';
}

/** Retest shard name pattern — mirrors the server's retest classification. */
const RETEST_NAME_PATTERN = /retest|run[-_ ]?failed/i;

export function isRetestName(name?: string | null): boolean {
  return !!name && RETEST_NAME_PATTERN.test(name);
}

/**
 * Stats shape consumed by the listing rows and aggregations. Mirrors the
 * fields of `TestStats` plus an `isOrchestration` discriminator so the UI
 * can label spec-file-level numbers (e.g. "8 specs" rather than "8 tests")
 * when the underlying source is the orchestration counts.
 */
export interface DisplayStats {
  total: number;
  passed: number;
  failed: number;
  skipped: number;
  flaky: number;
  duration_ms?: number;
  wall_clock_ms?: number;
  retest_wall_clock_ms?: number;
  /**
   * True when the numbers came from the orchestration summary (per
   * spec-file). False when they came from the framework's reported
   * `test_stats` (per individual test). Used to disambiguate row labels
   * and tooltips so we don't claim "tests passed" when the count is
   * actually "spec files passed".
   */
  isOrchestration: boolean;
}

interface OrchestrationLikeCounts {
  status: 'in_progress' | 'completed' | 'timed_out';
  total_units: number;
  counts: {
    pending: number;
    leased: number;
    completed_pass: number;
    completed_fail: number;
    completed_skipped: number;
    abandoned: number;
    retest_eligible: number;
  };
  /** Server-rolled-up test-case counts from `attempts.test_cases`. */
  tests?: {
    passed: number;
    failed: number;
    flaky: number;
    skipped: number;
    total: number;
  };
}

interface TestStatsLike {
  total: number;
  passed: number;
  failed: number;
  skipped: number;
  flaky: number;
  duration_ms?: number;
  wall_clock_ms?: number;
  retest_wall_clock_ms?: number;
}

/**
 * Source-of-truth resolver for row-level test summaries on the listing
 * pages. Three-step preference, all at test-case granularity:
 *
 *   1. The server's `orchestration.tests` rollup — the orchestrator's
 *      canonical record of what was dispatched and what each attempt
 *      reported. Used by default so in-flight and completed runs share
 *      a single source of truth.
 *   2. The framework's `test_stats` — fallback for legacy report
 *      uploads that have no associated orchestration_run row.
 *   3. `null` when neither source has any tests yet (a fresh run with
 *      every dispatch unit still pending). The inline orchestration
 *      strip below the row conveys spec-level progress in that case.
 */
export function resolveDisplayStats(entry: {
  test_stats?: TestStatsLike;
  orchestration?: OrchestrationLikeCounts;
}): DisplayStats | null {
  const tests = entry.orchestration?.tests;
  if (tests && tests.total > 0) {
    return {
      total: tests.total,
      passed: tests.passed,
      failed: tests.failed,
      skipped: tests.skipped,
      flaky: tests.flaky,
      duration_ms: entry.test_stats?.duration_ms,
      wall_clock_ms: entry.test_stats?.wall_clock_ms,
      retest_wall_clock_ms: entry.test_stats?.retest_wall_clock_ms,
      isOrchestration: true,
    };
  }
  if (entry.test_stats && entry.test_stats.total > 0) {
    return { ...entry.test_stats, isOrchestration: false };
  }
  return null;
}
