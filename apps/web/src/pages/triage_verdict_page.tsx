import { Link, useParams } from 'react-router-dom';
import { AlertTriangle, ShieldCheck } from 'lucide-react';
import { useTriageVerdict } from '@/services/triage';
import { panel, TriageStatus } from '@/pages/triage_health_page';
import type { TriageFinding } from '@/types/triage';

function Findings({ title, findings }: { title: string; findings: TriageFinding[] }) {
  if (!findings.length) return null;
  return (
    <section className={panel}>
      <h3 className="mb-3 text-lg font-semibold">
        {title} ({findings.length})
      </h3>
      <div className="space-y-4">
        {findings.map((finding, index) => (
          <article
            key={`${finding.identity_id}/${index}`}
            className="border-t pt-3 dark:border-gray-700"
          >
            <div className="flex flex-wrap items-center gap-2">
              <TriageStatus value={finding.class} />
              {finding.identity_id ? (
                <Link
                  className="font-medium text-blue-600 hover:underline dark:text-blue-400"
                  to={`/triage/tests/${finding.identity_id}`}
                >
                  {finding.full_title}
                </Link>
              ) : (
                <span className="font-medium">{finding.full_title}</span>
              )}
            </div>
            <p className="mt-2 text-sm">{finding.reason}</p>
            <p className="mt-1 text-xs text-gray-500">
              {finding.file} · {finding.lane || 'default lane'} · trunk: {finding.trunk.runs} runs,{' '}
              {finding.trunk.fails} failures, {finding.trunk.flaky} flakes · PR:{' '}
              {finding.pr.fail_count} failing executions
            </p>
            {finding.pr.error_excerpt && (
              <pre className="mt-2 whitespace-pre-wrap break-words rounded bg-gray-50 p-3 text-xs dark:bg-gray-900">
                {finding.pr.error_excerpt}
                {finding.pr.failure_locus ? `\n${finding.pr.failure_locus}` : ''}
              </pre>
            )}
          </article>
        ))}
      </div>
    </section>
  );
}
export function TriageVerdictPage() {
  const { id = '' } = useParams();
  const query = useTriageVerdict(id);
  if (query.isLoading) return <p role="status">Loading verdict…</p>;
  if (query.error)
    return (
      <p role="alert" className="text-red-600">
        {query.error.message}
      </p>
    );
  if (!query.data) return null;
  const v = query.data;
  const blocking = v.findings.filter((f) => f.blocking);
  const exonerated = v.findings.filter((f) => !f.blocking);
  return (
    <div className="space-y-5 text-gray-900 dark:text-gray-100">
      <Link to="/triage/health" className="text-sm text-blue-600 dark:text-blue-400">
        ← Test health
      </Link>
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-2xl font-semibold">CI triage verdict</h2>
        <TriageStatus value={v.verdict} />
      </div>
      {v.human_override && (
        <div className="rounded-lg border border-amber-300 bg-amber-50 p-4 text-amber-950 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-100">
          <p className="flex items-center gap-2 font-semibold">
            <ShieldCheck size={18} />
            Human override: {v.human_override.resulting_state}
          </p>
          <p className="mt-1 text-sm">
            {v.human_override.actor} applied {v.human_override.label} at{' '}
            {new Date(v.human_override.at).toLocaleString()}. The engine decision below is retained
            for audit.
          </p>
        </div>
      )}
      <section className={panel}>
        <div className="flex flex-wrap gap-6">
          <p>
            <strong>{v.mode}</strong>
            <span className="block text-xs text-gray-500">Policy mode</span>
          </p>
          <p>
            <strong>{(v.confidence * 100).toFixed(1)}%</strong>
            <span className="block text-xs text-gray-500">Confidence</span>
          </p>
          <p>
            <strong>
              {v.counts.passed} passed · {v.counts.failed} failed
            </strong>
            <span className="block text-xs text-gray-500">
              {v.counts.exonerated} exonerated · {v.counts.blocking} blocking · {v.counts.infra}{' '}
              infra
            </span>
          </p>
        </div>
        {v.reason && (
          <p className="mt-4 flex items-start gap-2 text-sm">
            <AlertTriangle className="shrink-0" size={18} />
            {v.reason}
          </p>
        )}
        <p className="mt-4 text-xs text-gray-500">
          {new Date(v.computed_at).toLocaleString()} · {v.engine_version} ·{' '}
          <Link className="text-blue-600 dark:text-blue-400" to={`/reports/g/${v.report_group_id}`}>
            Original report
          </Link>
        </p>
      </section>
      <Findings title="Blocking findings" findings={blocking} />
      <Findings title="Exonerated and neutral findings" findings={exonerated} />
      <section className={panel}>
        <h3 className="mb-3 font-semibold">Thresholds used</h3>
        <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {Object.entries(v.thresholds_used).map(([key, value]) => (
            <div key={key}>
              <dt className="text-xs text-gray-500">{key.replaceAll('_', ' ')}</dt>
              <dd className="text-sm font-medium">{value}</dd>
            </div>
          ))}
        </dl>
      </section>
    </div>
  );
}
