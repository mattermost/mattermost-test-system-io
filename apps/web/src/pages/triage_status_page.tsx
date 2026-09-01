import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Loader2, Activity, Clock, ListTodo, BellRing, Gauge } from 'lucide-react';
import {
  useTriagePhase,
  useSlaReport,
  useStabilizationQueue,
  useAlertEvaluation,
} from '@/services/api';

const PHASES = ['shadow', 'PR gate', 'master gate', 'self-healing'];

/**
 * The triage status surface: rollout phase, past-SLA list (the weekly review's
 * one-click view — W15c), the stabilization queue head (W14), and the dry
 * alert evaluation (W7). Read-only by design: writes go through the
 * authenticated API by CI jobs and maintainers.
 */
export function TriageStatusPage() {
  const [params] = useSearchParams();
  const repo = params.get('repo') || 'mattermost';
  const [repoInput, setRepoInput] = useState(repo);
  const phase = useTriagePhase();
  const sla = useSlaReport(repo);
  const queue = useStabilizationQueue(repo);
  const alerts = useAlertEvaluation(repo);

  const submitRepo = () => {
    const value = repoInput.trim();
    window.location.search = value ? `?repo=${encodeURIComponent(value)}` : '';
  };

  return (
    <div className="mx-auto max-w-5xl px-4 py-8">
      <header className="mb-6 flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-semibold">
            <Gauge className="size-6" />
            Triage status
          </h1>
          <p className="mt-1 text-sm text-neutral-600 dark:text-neutral-400">
            Rollout phase, SLA clocks, stabilization queue, and the dry alert view.
          </p>
        </div>
        <div className="flex items-center gap-2 text-sm">
          <label className="font-medium" htmlFor="triage-repo">
            Repo
          </label>
          <input
            id="triage-repo"
            value={repoInput}
            onChange={(e) => setRepoInput(e.target.value)}
            className="rounded border border-neutral-300 bg-transparent px-2 py-1 dark:border-neutral-700"
            onKeyDown={(e) => {
              if (e.key === 'Enter') submitRepo();
            }}
          />
          <button
            type="button"
            onClick={submitRepo}
            className="rounded bg-neutral-800 px-2 py-1 text-white dark:bg-neutral-200 dark:text-black"
          >
            View
          </button>
        </div>
      </header>

      <section className="mb-6 rounded-lg border border-neutral-200 p-4 dark:border-neutral-800">
        <h2 className="flex items-center gap-2 font-medium">
          <Activity className="size-4" />
          Rollout phase
        </h2>
        {phase.isLoading ? (
          <div className="mt-2 flex items-center gap-2 text-sm text-neutral-500">
            <Loader2 className="size-4 animate-spin" /> loading…
          </div>
        ) : phase.data ? (
          <div className="mt-2 flex flex-wrap items-baseline gap-3">
            <span className="text-3xl font-semibold">{phase.data.phase}</span>
            <span className="text-sm font-medium">{PHASES[phase.data.phase]}</span>
            <span className="text-xs text-neutral-500">
              set by {phase.data.updated_by} · {new Date(phase.data.updated_at).toLocaleString()}
            </span>
          </div>
        ) : null}
      </section>

      <section className="mb-6 rounded-lg border border-neutral-200 p-4 dark:border-neutral-800">
        <h2 className="flex items-center gap-2 font-medium">
          <Clock className="size-4" />
          Past SLA {sla.data && sla.data.entries.length > 0 && `(${sla.data.entries.length})`}
        </h2>
        <p className="mt-1 text-xs text-neutral-500">
          The weekly review list — flag2 notifies the owning lead; flag1 lands on the review agenda.
        </p>
        {sla.data && sla.data.entries.length > 0 ? (
          <table className="mt-3 w-full text-sm">
            <thead>
              <tr className="text-left text-xs text-neutral-500">
                <th className="py-1">Test</th>
                <th>Verdict</th>
                <th>Age / limit</th>
                <th>State</th>
              </tr>
            </thead>
            <tbody>
              {sla.data.entries.map((e) => (
                <tr
                  key={e.verdict_id}
                  className="border-t border-neutral-100 dark:border-neutral-900"
                >
                  <td className="py-1 font-mono text-xs">{e.external_test_id ?? '—'}</td>
                  <td>{e.verdict}</td>
                  <td>
                    {e.age_days}d / {e.limit_days}d
                  </td>
                  <td>
                    <span
                      className={`rounded px-1.5 py-0.5 text-xs ${
                        e.state === 'flag2'
                          ? 'bg-red-100 text-red-700 dark:bg-red-900/50 dark:text-red-300'
                          : e.state === 'flag1'
                            ? 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/50 dark:text-yellow-300'
                            : 'bg-neutral-100 dark:bg-neutral-900'
                      }`}
                    >
                      {e.state}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p className="mt-2 text-sm text-neutral-500">
            {sla.isLoading ? 'loading…' : 'No open SLA clocks — nothing past its limit.'}
          </p>
        )}
      </section>

      <section className="mb-6 rounded-lg border border-neutral-200 p-4 dark:border-neutral-800">
        <h2 className="flex items-center gap-2 font-medium">
          <ListTodo className="size-4" />
          Stabilization queue
        </h2>
        {queue.data ? (
          <div className="mt-2 space-y-2 text-sm">
            {queue.data.promoted.length > 0 && (
              <div>
                <p className="text-xs font-medium text-neutral-500">Promoted (guard-filed)</p>
                {queue.data.promoted.map((e) => (
                  <p key={e.test_id} className="font-mono text-xs">
                    {e.test_id} — {e.promotion_source}: {e.promotion_reason}
                  </p>
                ))}
              </div>
            )}
            <div>
              <p className="text-xs font-medium text-neutral-500">Ranked (most unstable first)</p>
              {queue.data.ranked.length === 0 ? (
                <p className="text-sm text-neutral-500">queue empty</p>
              ) : (
                queue.data.ranked.map((e) => (
                  <p key={e.test_id} className="font-mono text-xs">
                    {e.test_id} — {e.failed ?? '?'}/{e.runs ?? '?'} failing
                    {e.flips ? `, ${e.flips} flips` : ''}
                  </p>
                ))
              )}
            </div>
          </div>
        ) : (
          <p className="mt-2 text-sm text-neutral-500">loading…</p>
        )}
      </section>

      <section className="rounded-lg border border-neutral-200 p-4 dark:border-neutral-800">
        <h2 className="flex items-center gap-2 font-medium">
          <BellRing className="size-4" />
          Alert evaluation (dry)
        </h2>
        <p className="mt-1 text-xs text-neutral-500">
          What the master alerting rules would fire right now. The firing job is POST
          /triage/alerts/evaluate — this view never posts.
        </p>
        {alerts.data ? (
          alerts.data.alerts.length === 0 ? (
            <p className="mt-2 text-sm text-green-600 dark:text-green-400">No rules firing.</p>
          ) : (
            <ul className="mt-2 list-disc pl-4 text-sm">
              {alerts.data.alerts.map((a, i) => (
                <li key={i}>
                  <span className="font-mono text-xs">{a.rule}</span> — {a.subject} ({a.severity})
                </li>
              ))}
            </ul>
          )
        ) : (
          <p className="mt-2 text-sm text-neutral-500">loading…</p>
        )}
      </section>
    </div>
  );
}
