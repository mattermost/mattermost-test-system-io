import {
  GitBranch,
  GitCommit,
  CheckCircle,
  XCircle,
  Clock,
  Loader2,
  AlertCircle,
  Play,
  RotateCcw,
} from 'lucide-react';
import { Link } from 'react-router-dom';
import type { RepositoryGroup, RunEntry } from '@/types';
import {
  formatDuration,
  calculatePassRate,
  getPassRateColorClass,
  resolveDisplayStats,
} from '@/components/report_card_parts';
import { OrchestrationInlineSummary } from '@/components/orchestration_inline_summary';
import { resolveEffectiveReportStatus } from '@/components/report_summary';

function status_icon(entry: RunEntry) {
  const effective = resolveEffectiveReportStatus(entry.status, entry.last_upload_at);

  // Still in progress (recent upload activity).
  if (effective === 'in_progress') {
    return <Loader2 className="h-4 w-4 animate-spin text-blue-500" />;
  }
  if (effective === 'incomplete') {
    return <AlertCircle className="h-4 w-4 text-orange-500" />;
  }

  // Completed — use the same source-of-truth resolver as the row body so
  // the icon, the count line, and the pass-rate pill always agree on
  // which side (orchestration vs. uploaded reports) is driving the row.
  const stats = resolveDisplayStats(entry);
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
  // Prefer the orchestration counts over the framework's `test_stats`
  // when both exist, so the row reflects the orchestrator's view of the
  // run (including in-progress dispatch units before any shard reports
  // have uploaded). Falls back to `test_stats` only when no orchestration
  // run is associated with this entry.
  const stats = resolveDisplayStats(entry);
  const hasStats = !!stats && stats.total > 0;
  const rate = hasStats ? calculatePassRate(stats) : null;
  const rateColorClass = getPassRateColorClass(rate);
  // Both source branches of `resolveDisplayStats` (test_stats and the
  // orchestration server-side rollup) are at test-case granularity, so
  // the unit label is unconditionally "tests".
  const unit = 'tests';

  return (
    <Link
      key={entry.report_id}
      to={entry.url_path}
      className="flex cursor-pointer items-center px-3 py-2 text-sm"
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
        {entry.gh_run_id && (
          <span
            className="inline-flex items-center gap-1 font-mono text-xs text-gray-500 dark:text-gray-500 flex-shrink-0"
            title={`GitHub Actions run ${entry.gh_run_id}`}
          >
            <Play className="h-3 w-3" />
            {entry.gh_run_id}
          </span>
        )}
        {entry.gh_run_attempt && entry.gh_run_attempt !== '1' && (
          <span
            className="inline-flex items-center gap-1 text-xs text-gray-500 dark:text-gray-500 flex-shrink-0"
            title={`GitHub Actions run attempt ${entry.gh_run_attempt}`}
          >
            <RotateCcw className="h-3 w-3" />
            attempt {entry.gh_run_attempt}
          </span>
        )}
      </div>

      {/* Middle: test summary, pass rate (flex-1 pushes right group to edge) */}
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
            className={`rounded px-1.5 py-0.5 text-xs font-medium w-12 text-center ${rateColorClass}`}
            title={`${stats.passed} passed${stats.failed > 0 ? `, ${stats.failed} failed` : ''}${(stats.flaky ?? 0) > 0 ? `, ${stats.flaky} flaky` : ''}${(stats.skipped ?? 0) > 0 ? `, ${stats.skipped} skipped` : ''} — ${stats.total} total ${unit}`}
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
            hasStats && stats?.retest_wall_clock_ms
              ? 'Parallel shard batch, then separate retest run'
              : undefined
          }
        >
          {hasStats && stats?.wall_clock_ms != null && stats.wall_clock_ms > 0 && (
            <>
              <Clock className="h-3 w-3" />
              {formatDuration(stats.wall_clock_ms)}
            </>
          )}
          {hasStats && stats?.retest_wall_clock_ms != null && stats.retest_wall_clock_ms > 0 && (
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

/**
 * Renders the orchestration progress strip beneath a run row when the
 * report_group has a matching orchestration_run. Kept as a sibling element
 * so the existing run row layout (status + stats + duration) stays intact
 * and the orchestration data flows below as a secondary detail.
 */
function orchestration_strip(entry: RunEntry) {
  // Only surface the live progress strip while the orchestration is
  // actually running. Once it terminates the row's main test_stats line
  // already conveys the outcome, so the strip becomes redundant noise.
  if (!entry.orchestration || entry.orchestration.status !== 'in_progress') return null;
  return (
    <div className="px-3 pb-2 -mt-1">
      <OrchestrationInlineSummary orchestration={entry.orchestration} />
    </div>
  );
}

interface RepoGroupCardProps {
  group: RepositoryGroup;
}

export function RepoGroupCard({ group }: RepoGroupCardProps) {
  // Find the latest entry per unique consolidated key (url_path without query)
  // so non-latest entries get a `gh_run_id` query param. Disambiguating by
  // gh_run_id (rather than the report-group UUID via `gid`) keeps the URL
  // human-readable and matches the GitHub Actions run id surfaced elsewhere
  // in the UI; the orchestration tab and the consolidated view both pick up
  // this query param at page-load time.
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
          // Latest entry keeps the bare URL; older entries with the same
          // (repo, branch, commit, name) get a `gh_run_id` disambiguator.
          // Fall back to `gid` when gh_run_id is missing (older rows).
          const disambiguator = entry.gh_run_id
            ? `gh_run_id=${encodeURIComponent(entry.gh_run_id)}`
            : `gid=${entry.report_id}`;
          const urlPath = isLatest ? entry.url_path : `${entry.url_path}?${disambiguator}`;
          const decoratedEntry = { ...entry, url_path: urlPath };
          // Background + hover live on the wrapper so they cover both the
          // link row AND the orchestration progress strip below it. With
          // them on the link, hovering past the bottom edge of the link
          // would lose the highlight while the cursor was still over the
          // same logical card.
          const stats = resolveDisplayStats(decoratedEntry);
          const hasFailed = !!stats && stats.total > 0 && stats.failed > 0;
          const wrapperClass = `rounded-md transition-colors ${
            hasFailed
              ? 'bg-red-50/50 hover:bg-red-50 dark:bg-red-950/20 dark:hover:bg-red-950/30'
              : 'hover:bg-gray-50 dark:hover:bg-gray-800'
          }`;
          return (
            <div key={entry.report_id} className={wrapperClass}>
              {run_entry_row({
                entry: decoratedEntry,
                repoName: group.repository_name || entry.url_path.split('/')[2] || '',
              })}
              {orchestration_strip(decoratedEntry)}
            </div>
          );
        })}
      </div>
    </div>
  );
}
