/**
 * Pill that flags a per-spec disagreement between the orchestration-recorded
 * outcome and the canonical artifact-derived outcome.
 *
 * Surfaced from both the per-spec attempt grid (orchestration tab) and the
 * per-test-case row in the artifact tab so reviewers spot divergences from
 * either side without manually cross-referencing the two views.
 */

import { AlertTriangle } from 'lucide-react';
import type { TestCaseStatus } from '@/types/orchestration';
import { cn } from '@/lib/utils';

interface DivergenceBadgeProps {
  orchestrationStatus: TestCaseStatus;
  artifactStatus: TestCaseStatus;
  className?: string;
}

export function DivergenceBadge({
  orchestrationStatus,
  artifactStatus,
  className,
}: DivergenceBadgeProps) {
  const tooltip =
    `Orchestration recorded "${orchestrationStatus}" for this spec, but the ` +
    `uploaded report artifacts say "${artifactStatus}". Both sources are ` +
    `preserved — review which one represents the run's true outcome.`;

  return (
    <span
      role="status"
      aria-label="Divergence between orchestration and artifact outcomes"
      title={tooltip}
      className={cn(
        'inline-flex items-center gap-1 rounded-full bg-yellow-100 px-2 py-0.5 text-[11px] font-medium text-yellow-800 ring-1 ring-inset ring-yellow-200 dark:bg-yellow-900/40 dark:text-yellow-200 dark:ring-yellow-800/60',
        className,
      )}
    >
      <AlertTriangle className="h-3 w-3" aria-hidden="true" />
      <span>
        Orchestration: <strong className="font-semibold">{orchestrationStatus}</strong>
        <span className="mx-1 text-yellow-600 dark:text-yellow-400">/</span>
        Artifacts: <strong className="font-semibold">{artifactStatus}</strong>
      </span>
    </span>
  );
}
