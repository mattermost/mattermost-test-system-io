import { useEffect, useMemo } from 'react';
import { useParams, Link, useSearchParams } from 'react-router-dom';
import { Loader2, Inbox } from 'lucide-react';
import { useGroupedReports, useBranchFilters } from '@/services/api';
import { RepoGroupCard } from '@/components/repo_group_card';
import { Breadcrumb } from '@/components/breadcrumb';
import { isLiveHomeRun } from '@/components/report_summary';
import type { RepositoryGroup } from '@/types';
import { stripRefPrefix, encodeBranchPathSegment } from '@/lib/report_urls';

function short_branch(branch: string): string {
  return stripRefPrefix(branch);
}

function filterGroups(
  groups: RepositoryGroup[],
  repo?: string,
  branch?: string,
  commit?: string,
): RepositoryGroup[] {
  return groups
    .filter((group) => {
      if (repo && group.repository_name !== repo) return false;
      return true;
    })
    .map((group) => {
      const filtered_runs = group.runs.filter((entry) => {
        if (branch && short_branch(entry.branch) !== branch) return false;
        if (commit && !entry.commit.toLowerCase().startsWith(commit.toLowerCase())) return false;
        return true;
      });
      return { ...group, runs: filtered_runs };
    })
    .filter((group) => group.runs.length > 0);
}

export type FilteredReportsPageProps = {
  repo?: string;
  branch?: string;
  commit?: string;
};

export function FilteredReportsPage(props: FilteredReportsPageProps = {}) {
  const params = useParams<{
    repo: string;
    branch: string;
    commit: string;
    param: string;
  }>();
  const repoName = props.repo ?? params.repo ?? params.param;
  const branch = props.branch ?? params.branch;
  const commit = props.commit ?? params.commit;
  const isRepoScoped = !!repoName && !branch && !commit;

  const [searchParams, setSearchParams] = useSearchParams();
  const selectedBranchFilter = searchParams.get('branch_filter') || '';
  const pageParam = parseInt(searchParams.get('page') || '1', 10);
  const page = isNaN(pageParam) || pageParam < 1 ? 1 : pageParam;
  const limit = 50;

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

  const setPage = (p: number) => {
    const next = new URLSearchParams(searchParams);
    if (p <= 1) {
      next.delete('page');
    } else {
      next.set('page', String(p));
    }
    setSearchParams(next, { replace: true });
  };

  const { data: branchFiltersData } = useBranchFilters(repoName || '', {
    enabled: isRepoScoped,
  });
  const branchOptions = branchFiltersData?.options ?? [];
  const soleBranchOption = branchOptions.length === 1 ? branchOptions[0] : null;
  const effectiveBranchFilter =
    selectedBranchFilter || (soleBranchOption ? soleBranchOption.value : '');

  useEffect(() => {
    if (isRepoScoped && soleBranchOption && !selectedBranchFilter) {
      setBranchFilter(soleBranchOption.value);
    }
  }, [isRepoScoped, soleBranchOption, selectedBranchFilter]);

  const groupedOptions = {
    repository: isRepoScoped ? repoName : undefined,
    branchFilter: isRepoScoped ? effectiveBranchFilter : undefined,
  };

  const {
    data: groupedData,
    isLoading,
    error,
  } = useGroupedReports(page, limit, groupedOptions);

  const { data: liveSourceData } = useGroupedReports(1, limit, {
    enabled: isRepoScoped,
    ...groupedOptions,
  });

  const filteredGroups = useMemo(() => {
    if (!groupedData) return [];
    if (isRepoScoped) return groupedData.groups;
    return filterGroups(groupedData.groups, repoName, branch, commit);
  }, [groupedData, isRepoScoped, repoName, branch, commit]);

  const liveRuns = useMemo(() => {
    if (!isRepoScoped || !liveSourceData) return [];
    return liveSourceData.groups
      .flatMap((g) => g.runs)
      .filter(isLiveHomeRun)
      .sort((a, b) =>
        a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0,
      );
  }, [isRepoScoped, liveSourceData]);

  const breadcrumbItems: { label: string; to?: string }[] = [{ label: 'Reports', to: '/reports' }];
  if (repoName) {
    if (branch || commit) {
      breadcrumbItems.push({ label: repoName, to: `/reports/${repoName}` });
    } else {
      breadcrumbItems.push({ label: repoName });
    }
  }
  if (branch) {
    if (commit) {
      breadcrumbItems.push({
        label: branch,
        to: `/reports/${repoName}/${encodeURIComponent(encodeBranchPathSegment(branch))}`,
      });
    } else {
      breadcrumbItems.push({ label: branch });
    }
  }
  if (commit) {
    breadcrumbItems.push({ label: commit });
  }

  const repoRuns = filteredGroups.flatMap((g) => g.runs);
  const staticRuns = isRepoScoped
    ? repoRuns
        .filter((r) => !isLiveHomeRun(r))
        .sort((a, b) =>
          a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0,
        )
    : [];
  const total = isRepoScoped ? (groupedData?.total ?? repoRuns.length) : 0;
  const totalPages = isRepoScoped ? Math.max(1, Math.ceil(total / limit)) : 0;

  return (
    <div>
      <div className={isRepoScoped ? undefined : 'mb-6'}>
        <Breadcrumb items={breadcrumbItems} />
      </div>

      {isRepoScoped && (
        <section className="mb-6 mt-6" aria-label="Report filters">
          <div className="flex flex-wrap items-center gap-3">
            <select
              aria-label="Filter by branch or pull request"
              value={effectiveBranchFilter}
              onChange={(e) => setBranchFilter(e.target.value)}
              className="w-64 max-w-full cursor-pointer px-3 py-1.5 text-sm border border-gray-200 rounded-md bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-blue-500"
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
      )}

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
        </div>
      )}

      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800 dark:bg-red-950/30 dark:text-red-400">
          Failed to load reports: {error instanceof Error ? error.message : 'Unknown error'}
        </div>
      )}

      {isRepoScoped && !isLoading && !error && liveRuns.length === 0 && staticRuns.length === 0 && (
        <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
          <Inbox className="h-12 w-12 mb-3" />
          <p className="text-sm">No matching reports</p>
          <p className="text-xs mt-1">
            No reports match the current filters.{' '}
            <Link to="/reports" className="text-blue-600 hover:underline dark:text-blue-400">
              View all reports
            </Link>
          </p>
        </div>
      )}

      {isRepoScoped && !isLoading && !error && (liveRuns.length > 0 || staticRuns.length > 0) && (
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
                Showing {(page - 1) * limit + 1} to {Math.min(page * limit, total)} of {total}{' '}
                report groups
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
      )}

      {!isRepoScoped && groupedData && filteredGroups.length === 0 && !isLoading && (
        <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
          <Inbox className="h-12 w-12 mb-3" />
          <p className="text-sm">No matching reports</p>
          <p className="text-xs mt-1">
            No reports match the current filters.{' '}
            <Link to="/reports" className="text-blue-600 hover:underline dark:text-blue-400">
              View all reports
            </Link>
          </p>
        </div>
      )}

      {!isRepoScoped && filteredGroups.length > 0 && (
        <div className="space-y-4">
          {filteredGroups.map((group) => (
            <RepoGroupCard key={group.repository_name} group={group} />
          ))}
        </div>
      )}
    </div>
  );
}
