/**
 * Per-spec attempt history grid for terminal runs.
 *
 * One row per orchestration attempt. Expanding a row reveals the test-case
 * detail (title, full title, status, duration, error preview, attachments)
 * the worker reported alongside the lease. Screenshot attachments render as
 * thumbnails via the `/files/{key}` redirect.
 */

import { useMemo, useState } from 'react';
import {
  CheckCircle2,
  XCircle,
  AlertTriangle,
  MinusCircle,
  Clock,
  ChevronDown,
  ChevronRight,
  Inbox,
  Repeat,
} from 'lucide-react';
import type {
  CompositeIdentity,
  Divergence,
  OrchestrationAttempt,
  RunSnapshot,
  TestCaseDetail,
  TestCaseStatus,
} from '@/types/orchestration';
import { cn } from '@/lib/utils';
import { formatDuration } from '@/components/report_card_parts';
import { DivergenceBadge } from './divergence_badge';

interface AttemptGridProps {
  identity: CompositeIdentity;
  run: RunSnapshot;
  attempts: OrchestrationAttempt[];
  /**
   * Per-spec disagreements between orchestration and artifact outcomes.
   * Optional — when omitted no divergence pills render.
   */
  divergences?: Divergence[];
}

interface ScreenshotRef {
  key: string;
  relative_path?: string;
}

/**
 * Pull screenshot references off an attachments blob. The schema is loose
 * (`Record<string, unknown>`) at the type level but in practice carries a
 * `screenshots: [{ key, relative_path }]` shape per the OpenAPI contract.
 */
function pickScreenshots(attachments: Record<string, unknown> | null | undefined): ScreenshotRef[] {
  if (!attachments) return [];
  const raw = (attachments as Record<string, unknown>).screenshots;
  if (!Array.isArray(raw)) return [];
  const out: ScreenshotRef[] = [];
  for (const entry of raw) {
    if (entry && typeof entry === 'object' && 'key' in entry) {
      const e = entry as { key?: unknown; relative_path?: unknown };
      if (typeof e.key === 'string' && e.key.length > 0) {
        out.push({
          key: e.key,
          relative_path: typeof e.relative_path === 'string' ? e.relative_path : undefined,
        });
      }
    }
  }
  return out;
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

function StatusPill({ status }: { status: TestCaseStatus | null }) {
  if (!status) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-gray-100 px-2 py-0.5 text-[11px] font-medium text-gray-600 ring-1 ring-inset ring-gray-200 dark:bg-gray-800 dark:text-gray-400 dark:ring-gray-700">
        <Clock className="h-3 w-3" />
        Pending
      </span>
    );
  }

  let palette;
  let icon;
  switch (status) {
    case 'passed':
      icon = <CheckCircle2 className="h-3 w-3" />;
      palette =
        'bg-green-100 text-green-800 ring-green-200 dark:bg-green-900/40 dark:text-green-200 dark:ring-green-800/60';
      break;
    case 'failed':
    case 'timedOut':
    case 'interrupted':
      icon = <XCircle className="h-3 w-3" />;
      palette =
        'bg-red-100 text-red-800 ring-red-200 dark:bg-red-900/40 dark:text-red-200 dark:ring-red-800/60';
      break;
    case 'flaky':
      icon = <AlertTriangle className="h-3 w-3" />;
      palette =
        'bg-yellow-100 text-yellow-800 ring-yellow-200 dark:bg-yellow-900/40 dark:text-yellow-200 dark:ring-yellow-800/60';
      break;
    case 'skipped':
      icon = <MinusCircle className="h-3 w-3" />;
      palette =
        'bg-gray-100 text-gray-700 ring-gray-200 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700';
      break;
    default:
      icon = <MinusCircle className="h-3 w-3" />;
      palette =
        'bg-gray-100 text-gray-700 ring-gray-200 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700';
      break;
  }

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset',
        palette,
      )}
    >
      {icon}
      {status}
    </span>
  );
}

function TestCaseRow({ tc }: { tc: TestCaseDetail }) {
  const screenshots = pickScreenshots(tc.attachments);
  const errorPreview = tc.error_message?.split('\n')[0]?.slice(0, 200);
  return (
    <li className="rounded-md border border-gray-200 bg-white p-2 text-xs dark:border-gray-700 dark:bg-gray-950">
      <div className="flex flex-wrap items-center gap-2">
        <StatusPill status={tc.status} />
        <span className="font-medium text-gray-800 dark:text-gray-100">{tc.title}</span>
        {tc.duration_ms !== null && tc.duration_ms !== undefined && (
          <span className="text-gray-500 dark:text-gray-400">{formatDuration(tc.duration_ms)}</span>
        )}
        {tc.retry_count > 0 && (
          <span className="text-[10px] text-gray-500 dark:text-gray-400">
            retried {tc.retry_count}×
          </span>
        )}
      </div>
      <div className="mt-1 font-mono text-[11px] text-gray-500 dark:text-gray-500 break-all">
        {tc.full_title}
      </div>
      {errorPreview && (
        <pre className="mt-1 whitespace-pre-wrap break-words rounded bg-red-50 p-2 text-[11px] text-red-800 dark:bg-red-950/40 dark:text-red-200">
          {errorPreview}
        </pre>
      )}
      {screenshots.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-2">
          {screenshots.map((s) => (
            <img
              key={s.key}
              src={`/files/${s.key}`}
              alt={s.relative_path ?? `Screenshot for ${tc.title}`}
              loading="lazy"
              className="h-20 w-32 rounded border border-gray-200 object-cover dark:border-gray-700"
            />
          ))}
        </div>
      )}
    </li>
  );
}

interface AttemptRowProps {
  attempt: OrchestrationAttempt;
  index: number;
  /**
   * True when this attempt is a retest dispatch — either flagged explicitly
   * by `attempt.is_retest` or inferred from being the second-or-later attempt
   * the same spec produced in this run.
   */
  isRetest: boolean;
  /** Divergence flagged for this attempt's spec, if any. */
  divergence?: Divergence;
}

function AttemptRow({ attempt, index, isRetest, divergence }: AttemptRowProps) {
  const [expanded, setExpanded] = useState(false);
  const hasCases = !!attempt.test_cases && attempt.test_cases.length > 0;

  return (
    <li
      className={cn(
        'rounded-md border bg-white text-sm dark:bg-gray-950',
        attempt.expired || attempt.late_report
          ? 'border-amber-200 dark:border-amber-800/60'
          : 'border-gray-200 dark:border-gray-700',
      )}
    >
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        disabled={!hasCases}
        className={cn(
          'flex w-full items-center gap-3 px-3 py-2 text-left',
          hasCases ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-900/50' : 'cursor-default',
        )}
      >
        {hasCases ? (
          expanded ? (
            <ChevronDown className="h-3.5 w-3.5 text-gray-500" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 text-gray-500" />
          )
        ) : (
          <span className="inline-block h-3.5 w-3.5" />
        )}
        <span className="font-mono text-xs text-gray-500 dark:text-gray-400">#{index + 1}</span>
        {isRetest && (
          <span
            className="inline-flex items-center gap-1 rounded-full bg-orange-100 px-2 py-0.5 text-[11px] font-medium text-orange-800 ring-1 ring-inset ring-orange-200 dark:bg-orange-900/40 dark:text-orange-200 dark:ring-orange-800/60"
            title="Retest dispatch — the unit had already failed at least once when this attempt was issued."
            aria-label="retest"
            data-testid="retest-icon"
          >
            <Repeat className="h-3 w-3" aria-hidden="true" />
            retest
          </span>
        )}
        <StatusPill status={attempt.status} />
        <span className="text-xs text-gray-700 dark:text-gray-300">{attempt.gh_job_name}</span>
        {attempt.spec_path && (
          <span className="font-mono text-[11px] text-gray-500 dark:text-gray-400 break-all">
            {attempt.spec_path}
          </span>
        )}
        <span className="text-[11px] text-gray-400 dark:text-gray-500">
          deadline <time dateTime={attempt.deadline}>{formatTime(attempt.deadline)}</time>
        </span>
        {attempt.actual_duration_ms !== null && attempt.actual_duration_ms !== undefined && (
          <span className="text-xs text-gray-500 dark:text-gray-400">
            {formatDuration(attempt.actual_duration_ms)}
          </span>
        )}
        <span className="ml-auto flex items-center gap-1.5">
          {divergence && (
            <DivergenceBadge
              orchestrationStatus={divergence.orchestration_status}
              artifactStatus={divergence.artifact_status}
            />
          )}
          {attempt.expired && (
            <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-medium text-amber-800 ring-1 ring-inset ring-amber-200 dark:bg-amber-900/40 dark:text-amber-200 dark:ring-amber-800/60">
              expired
            </span>
          )}
          {attempt.late_report && (
            <span className="inline-flex items-center gap-1 rounded-full bg-yellow-100 px-2 py-0.5 text-[11px] font-medium text-yellow-800 ring-1 ring-inset ring-yellow-200 dark:bg-yellow-900/40 dark:text-yellow-200 dark:ring-yellow-800/60">
              late
            </span>
          )}
        </span>
      </button>

      {expanded && hasCases && (
        <div className="border-t border-gray-200 px-3 py-2 dark:border-gray-700">
          <ul className="space-y-2">
            {attempt.test_cases!.map((tc) => (
              <TestCaseRow key={`${tc.full_title}-${tc.ordinal}`} tc={tc} />
            ))}
          </ul>
        </div>
      )}
    </li>
  );
}

export function AttemptGrid({
  identity: _identity,
  run: _run,
  attempts,
  divergences,
}: AttemptGridProps) {
  const ordered = useMemo(() => {
    return [...attempts].sort((a, b) => {
      const aT = a.reported_at ?? a.deadline;
      const bT = b.reported_at ?? b.deadline;
      return new Date(aT).getTime() - new Date(bT).getTime();
    });
  }, [attempts]);

  // Per-spec attempt counters, used to infer the "retest" flag for the
  // second-and-later attempt on a spec when the orchestrator did not set
  // `is_retest` explicitly on the row.
  const retestFlags = useMemo(() => {
    const seen = new Map<string, number>();
    const out: boolean[] = [];
    for (const a of ordered) {
      if (a.is_retest) {
        out.push(true);
        if (a.spec_path) seen.set(a.spec_path, (seen.get(a.spec_path) ?? 0) + 1);
        continue;
      }
      if (!a.spec_path) {
        out.push(false);
        continue;
      }
      const prior = seen.get(a.spec_path) ?? 0;
      out.push(prior > 0);
      seen.set(a.spec_path, prior + 1);
    }
    return out;
  }, [ordered]);

  const divergenceBySpec = useMemo(() => {
    const out = new Map<string, Divergence>();
    for (const d of divergences ?? []) out.set(d.spec_path, d);
    return out;
  }, [divergences]);

  if (ordered.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-gray-200 bg-gray-50 py-10 text-gray-400 dark:border-gray-700 dark:bg-gray-900/40 dark:text-gray-500">
        <Inbox className="mb-2 h-8 w-8" />
        <p className="text-sm">No attempts recorded</p>
      </div>
    );
  }

  return (
    <section className="space-y-3">
      <header className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-200">Attempts</h3>
        <span className="text-xs text-gray-500 dark:text-gray-400">{ordered.length} recorded</span>
      </header>
      <ol className="space-y-2">
        {ordered.map((attempt, i) => (
          <AttemptRow
            key={`${attempt.lease_id}-${attempt.gh_job_id}-${attempt.spec_path ?? ''}-${i}`}
            attempt={attempt}
            index={i}
            isRetest={retestFlags[i] ?? false}
            divergence={attempt.spec_path ? divergenceBySpec.get(attempt.spec_path) : undefined}
          />
        ))}
      </ol>
    </section>
  );
}
