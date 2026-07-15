import type { ProgressBarProps } from './types';

export function ProgressBar({ stats }: ProgressBarProps) {
  const total = stats.expected + stats.unexpected + stats.flaky + stats.skipped;
  if (total === 0) return <div className="h-2 rounded-full bg-gray-200 dark:bg-gray-700" />;

  return (
    <div className="flex h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
      {stats.expected > 0 && (
        <div
          className="h-full bg-green-500"
          style={{ width: `${(stats.expected / total) * 100}%` }}
        />
      )}
      {stats.flaky > 0 && (
        <div
          className="h-full bg-yellow-500"
          style={{ width: `${(stats.flaky / total) * 100}%` }}
        />
      )}
      {stats.unexpected > 0 && (
        <div
          className="h-full bg-red-500"
          style={{ width: `${(stats.unexpected / total) * 100}%` }}
        />
      )}
      {stats.skipped > 0 && (
        <div
          className="h-full bg-gray-400 dark:bg-gray-500"
          style={{ width: `${(stats.skipped / total) * 100}%` }}
        />
      )}
    </div>
  );
}
