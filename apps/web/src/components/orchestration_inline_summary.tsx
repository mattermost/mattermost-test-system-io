/**
 * Compact inline rendering of an OrchestrationSummary, designed to appear
 * on report-index rows alongside the canonical TestStats column. Surfaces
 * the live orchestration_runs status as a colored pill and a one-line
 * counts breakdown so reviewers can spot in-flight work without leaving the
 * index page.
 *
 * The orchestration counts and canonical TestStats are independent: while
 * worker shards are uploading their reports, the canonical test_stats grow
 * lazily and may briefly disagree with the orchestration tally. Both
 * blocks render side-by-side; the dashboard never reconciles them here.
 */

import { Loader2, CheckCircle2, AlertTriangle } from 'lucide-react';
import type { OrchestrationSummary } from '@/types';

interface OrchestrationInlineSummaryProps {
  orchestration: OrchestrationSummary;
}

const STATUS_LABEL: Record<OrchestrationSummary['status'], string> = {
  in_progress: 'In progress',
  completed: 'Completed',
  timed_out: 'Timed out',
};

function statusPalette(status: OrchestrationSummary['status']): string {
  switch (status) {
    case 'in_progress':
      return 'bg-blue-100 text-blue-800 ring-blue-200 dark:bg-blue-900/40 dark:text-blue-200 dark:ring-blue-800/60';
    case 'completed':
      return 'bg-green-100 text-green-800 ring-green-200 dark:bg-green-900/40 dark:text-green-200 dark:ring-green-800/60';
    case 'timed_out':
      return 'bg-amber-100 text-amber-800 ring-amber-200 dark:bg-amber-900/40 dark:text-amber-200 dark:ring-amber-800/60';
  }
}

function statusIcon(status: OrchestrationSummary['status']) {
  switch (status) {
    case 'in_progress':
      return <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />;
    case 'completed':
      return <CheckCircle2 className="h-3 w-3" aria-hidden="true" />;
    case 'timed_out':
      return <AlertTriangle className="h-3 w-3" aria-hidden="true" />;
  }
}

export function OrchestrationInlineSummary({ orchestration }: OrchestrationInlineSummaryProps) {
  const { status, total_units, counts } = orchestration;
  return (
    <span
      className="inline-flex flex-wrap items-center gap-1.5 text-[11px] text-gray-500 dark:text-gray-400"
      data-testid="orchestration-inline-summary"
    >
      <span
        className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset ${statusPalette(
          status,
        )}`}
        title={`Orchestration ${STATUS_LABEL[status]}`}
      >
        {status !== 'in_progress' && statusIcon(status)}
        Live · {STATUS_LABEL[status]}
      </span>
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
