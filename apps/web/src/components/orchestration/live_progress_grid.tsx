/**
 * Live grid that mirrors every dispatch unit in an in-progress run.
 *
 * Subscribes to orchestration WebSocket events for the current identity and
 * patches per-unit state in place as `unit.leased`, `unit.completed`, and
 * `lease.expired` events arrive. Rendering stays a simple table keyed by
 * `unit_id` so React reuses rows across updates.
 */

import { useEffect, useMemo, useState } from 'react';
import { Hourglass, Loader2, CheckCircle2, XCircle, MinusCircle, Ban, Inbox } from 'lucide-react';
import type {
  CompositeIdentity,
  OrchestrationEvent,
  OrchestrationUnit,
  RunSnapshot,
  UnitState,
} from '@/types/orchestration';
// TODO: pending sibling agent — `subscribeToOrchestrationRun` is expected to
// be exported from `@/services/websocket`. Until it lands the import below
// will fail to type-check; remove the cast once the helper is in place.
import * as wsModule from '@/services/websocket';
import { cn } from '@/lib/utils';

type SubscribeFn = (
  identity: CompositeIdentity,
  callback: (event: OrchestrationEvent) => void,
) => () => void;

const subscribeToOrchestrationRun: SubscribeFn | undefined = (
  wsModule as unknown as { subscribeToOrchestrationRun?: SubscribeFn }
).subscribeToOrchestrationRun;

interface LiveProgressGridProps {
  identity: CompositeIdentity;
  run: RunSnapshot;
  units: OrchestrationUnit[];
}

interface UnitRowState {
  unit: OrchestrationUnit;
  /** Most recent worker that picked up this unit (post-lease event). */
  latestWorker?: string;
  /** ISO timestamp of the latest event we've reflected for the row. */
  latestEventAt?: string;
}

const STATE_LABEL: Record<UnitState, string> = {
  pending: 'Pending',
  leased: 'Leased',
  completed_pass: 'Passed',
  completed_fail: 'Failed',
  completed_skipped: 'Skipped',
  abandoned: 'Abandoned',
};

function StateBadge({ state }: { state: UnitState }) {
  let icon;
  let palette;
  switch (state) {
    case 'pending':
      icon = <Hourglass className="h-3 w-3" />;
      palette =
        'bg-gray-100 text-gray-700 ring-gray-200 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700';
      break;
    case 'leased':
      icon = <Loader2 className="h-3 w-3 animate-spin" />;
      palette =
        'bg-amber-100 text-amber-800 ring-amber-200 dark:bg-amber-900/40 dark:text-amber-200 dark:ring-amber-800/60';
      break;
    case 'completed_pass':
      icon = <CheckCircle2 className="h-3 w-3" />;
      palette =
        'bg-green-100 text-green-800 ring-green-200 dark:bg-green-900/40 dark:text-green-200 dark:ring-green-800/60';
      break;
    case 'completed_fail':
      icon = <XCircle className="h-3 w-3" />;
      palette =
        'bg-red-100 text-red-800 ring-red-200 dark:bg-red-900/40 dark:text-red-200 dark:ring-red-800/60';
      break;
    case 'completed_skipped':
      icon = <MinusCircle className="h-3 w-3" />;
      palette =
        'bg-gray-100 text-gray-600 ring-gray-200 dark:bg-gray-800 dark:text-gray-400 dark:ring-gray-700';
      break;
    case 'abandoned':
      icon = <Ban className="h-3 w-3" />;
      palette =
        'bg-zinc-200 text-zinc-700 ring-zinc-300 dark:bg-zinc-800 dark:text-zinc-300 dark:ring-zinc-700';
      break;
  }
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset',
        palette,
      )}
    >
      {icon}
      {STATE_LABEL[state]}
    </span>
  );
}

export function LiveProgressGrid({ identity, run, units }: LiveProgressGridProps) {
  // Seed local row state from the snapshot so newer event payloads can
  // overwrite it without losing the initial counts.
  const [rows, setRows] = useState<Map<string, UnitRowState>>(() => {
    const next = new Map<string, UnitRowState>();
    for (const u of units) next.set(u.unit_id, { unit: u });
    return next;
  });

  // Re-seed when the snapshot input changes (e.g. when a new run loads).
  useEffect(() => {
    setRows(() => {
      const next = new Map<string, UnitRowState>();
      for (const u of units) next.set(u.unit_id, { unit: u });
      return next;
    });
  }, [units]);

  // Subscribe to live updates for this identity. The subscription helper
  // returns its own unsubscribe; we proxy unmount to it so we leave no
  // dangling listeners.
  useEffect(() => {
    if (!subscribeToOrchestrationRun) return;
    const unsubscribe = subscribeToOrchestrationRun(identity, (event) => {
      setRows((prev) => {
        const next = new Map(prev);
        const stamp = event.timestamp;
        switch (event.type) {
          case 'orchestration.unit.leased': {
            for (const unitId of event.payload.unit_ids) {
              const existing = next.get(unitId);
              if (!existing) continue;
              next.set(unitId, {
                unit: {
                  ...existing.unit,
                  state: 'leased',
                  lease_count: existing.unit.lease_count + 1,
                },
                latestWorker: event.payload.gh_job_name,
                latestEventAt: stamp,
              });
            }
            break;
          }
          case 'orchestration.unit.completed': {
            const existing = next.get(event.payload.unit_id);
            if (!existing) break;
            const isFail = event.payload.outcome === 'completed_fail';
            next.set(event.payload.unit_id, {
              unit: {
                ...existing.unit,
                state: event.payload.outcome,
                fail_count: isFail ? existing.unit.fail_count + 1 : existing.unit.fail_count,
              },
              latestWorker: existing.latestWorker,
              latestEventAt: stamp,
            });
            break;
          }
          case 'orchestration.lease.expired': {
            for (const unitId of event.payload.reclaimed_unit_ids) {
              const existing = next.get(unitId);
              if (!existing) continue;
              next.set(unitId, {
                unit: { ...existing.unit, state: 'pending' },
                latestWorker: existing.latestWorker,
                latestEventAt: stamp,
              });
            }
            break;
          }
          default:
            break;
        }
        return next;
      });
    });
    return () => {
      unsubscribe?.();
    };
  }, [identity]);

  const ordered = useMemo(() => {
    return [...rows.values()].sort((a, b) => a.unit.dispatch_seq - b.unit.dispatch_seq);
  }, [rows]);

  if (ordered.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-gray-200 bg-gray-50 py-10 text-gray-400 dark:border-gray-700 dark:bg-gray-900/40 dark:text-gray-500">
        <Inbox className="mb-2 h-8 w-8" />
        <p className="text-sm">Awaiting unit dispatch</p>
        <p className="mt-1 text-xs">
          {run.total_units > 0
            ? `Run accepted ${run.total_units} unit(s); waiting for first worker checkout.`
            : 'No units have been submitted to this run yet.'}
        </p>
      </div>
    );
  }

  return (
    <div className="overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
      <table className="min-w-full divide-y divide-gray-200 text-sm dark:divide-gray-700">
        <thead className="bg-gray-50 dark:bg-gray-900/50">
          <tr className="text-left text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            <th scope="col" className="w-10 px-3 py-2">
              #
            </th>
            <th scope="col" className="px-3 py-2">
              Spec paths
            </th>
            <th scope="col" className="px-3 py-2">
              State
            </th>
            <th scope="col" className="px-3 py-2">
              Leases
            </th>
            <th scope="col" className="px-3 py-2">
              Fails
            </th>
            <th scope="col" className="px-3 py-2">
              Latest worker
            </th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-200 bg-white dark:divide-gray-800 dark:bg-gray-950">
          {ordered.map(({ unit, latestWorker }) => (
            <tr key={unit.unit_id}>
              <td className="px-3 py-2 font-mono text-xs text-gray-500 dark:text-gray-400">
                {unit.dispatch_seq}
              </td>
              <td className="px-3 py-2 font-mono text-xs text-gray-700 dark:text-gray-300 break-all">
                {unit.spec_path}
              </td>
              <td className="px-3 py-2">
                <StateBadge state={unit.state} />
              </td>
              <td className="px-3 py-2 tabular-nums text-gray-700 dark:text-gray-300">
                {unit.lease_count}
              </td>
              <td className="px-3 py-2 tabular-nums">
                {unit.fail_count > 0 ? (
                  <span className="font-medium text-red-600 dark:text-red-400">
                    {unit.fail_count}
                  </span>
                ) : (
                  <span className="text-gray-400 dark:text-gray-600">—</span>
                )}
              </td>
              <td className="px-3 py-2 text-xs text-gray-600 dark:text-gray-400">
                {latestWorker ?? <span className="text-gray-400 dark:text-gray-600">—</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
