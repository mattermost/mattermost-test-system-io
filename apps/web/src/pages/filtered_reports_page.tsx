import { useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import { Loader2, Inbox } from 'lucide-react';
import { useGroupedReports } from '@/services/api';
import { RepoGroupCard } from '@/components/repo_group_card';
import { Breadcrumb } from '@/components/breadcrumb';
import { ReportSummary, resolveEffectiveReportStatus } from '@/components/report_summary';
import { resolveDisplayStats } from '@/components/report_card_parts';
import type { RepositoryGroup, RunEntry } from '@/types';

function short_branch(branch: string): string {
  return branch.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '');
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
        // entry.commit is the full 40-char SHA; the URL segment may be any
        // prefix (7-char short form when linked from a card, 40-char full
        // when pasted). Case-insensitive startsWith handles both cleanly.
        if (commit && !entry.commit.toLowerCase().startsWith(commit.toLowerCase())) return false;
        return true;
      });
      return { ...group, runs: filtered_runs };
    })
    .filter((group) => group.runs.length > 0);
}

interface AggregatedStats {
  passed: number;
  failed: number;
  skipped: number;
  flaky: number;
  total: number;
  duration_ms: number | null;
  retest_duration_ms: number | null;
  test_status: 'passed' | 'failed' | 'timed_out';
  progress_status: 'in_progress' | 'completed' | 'timed_out' | 'incomplete';
  latest_created_at: string;
  nameLinks: { label: string; href: string }[];
}

function aggregateRunStats(groups: RepositoryGroup[]): AggregatedStats {
  const allRuns: RunEntry[] = groups.flatMap((g) => g.runs);

  // Deduplicate by name: keep only the latest run for each name
  const latestByName = new Map<string, RunEntry>();
  for (const run of allRuns) {
    const existing = latestByName.get(run.name);
    if (!existing || run.created_at > existing.created_at) {
      latestByName.set(run.name, run);
    }
  }
  const runs = [...latestByName.values()];

  // Latest created_at across ALL runs (indicates most recent update)
  let latest: string | null = null;
  for (const run of allRuns) {
    if (!latest || run.created_at > latest) latest = run.created_at;
  }

  let passed = 0,
    failed = 0,
    skipped = 0,
    flaky = 0,
    total = 0;
  let maxWallClock: number | null = null;
  let maxRetestWallClock: number | null = null;
  let hasActiveInProgress = false;
  let hasIncomplete = false;

  for (const run of runs) {
    const effective = resolveEffectiveReportStatus(
      run.status,
      run.last_upload_at,
      run.orchestration,
    );
    if (effective === 'in_progress') {
      hasActiveInProgress = true;
    } else if (effective === 'incomplete' || run.status === 'incomplete') {
      hasIncomplete = true;
    }

    // Source-of-truth resolver: prefer orchestration counts, fall back
    // to the framework's `test_stats`. Keeps the branch/repo-level
    // summary numbers aligned with the per-row display in `RepoGroupCard`.
    const stats = resolveDisplayStats(run);
    if (stats) {
      passed += stats.passed;
      failed += stats.failed;
      skipped += stats.skipped;
      flaky += stats.flaky;
      total += stats.total;

      if (stats.wall_clock_ms != null) {
        maxWallClock = Math.max(maxWallClock ?? 0, stats.wall_clock_ms);
      }
      if (stats.retest_wall_clock_ms != null) {
        maxRetestWallClock = Math.max(maxRetestWallClock ?? 0, stats.retest_wall_clock_ms);
      }
    }
  }

  const progress_status: AggregatedStats['progress_status'] = hasActiveInProgress
    ? 'in_progress'
    : hasIncomplete
      ? 'incomplete'
      : 'completed';
  // Overall verdict is Passed / Failed / Timed Out — flaky-but-passed
  // tests count as Passed at the run level (flaky lives per-test-case).
  const test_status: AggregatedStats['test_status'] =
    progress_status === 'incomplete' ? 'timed_out' : failed > 0 ? 'failed' : 'passed';

  return {
    passed,
    failed,
    skipped,
    flaky,
    total,
    duration_ms: maxWallClock,
    retest_duration_ms: maxRetestWallClock,
    test_status,
    progress_status,
    latest_created_at: latest || '',
    nameLinks: runs
      .map((r) => ({ label: r.name, href: r.url_path }))
      .sort((a, b) => a.label.localeCompare(b.label)),
  };
}

export function FilteredReportsPage() {
  const { repo, branch, commit, param } = useParams<{
    repo: string;
    branch: string;
    commit: string;
    param: string;
  }>();
  const repoName = repo || param;

  const { data, isLoading, error } = useGroupedReports();

  const filteredGroups = useMemo(() => {
    if (!data) return [];
    return filterGroups(data.groups, repoName, branch, commit);
  }, [data, repoName, branch, commit]);

  // Resolve the commit SHA to base stats on:
  // - If commit param is set, use it (already filtered by filterGroups)
  // - If branch param is set, find the latest commit on that branch
  // - Otherwise (repo-only view), prefer the latest commit on main/master
  const resolvedCommit = useMemo(() => {
    if (commit) return null; // already filtered by route param
    const allRuns = filteredGroups.flatMap((g) => g.runs);
    if (allRuns.length === 0) return null;

    // When no branch is specified, prefer main/master
    let candidates = allRuns;
    if (!branch) {
      const mainRuns = allRuns.filter((r) => r.branch === 'main' || r.branch === 'master');
      if (mainRuns.length > 0) candidates = mainRuns;
    }

    let latest = candidates[0]!;
    for (const run of candidates) {
      if (run.created_at > latest.created_at) latest = run;
    }
    return { full: latest.commit, short: latest.short_sha || latest.commit.slice(0, 7) };
  }, [filteredGroups, branch, commit]);

  // Filter groups to the resolved latest commit for stats aggregation
  const statsGroups = useMemo(() => {
    if (!resolvedCommit) return filteredGroups; // commit param present, already filtered
    return filteredGroups
      .map((g) => ({
        ...g,
        runs: g.runs.filter((r) => r.commit === resolvedCommit.full),
      }))
      .filter((g) => g.runs.length > 0);
  }, [filteredGroups, resolvedCommit]);

  const commitStats = useMemo(() => {
    if (statsGroups.length === 0) return null;
    return aggregateRunStats(statsGroups);
  }, [statsGroups]);

  // Extract git context from the stats groups (same data the summary is based on)
  const gitContext = useMemo(() => {
    const sourceGroups = statsGroups.length > 0 ? statsGroups : filteredGroups;
    if (sourceGroups.length === 0) return null;
    const group = sourceGroups[0]!;
    const firstRun = group.runs[0];
    if (!firstRun) return null;

    const repository = group.repository; // e.g. "mattermost/mattermost"
    const branchName = firstRun.branch;
    const fullCommit = firstRun.commit;
    const shortSha = firstRun.short_sha || firstRun.commit.slice(0, 7);

    // PR number: prefer API field, fallback to parsing branch name
    let prNumber: number | undefined = firstRun.gh_pr_number;
    if (!prNumber) {
      const prMatch = branchName.match(/^pr-(\d+)/i);
      if (prMatch) prNumber = parseInt(prMatch[1]!, 10);
    }

    return { repository, fullCommit, shortSha, branchName, prNumber };
  }, [filteredGroups, commit, resolvedCommit]);

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
      breadcrumbItems.push({ label: branch, to: `/reports/${repoName}/${branch}` });
    } else {
      breadcrumbItems.push({ label: branch });
    }
  }
  if (commit) {
    breadcrumbItems.push({ label: commit });
  }

  return (
    <div>
      <div className="mb-6">
        <Breadcrumb items={breadcrumbItems} />

        {commitStats && (
          <ReportSummary
            testStatus={commitStats.test_status}
            nameLinks={commitStats.nameLinks}
            passed={commitStats.passed}
            failed={commitStats.failed}
            flaky={commitStats.flaky}
            skipped={commitStats.skipped}
            total={commitStats.total}
            durationMs={commitStats.duration_ms}
            retestDurationMs={commitStats.retest_duration_ms}
            createdAt={commitStats.latest_created_at}
            progressStatus={commitStats.progress_status}
            repository={gitContext?.repository}
            branch={gitContext?.branchName}
            commit={gitContext?.fullCommit}
            ghPrNumber={gitContext?.prNumber}
          />
        )}
      </div>

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

      {data && filteredGroups.length === 0 && (
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

      {filteredGroups.length > 0 && (
        <div className="space-y-4">
          {filteredGroups.map((group) => (
            <RepoGroupCard key={group.repository_name} group={group} />
          ))}
        </div>
      )}
    </div>
  );
}
