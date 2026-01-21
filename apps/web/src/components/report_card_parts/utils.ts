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

export function calculatePassRate(stats: { passed: number; failed: number; flaky: number }): number | null {
  const passed = stats.passed + stats.flaky;
  const failed = stats.failed;
  const total = passed + failed;
  if (total === 0) return null;
  return Math.round((passed * 100) / total);
}

export function getPassRateColorClass(passRate: number | null): string {
  if (passRate === null) return '';
  return passRate === 100
    ? 'bg-green-100 text-green-700 dark:bg-green-900/50 dark:text-green-300'
    : 'bg-red-100 text-red-700 dark:bg-red-900/50 dark:text-red-300';
}
