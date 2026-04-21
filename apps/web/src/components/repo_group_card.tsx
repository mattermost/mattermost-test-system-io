import {
  GitBranch,
  GitCommit,
  CheckCircle,
  XCircle,
  Clock,
  Loader2,
  AlertCircle,
} from 'lucide-react';
import { Link } from 'react-router-dom';
import type { RepositoryGroup, RunEntry } from '@/types';
import {
  formatDuration,
  calculatePassRate,
  getPassRateColorClass,
} from '@/components/report_card_parts';

const TIMED_OUT_THRESHOLD_MS = 3_600_000; // 1 hour

function status_icon(entry: RunEntry) {
  // Still in progress
  if (entry.status === 'in_progress') {
    if (entry.created_at) {
      const isTimedOut = new Date(entry.created_at).getTime() < Date.now() - TIMED_OUT_THRESHOLD_MS;
      if (isTimedOut) {
        return <AlertCircle className="h-4 w-4 text-orange-500" />;
      }
    }
    return <Loader2 className="h-4 w-4 animate-spin text-blue-500" />;
  }

  // Completed — use test results
  const stats = entry.test_stats;
  if (stats && stats.total > 0) {
    if (stats.failed > 0) return <XCircle className="h-4 w-4 text-red-500" />;
    if ((stats.flaky ?? 0) > 0) return <AlertCircle className="h-4 w-4 text-yellow-500" />;
    return <CheckCircle className="h-4 w-4 text-green-500" />;
  }

  return <CheckCircle className="h-4 w-4 text-green-500" />;
}

function short_branch(branch: string): string {
  return branch.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '');
}

function format_time(date_string: string): string {
  const date = new Date(date_string);
  const now = new Date();
  const diff_ms = now.getTime() - date.getTime();
  const diff_mins = Math.floor(diff_ms / 60000);

  if (diff_mins < 1) return 'just now';
  if (diff_mins < 60) return `${diff_mins}m ago`;
  const diff_hours = Math.floor(diff_mins / 60);
  if (diff_hours < 24) return `${diff_hours}h ago`;
  const diff_days = Math.floor(diff_hours / 24);
  if (diff_days < 7) return `${diff_days}d ago`;
  return date.toLocaleDateString();
}

function run_entry_row({ entry, repoName }: { entry: RunEntry; repoName?: string }) {
  const branch = short_branch(entry.branch);
  const stats = entry.test_stats;
  const hasStats = stats && stats.total > 0;
  const rate = hasStats ? calculatePassRate(stats) : null;
  const rateColorClass = getPassRateColorClass(rate);
  const hasFailed = hasStats && stats.failed > 0;

  return (
    <Link
      key={entry.report_id}
      to={entry.url_path}
      className={`flex items-center rounded-md px-3 py-2 text-sm transition-colors ${
        hasFailed
          ? 'bg-red-50/50 hover:bg-red-50 dark:bg-red-950/20 dark:hover:bg-red-950/30'
          : 'hover:bg-gray-50 dark:hover:bg-gray-800'
      }`}
    >
      {/* Left: status, repo, branch, commit, name */}
      <div className="flex items-center gap-2 min-w-0 flex-shrink-0">
        {status_icon(entry)}
        {repoName && (
          <span className="truncate max-w-[100px] text-xs font-medium text-gray-500 dark:text-gray-400">
            {repoName}
          </span>
        )}
        <span className="inline-flex items-center gap-1 text-xs text-gray-600 dark:text-gray-400 flex-shrink-0">
          <GitBranch className="h-3 w-3" />
          <span className="max-w-[100px] truncate">{branch}</span>
        </span>
        <span className="inline-flex items-center gap-1 font-mono text-xs text-gray-500 dark:text-gray-500 flex-shrink-0">
          <GitCommit className="h-3 w-3" />
          {entry.short_sha}
        </span>
        <span className="truncate text-xs text-gray-700 dark:text-gray-300">{entry.name}</span>
      </div>

      {/* Middle: test summary, pass rate (flex-1 pushes right group to edge) */}
      <div className="flex-1 flex items-center justify-end gap-2 text-xs mx-2">
        {hasStats && (
          <span
            className="text-gray-500 dark:text-gray-400"
            title={`${stats.passed} passed${stats.failed > 0 ? `, ${stats.failed} failed` : ''}${(stats.flaky ?? 0) > 0 ? `, ${stats.flaky} flaky` : ''}${(stats.skipped ?? 0) > 0 ? `, ${stats.skipped} skipped` : ''} — ${stats.total} total`}
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
        {hasStats && rate !== null && (
          <span
            className={`rounded px-1.5 py-0.5 text-xs font-medium w-12 text-center ${rateColorClass}`}
            title={`${stats.passed} passed${stats.failed > 0 ? `, ${stats.failed} failed` : ''}${(stats.flaky ?? 0) > 0 ? `, ${stats.flaky} flaky` : ''}${(stats.skipped ?? 0) > 0 ? `, ${stats.skipped} skipped` : ''} — ${stats.total} total`}
          >
            {rate}%
          </span>
        )}
      </div>

      {/* Right: wall clock (numbered batch + optional retest) + relative time */}
      <div className="flex items-center gap-2 text-xs flex-shrink-0">
        <span
          className="inline-flex items-center justify-end gap-1 text-gray-400 dark:text-gray-500"
          title={
            hasStats && stats.retest_wall_clock_ms
              ? 'Parallel shard batch, then separate retest run'
              : undefined
          }
        >
          {hasStats && stats.wall_clock_ms != null && stats.wall_clock_ms > 0 && (
            <>
              <Clock className="h-3 w-3" />
              {formatDuration(stats.wall_clock_ms)}
            </>
          )}
          {hasStats && stats.retest_wall_clock_ms != null && stats.retest_wall_clock_ms > 0 && (
            <span className="text-gray-400 dark:text-gray-500">
              {' + '}
              {formatDuration(stats.retest_wall_clock_ms)}
            </span>
          )}
        </span>
        <span className="text-gray-400 dark:text-gray-600 w-16 text-right">
          {format_time(entry.created_at)}
        </span>
      </div>
    </Link>
  );
}

interface RepoGroupCardProps {
  group: RepositoryGroup;
}

export function RepoGroupCard({ group }: RepoGroupCardProps) {
  // Find the latest entry per unique consolidated key (url_path without gid)
  // so non-latest entries get a gid query param
  const latestByKey = new Map<string, string>();
  for (const entry of group.runs) {
    const key = entry.url_path;
    const existing = latestByKey.get(key);
    if (!existing || entry.created_at > existing) {
      latestByKey.set(key, entry.created_at);
    }
  }

  return (
    <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800/50">
      <div className="divide-y divide-gray-50 dark:divide-gray-700/50">
        {group.runs.map((entry) => {
          const isLatest = entry.created_at === latestByKey.get(entry.url_path);
          const urlPath = isLatest ? entry.url_path : `${entry.url_path}?gid=${entry.report_id}`;
          return (
            <div key={entry.report_id}>
              {run_entry_row({
                entry: { ...entry, url_path: urlPath },
                repoName: group.repository_name || entry.url_path.split('/')[2] || '',
              })}
            </div>
          );
        })}
      </div>
    </div>
  );
}
