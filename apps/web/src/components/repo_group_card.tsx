import {
  Folder,
  GitBranch,
  GitCommit,
  GitPullRequest,
  Clock,
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
import { resolveEffectiveReportStatus } from '@/components/report_summary';
import { ensureRunQueryParams } from '@/lib/report_urls';
import { LivePulseDot } from '@/components/live_pulse_dot';

function status_icon(entry: RunEntry) {
  const effective = resolveEffectiveReportStatus(
    entry.status,
    entry.last_upload_at,
    entry.orchestration,
  );
  const orchStatus = entry.orchestration?.status;

  let pill: React.ReactNode;
  if (effective === 'in_progress' || orchStatus === 'in_progress') {
    pill = <LivePulseDot />;
  } else {
    const stats = resolveDisplayStats(entry);
    const counts = entry.orchestration?.counts;
    const failed =
      effective === 'incomplete' ||
      orchStatus === 'timed_out' ||
      (counts?.abandoned ?? 0) > 0 ||
      (counts?.completed_fail ?? 0) > 0 ||
      (!!stats && stats.failed > 0);

    pill = failed ? (
      <span className="inline-flex items-center rounded bg-red-100 px-2 py-0.5 text-xs font-bold tracking-wide text-red-700 dark:bg-red-950/50 dark:text-red-400">
        FAIL
      </span>
    ) : (
      <span className="inline-flex items-center rounded bg-green-100 px-2 py-0.5 text-xs font-bold tracking-wide text-green-700 dark:bg-green-950/50 dark:text-green-400">
        PASS
      </span>
    );
  }

  const createdMs = Date.parse(entry.created_at);
  const isNew =
    Number.isFinite(createdMs) &&
    Date.now() - createdMs >= 0 &&
    Date.now() - createdMs <= 60 * 60 * 1000 &&
    orchStatus !== 'in_progress' &&
    effective !== 'in_progress';

  return (
    <span className="inline-flex flex-col items-center justify-center gap-0.5">
      {pill}
      {isNew && (
        <span className="text-[9px] font-bold tracking-wider text-blue-600 dark:text-blue-400" title="Created within the last hour">
          NEW
        </span>
      )}
    </span>
  );
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

function StatDot() {
  return (
    <span className="shrink-0 select-none text-gray-300 dark:text-gray-600" aria-hidden="true">
      ·
    </span>
  );
}

function StatSep() {
  return (
    <span className="shrink-0 select-none text-gray-300 dark:text-gray-600" aria-hidden="true">
      |
    </span>
  );
}

function StatWord({ children }: { children: string }) {
  return <span className="max-lg:hidden">{children}</span>;
}

function StatLine({
  title,
  className,
  parts,
}: {
  title?: string;
  className?: string;
  parts: Array<React.ReactNode | false | null | undefined>;
}) {
  const segments = parts.filter((part) => part != null && part !== false);
  if (segments.length === 0) return null;

  const nodes: React.ReactNode[] = [segments[0]];
  for (let i = 1; i < segments.length; i++) {
    nodes.push(
      <span key={`seg-${i}`} className="inline-flex items-center gap-x-1 whitespace-nowrap">
        <StatDot />
        {segments[i]}
      </span>,
    );
  }

  return (
    <span
      className={`inline-flex max-w-full flex-wrap items-center justify-end gap-x-1.5 gap-y-0.5 text-right ${className ?? ''}`}
      title={title}
    >
      {nodes}
    </span>
  );
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
      className="flex cursor-pointer items-center px-3 py-2 text-sm max-lg:grid max-lg:items-center max-lg:gap-x-2 max-lg:gap-y-1.5 max-sm:grid-cols-[1.5rem_3.25rem_minmax(0,1fr)] max-sm:grid-rows-[repeat(5,auto)] sm:max-lg:grid-cols-[1.5rem_3.25rem_minmax(0,1fr)_auto] sm:max-lg:grid-rows-[repeat(3,auto)]"
    >
      {rowNumber != null && (
        <span className="inline-flex w-6 flex-shrink-0 items-center justify-end self-stretch text-right text-xs leading-none text-gray-400 max-lg:col-start-1 max-lg:row-span-full dark:text-gray-500">
          {rowNumber}
        </span>
      )}
      <span className="inline-flex min-w-[3rem] flex-shrink-0 items-center justify-center self-stretch max-lg:col-start-2 max-lg:row-span-full">
        {status_icon(entry)}
      </span>
      <div className="flex min-w-0 shrink flex-col gap-0.5 max-lg:contents">
          <div className="flex flex-wrap items-center gap-2 max-lg:col-start-3 max-lg:row-start-1 max-lg:min-w-0">
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
          <div className="flex flex-wrap items-center gap-2 max-lg:col-start-3 max-lg:row-start-2 max-lg:min-w-0">
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

      <div className="mx-2 flex flex-1 items-center justify-end gap-2 text-sm max-lg:contents max-lg:mx-0 lg:flex-col lg:items-end lg:gap-0.5 lg:justify-center">
        {entry.orchestration &&
          entry.orchestration.total_units > 0 &&
          (() => {
            const c = entry.orchestration.counts;
            const pass = c.completed_pass ?? 0;
            const fail = c.completed_fail ?? 0;
            const skipped = c.completed_skipped ?? 0;
            const abandoned = c.abandoned ?? 0;
            const pending = c.pending ?? 0;
            const leased = c.leased ?? 0;
            const retestEligible = c.retest_eligible ?? 0;
            const total = entry.orchestration.total_units;
            const inProgress = entry.orchestration.status === 'in_progress';
            const abandonedUntested = !inProgress && abandoned > 0 ? abandoned : 0;
            return (
              <StatLine
                className="max-sm:col-start-3 max-sm:row-start-3 max-sm:min-w-0 max-sm:justify-self-start max-sm:justify-start sm:max-lg:col-start-4 sm:max-lg:row-start-1 sm:max-lg:min-w-0 sm:max-lg:justify-self-end text-gray-500 dark:text-gray-400"
                title={`${total} specs · ${pass} passed${fail > 0 ? ` · ${fail} failed` : ''}${skipped > 0 ? ` · ${skipped} skipped` : ''}${inProgress && pending > 0 ? ` · ${pending} pending` : ''}${inProgress && leased > 0 ? ` · ${leased} running` : ''}${abandonedUntested > 0 ? ` · ${abandonedUntested} no report` : ''}${retestEligible > 0 ? ` · (${retestEligible} for retest)` : ''}`}
                parts={[
                  <span key="total" className="whitespace-nowrap text-gray-500 dark:text-gray-400">
                    {total} specs
                  </span>,
                  <span key="pass" className="whitespace-nowrap text-green-500">
                    {pass}
                    <StatWord> passed</StatWord>
                  </span>,
                  fail > 0 && (
                    <span key="fail" className="whitespace-nowrap text-red-500">
                      {fail}
                      <StatWord> failed</StatWord>
                    </span>
                  ),
                  skipped > 0 && (
                    <span key="skipped" className="whitespace-nowrap text-gray-500 dark:text-gray-400">
                      {skipped}
                      <StatWord> skipped</StatWord>
                    </span>
                  ),
                  inProgress &&
                    pending > 0 && (
                      <span key="pending" className="whitespace-nowrap text-gray-500 dark:text-gray-400">
                        {pending}
                        <StatWord> pending</StatWord>
                      </span>
                    ),
                  inProgress &&
                    leased > 0 && (
                      <span key="leased" className="whitespace-nowrap text-blue-600 dark:text-blue-400">
                        {leased}
                        <StatWord> running</StatWord>
                      </span>
                    ),
                  abandonedUntested > 0 && (
                    <span key="abandoned" className="whitespace-nowrap text-red-500">
                      {abandonedUntested}
                      <StatWord> no report</StatWord>
                    </span>
                  ),
                  retestEligible > 0 && (
                    <span key="retest" className="whitespace-nowrap text-orange-700 dark:text-orange-400">
                      ({retestEligible}
                      <StatWord> for retest</StatWord>)
                    </span>
                  ),
                ]}
              />
            );
          })()}
        {hasStats && stats && (
          <div className="inline-flex max-w-full items-center justify-end gap-x-1.5 max-sm:col-start-3 max-sm:row-start-4 max-sm:min-w-0 max-sm:justify-self-start max-sm:justify-start sm:max-lg:col-start-4 sm:max-lg:row-start-2 sm:max-lg:min-w-0 sm:max-lg:justify-self-end">
            <StatLine
              className="min-w-0 max-sm:justify-start text-gray-500 dark:text-gray-400"
              title={`${stats.total} tests · ${stats.passed} passed${stats.failed > 0 ? ` · ${stats.failed} failed` : ''}${(stats.flaky ?? 0) > 0 ? ` · ${stats.flaky} flaky` : ''}${(stats.skipped ?? 0) > 0 ? ` · ${stats.skipped} skipped` : ''}`}
              parts={[
                <span key="total" className="whitespace-nowrap text-gray-500 dark:text-gray-400">
                  {stats.total} tests
                </span>,
                <span key="pass" className="whitespace-nowrap text-green-500">
                  {stats.passed}
                  <StatWord> passed</StatWord>
                </span>,
                stats.failed > 0 && (
                  <span key="fail" className="whitespace-nowrap text-red-500">
                    {stats.failed}
                    <StatWord> failed</StatWord>
                  </span>
                ),
                (stats.flaky ?? 0) > 0 && (
                  <span key="flaky" className="whitespace-nowrap text-yellow-700 dark:text-yellow-400">
                    {stats.flaky}
                    <StatWord> flaky</StatWord>
                  </span>
                ),
                (stats.skipped ?? 0) > 0 && (
                  <span key="skipped" className="whitespace-nowrap text-gray-500 dark:text-gray-400">
                    {stats.skipped}
                    <StatWord> skipped</StatWord>
                  </span>
                ),
              ]}
            />
            {rate !== null && (
              <span
                className="inline-flex items-center gap-x-1.5 whitespace-nowrap text-sm font-medium"
                title={`${stats.passed} passed${stats.failed > 0 ? `, ${stats.failed} failed` : ''}${(stats.flaky ?? 0) > 0 ? `, ${stats.flaky} flaky` : ''}${(stats.skipped ?? 0) > 0 ? `, ${stats.skipped} skipped` : ''} — ${stats.total} total ${unit}`}
              >
                <StatSep />
                <span className={rateColorClass}>{rate}%</span>
              </span>
            )}
          </div>
        )}
      </div>

      {/* Right: total wall-clock + relative time. Total is `last_test_at − begin_at`
          (always; phases may overlap due to per-failure re-dispatch, so summing
          setup + first-pass + retest overcounts). Tooltip surfaces the segment
          breakdown for users who want to see the split, with an explicit
          "may overlap" note. Falls back to shard-level test_stats.wall_clock_ms
          only when no orchestration data is present. */}
      <div className="flex flex-shrink-0 items-center gap-2 text-sm max-sm:col-start-3 max-sm:row-start-5 max-sm:w-auto max-sm:max-w-full max-sm:justify-self-start max-sm:justify-start sm:max-lg:col-span-2 sm:max-lg:col-start-3 sm:max-lg:row-start-3 sm:max-lg:w-auto sm:max-lg:max-w-full sm:max-lg:justify-self-end sm:max-lg:justify-end lg:grid lg:w-auto lg:min-w-[13rem] lg:grid-cols-[7rem_auto] lg:gap-2 lg:justify-items-end">
        <div className="flex w-full min-w-0 justify-end overflow-hidden max-sm:justify-start sm:max-lg:justify-end lg:w-[7rem]">
          <DurationCell entry={entry} stats={stats} hasStats={hasStats} />
        </div>
        <span className="shrink-0 whitespace-nowrap text-right tabular-nums text-gray-400 max-sm:text-left sm:max-lg:text-right dark:text-gray-600 lg:min-w-[5.5rem]">
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
          className="inline-flex min-w-0 max-w-full items-center justify-end gap-1 truncate tabular-nums text-gray-400 max-sm:justify-start sm:max-lg:justify-end dark:text-gray-500"
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
      className="inline-flex min-w-0 max-w-full items-center justify-end gap-1 truncate tabular-nums text-gray-400 max-sm:justify-start sm:max-lg:justify-end dark:text-gray-500"
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

interface RepoGroupCardProps {
  group: RepositoryGroup;
  // 1-based index of the first row in `group.runs` within a paginated list.
  // When the home page is on page 2 with limit 50, pass 51 so rows render
  // 51, 52, … instead of restarting at 1.
  startNumber?: number;
  // Omit outer card chrome when nested.
  bare?: boolean;
}

export function RepoGroupCard({ group, startNumber = 1, bare = false }: RepoGroupCardProps) {
  const list = (
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
            </div>
          );
        })}
      </div>
  );
  if (bare) return list;
  return (
    <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800/50">
      {list}
    </div>
  );
}
