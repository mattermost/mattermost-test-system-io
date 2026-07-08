import { useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useReportDetail, useReportSuites } from '@/services/api';
import { TestSuitesView } from '@/components/test_suites_view';
import { Loader2, AlertCircle } from 'lucide-react';
import { Breadcrumb } from '@/components/breadcrumb';
import { ReportSummary } from '@/components/report_summary';
import { isRetestName } from '@/components/report_card_parts';
import { EnvironmentMetadataDisplay } from '@/components/report_card_parts/environment_metadata';
import { OrchestrationInlineSummary } from '@/components/orchestration_inline_summary';

export function ReportDetailPage() {
  const { id } = useParams<{ id: string }>();
  const reportId = id || '';
  const { data: report, isLoading, error } = useReportDetail(reportId);
  // Use the group ID for suites (reportId may be an individual report UUID)
  const groupId = report?.id || '';
  const {
    data: suitesData,
    isLoading: isLoadingSuites,
    error: suitesError,
  } = useReportSuites(groupId);

  // Split report entries by numbered vs retest shard for the summary "4+1"
  // display. Retest classification mirrors the server's regex on gh_job_name /
  // display_name.
  const reportCountSplit = useMemo(() => {
    const entries = report?.reports ?? [];
    let retest = 0;
    for (const e of entries) {
      if (isRetestName(e.gh_job_name) || isRetestName(e.display_name)) retest++;
    }
    return { numbered: entries.length - retest, retest };
  }, [report?.reports]);

  // Detect if URL points to an individual report (not the group)
  const isIndividualReport = report && report.id !== reportId;
  const individualReport = isIndividualReport
    ? report?.reports.find((r) => r.id === reportId)
    : null;

  // Filter suites to only the individual report when viewing one
  const filteredSuites =
    isIndividualReport && suitesData?.suites
      ? suitesData.suites.filter((s) => s.report_id === reportId)
      : suitesData?.suites || [];

  // Compute test stats from filtered suites
  const testStats = useMemo(() => {
    let passed = 0,
      failed = 0,
      skipped = 0,
      flaky = 0,
      total = 0,
      durationMs = 0;
    for (const s of filteredSuites) {
      passed += s.passed_count;
      failed += s.failed_count;
      skipped += s.skipped_count ?? 0;
      flaky += s.flaky_count ?? 0;
      total += s.passed_count + s.failed_count + (s.skipped_count ?? 0) + (s.flaky_count ?? 0);
      durationMs += s.duration_ms ?? 0;
    }
    return { passed, failed, skipped, flaky, total, durationMs };
  }, [filteredSuites]);

  // Group-level duration: the numbered + retest wall-clock spans the backend
  // already computes (min start → max end per shard batch), not a sum of
  // every shard's test durations — shards run in parallel, so summing them
  // (testStats.durationMs above) overstates elapsed time by roughly the
  // shard count (e.g. 5 parallel ~11m shards reads as "55m" instead of the
  // actual ~21m wall-clock). Only meaningful for the whole-group view;
  // an individual shard has no separate wall-clock field, so it keeps using
  // the summed per-suite duration as a reasonable single-shard proxy.
  const groupDurationMs =
    (report?.test_stats?.wall_clock_ms ?? 0) + (report?.test_stats?.retest_wall_clock_ms ?? 0);

  // Build URL segments from report data for breadcrumb and name link
  const urlParts = useMemo(() => {
    if (!report?.repository || !report?.branch || !report?.commit || !report?.name) return null;
    const repoName = report.repository.split('/').pop() || report.repository;
    const shortBranch = report.branch.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '');
    const prMatch = shortBranch.match(/^pr-(\d+)/i) || report.branch.match(/^refs\/pull\/(\d+)\//);
    const branchSegment = prMatch ? `pr-${prMatch[1]}` : shortBranch;
    const shortSha = report.commit.slice(0, 7);
    return { repoName, branchSegment, shortSha, name: report.name };
  }, [report]);

  const nameHref = urlParts
    ? `/reports/${encodeURIComponent(urlParts.repoName)}/${encodeURIComponent(urlParts.branchSegment)}/${urlParts.shortSha}/${encodeURIComponent(urlParts.name)}`
    : undefined;

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-8 w-8 animate-spin text-blue-600 dark:text-blue-400" />
      </div>
    );
  }

  if (error || !report) {
    return (
      <div className="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-900/20 dark:text-red-400">
        <p className="font-medium">Error loading report</p>
        <p className="text-sm">{error?.message || 'Report not found'}</p>
        <Link
          to="/"
          className="mt-4 inline-block text-sm text-red-600 hover:text-red-500 dark:text-red-400 dark:hover:text-red-300"
        >
          Back to reports
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <Breadcrumb
        items={[
          { label: 'Reports', to: '/reports' },
          ...(urlParts
            ? [
                {
                  label: urlParts.repoName,
                  to: `/reports/${encodeURIComponent(urlParts.repoName)}`,
                },
                {
                  label: urlParts.branchSegment,
                  to: `/reports/${encodeURIComponent(urlParts.repoName)}/${encodeURIComponent(urlParts.branchSegment)}`,
                },
                {
                  label: urlParts.shortSha,
                  to: `/reports/${encodeURIComponent(urlParts.repoName)}/${encodeURIComponent(urlParts.branchSegment)}/${urlParts.shortSha}`,
                },
                { label: report.id.slice(0, 13) },
              ]
            : [{ label: report.id.slice(0, 13) }]),
        ]}
      />

      {/* Header */}
      <ReportSummary
        testStatus={
          // Overall verdict is Passed / Failed only at this level —
          // flaky lives per-test-case; a run with flaky-but-passed
          // tests rolls up as Passed.
          testStats.total === 0 ? undefined : testStats.failed > 0 ? 'failed' : 'passed'
        }
        name={report.name}
        nameHref={nameHref}
        secondaryName={individualReport?.display_name}
        secondaryNameFailed={!!individualReport && testStats.failed > 0}
        passed={testStats.passed}
        failed={testStats.failed}
        flaky={testStats.flaky}
        skipped={testStats.skipped}
        total={testStats.total}
        durationMs={
          !isIndividualReport && groupDurationMs > 0
            ? groupDurationMs
            : testStats.durationMs > 0
              ? testStats.durationMs
              : undefined
        }
        beginAt={report.orchestration?.durations?.begin_at}
        firstTestAt={report.orchestration?.durations?.first_test_at}
        firstRetestAt={report.orchestration?.durations?.first_retest_at}
        lastTestAt={report.orchestration?.durations?.last_test_at}
        createdAt={individualReport?.created_at || report.created_at}
        framework={report.framework}
        reportCount={isIndividualReport ? undefined : reportCountSplit.numbered}
        retestReportCount={isIndividualReport ? undefined : reportCountSplit.retest}
        repository={report.repository}
        branch={report.branch}
        commit={report.commit}
        ghPrNumber={report.gh_pr_number}
        ghRunId={report.gh_run_id}
        ghJobId={individualReport?.gh_job_id}
      />

      {/* Live orchestration progress (when this report_group has a matching orchestration_run).
          Only rendered while the run is still in flight; once it terminates the report
          summary above already conveys the outcome and this strip becomes redundant. */}
      {report.orchestration?.status === 'in_progress' && (
        <div className="px-4 sm:px-6">
          <OrchestrationInlineSummary orchestration={report.orchestration} />
        </div>
      )}

      {/* Environment metadata (tool + server info) */}
      {report.environment_metadata && (
        <div className="px-4 sm:px-6 pb-4">
          <EnvironmentMetadataDisplay metadata={report.environment_metadata} />
        </div>
      )}

      {/* Test Results */}
      <div>
        {isLoadingSuites ? (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="h-8 w-8 animate-spin text-blue-600 dark:text-blue-400" />
          </div>
        ) : suitesError ? (
          <div className="rounded-lg bg-yellow-50 border border-yellow-200 p-4 text-yellow-800 dark:bg-yellow-900/20 dark:border-yellow-800 dark:text-yellow-300">
            <p className="font-medium">Unable to load test results</p>
            <p className="text-sm mt-1">{suitesError?.message}</p>
          </div>
        ) : (
          <TestSuitesView
            reportId={report.id}
            suites={filteredSuites}
            title={
              individualReport ? `${report.name} / ${individualReport.display_name}` : report.name
            }
            reports={isIndividualReport ? undefined : suitesData?.reports}
          />
        )}
      </div>

      {/* Error Message */}
      {report.error_message && (
        <div className="rounded-lg bg-red-50 border border-red-200 p-4 dark:bg-red-900/20 dark:border-red-800">
          <div className="flex items-start gap-3">
            <AlertCircle className="h-5 w-5 text-red-500 dark:text-red-400 flex-shrink-0 mt-0.5" />
            <div>
              <p className="text-sm font-medium text-red-800 dark:text-red-300">Report Error</p>
              <p className="mt-1 text-sm text-red-700 dark:text-red-400">{report.error_message}</p>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
