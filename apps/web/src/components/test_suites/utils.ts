import type { ReportStats } from '@/types';

export function calcPassRate(stats: ReportStats): string {
  // Pass rate excludes skipped tests: (passed + flaky) / (passed + flaky + failed)
  const countedTotal = stats.expected + stats.flaky + stats.unexpected;
  if (countedTotal === 0) return '0';
  return (((stats.expected + stats.flaky) / countedTotal) * 100).toFixed(1);
}

export function formatDuration(ms: number): string {
  const totalSeconds = ms / 1000;
  const minutes = Math.floor(totalSeconds / 60);
  const hours = Math.floor(minutes / 60);

  if (hours > 0) {
    return `${hours}h ${minutes % 60}m ${Math.floor(totalSeconds % 60)}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${Math.floor(totalSeconds % 60)}s`;
  }
  return `${totalSeconds.toFixed(2)}s`;
}
