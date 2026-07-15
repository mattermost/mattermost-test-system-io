/**
 * Per-worker swim-lane timeline rendered for terminal runs.
 *
 * Groups every lease and matching attempt by `gh_job_id`, ordering issuance
 * inside each lane chronologically. Reclaimed leases are flagged inline so
 * the cause of a re-dispatch is visible without cross-referencing.
 */

import { useMemo } from 'react';
import { Wrench, AlertTriangle, CheckCircle2, Clock, Inbox } from 'lucide-react';
import type { OrchestrationAttempt, OrchestrationLease, RunSnapshot } from '@/types/orchestration';
import { cn } from '@/lib/utils';
import { formatDuration } from '@/components/report_card_parts';

interface WorkerTimelineProps {
  run: RunSnapshot;
  leases: OrchestrationLease[];
  attempts: OrchestrationAttempt[];
}

interface WorkerLane {
  ghJobId: string;
  ghJobName: string;
  leases: OrchestrationLease[];
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

function leaseDurationMs(lease: OrchestrationLease): number | null {
  if (!lease.released_at) return null;
  return new Date(lease.released_at).getTime() - new Date(lease.issued_at).getTime();
}

export function WorkerTimeline({ run, leases, attempts }: WorkerTimelineProps) {
  // Pre-bucket attempts by lease so each lease block can render the outcomes
  // it produced without re-scanning the global list per row.
  const attemptsByLease = useMemo(() => {
    const map = new Map<string, OrchestrationAttempt[]>();
    for (const a of attempts) {
      if (!a.lease_id) continue;
      const list = map.get(a.lease_id) ?? [];
      list.push(a);
      map.set(a.lease_id, list);
    }
    return map;
  }, [attempts]);

  const lanes = useMemo<WorkerLane[]>(() => {
    const grouped = new Map<string, WorkerLane>();
    for (const lease of leases) {
      const lane = grouped.get(lease.gh_job_id);
      if (lane) {
        lane.leases.push(lease);
      } else {
        grouped.set(lease.gh_job_id, {
          ghJobId: lease.gh_job_id,
          ghJobName: lease.gh_job_name,
          leases: [lease],
        });
      }
    }
    for (const lane of grouped.values()) {
      lane.leases.sort((a, b) => new Date(a.issued_at).getTime() - new Date(b.issued_at).getTime());
    }
    return [...grouped.values()].sort((a, b) => a.ghJobName.localeCompare(b.ghJobName));
  }, [leases]);

  if (lanes.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-gray-200 bg-gray-50 py-10 text-gray-400 dark:border-gray-700 dark:bg-gray-900/40 dark:text-gray-500">
        <Inbox className="mb-2 h-8 w-8" />
        <p className="text-sm">No worker activity recorded</p>
      </div>
    );
  }

  return (
    <section className="space-y-4">
      <header className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-200">Worker timeline</h3>
        <span className="text-xs text-gray-500 dark:text-gray-400">
          Run{' '}
          {run.terminal_at ? (
            <>
              ended <time dateTime={run.terminal_at}>{formatTime(run.terminal_at)}</time>
            </>
          ) : (
            <>
              started <time dateTime={run.started_at}>{formatTime(run.started_at)}</time>
            </>
          )}
        </span>
      </header>

      <ol className="space-y-4">
        {lanes.map((lane) => (
          <li
            key={lane.ghJobId}
            className="rounded-lg border border-gray-200 bg-white p-3 dark:border-gray-700 dark:bg-gray-950"
          >
            <header className="mb-2 flex items-center gap-2">
              <Wrench className="h-3.5 w-3.5 text-gray-500 dark:text-gray-400" />
              <span className="text-sm font-medium text-gray-700 dark:text-gray-200">
                {lane.ghJobName}
              </span>
              <span className="font-mono text-xs text-gray-400 dark:text-gray-500">
                #{lane.ghJobId}
              </span>
              <span className="ml-auto text-xs text-gray-500 dark:text-gray-400">
                {lane.leases.length} lease{lane.leases.length === 1 ? '' : 's'}
              </span>
            </header>

            <ul className="space-y-2">
              {lane.leases.map((lease, idx) => {
                const reclaimed = lease.release_reason === 'expired';
                const durationMs = leaseDurationMs(lease);
                const leaseAttempts = lease.lease_id ? attemptsByLease.get(lease.lease_id) : [];
                return (
                  <li
                    key={`${lane.ghJobId}-${idx}-${lease.issued_at}`}
                    className={cn(
                      'rounded-md border px-3 py-2 text-xs',
                      reclaimed
                        ? 'border-red-200 bg-red-50 dark:border-red-800/60 dark:bg-red-950/30'
                        : 'border-gray-200 bg-gray-50 dark:border-gray-700 dark:bg-gray-900/40',
                    )}
                  >
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-gray-700 dark:text-gray-300">
                      <span className="inline-flex items-center gap-1">
                        <Clock className="h-3 w-3 text-gray-400 dark:text-gray-500" />
                        <span>checked out</span>
                        <time dateTime={lease.issued_at} className="font-mono">
                          {formatTime(lease.issued_at)}
                        </time>
                      </span>
                      <span className="text-gray-400 dark:text-gray-500">→</span>
                      <span className="inline-flex items-center gap-1">
                        <span>deadline</span>
                        <time dateTime={lease.deadline} className="font-mono">
                          {formatTime(lease.deadline)}
                        </time>
                      </span>
                      {lease.released_at && (
                        <>
                          <span className="text-gray-400 dark:text-gray-500">→</span>
                          <span className="inline-flex items-center gap-1">
                            <span>released</span>
                            <time dateTime={lease.released_at} className="font-mono">
                              {formatTime(lease.released_at)}
                            </time>
                          </span>
                        </>
                      )}
                      {durationMs !== null && (
                        <span className="text-gray-500 dark:text-gray-400">
                          ({formatDuration(durationMs)})
                        </span>
                      )}
                    </div>

                    <div className="mt-1 flex flex-wrap items-center gap-2">
                      {reclaimed ? (
                        <span className="inline-flex items-center gap-1 rounded-full bg-red-100 px-2 py-0.5 text-[11px] font-medium text-red-800 ring-1 ring-inset ring-red-200 dark:bg-red-900/40 dark:text-red-200 dark:ring-red-800/60">
                          <AlertTriangle className="h-3 w-3" />
                          Reclaimed (lease expired)
                        </span>
                      ) : lease.release_reason === 'completed' ? (
                        <span className="inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-[11px] font-medium text-green-800 ring-1 ring-inset ring-green-200 dark:bg-green-900/40 dark:text-green-200 dark:ring-green-800/60">
                          <CheckCircle2 className="h-3 w-3" />
                          Completed
                        </span>
                      ) : lease.release_reason === 'run_timed_out' ? (
                        <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-medium text-amber-800 ring-1 ring-inset ring-amber-200 dark:bg-amber-900/40 dark:text-amber-200 dark:ring-amber-800/60">
                          <AlertTriangle className="h-3 w-3" />
                          Run timed out
                        </span>
                      ) : null}

                      {leaseAttempts && leaseAttempts.length > 0 && (
                        <span className="text-[11px] text-gray-500 dark:text-gray-400">
                          {leaseAttempts.length} attempt{leaseAttempts.length === 1 ? '' : 's'}
                          {' · '}
                          outcomes:{' '}
                          {leaseAttempts.map((a, i) => (
                            <span key={i} className="font-mono">
                              {a.status ?? 'pending'}
                              {i < leaseAttempts.length - 1 ? ', ' : ''}
                            </span>
                          ))}
                        </span>
                      )}
                    </div>
                  </li>
                );
              })}
            </ul>
          </li>
        ))}
      </ol>
    </section>
  );
}
