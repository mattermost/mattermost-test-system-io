import { Link, useSearchParams } from 'react-router-dom';
import { Activity, ChevronRight } from 'lucide-react';
import { useTriageHealth } from '@/services/triage';

export const panel =
  'rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800';
export const inputStyle =
  'rounded border border-gray-300 bg-white px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100';
export const buttonStyle =
  'rounded bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50';
export function TriageStatus({ value }: { value: string }) {
  const color = ['passed', 'healthy', 'SUCCESS'].includes(value)
    ? 'bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-200'
    : ['failed', 'broken', 'FAILURE'].includes(value)
      ? 'bg-red-100 text-red-800 dark:bg-red-950 dark:text-red-200'
      : 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-200';
  return (
    <span className={`inline-block rounded px-2 py-1 text-xs font-medium ${color}`}>{value}</span>
  );
}
export function RunSparkline({ statuses }: { statuses: string[] }) {
  return (
    <svg
      role="img"
      aria-label={`Last ${statuses.length} trunk runs: ${statuses.join(', ')}`}
      width="120"
      height="24"
      viewBox="0 0 120 24"
    >
      {statuses.map((status, i) => (
        <rect
          key={i}
          x={i * 4}
          y={status === 'passed' ? 12 : 3}
          width="3"
          height={status === 'passed' ? 10 : 19}
          fill={
            status === 'passed'
              ? '#16a34a'
              : status === 'flaky'
                ? '#d97706'
                : status === 'skipped'
                  ? '#6b7280'
                  : '#dc2626'
          }
        >
          <title>{status}</title>
        </rect>
      ))}
    </svg>
  );
}
export function TriageHealthPage() {
  const [params, setParams] = useSearchParams();
  const query = useTriageHealth(params);
  const rows = query.data?.items ?? [];
  const filter = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    next.delete('cursor');
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next);
  };
  return (
    <div className="space-y-5 text-gray-900 dark:text-gray-100">
      <div>
        <h2 className="flex items-center gap-2 text-2xl font-semibold">
          <Activity />
          Test health
        </h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          Trunk history by test and lane. Required CI checks remain unchanged in shadow mode.
        </p>
      </div>
      <form className={`${panel} flex flex-wrap gap-3`} onSubmit={(e) => e.preventDefault()}>
        <input
          aria-label="Repository"
          placeholder="Repository"
          className={inputStyle}
          value={params.get('repository') ?? ''}
          onChange={(e) => filter('repository', e.target.value)}
        />
        <select
          aria-label="Framework"
          className={inputStyle}
          value={params.get('framework') ?? ''}
          onChange={(e) => filter('framework', e.target.value)}
        >
          <option value="">All frameworks</option>
          {['playwright', 'detox', 'cypress', 'maestro'].map((v) => (
            <option key={v}>{v}</option>
          ))}
        </select>
        <input
          aria-label="Lane"
          placeholder="Lane"
          className={inputStyle}
          value={params.get('lane') ?? ''}
          onChange={(e) => filter('lane', e.target.value)}
        />
        <select
          aria-label="Classification"
          className={inputStyle}
          value={params.get('classification') ?? ''}
          onChange={(e) => filter('classification', e.target.value)}
        >
          <option value="">All classifications</option>
          {['healthy', 'flaky', 'broken', 'unstable', 'unknown', 'retired'].map((v) => (
            <option key={v}>{v}</option>
          ))}
        </select>
        <input
          aria-label="Search tests"
          placeholder="Search title or file"
          className={inputStyle}
          value={params.get('q') ?? ''}
          onChange={(e) => filter('q', e.target.value)}
        />
      </form>
      {query.isLoading && <p role="status">Loading health…</p>}
      {query.error && (
        <p role="alert" className="text-red-600">
          {query.error.message}
        </p>
      )}
      {query.data && (
        <div className={`${panel} overflow-x-auto`}>
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b dark:border-gray-700">
                <th className="pb-3">Test</th>
                <th>Lane / branch</th>
                <th>Health</th>
                <th>Instability</th>
                <th>Last 30 trunk runs</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr
                  key={`${row.identity_id}/${row.lane}/${row.base_ref}`}
                  className="border-b last:border-0 dark:border-gray-700"
                >
                  <td className="max-w-md py-3 pr-4">
                    <Link
                      className="font-medium text-blue-600 hover:underline dark:text-blue-400"
                      to={`/triage/tests/${row.identity_id}`}
                    >
                      {row.full_title}
                    </Link>
                    <p className="mt-1 break-all text-xs text-gray-500">
                      {row.repository} · {row.file}
                    </p>
                  </td>
                  <td className="pr-4">
                    {row.lane || 'default'}
                    <p className="text-xs text-gray-500">{row.base_ref}</p>
                  </td>
                  <td className="pr-4">
                    <TriageStatus value={row.classification} />
                  </td>
                  <td>
                    {(row.instability_rate * 100).toFixed(1)}%
                    <p className="text-xs text-gray-500">{row.window_runs} runs</p>
                  </td>
                  <td>
                    <RunSparkline statuses={row.history ?? []} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {rows.length === 0 && (
            <p className="py-6 text-center text-gray-500">
              No health evidence matches these filters.
            </p>
          )}
        </div>
      )}
      <div className="flex gap-3">
        {params.has('cursor') && (
          <button className={buttonStyle} onClick={() => filter('cursor', '')}>
            First page
          </button>
        )}
        {query.data?.next_cursor && (
          <button
            className={`${buttonStyle} flex items-center gap-1`}
            onClick={() => {
              const next = new URLSearchParams(params);
              next.set('cursor', query.data.next_cursor);
              setParams(next);
            }}
          >
            Next page <ChevronRight size={16} />
          </button>
        )}
      </div>
    </div>
  );
}
