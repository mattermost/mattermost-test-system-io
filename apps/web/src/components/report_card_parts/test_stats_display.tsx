import { CheckCircle2, XCircle, SkipForward, CircleDot } from 'lucide-react';
import type { TestStats } from '../../types';

interface TestStatsDisplayProps {
  stats: TestStats;
  compact?: boolean;
}

export function TestStatsDisplay({ stats, compact }: TestStatsDisplayProps) {
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
        {!compact && <CheckCircle2 className="h-3 w-3" />}
        {compact && <CheckCircle2 className="hidden min-[480px]:inline h-3 w-3" />}
        {stats.passed}
      </span>
      {stats.failed > 0 && (
        <>
          {compact && <span className="min-[480px]:hidden text-gray-300 dark:text-gray-600">|</span>}
          <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
            {!compact && <XCircle className="h-3 w-3" />}
            {compact && <XCircle className="hidden min-[480px]:inline h-3 w-3" />}
            {stats.failed}
          </span>
        </>
      )}
      {!compact && stats.skipped > 0 && (
        <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-400">
          <SkipForward className="h-3 w-3" />
          {stats.skipped}
        </span>
      )}
      {!compact && stats.flaky > 0 && (
        <span className="inline-flex items-center gap-1 text-yellow-600 dark:text-yellow-400">
          <CircleDot className="h-3 w-3" />
          {stats.flaky}
        </span>
      )}
    </div>
  );
}
