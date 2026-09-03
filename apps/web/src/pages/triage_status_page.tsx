import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { ListTodo, BellRing, Gauge } from 'lucide-react';
import { useStabilizationQueue, useAlertEvaluation } from '@/services/api';

/**
 * The triage status surface: the stabilization queue head (W14) and the dry
 * alert evaluation (W7). Read-only by design: writes go through the
 * authenticated API by CI jobs and maintainers.
 */
export function TriageStatusPage() {
  const [params] = useSearchParams();
  const repo = params.get('repo') || 'mattermost';
  const [repoInput, setRepoInput] = useState(repo);
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
            The stabilization queue and the dry alert view.
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
          <ListTodo className="size-4" />
          Stabilization queue
        </h2>
        {queue.data ? (
          <div className="mt-2 space-y-2 text-sm">
            <div>
              <p className="text-xs font-medium text-neutral-500">
                Ranked by blast radius — distinct PRs broken, then master failures
              </p>
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
