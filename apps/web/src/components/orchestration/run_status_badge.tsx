/**
 * Pill-shaped badge that visualises the current orchestration run status.
 *
 * Maps each {@link RunStatus} value to an icon + colour palette, with both
 * light- and dark-mode classes so it reads cleanly against the page surface.
 */

import { CheckCircle2, Loader2, AlertTriangle, Repeat } from 'lucide-react';
import type { RunStatus } from '@/types/orchestration';
import { cn } from '@/lib/utils';

interface RunStatusBadgeProps {
  status: RunStatus;
  /**
   * Optional retest backlog. When non-zero AND status === 'in_progress',
   * renders a small "N retest pending" annotation alongside the status pill
   * so the run page surfaces the retest backlog at a glance.
   */
  retestEligibleCount?: number;
  className?: string;
}

const STATUS_LABEL: Record<RunStatus, string> = {
  in_progress: 'In progress',
  completed: 'Completed',
  timed_out: 'Timed out',
};

export function RunStatusBadge({ status, retestEligibleCount, className }: RunStatusBadgeProps) {
  let icon;
  let palette;

  switch (status) {
    case 'in_progress':
      icon = <Loader2 className="h-3.5 w-3.5 animate-spin" />;
      palette =
        'bg-amber-100 text-amber-800 ring-amber-200 dark:bg-amber-900/40 dark:text-amber-200 dark:ring-amber-800/60';
      break;
    case 'completed':
      icon = <CheckCircle2 className="h-3.5 w-3.5" />;
      palette =
        'bg-green-100 text-green-800 ring-green-200 dark:bg-green-900/40 dark:text-green-200 dark:ring-green-800/60';
      break;
    case 'timed_out':
      icon = <AlertTriangle className="h-3.5 w-3.5" />;
      palette =
        'bg-red-100 text-red-800 ring-red-200 dark:bg-red-900/40 dark:text-red-200 dark:ring-red-800/60';
      break;
  }

  const showRetest =
    status === 'in_progress' && typeof retestEligibleCount === 'number' && retestEligibleCount > 0;

  return (
    <span className={cn('inline-flex items-center gap-2', className)}>
      <span
        className={cn(
          'inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ring-1 ring-inset',
          palette,
        )}
      >
        {icon}
        {STATUS_LABEL[status]}
      </span>
      {showRetest && (
        <span
          className="inline-flex items-center gap-1 rounded-full bg-orange-100 px-2 py-0.5 text-[11px] font-medium text-orange-800 ring-1 ring-inset ring-orange-200 dark:bg-orange-900/40 dark:text-orange-200 dark:ring-orange-800/60"
          title={`${retestEligibleCount} unit(s) eligible for retest dispatch`}
          data-testid="retest-pending"
        >
          <Repeat className="h-3 w-3" aria-hidden="true" />
          {retestEligibleCount} retest pending
        </span>
      )}
    </span>
  );
}
