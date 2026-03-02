import { useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import { AlertCircle, Loader2, Inbox, GitCommit } from 'lucide-react';
import { Breadcrumb } from '@/components/breadcrumb';
import { useGroupedReports } from '@/services/api';
import { RepoGroupCard } from '@/components/repo_group_card';
import { ReportSummary } from '@/components/report_summary';
import type { RepositoryGroup, RunEntry } from '@/types';

const TIMED_OUT_THRESHOLD_MS = 3_600_000; // 1 hour

interface AggregatedStats {
  passed: number;
  failed: number;
  skipped: number;
  flaky: number;
  total: number;
  duration_ms: number | null;
  test_status: 'passed' | 'failed' | 'flaky';
  progress_status: 'in_progress' | 'completed' | 'timed_out';
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
  let hasActiveInProgress = false;
  let hasTimedOut = false;
  const now = Date.now();

  for (const run of runs) {
    if (run.status === 'in_progress') {
      const isTimedOut = new Date(run.created_at).getTime() < now - TIMED_OUT_THRESHOLD_MS;
      if (isTimedOut) {
        hasTimedOut = true;
      } else {
        hasActiveInProgress = true;
      }
    }

    const stats = run.test_stats;
    if (stats) {
      passed += stats.passed;
      failed += stats.failed;
      skipped += stats.skipped;
      flaky += stats.flaky;
      total += stats.total;

      if (stats.wall_clock_ms != null) {
        maxWallClock = Math.max(maxWallClock ?? 0, stats.wall_clock_ms);
      }
    }
  }

  const test_status: AggregatedStats['test_status'] =
    failed > 0 ? 'failed' : flaky > 0 ? 'flaky' : 'passed';
  const progress_status: AggregatedStats['progress_status'] = hasActiveInProgress
    ? 'in_progress'
    : hasTimedOut
      ? 'timed_out'
      : 'completed';

  // Build name links from deduplicated runs, sorted alphabetically
  const nameLinks = runs
    .map((r) => ({ label: r.name, href: r.url_path }))
    .sort((a, b) => a.label.localeCompare(b.label));

  return {
    passed,
    failed,
    skipped,
    flaky,
    total,
    duration_ms: maxWallClock,
    test_status,
    progress_status,
    latest_created_at: latest || '',
    nameLinks,
  };
}

export function CommitReportsPage() {
  const { sha: shaParam, param } = useParams<{ sha: string; param: string }>();
  const sha = shaParam || param || '';

  const { data, isLoading, error } = useGroupedReports();

  // Filter all groups' runs by commit SHA prefix
  const filteredGroups = useMemo(() => {
    if (!data || !sha) return [];
    return data.groups
      .map((group) => ({
        ...group,
        runs: group.runs.filter(
          (entry) => entry.commit.startsWith(sha) || entry.short_sha.startsWith(sha),
        ),
      }))
      .filter((group) => group.runs.length > 0);
  }, [data, sha]);

  // Check for ambiguous SHA
  const distinctCommits = useMemo(() => {
    const commits = new Set<string>();
    for (const group of filteredGroups) {
      for (const run of group.runs) {
        commits.add(run.commit);
      }
    }
    return commits;
  }, [filteredGroups]);

  const isAmbiguous = distinctCommits.size > 1;

  const commitStats = useMemo(() => {
    if (filteredGroups.length === 0 || isAmbiguous) return null;
    return aggregateRunStats(filteredGroups);
  }, [filteredGroups, isAmbiguous]);

  // Extract git context from filtered groups for badges
  const gitContext = useMemo(() => {
    if (filteredGroups.length === 0 || isAmbiguous) return null;
    const group = filteredGroups[0]!;
    const firstRun = group.runs[0];
    if (!firstRun) return null;

    const repository = group.repository;
    const fullCommit = firstRun.commit;
    const shortSha = firstRun.short_sha || fullCommit.slice(0, 7);
    const branchName = firstRun.branch;

    // PR number: prefer API field, fallback to parsing branch name
    let prNumber: number | undefined = firstRun.gh_pr_number;
    if (!prNumber) {
      const prMatch = branchName.match(/^pr-(\d+)/i);
      if (prMatch) prNumber = parseInt(prMatch[1]!, 10);
    }

    return { repository, fullCommit, shortSha, branchName, prNumber };
  }, [filteredGroups, isAmbiguous]);

  return (
    <div>
      <div className="mb-6">
        <Breadcrumb items={[{ label: 'Reports', to: '/reports' }, { label: sha }]} />

        {isAmbiguous && (
          <div className="mt-3 rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-700 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-400">
            <div className="flex items-start gap-2">
              <AlertCircle className="h-4 w-4 mt-0.5 flex-shrink-0" />
              <div>
                <p className="font-medium">Ambiguous SHA</p>
                <p className="mt-1">
                  The short SHA &apos;{sha}&apos; matches {distinctCommits.size} distinct commits:
                </p>
                <div className="flex flex-wrap gap-2 mt-2">
                  {[...distinctCommits].map((fullSha) => (
                    <Link
                      key={fullSha}
                      to={`/reports/c/${fullSha}`}
                      className="inline-flex items-center gap-1 px-2 py-1 bg-amber-100 rounded-md text-xs font-mono text-amber-800 hover:bg-amber-200 dark:bg-amber-900/50 dark:text-amber-300 dark:hover:bg-amber-900/70 transition-colors"
                    >
                      <GitCommit className="h-3 w-3" />
                      {fullSha.slice(0, 12)}
                    </Link>
                  ))}
                </div>
              </div>
            </div>
          </div>
        )}

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

      {data && !isAmbiguous && filteredGroups.length === 0 && (
        <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
          <Inbox className="h-12 w-12 mb-3" />
          <p className="text-sm">No matching reports</p>
          <p className="text-xs mt-1">
            No reports match the commit SHA &apos;{sha}&apos;.{' '}
            <Link to="/reports" className="text-blue-600 hover:underline dark:text-blue-400">
              View all reports
            </Link>
          </p>
        </div>
      )}

      {!isAmbiguous && filteredGroups.length > 0 && (
        <div className="space-y-4">
          {filteredGroups.map((group) => (
            <RepoGroupCard key={group.repository_name} group={group} />
          ))}
        </div>
      )}
    </div>
  );
}
