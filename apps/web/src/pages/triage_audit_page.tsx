import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Loader2,
  ClipboardCheck,
  ThumbsUp,
  ThumbsDown,
  Eye,
  EyeOff,
  ShieldCheck,
  AlertTriangle,
  GitCommit,
  FlaskConical,
} from 'lucide-react';
import {
  useAuditSample,
  useAuditAgreement,
  useSubmitAuditReview,
  type AuditSampleItem,
  type AuditVerdictDetail,
} from '@/services/api';

/**
 * W3 — blind waiver audit. The server omits the AI verdict from the sample
 * payload; this page renders exactly what the server sends and reveals the
 * verdict only after the reviewer has submitted their own call. The reveal
 * comes from the submit response (and the authenticated item detail), never
 * from a second unauthenticated fetch.
 */
export function TriageAuditPage() {
  const [params] = useSearchParams();
  const repo = params.get('repo') || 'mattermost';
  const sample = useAuditSample(repo);
  const agreement = useAuditAgreement(repo);
  const submit = useSubmitAuditReview();
  const [revealed, setRevealed] = useState<Record<string, AuditVerdictDetail | null>>({});

  const strataCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const item of sample.data?.items ?? []) {
      counts[item.stratum] = (counts[item.stratum] ?? 0) + 1;
    }
    return counts;
  }, [sample.data]);

  const callIt = (item: AuditSampleItem, humanAgree: boolean) => {
    submit.mutate(
      { verdict_id: item.verdict_id, human_agree: humanAgree },
      {
        onSuccess: (data) => {
          setRevealed((prev) => ({ ...prev, [item.verdict_id]: data.ai_verdict }));
        },
      },
    );
  };

  return (
    <div className="mx-auto max-w-4xl px-4 py-8">
      <header className="mb-6">
        <h1 className="flex items-center gap-2 text-2xl font-semibold">
          <ClipboardCheck className="size-6" />
          Blind waiver audit
        </h1>
        <p className="mt-1 text-sm text-neutral-600 dark:text-neutral-400">
          Call each waived failure as you see it — the AI verdict is hidden until you submit.
          Agreement rate gates how much authority triage keeps.
        </p>
      </header>

      <div className="mb-6 flex flex-wrap items-center gap-2 text-sm">
        <label className="font-medium" htmlFor="audit-repo">
          Repo
        </label>
        <input
          id="audit-repo"
          defaultValue={repo}
          className="rounded border border-neutral-300 bg-transparent px-2 py-1 dark:border-neutral-700"
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              const value = (e.target as HTMLInputElement).value.trim();
              window.location.search = value ? `?repo=${encodeURIComponent(value)}` : '';
            }
          }}
        />
      </div>

      {agreement.data && (
        <section className="mb-6 rounded-lg border border-neutral-200 p-4 dark:border-neutral-800">
          <h2 className="flex items-center gap-2 font-medium">
            <ShieldCheck className="size-4" />
            Agreement — pooled {agreement.data.pooled_weeks} weeks
          </h2>
          <div className="mt-2 flex items-baseline gap-3">
            <span
              className={`text-3xl font-semibold ${
                agreement.data.audit_agreement_rate >= 0.95
                  ? 'text-green-600 dark:text-green-400'
                  : agreement.data.audit_agreement_rate >= 0.9
                    ? 'text-yellow-600 dark:text-yellow-400'
                    : 'text-red-600 dark:text-red-400'
              }`}
            >
              {(agreement.data.audit_agreement_rate * 100).toFixed(1)}%
            </span>
            <span className="text-sm text-neutral-500">
              {agreement.data.agree}/{agreement.data.reviews} reviews agree
            </span>
          </div>
          <div className="mt-2 flex flex-wrap gap-2 text-xs">
            {agreement.data.per_week.map((w) => (
              <span
                key={w.week_start}
                className="rounded bg-neutral-100 px-2 py-1 dark:bg-neutral-900"
              >
                {w.week_start}: {(w.agreement_rate * 100).toFixed(0)}% ({w.reviews})
              </span>
            ))}
          </div>
        </section>
      )}

      {sample.isLoading && (
        <div className="flex items-center gap-2 text-neutral-500">
          <Loader2 className="size-4 animate-spin" /> Sampling waived failures…
        </div>
      )}

      {sample.data && (
        <div className="mb-4 flex flex-wrap items-center gap-2 text-xs text-neutral-500">
          <span>
            {sample.data.items.length}/{sample.data.target_size} sampled · pool{' '}
            {sample.data.pool_size}
          </span>
          <span className="flex items-center gap-1">
            <FlaskConical className="size-3" />
            {Object.entries(strataCounts)
              .map(([k, v]) => `${k}=${v}`)
              .join(' · ')}
          </span>
          {sample.data.shortfall > 0 && (
            <span className="flex items-center gap-1 text-yellow-600 dark:text-yellow-400">
              <AlertTriangle className="size-3" />
              small pool — sampled everything available ({sample.data.shortfall} short)
            </span>
          )}
        </div>
      )}

      <div className="space-y-4">
        {sample.data?.items.map((item) => (
          <article
            key={item.verdict_id}
            className="rounded-lg border border-neutral-200 p-4 dark:border-neutral-800"
          >
            <div className="flex flex-wrap items-center gap-2 text-xs text-neutral-500">
              <span className="rounded bg-neutral-100 px-2 py-0.5 dark:bg-neutral-900">
                {item.stratum}
              </span>
              {item.external_test_id && <span className="font-mono">{item.external_test_id}</span>}
              {item.gh_pr_number != null && <span>#{item.gh_pr_number}</span>}
              <span className="flex items-center gap-1">
                <GitCommit className="size-3" />
                {item.commit_sha.slice(0, 8)}
              </span>
              <span>{item.branch}</span>
            </div>

            <ul className="mt-2 list-disc pl-4 text-sm text-neutral-600 dark:text-neutral-400">
              {item.evidence.slice(0, 4).map((e, i) => (
                <li key={i}>{summarizeEvidence(e)}</li>
              ))}
              {item.evidence.length === 0 && <li>no evidence recorded</li>}
            </ul>

            {revealed[item.verdict_id] ? (
              <div className="mt-3 rounded border border-dashed border-neutral-300 p-3 text-sm dark:border-neutral-700">
                <p className="flex items-center gap-1 font-medium">
                  <Eye className="size-4" />
                  AI said: {revealed[item.verdict_id]!.verdict} (
                  {(revealed[item.verdict_id]!.confidence * 100).toFixed(0)}% confidence)
                </p>
                {revealed[item.verdict_id]!.root_cause && (
                  <p className="mt-1 text-neutral-600 dark:text-neutral-400">
                    {revealed[item.verdict_id]!.root_cause}
                  </p>
                )}
              </div>
            ) : (
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  disabled={submit.isPending || item.reviewed}
                  onClick={() => callIt(item, true)}
                  className="flex items-center gap-1 rounded bg-green-600 px-3 py-1.5 text-sm text-white disabled:opacity-50"
                >
                  <ThumbsUp className="size-4" /> Waive was right
                </button>
                <button
                  type="button"
                  disabled={submit.isPending || item.reviewed}
                  onClick={() => callIt(item, false)}
                  className="flex items-center gap-1 rounded bg-red-600 px-3 py-1.5 text-sm text-white disabled:opacity-50"
                >
                  <ThumbsDown className="size-4" /> Should have stayed red
                </button>
                {item.reviewed && (
                  <span className="flex items-center gap-1 text-xs text-neutral-500">
                    <EyeOff className="size-3" /> you already called this one
                  </span>
                )}
              </div>
            )}
          </article>
        ))}
        {sample.data && sample.data.items.length === 0 && (
          <p className="text-sm text-neutral-500">
            No waived failures in the trailing week — nothing to audit.
          </p>
        )}
      </div>
    </div>
  );
}

function summarizeEvidence(e: Record<string, unknown>): string {
  const kind = String(e.kind ?? e.type ?? 'evidence');
  const detail = e.detail ?? e.reason ?? e.message ?? '';
  return detail ? `${kind}: ${String(detail)}` : kind;
}
