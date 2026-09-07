import { useEffect, useMemo } from 'react';
import {
  useGroupedReports,
  useIndividualReports,
  useReportRepositories,
  useBranchFilters,
  type IndividualReportSummary,
} from '@/services/api';
import { RepoGroupCard } from '@/components/repo_group_card';
import {
  Loader2,
  Inbox,
  List,
  Layers,
  CheckCircle,
  XCircle,
  AlertCircle,
  Clock,
  GitBranch,
  GitCommit,
} from 'lucide-react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  formatDuration,
  calculatePassRate,
  getPassRateColorClass,
} from '@/components/report_card_parts';
import { OrchestrationInlineSummary } from '@/components/orchestration_inline_summary';
import { isLiveHomeRun } from '@/components/report_summary';
import { matchesBranchFilterToken } from '@/lib/branch_filter';

type ViewMode = 'grouped' | 'individual';

function format_time(date_string: string): string {
  const diff = Date.now() - new Date(date_string).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  if (days < 7) return `${days}d ago`;
  return new Date(date_string).toLocaleDateString();
}

function IndividualReportCard({ report }: { report: IndividualReportSummary }) {
  // Per-shard counts only — `test_stats` is computed from this shard's
  // own uploaded JSON. Don't fall back to the orchestration rollup here:
  // orchestration_runs are group-scoped, so every individual row under
  // the same group would otherwise display the group's total (the same
  // numbers across all workers in the matrix). The grouped view, where
  // a row IS the group, is the place to consult orchestration counts.
  const stats = report.test_stats && report.test_stats.total > 0 ? report.test_stats : null;
  const hasStats = !!stats && stats.total > 0;
  const rate = hasStats ? calculatePassRate(stats) : null;
  const rateColorClass = getPassRateColorClass(rate);
  const repoName = report.repository?.split('/').pop() || '';
  const branch = report.branch?.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '') || '';
  const shortSha = report.commit?.slice(0, 7) || '';
  const unit = 'tests';

  const statusIcon =
    report.status !== 'complete' && report.status !== 'failed' ? (
      <Loader2 className="h-4 w-4 animate-spin text-blue-500 flex-shrink-0" />
    ) : hasStats && stats.failed > 0 ? (
      <XCircle className="h-4 w-4 text-red-500 flex-shrink-0" />
    ) : hasStats && (stats.flaky ?? 0) > 0 ? (
      <AlertCircle className="h-4 w-4 text-yellow-500 flex-shrink-0" />
    ) : (
      <CheckCircle className="h-4 w-4 text-green-500 flex-shrink-0" />
    );

  const hasFailed = hasStats && stats.failed > 0;

  return (
    <div>
      <Link
        to={`/reports/r/${report.id}`}
        className={`flex cursor-pointer items-center rounded-md px-3 py-2 text-sm transition-colors ${
          hasFailed
            ? 'bg-red-50/50 hover:bg-red-50 dark:bg-red-950/20 dark:hover:bg-red-950/30'
            : 'hover:bg-gray-50 dark:hover:bg-gray-800'
        }`}
      >
        {/* Left: status, repo, branch, commit, name / job name */}
        <div className="flex items-center gap-2 min-w-0 flex-shrink-0">
          {statusIcon}
          {repoName && (
            <span className="truncate max-w-[100px] text-xs font-medium text-gray-500 dark:text-gray-400">
              {repoName}
            </span>
          )}
          {branch && (
            <span className="inline-flex items-center gap-1 text-xs text-gray-600 dark:text-gray-400 flex-shrink-0">
              <GitBranch className="h-3 w-3" />
              <span className="max-w-[100px] truncate">{branch}</span>
            </span>
          )}
          {shortSha && (
            <span className="inline-flex items-center gap-1 font-mono text-xs text-gray-500 dark:text-gray-500 flex-shrink-0">
              <GitCommit className="h-3 w-3" />
              {shortSha}
            </span>
          )}
          <span className="truncate text-xs text-gray-700 dark:text-gray-300">
            {report.group_name || report.name}
          </span>
          {report.gh_job_name && report.gh_job_name !== report.group_name && (
            <>
              <span className="text-xs text-gray-400 dark:text-gray-500">/</span>
              <span className="truncate text-xs text-gray-500 dark:text-gray-400">
                {report.gh_job_name}
              </span>
            </>
          )}
        </div>

        {/* Middle: test summary + pass rate */}
        <div className="flex-1 flex items-center justify-end gap-2 text-xs mx-2">
          {hasStats && stats && (
            <span
              className="text-gray-500 dark:text-gray-400"
              title={`${stats.passed} passed${stats.failed > 0 ? `, ${stats.failed} failed` : ''}${(stats.flaky ?? 0) > 0 ? `, ${stats.flaky} flaky` : ''}${(stats.skipped ?? 0) > 0 ? `, ${stats.skipped} skipped` : ''} — ${stats.total} total ${unit}`}
            >
              <span className="text-green-700 dark:text-green-400">{stats.passed}</span>
              {stats.failed > 0 && (
                <>
                  {' / '}
                  <span className="text-red-700 dark:text-red-400">{stats.failed} failed</span>
                </>
              )}
              {(stats.flaky ?? 0) > 0 && (
                <>
                  {' / '}
                  <span className="text-yellow-700 dark:text-yellow-400">{stats.flaky}</span>
                </>
              )}
              {(stats.skipped ?? 0) > 0 && (
                <>
                  {' / '}
                  <span className="text-gray-500 dark:text-gray-400">{stats.skipped}</span>
                </>
              )}
            </span>
          )}
          {hasStats && stats && rate !== null && (
            <span
              className={`text-xs font-medium w-12 text-center ${rateColorClass}`}
              title={`${stats.passed} passed${stats.failed > 0 ? `, ${stats.failed} failed` : ''}${(stats.flaky ?? 0) > 0 ? `, ${stats.flaky} flaky` : ''}${(stats.skipped ?? 0) > 0 ? `, ${stats.skipped} skipped` : ''} — ${stats.total} total ${unit}`}
            >
              {rate}%
            </span>
          )}
        </div>

        {/* Right: duration + relative time */}
        <div className="flex items-center text-xs flex-shrink-0">
          <span className="inline-flex items-center justify-end gap-1 text-gray-400 dark:text-gray-500 w-20 text-right">
            {hasStats &&
              stats &&
              (stats.duration_ms ?? report.duration_ms) != null &&
              (stats.duration_ms ?? report.duration_ms ?? 0) > 0 && (
                <>
                  <Clock className="h-3 w-3" />
                  {formatDuration((stats.duration_ms ?? report.duration_ms)!)}
                </>
              )}
          </span>
          <span className="text-gray-400 dark:text-gray-600 w-16 text-right">
            {format_time(report.created_at)}
          </span>
        </div>
      </Link>
      {/* Live progress strip only while the orchestration is actively
          running. Once it terminates, the row above already conveys the
          outcome via test_stats, so the strip would be redundant. */}
      {report.orchestration?.status === 'in_progress' && (
        <div className="px-3 pb-2 -mt-1">
          <OrchestrationInlineSummary orchestration={report.orchestration} />
        </div>
      )}
    </div>
  );
}

export function HomePage() {
  const [searchParams, setSearchParams] = useSearchParams();
  // Individual-report view is reserved for power users / debugging; only
  // exposed when the URL opts in via `?individual=1`. Without that flag the
  // toggle is hidden and the page is locked to the grouped view, so the
  // average visitor never sees the unfiltered shard list.
  const showViewToggle = searchParams.get('individual') === '1';
  const viewMode: ViewMode = showViewToggle
    ? ((searchParams.get('view') as ViewMode) ?? 'grouped')
    : 'grouped';
  const setViewMode = (mode: ViewMode) => {
    if (mode === 'grouped') {
      searchParams.delete('view');
    } else {
      searchParams.set('view', mode);
    }
    setSearchParams(searchParams, { replace: true });
  };
  const selectedRepository = searchParams.get('repository') || '';
  const selectedBranchFilter = searchParams.get('branch_filter') || '';
  const setRepository = (repository: string) => {
    const next = new URLSearchParams(searchParams);
    if (repository) {
      next.set('repository', repository);
    } else {
      next.delete('repository');
    }
    next.delete('branch_filter');
    next.delete('page');
    setSearchParams(next, { replace: true });
  };
  const setBranchFilter = (branchFilter: string) => {
    const next = new URLSearchParams(searchParams);
    if (branchFilter) {
      next.set('branch_filter', branchFilter);
    } else {
      next.delete('branch_filter');
    }
    next.delete('page');
    setSearchParams(next, { replace: true });
  };
  const pageParam = parseInt(searchParams.get('page') || '1', 10);
  const page = isNaN(pageParam) || pageParam < 1 ? 1 : pageParam;
  const limit = 50;
  const setPage = (p: number) => {
    if (p <= 1) {
      searchParams.delete('page');
    } else {
      searchParams.set('page', String(p));
    }
    setSearchParams(searchParams, { replace: true });
  };

  // Grouped data (paginated). Only fetches when the grouped view is
  // visible — the individual view shouldn't pay for an unused grouped fetch.
  const { data: repositoriesData } = useReportRepositories();
  const { data: branchFiltersData } = useBranchFilters(selectedRepository);
  const branchOptions = branchFiltersData?.options ?? [];
  const soleBranchOption = branchOptions.length === 1 ? branchOptions[0] : null;
  const effectiveBranchFilter =
    selectedBranchFilter || (soleBranchOption ? soleBranchOption.value : '');

  useEffect(() => {
    if (soleBranchOption && !selectedBranchFilter) {
      setBranchFilter(soleBranchOption.value);
    }
  }, [soleBranchOption, selectedBranchFilter]);

  const {
    data: groupedData,
    isLoading: isGroupedLoading,
    error: groupedError,
  } = useGroupedReports(page, limit, {
    enabled: viewMode === 'grouped',
    repository: selectedRepository,
    branchFilter: effectiveBranchFilter,
  });

  const { data: liveSourceData } = useGroupedReports(1, limit, {
    enabled: viewMode === 'grouped',
    repository: selectedRepository,
    branchFilter: effectiveBranchFilter,
  });

  // Individual data (paginated). Same `enabled` gating in the other direction.
  const {
    data: individualData,
    isLoading: isIndividualLoading,
    error: individualError,
  } = useIndividualReports(page, limit, { enabled: viewMode === 'individual' });

  const isLoading = viewMode === 'grouped' ? isGroupedLoading : isIndividualLoading;
  const error = viewMode === 'grouped' ? groupedError : individualError;

  const filteredReports = useMemo(() => {
    if (!individualData) return [];
    return individualData.reports.filter((report) => {
      if (selectedRepository) {
        const repo = report.repository || '';
        const slug = selectedRepository.toLowerCase();
        const lower = repo.toLowerCase();
        const name = lower.split('/').pop() || lower;
        if (lower !== slug && name !== slug && !lower.endsWith('/' + slug)) {
          return false;
        }
      }
      if (
        effectiveBranchFilter &&
        !matchesBranchFilterToken(effectiveBranchFilter, report.branch || '', null)
      ) {
        return false;
      }
      return true;
    });
  }, [individualData, selectedRepository, effectiveBranchFilter]);

  const liveRuns = useMemo(() => {
    if (!liveSourceData) return [];
    return liveSourceData.groups
      .flatMap((g) => g.runs)
      .filter(isLiveHomeRun)
      .sort((a, b) =>
        a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0,
      );
  }, [liveSourceData]);

  return (
    <div>
      <section className="mb-6" aria-label="Report filters">
        {showViewToggle && (
          <div className="mb-3 flex items-center justify-between">
            {/* Toggle */}
            <div className="ml-auto flex items-center rounded-lg border border-gray-200 p-0.5 dark:border-gray-700">
              <button
                onClick={() => setViewMode('grouped')}
                className={`inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-md transition-colors ${
                  viewMode === 'grouped'
                    ? 'bg-gray-900 text-white dark:bg-white dark:text-gray-900'
                    : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
                }`}
              >
                <Layers className="h-3.5 w-3.5" />
                Grouped
              </button>
              <button
                onClick={() => setViewMode('individual')}
                className={`inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-md transition-colors ${
                  viewMode === 'individual'
                    ? 'bg-gray-900 text-white dark:bg-white dark:text-gray-900'
                    : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
                }`}
              >
                <List className="h-3.5 w-3.5" />
                Individual
              </button>
            </div>
          </div>
        )}

        <div className="flex flex-wrap items-center gap-3">
          <select
            aria-label="Filter by repository"
            value={selectedRepository}
            onChange={(e) => setRepository(e.target.value)}
            className="w-64 max-w-full cursor-pointer px-3 py-1.5 text-sm border border-gray-200 rounded-md bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            <option value="">All repositories</option>
            {(repositoriesData?.repositories ?? []).map((repo) => (
              <option key={repo.repository} value={repo.repository}>
                {repo.repository_name}
              </option>
            ))}
          </select>
          <select
            aria-label="Filter by branch or pull request"
            value={effectiveBranchFilter}
            onChange={(e) => setBranchFilter(e.target.value)}
            disabled={!selectedRepository}
            className="w-64 max-w-full cursor-pointer px-3 py-1.5 text-sm border border-gray-200 rounded-md bg-white disabled:cursor-not-allowed disabled:opacity-60 dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            {branchOptions.length !== 1 && <option value="">All branches</option>}
            {branchOptions.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </div>
      </section>

      {/* Loading */}
      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
        </div>
      )}

      {/* Error */}
      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800 dark:bg-red-950/30 dark:text-red-400">
          Failed to load reports: {error instanceof Error ? error.message : 'Unknown error'}
        </div>
      )}

      {/* Grouped view */}
      {viewMode === 'grouped' && !isLoading && !error && (
        <>
          {(!groupedData || groupedData.groups.flatMap((g) => g.runs).length === 0) && (
            <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
              <Inbox className="h-12 w-12 mb-3" />
              <p className="text-sm">No reports yet</p>
              <p className="text-xs mt-1">Upload test reports to see them here</p>
            </div>
          )}
          {groupedData && groupedData.groups.flatMap((g) => g.runs).length > 0 &&
            (() => {
              const allRuns = groupedData.groups
                .flatMap((g) => g.runs)
                .sort((a, b) =>
                  a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0,
                );
              const staticRuns = allRuns.filter((r) => !isLiveHomeRun(r));
              const total = groupedData.total ?? allRuns.length;
              const totalPages = Math.max(1, Math.ceil(total / limit));
              return (
                <div className="space-y-4">
                  {liveRuns.length > 0 && (
                    <div className="rounded-lg border border-blue-200 bg-white dark:border-blue-900/60 dark:bg-gray-800/50">
                      <div className="flex items-center gap-2 border-b border-blue-100 px-3 py-2 dark:border-blue-900/40">
                        <span
                          className="h-2 w-2 flex-shrink-0 rounded-full bg-blue-500 shadow-[0_0_0_3px_rgba(59,130,246,0.2)]"
                          aria-hidden
                        />
                        <span className="text-xs font-semibold text-gray-600 dark:text-gray-300">
                          In progress
                        </span>
                        <span className="tabular-nums text-xs text-gray-400 dark:text-gray-500">
                          {liveRuns.length}
                        </span>
                      </div>
                      <RepoGroupCard
                        group={{
                          repository: '',
                          repository_name: '',
                          latest_run_at: '',
                          runs: liveRuns,
                        }}
                        startNumber={1}
                        bare
                      />
                    </div>
                  )}
                  {staticRuns.length > 0 && (
                    <RepoGroupCard
                      group={{
                        repository: '',
                        repository_name: '',
                        latest_run_at: '',
                        runs: staticRuns,
                      }}
                      startNumber={(page - 1) * limit + 1}
                    />
                  )}
                  {liveRuns.length > 0 && staticRuns.length === 0 && (
                    <div className="rounded-lg border border-gray-200 bg-white px-3 py-4 text-center text-sm text-gray-400 dark:border-gray-700 dark:bg-gray-800/50 dark:text-gray-500">
                      No completed runs on this page
                    </div>
                  )}
                  {totalPages > 1 && (
                    <div className="flex items-center justify-between border-t border-gray-200 pt-4 dark:border-gray-700">
                      <div className="text-sm text-gray-500 dark:text-gray-400">
                        Showing {(page - 1) * limit + 1} to {Math.min(page * limit, total)} of{' '}
                        {total} report groups
                      </div>
                      <div className="flex gap-2">
                        <button
                          type="button"
                          onClick={() => setPage(page - 1)}
                          disabled={page === 1}
                          className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
                        >
                          Previous
                        </button>
                        <button
                          type="button"
                          onClick={() => setPage(page + 1)}
                          disabled={page >= totalPages}
                          className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
                        >
                          Next
                        </button>
                      </div>
                    </div>
                  )}
                </div>
              );
            })()}
        </>
      )}

      {/* Individual view */}
      {viewMode === 'individual' && !isLoading && !error && (
        <>
          {filteredReports.length === 0 && (
            <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
              <Inbox className="h-12 w-12 mb-3" />
              <p className="text-sm">No reports found</p>
            </div>
          )}
          {filteredReports.length > 0 &&
            (() => {
              // Filters are applied client-side to the current page only;
              // while one is active, the server's `total` is the unfiltered
              // count and is misleading. Switch the count text to a
              // page-local view so users aren't shown "Z of 200" when they
              // see 3 matches.
              const hasClientFilter = !!selectedRepository || !!effectiveBranchFilter;
              const total = individualData?.total ?? 0;
              const totalPages = Math.ceil(total / limit);
              return (
                <div className="space-y-4">
                  <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800/50">
                    <div className="divide-y divide-gray-50 dark:divide-gray-700/50">
                      {filteredReports.map((report) => (
                        <div key={report.id}>
                          <IndividualReportCard report={report} />
                        </div>
                      ))}
                    </div>
                  </div>

                  {hasClientFilter ? (
                    <div className="border-t border-gray-200 pt-4 text-sm text-gray-500 dark:border-gray-700 dark:text-gray-400">
                      Showing {filteredReports.length} match
                      {filteredReports.length === 1 ? '' : 'es'} on page {page}. Filters apply to
                      the current page only; clear filters to page through all reports.
                    </div>
                  ) : (
                    totalPages > 1 && (
                      <div className="flex items-center justify-between border-t border-gray-200 pt-4 dark:border-gray-700">
                        <div className="text-sm text-gray-500 dark:text-gray-400">
                          Showing {(page - 1) * limit + 1} to {Math.min(page * limit, total)} of{' '}
                          {total} reports
                        </div>
                        <div className="flex gap-2">
                          <button
                            type="button"
                            onClick={() => setPage(page - 1)}
                            disabled={page === 1}
                            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
                          >
                            Previous
                          </button>
                          <button
                            type="button"
                            onClick={() => setPage(page + 1)}
                            disabled={page >= totalPages}
                            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
                          >
                            Next
                          </button>
                        </div>
                      </div>
                    )
                  )}
                </div>
              );
            })()}
        </>
      )}
    </div>
  );
}
