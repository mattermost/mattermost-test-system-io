import {
  Folder,
  GitBranch,
  GitCommit,
  GitPullRequest,
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
import { ensureRunQueryParams } from '@/lib/report_urls';

function status_icon(entry: RunEntry) {
  const effective = resolveEffectiveReportStatus(
    entry.status,
    entry.last_upload_at,
    entry.orchestration,
  );

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
    if ((stats.flaky ?? 0) > 0) {
      // Passed overall, but at least one test recovered after a retry.
      // Reuse the alert shape (so the row stands out at a glance) but in
      // green (so it doesn't read as a failure). Tooltip explains the
      // distinction so the icon is self-documenting on hover.
      const flaky = stats.flaky ?? 0;
      return (
        <span
          className="inline-flex"
          title={`Passed — ${flaky} flaky test${flaky === 1 ? '' : 's'} recovered after retry; worth a look.`}
        >
          <AlertCircle className="h-4 w-4 text-green-500" />
        </span>
      );
    }
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

function run_entry_row({
  entry,
  repoName,
  rowNumber,
}: {
  entry: RunEntry;
  repoName?: string;
  rowNumber?: number;
}) {
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
      {/* Left: status icon + two-row identity (name/run on top, repo
          context below). The icon centers vertically against the full
          two-row block (items-center on the wrapper). Nothing here is
          truncated — long repo names like `mattermost-test-system-io`
          would otherwise lose characters and force the user to hover
          for context. */}
      <div className="flex items-center gap-2 flex-shrink-0">
        {rowNumber != null && (
          <span className="w-6 flex-shrink-0 text-right text-xs text-gray-400 dark:text-gray-500">
            {rowNumber}
          </span>
        )}
        {status_icon(entry)}
        <div className="flex flex-col gap-0.5">
          {/* Row 1: name + gh_run_id (+ optional non-1 attempt) */}
          <div className="flex items-center gap-2">
            <span className="text-sm text-gray-700 dark:text-gray-300">{entry.name}</span>
            {entry.gh_run_id && (
              <span
                className="inline-flex items-center gap-1 font-mono text-sm text-gray-500 dark:text-gray-500"
                title={`GitHub Actions run ${entry.gh_run_id}`}
              >
                <Play className="h-3.5 w-3.5" />
                {entry.gh_run_id}
              </span>
            )}
            {entry.gh_run_attempt && entry.gh_run_attempt !== '1' && (
              <span
                className="inline-flex items-center gap-1 text-sm text-gray-500 dark:text-gray-500"
                title={`GitHub Actions run attempt ${entry.gh_run_attempt}`}
              >
                <RotateCcw className="h-3.5 w-3.5" />
                attempt {entry.gh_run_attempt}
              </span>
            )}
          </div>
          {/* Row 2: repo + (PR # | branch) + commit */}
          <div className="flex items-center gap-2">
            {repoName && (
              <span className="inline-flex items-center gap-1 text-sm font-medium text-gray-500 dark:text-gray-400">
                <Folder className="h-3.5 w-3.5" />
                {repoName}
              </span>
            )}
            {entry.gh_pr_number != null ? (
              <span className="inline-flex items-center gap-1 text-sm text-gray-600 dark:text-gray-400">
                <GitPullRequest className="h-3.5 w-3.5" />#{entry.gh_pr_number}
              </span>
            ) : (
              <span className="inline-flex items-center gap-1 text-sm text-gray-600 dark:text-gray-400">
                <GitBranch className="h-3.5 w-3.5" />
                {branch}
              </span>
            )}
            <span className="inline-flex items-center gap-1 font-mono text-sm text-gray-500 dark:text-gray-500">
              <GitCommit className="h-3.5 w-3.5" />
              {entry.short_sha}
            </span>
          </div>
        </div>
      </div>

      {/* Middle: spec/test counts, breakdown, pass rate (flex-1 pushes right group to edge) */}
      <div className="flex-1 flex items-center justify-end gap-2 text-sm mx-2">
        {entry.orchestration &&
          entry.orchestration.total_units > 0 &&
          (() => {
            const c = entry.orchestration.counts;
            const done =
              (c.completed_pass ?? 0) +
              (c.completed_fail ?? 0) +
              (c.completed_skipped ?? 0) +
              (c.abandoned ?? 0);
            const total = entry.orchestration.total_units;
            // Icon + color follow the report-summary header convention:
            // alert + blue while in flight, alert + orange after the
            // run terminates with unfinished units, check + neutral
            // once everything is terminal.
            const hasMismatch = done < total;
            const inProgress = entry.orchestration.status === 'in_progress';
            const Icon = hasMismatch ? AlertCircle : CheckCircle;
            const cls = hasMismatch
              ? inProgress
                ? 'inline-flex items-center gap-1 text-blue-600 dark:text-blue-400'
                : 'inline-flex items-center gap-1 text-orange-600 dark:text-orange-400'
              : 'inline-flex items-center gap-1 text-gray-500 dark:text-gray-400';
            return (
              <span
                className={cls}
                title={`${done} of ${total} dispatch units have reached a terminal state`}
              >
                <Icon className="h-3.5 w-3.5" />
                {done}/{total} specs
              </span>
            );
          })()}
        {hasStats && stats && (
          <span
            className="text-gray-500 dark:text-gray-400"
            title={`${stats.passed} passed${stats.failed > 0 ? `, ${stats.failed} failed` : ''}${(stats.flaky ?? 0) > 0 ? `, ${stats.flaky} flaky` : ''}${(stats.skipped ?? 0) > 0 ? `, ${stats.skipped} skipped` : ''} — ${stats.total} total ${unit}`}
          >
            {stats.total} tests {' / '}
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
            className={`rounded px-1.5 py-0.5 text-sm font-medium w-12 text-center ${rateColorClass}`}
            title={`${stats.passed} passed${stats.failed > 0 ? `, ${stats.failed} failed` : ''}${(stats.flaky ?? 0) > 0 ? `, ${stats.flaky} flaky` : ''}${(stats.skipped ?? 0) > 0 ? `, ${stats.skipped} skipped` : ''} — ${stats.total} total ${unit}`}
          >
            {rate}%
          </span>
        )}
      </div>

      {/* Right: total wall-clock + relative time. Total is `last_test_at − begin_at`
          (always; phases may overlap due to per-failure re-dispatch, so summing
          setup + first-pass + retest overcounts). Tooltip surfaces the segment
          breakdown for users who want to see the split, with an explicit
          "may overlap" note. Falls back to shard-level test_stats.wall_clock_ms
          only when no orchestration data is present. */}
      <div className="flex items-center gap-2 text-sm flex-shrink-0">
        <DurationCell entry={entry} stats={stats} hasStats={hasStats} />
        <span className="text-gray-400 dark:text-gray-600 w-16 text-right">
          {format_time(entry.created_at)}
        </span>
      </div>
    </Link>
  );
}

function DurationCell({
  entry,
  stats,
  hasStats,
}: {
  entry: RunEntry;
  stats: ReturnType<typeof resolveDisplayStats>;
  hasStats: boolean;
}) {
  const d = entry.orchestration?.durations;
  if (d && d.begin_at && d.last_test_at) {
    const beginMs = Date.parse(d.begin_at);
    const lastMs = Date.parse(d.last_test_at);
    if (Number.isFinite(beginMs) && Number.isFinite(lastMs) && lastMs > beginMs) {
      const totalMs = lastMs - beginMs;
      const firstTestMs = d.first_test_at ? Date.parse(d.first_test_at) : NaN;
      const setupMs = Number.isFinite(firstTestMs) ? Math.max(0, firstTestMs - beginMs) : null;
      // Pure first-pass = (first_pass_ms − setup), clamped at zero — same
      // derivation the orchestration tab + summary action use. first_pass_ms
      // is begin → first-pass end (the server's existing semantic), so we
      // subtract setup to get the dispatch-only duration.
      const firstPassPureMs =
        d.first_pass_ms != null && setupMs != null
          ? Math.max(0, d.first_pass_ms - setupMs)
          : (d.first_pass_ms ?? null);
      const retestMs = d.retest_ms ?? null;

      const segments = [
        setupMs != null && setupMs > 0 ? `${formatDuration(setupMs)} setup` : null,
        firstPassPureMs != null && firstPassPureMs > 0
          ? `${formatDuration(firstPassPureMs)} first-pass`
          : null,
        retestMs != null && retestMs > 0 ? `${formatDuration(retestMs)} retest` : null,
      ].filter(Boolean);
      const title =
        segments.length > 0 ? `${segments.join(' + ')} (phases may overlap)` : undefined;

      return (
        <span
          className="inline-flex items-center justify-end gap-1 text-gray-400 dark:text-gray-500"
          title={title}
        >
          <Clock className="h-3.5 w-3.5" />
          {formatDuration(totalMs)}
        </span>
      );
    }
  }

  // No orchestration data — fall back to shard-level wall_clock_ms (may
  // under-represent total elapsed time since it excludes setup and retest).
  return (
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
          <Clock className="h-3.5 w-3.5" />
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
  // Left padding aligns the strip's text with the run-name column above:
  // link px-3 (12) + row-number w-6 (24) + gap-2 (8) + status-icon h-4 w-4 (16)
  // + gap-2 (8) = 68px. Right side keeps px-3 for symmetry.
  return (
    <div className="pl-[68px] pr-3 pb-2 -mt-1">
      <OrchestrationInlineSummary orchestration={entry.orchestration} />
    </div>
  );
}

interface RepoGroupCardProps {
  group: RepositoryGroup;
  // 1-based index of the first row in `group.runs` within a paginated list.
  // When the home page is on page 2 with limit 50, pass 51 so rows render
  // 51, 52, … instead of restarting at 1.
  startNumber?: number;
}

export function RepoGroupCard({ group, startNumber = 1 }: RepoGroupCardProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800/50">
      <div className="divide-y divide-gray-50 dark:divide-gray-700/50">
        {group.runs.map((entry, idx) => {
          // Always stamp gh_run_id so same-SHA rows open the correct Actions run
          // (bare consolidated URLs otherwise resolve to the latest run).
          const urlPath = entry.gh_run_id
            ? ensureRunQueryParams(entry.url_path, entry.gh_run_id, entry.gh_run_attempt)
            : `${entry.url_path}${entry.url_path.includes('?') ? '&' : '?'}gid=${entry.report_id}`;
          const decoratedEntry = { ...entry, url_path: urlPath };
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
                rowNumber: startNumber + idx,
              })}
              {orchestration_strip(decoratedEntry)}
            </div>
          );
        })}
      </div>
    </div>
  );
}
