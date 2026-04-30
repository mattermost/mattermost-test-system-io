/**
 * Per-shard chip cluster shown beneath the orchestration / report-group
 * summary. Each chip links to the per-shard upload's detail page; the
 * label splits a "<base>-<N>" `display_name` into the shared base text
 * and a numbered badge so an 8-worker matrix renders as a tidy row of
 * `orch-worker [1] orch-worker [2] …` instead of repeating the full
 * suffixed name on every chip.
 *
 * Color tracks the per-shard outcome aggregated from that shard's own
 * test_cases (red if any failed, yellow if any flaky, green if
 * complete with all-passed, blue spinner while still processing).
 *
 * Two callers feed this with already-shaped data: the Reports tab
 * embeds it inside its body, and the Combine / Dispatch tabs append
 * it after the orchestration tab so users always have a shortcut to a
 * specific worker's uploaded report regardless of which tab they're
 * viewing.
 */

import { CheckCircle, XCircle, AlertCircle, Loader2, Play, ExternalLink } from 'lucide-react';

export interface ContributingReportEntry {
  id: string;
  /** Parent report-group id; used for keying, not currently surfaced. */
  reportId: string;
  display_name: string;
  /** Server-side report row status (`processing` / `complete` / `failed` / …). */
  status: string;
}

export interface ContributingReportAttemptGroup {
  /** GH Actions run-attempt (string form, "1" / "2" / …). */
  attempt: string;
  reports: ContributingReportEntry[];
}

export interface ContributingReportRunGroup {
  /** GH Actions run id, or "unknown" when the report carries no run id. */
  runId: string;
  /** ISO timestamp of the earliest contributing report in this run. */
  createdAt: string;
  attempts: ContributingReportAttemptGroup[];
}

export interface ContributingReportsListProps {
  /** Pre-grouped report entries — one outer entry per gh_run_id, inner per run-attempt. */
  reportsByRun: ContributingReportRunGroup[];
  /**
   * Per-report aggregated test-result status (passed / failed / flaky),
   * keyed by report id. Used to color the chip beyond the report row's
   * own `status` field — a `complete` row with one failing test still
   * needs the red treatment.
   */
  reportTestStatus: Map<string, 'passed' | 'failed' | 'flaky'>;
  /**
   * Repository slug (`owner/repo`). When set, the per-run header turns
   * the run id into a link to the GitHub Actions run page.
   */
  repository?: string;
  /** Date formatter — passed in so the host page can keep a single locale-aware helper. */
  formatDate?: (iso: string) => string;
}

const defaultFormatDate = (iso: string) =>
  new Date(iso).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });

export function ContributingReportsList({
  reportsByRun,
  reportTestStatus,
  repository,
  formatDate = defaultFormatDate,
}: ContributingReportsListProps) {
  if (reportsByRun.length === 0) return null;
  return (
    <div className="mt-4">
      <h2 className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Reports</h2>
      {reportsByRun.map(({ runId, createdAt, attempts }) => (
        <div key={runId} className="mb-3">
          {reportsByRun.length > 1 && (
            <div className="flex items-center gap-2 text-xs mb-1">
              {repository && runId !== 'unknown' ? (
                <a
                  href={`https://github.com/${repository}/actions/runs/${runId}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 font-medium text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300 transition-colors"
                >
                  <Play className="h-3 w-3" />
                  Run {runId}
                  <ExternalLink className="h-3 w-3 opacity-50" />
                </a>
              ) : (
                <span className="font-medium text-gray-600 dark:text-gray-300">Run {runId}</span>
              )}
              <span className="text-gray-400 dark:text-gray-500">{formatDate(createdAt)}</span>
            </div>
          )}
          {attempts.map(({ attempt, reports }) => (
            <div key={attempt} className="mb-2">
              {attempts.length > 1 && (
                <p className="text-xs text-gray-500 dark:text-gray-400 mb-1 ml-1">
                  Run Attempt {attempt}
                </p>
              )}
              <div className="flex flex-wrap items-center gap-1.5">
                {reports.map((entry) => {
                  const testResult = reportTestStatus.get(entry.id);
                  const isFailed = testResult === 'failed';
                  const isFlaky = testResult === 'flaky';
                  const slotMatch = entry.display_name.match(/^(.*)-(\d+)$/);
                  const baseLabel = slotMatch ? slotMatch[1] : entry.display_name;
                  const slot = slotMatch ? slotMatch[2] : null;
                  return (
                    <a
                      key={entry.id}
                      href={`/reports/r/${entry.id}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className={`inline-flex items-center gap-1.5 px-2 py-1 rounded-md text-xs transition-colors ${
                        isFailed
                          ? 'bg-red-100 text-red-700 hover:bg-red-200 dark:bg-red-900/50 dark:text-red-300 dark:hover:bg-red-900/70'
                          : isFlaky
                            ? 'bg-yellow-100 text-yellow-700 hover:bg-yellow-200 dark:bg-yellow-900/50 dark:text-yellow-300 dark:hover:bg-yellow-900/70'
                            : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600'
                      }`}
                    >
                      {isFailed ? (
                        <XCircle className="h-3 w-3 text-red-500" />
                      ) : isFlaky ? (
                        <AlertCircle className="h-3 w-3 text-yellow-500" />
                      ) : entry.status === 'complete' ? (
                        <CheckCircle className="h-3 w-3 text-green-500" />
                      ) : (
                        <Loader2 className="h-3 w-3 animate-spin text-blue-500" />
                      )}
                      {baseLabel}
                      {slot && (
                        <span
                          className={`inline-flex h-4 min-w-4 items-center justify-center rounded px-0.5 text-[10px] font-semibold ${
                            isFailed
                              ? 'bg-red-200 text-red-800 dark:bg-red-800 dark:text-red-200'
                              : isFlaky
                                ? 'bg-yellow-200 text-yellow-800 dark:bg-yellow-800 dark:text-yellow-200'
                                : 'bg-gray-200 text-gray-800 dark:bg-gray-600 dark:text-gray-200'
                          }`}
                          title={entry.display_name}
                        >
                          {slot}
                        </span>
                      )}
                      <ExternalLink className="h-3 w-3 opacity-50" />
                    </a>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}
