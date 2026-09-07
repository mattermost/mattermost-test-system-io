/** One-line orchestration counts for index rows. */

import type { OrchestrationSummary } from '@/types';

interface OrchestrationInlineSummaryProps {
  orchestration: OrchestrationSummary;
}

export function OrchestrationInlineSummary({ orchestration }: OrchestrationInlineSummaryProps) {
  const { total_units, counts } = orchestration;
  return (
    <span
      className="inline-flex flex-wrap items-center gap-1.5 text-[11px] text-gray-500 dark:text-gray-400"
      data-testid="orchestration-inline-summary"
    >
      <span className="tabular-nums">
        Total {total_units}
        {counts.pending > 0 && <> · Pending {counts.pending}</>}
        {/* `leased` is the orchestrator's internal term — surfaced as
            "Running" so end users read "a worker has it now" rather than
            having to know the dispatch_units lifecycle. */}
        {counts.leased > 0 && <> · Running {counts.leased}</>}
        {counts.completed_pass > 0 && (
          <>
            {' '}
            ·{' '}
            <span className="text-green-700 dark:text-green-400">
              Passed {counts.completed_pass}
            </span>
          </>
        )}
        {counts.completed_fail > 0 && (
          <>
            {' '}
            · <span className="text-red-700 dark:text-red-400">Failed {counts.completed_fail}</span>
          </>
        )}
        {counts.completed_skipped > 0 && <> · Skipped {counts.completed_skipped}</>}
        {counts.abandoned > 0 && (
          <>
            {' '}
            ·{' '}
            <span className="text-amber-700 dark:text-amber-400">Abandoned {counts.abandoned}</span>
          </>
        )}
        {counts.retest_eligible > 0 && (
          <>
            {' '}
            ·{' '}
            <span className="text-orange-700 dark:text-orange-400">
              Retest {counts.retest_eligible}
            </span>
          </>
        )}
      </span>
    </span>
  );
}
