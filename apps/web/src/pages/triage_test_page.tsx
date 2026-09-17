import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Shield, ShieldOff } from 'lucide-react';
import { useAuth } from '@/contexts/auth_context';
import { useQuarantineMutation, useTriageIdentity, useTriageObservations } from '@/services/triage';
import { buttonStyle, inputStyle, panel, TriageStatus } from '@/pages/triage_health_page';

export function TriageTestPage() {
  const { identityId = '' } = useParams();
  const query = useTriageIdentity(identityId);
  const { user } = useAuth();
  const [cursor, setCursor] = useState('');
  const history = useTriageObservations(identityId, cursor);
  const mutation = useQuarantineMutation();
  const [lane, setLane] = useState('');
  const [baseRef, setBaseRef] = useState('');
  const [issue, setIssue] = useState('');
  if (query.isLoading) return <p role="status">Loading test history…</p>;
  if (query.error)
    return (
      <p role="alert" className="text-red-600">
        {query.error.message}
      </p>
    );
  if (!query.data) return null;
  const { identity, health, active_quarantine: quarantine } = query.data;
  return (
    <div className="space-y-5 text-gray-900 dark:text-gray-100">
      <Link to="/triage/health" className="text-sm text-blue-600 dark:text-blue-400">
        ← Test health
      </Link>
      <div>
        <h2 className="text-2xl font-semibold">{identity.full_title}</h2>
        <p className="mt-1 break-all text-sm text-gray-500">
          {identity.repository} · {identity.file}
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {health.map((h) => (
          <section key={`${h.lane}/${h.base_ref}`} className={panel}>
            <h3 className="mb-2 font-semibold">
              {h.lane || 'Default lane'} · {h.base_ref}
            </h3>
            <TriageStatus value={h.classification} />
            <p className="mt-3 text-sm">
              {h.window_runs} runs · {h.pass_count} passed · {h.fail_count} failed · {h.flaky_count}{' '}
              flaky
            </p>
            <p className="mt-1 text-sm text-gray-500">
              {(h.instability_rate * 100).toFixed(1)}% smoothed instability
            </p>
          </section>
        ))}
      </div>
      <section className={panel}>
        <h3 className="mb-3 flex items-center gap-2 font-semibold">
          <Shield size={18} />
          Active quarantine
        </h3>
        {quarantine.length === 0 && (
          <p className="text-sm text-gray-500">This test is not quarantined.</p>
        )}
        {quarantine.map((entry) => (
          <div
            key={entry.id}
            className="flex flex-wrap items-center justify-between gap-3 border-b py-3 last:border-0 dark:border-gray-700"
          >
            <div>
              <p>
                {entry.lane ?? 'All lanes'} · {entry.base_ref} · {entry.source}: {entry.reason}
              </p>
              <p className="text-xs text-gray-500">
                Since {new Date(entry.created_at).toLocaleString()}
              </p>
              {entry.issue_url && (
                <a
                  href={entry.issue_url}
                  rel="noreferrer"
                  target="_blank"
                  className="text-sm text-blue-600 underline"
                >
                  Tracking issue
                </a>
              )}
            </div>
            {user?.role === 'admin' && (
              <button
                className={`${buttonStyle} flex items-center gap-1`}
                disabled={mutation.isPending}
                onClick={() => mutation.mutate({ release: entry.id })}
              >
                <ShieldOff size={14} />
                Release
              </button>
            )}
          </div>
        ))}
      </section>
      {user?.role === 'admin' && (
        <form
          className={`${panel} space-y-3`}
          onSubmit={(e) => {
            e.preventDefault();
            mutation.mutate({
              identity_id: identityId,
              lane: lane || null,
              base_ref: baseRef || health[0]?.base_ref || '',
              reason: 'manual',
              ...(issue ? { issue_url: issue } : {}),
            });
          }}
        >
          <h3 className="font-semibold">Open manual quarantine</h3>
          <div className="flex flex-wrap gap-3">
            <input
              className={inputStyle}
              aria-label="Quarantine lane"
              placeholder="All lanes"
              value={lane}
              onChange={(e) => setLane(e.target.value)}
            />
            <input
              className={inputStyle}
              aria-label="Base branch"
              placeholder={health[0]?.base_ref || 'Base branch'}
              value={baseRef}
              onChange={(e) => setBaseRef(e.target.value)}
              required={!health[0]?.base_ref}
            />
            <input
              className={inputStyle}
              aria-label="Tracking issue URL"
              type="url"
              placeholder="https://… (optional issue)"
              value={issue}
              onChange={(e) => setIssue(e.target.value)}
            />
            <button className={buttonStyle} disabled={mutation.isPending}>
              Open quarantine
            </button>
          </div>
          <p className="text-xs text-gray-500">
            Manual quarantines remain active until released or expired. Infrastructure failures
            cannot be quarantined.
          </p>
        </form>
      )}
      {mutation.error && (
        <p role="alert" className="text-red-600">
          {mutation.error.message}
        </p>
      )}
      <section className={`${panel} overflow-x-auto`}>
        <h3 className="mb-3 font-semibold">Observation timeline</h3>
        {history.isLoading && <p role="status">Loading observations…</p>}
        {history.error && <p role="alert">{history.error.message}</p>}
        <table className="w-full text-left text-sm">
          <thead>
            <tr>
              <th>Observed</th>
              <th>Lane / branch</th>
              <th>Result</th>
              <th>Evidence</th>
            </tr>
          </thead>
          <tbody>
            {history.data?.items.map((o) => (
              <tr key={o.id} className="border-b last:border-0 dark:border-gray-700">
                <td className="whitespace-nowrap py-3 pr-4">
                  <Link
                    to={`/reports/g/${o.report_group_id}`}
                    className="text-blue-600 dark:text-blue-400"
                  >
                    {new Date(o.observed_at).toLocaleString()}
                  </Link>
                  <p className="text-xs text-gray-500">{o.commit_sha.slice(0, 12)}</p>
                </td>
                <td className="pr-4">
                  {o.lane || 'default'}
                  <p className="text-xs text-gray-500">
                    {o.branch_kind}: {o.branch}
                  </p>
                </td>
                <td className="pr-4">
                  <TriageStatus value={o.status} />
                  {o.is_infra_stub && <p className="text-xs">Infrastructure stub</p>}
                </td>
                <td className="max-w-lg break-words py-3">
                  {o.error_excerpt}
                  <p className="text-xs text-gray-500">{o.failure_locus}</p>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {history.data?.items.length === 0 && (
          <p className="py-4 text-gray-500">No observations yet.</p>
        )}
        <div className="mt-4 flex gap-3">
          {cursor && (
            <button className={buttonStyle} onClick={() => setCursor('')}>
              Newest
            </button>
          )}
          {history.data?.next_cursor && (
            <button className={buttonStyle} onClick={() => setCursor(history.data.next_cursor)}>
              Older observations
            </button>
          )}
        </div>
      </section>
    </div>
  );
}
