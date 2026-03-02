import { useMemo } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { useQueries } from '@tanstack/react-query';
import {
  useConsolidatedResults,
  useReportDetail,
  useReportSuites,
  fetchReportDetail,
  fetchReportSuites,
} from '@/services/api';
import { Breadcrumb } from '@/components/breadcrumb';
import { TestSuitesView } from '@/components/test_suites_view';
import { RunAttemptSelector } from '@/components/run_attempt_selector';
import {
  Loader2,
  Inbox,
  AlertTriangle,
  Play,
  CheckCircle,
  AlertCircle,
  XCircle,
  ExternalLink,
} from 'lucide-react';
import { ReportSummary } from '@/components/report_summary';

const TIMED_OUT_THRESHOLD_MS = 3_600_000; // 1 hour

function formatDate(dateString: string): string {
  return new Date(dateString).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
}

export function FilteredReportPage() {
  const { repo, branch, commit, name } = useParams<{
    repo: string;
    branch: string;
    commit: string;
    name: string;
  }>();

  const [search_params] = useSearchParams();
  const run_attempt_param = search_params.get('run_attempt');
  const run_attempt = run_attempt_param ? parseInt(run_attempt_param, 10) : undefined;
  const gid = search_params.get('gid') || undefined;

  // Step 1: Get consolidated results to find contributing report IDs
  const { data, isLoading, error } = useConsolidatedResults(
    repo || '',
    branch || '',
    commit || '',
    name || '',
    run_attempt,
    gid,
  );

  // Step 2: Fetch all contributing reports details
  const contributingIds = data?.contributing_reports ?? [];
  const allReportQueries = useQueries({
    queries: contributingIds.map((id) => ({
      queryKey: ['report-detail', id],
      queryFn: () => fetchReportDetail(id),
      enabled: !!id,
    })),
  });

  // Find the latest contributing report (most recent created_at) for header metadata
  const latestReportId = useMemo(() => {
    let latestId = '';
    let latestTime = '';
    for (const q of allReportQueries) {
      if (!q.data) continue;
      if (!latestTime || q.data.created_at > latestTime) {
        latestTime = q.data.created_at;
        latestId = q.data.id;
      }
    }
    return latestId || (data?.contributing_reports?.[0] ?? '');
  }, [allReportQueries, data]);

  const { data: report } = useReportDetail(latestReportId);
  const { data: suitesData, isLoading: isLoadingSuites } = useReportSuites(latestReportId);

  const totalReports = report?.reports.length ?? 0;

  // Step 3: Fetch suites for ALL contributing reports (for chip status indicators)
  const allSuitesQueries = useQueries({
    queries: contributingIds.map((id) => ({
      queryKey: ['report', id, 'suites'],
      queryFn: () => fetchReportSuites(id),
      enabled: !!id,
    })),
  });

  // Build per-report test result status from suites across ALL contributing reports
  const reportTestStatus = useMemo(() => {
    const map = new Map<string, 'passed' | 'failed' | 'flaky'>();
    for (const q of allSuitesQueries) {
      if (!q.data?.suites) continue;
      for (const suite of q.data.suites) {
        if (!suite.report_id) continue;
        const current = map.get(suite.report_id) || 'passed';
        if (suite.failed_count > 0) {
          map.set(suite.report_id, 'failed');
        } else if ((suite.flaky_count ?? 0) > 0 && current !== 'failed') {
          map.set(suite.report_id, 'flaky');
        } else if (!map.has(suite.report_id)) {
          map.set(suite.report_id, 'passed');
        }
      }
    }
    return map;
  }, [allSuitesQueries]);

  // Group reports by run ID, then by run attempt within each run
  const reportsByRun = (() => {
    type ReportChipEntry = { id: string; reportId: string; display_name: string; status: string };
    type AttemptGroup = { attempt: string; reports: ReportChipEntry[] };
    type RunGroup = { runId: string; createdAt: string; attempts: AttemptGroup[] };

    const runMap = new Map<
      string,
      { createdAt: string; attemptMap: Map<string, ReportChipEntry[]> }
    >();

    for (const q of allReportQueries) {
      if (!q.data) continue;
      const runId = q.data.gh_run_id || 'unknown';
      const attempt = q.data.gh_run_attempt || '1';

      if (!runMap.has(runId)) {
        runMap.set(runId, { createdAt: q.data.created_at, attemptMap: new Map() });
      }
      const run = runMap.get(runId)!;
      if (!run.attemptMap.has(attempt)) run.attemptMap.set(attempt, []);
      for (const entry of q.data.reports) {
        run.attemptMap.get(attempt)!.push({
          id: entry.id,
          reportId: q.data.id,
          display_name: entry.display_name,
          status: entry.status,
        });
      }
    }

    // Build sorted run groups: runs sorted by createdAt descending, attempts ascending
    const groups: RunGroup[] = [];
    for (const [runId, { createdAt, attemptMap }] of runMap) {
      const attempts: AttemptGroup[] = [...attemptMap.keys()]
        .sort((a, b) => parseInt(a) - parseInt(b))
        .map((attempt) => ({ attempt, reports: attemptMap.get(attempt)! }));
      groups.push({ runId, createdAt, attempts });
    }
    groups.sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime());
    return groups;
  })();

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <div className="mb-4">
        <Breadcrumb
          items={[
            { label: 'Reports', to: '/reports' },
            { label: repo || '', to: `/reports/${encodeURIComponent(repo || '')}` },
            {
              label: branch || '',
              to: `/reports/${encodeURIComponent(repo || '')}/${encodeURIComponent(branch || '')}`,
            },
            {
              label: commit || '',
              to: `/reports/${encodeURIComponent(repo || '')}/${encodeURIComponent(branch || '')}/${encodeURIComponent(commit || '')}`,
            },
            { label: name || '' },
          ]}
        />
      </div>

      {/* Run attempt selector */}
      {data && data.available_run_attempts.length > 1 && (
        <div className="mb-4">
          <RunAttemptSelector
            available_attempts={data.available_run_attempts}
            current_attempt={run_attempt}
          />
        </div>
      )}

      {/* Loading state */}
      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
        </div>
      )}

      {/* Error state */}
      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-800 dark:bg-red-950/30">
          <div className="flex items-center gap-2 text-sm text-red-700 dark:text-red-400">
            <AlertTriangle className="h-4 w-4" />
            {error instanceof Error && error.message.includes('ambiguous')
              ? 'The short SHA is ambiguous — please use the full 40-character SHA.'
              : `Failed to load results: ${error instanceof Error ? error.message : 'Unknown error'}`}
          </div>
        </div>
      )}

      {/* Empty state */}
      {data && data.total_specs === 0 && (
        <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
          <Inbox className="h-12 w-12 mb-3" />
          <p className="text-sm">No matching reports</p>
          <p className="text-xs mt-1">
            No test results found for {repo}/{branch}/{commit}/{name}
          </p>
        </div>
      )}

      {/* Report header + suites view */}
      {data && data.total_specs > 0 && (
        <>
          {/* Header with report metadata */}
          {report && (
            <ReportSummary
              testStatus={
                data.overall_status === 'failed'
                  ? 'failed'
                  : data.overall_status === 'flaky'
                    ? 'flaky'
                    : 'passed'
              }
              name={name}
              passed={data.passed}
              failed={data.failed}
              flaky={data.flaky}
              skipped={data.skipped}
              total={data.total_specs}
              durationMs={data.duration_ms}
              createdAt={report.created_at}
              framework={report.framework}
              reportCount={totalReports}
              progressStatus={
                report.status === 'in_progress'
                  ? new Date(report.created_at).getTime() < Date.now() - TIMED_OUT_THRESHOLD_MS
                    ? 'timed_out'
                    : 'in_progress'
                  : 'completed'
              }
              repository={report.repository}
              branch={report.branch}
              commit={report.commit}
              ghPrNumber={report.gh_pr_number}
              ghRunId={report.gh_run_id}
            />
          )}

          {/* Test suites view */}
          {isLoadingSuites ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-8 w-8 animate-spin text-blue-600 dark:text-blue-400" />
            </div>
          ) : (
            latestReportId && (
              <TestSuitesView
                reportId={latestReportId}
                suites={suitesData?.suites || []}
                title={`${name} — ${branch}/${commit}`}
                reports={suitesData?.reports}
              />
            )
          )}

          {/* Reports */}
          {reportsByRun.length > 0 && (
            <div className="mt-4">
              <h2 className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Reports</h2>
              {reportsByRun.map(({ runId, createdAt, attempts }) => (
                <div key={runId} className="mb-3">
                  {reportsByRun.length > 1 && (
                    <div className="flex items-center gap-2 text-xs mb-1">
                      {report?.repository && runId !== 'unknown' ? (
                        <a
                          href={`https://github.com/${report.repository}/actions/runs/${runId}`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 font-medium text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300 transition-colors"
                        >
                          <Play className="h-3 w-3" />
                          Run {runId}
                          <ExternalLink className="h-3 w-3 opacity-50" />
                        </a>
                      ) : (
                        <span className="font-medium text-gray-600 dark:text-gray-300">
                          Run {runId}
                        </span>
                      )}
                      <span className="text-gray-400 dark:text-gray-500">
                        {formatDate(createdAt)}
                      </span>
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
                              {entry.display_name}
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
          )}
        </>
      )}
    </div>
  );
}
