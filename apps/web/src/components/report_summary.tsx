import {
  CheckCircle,
  XCircle,
  AlertCircle,
  Clock,
  Calendar,
  Loader2,
  FlaskConical,
  Folder,
  GitBranch,
  GitCommit,
  GitPullRequest,
  Play,
  ExternalLink,
} from 'lucide-react';
import {
  formatDuration,
  calculateRawPassRate,
  getPassRateColorClass,
  formatTimeline,
} from '@/components/report_card_parts';

// Idle window past which an in-flight report group is rendered as
// `incomplete` even though the server hasn't yet committed to the
// transition. The server-side reaper takes 1 hour; surfacing the optimistic
// label sooner is honest about the run looking stuck without flipping the
// DB state. See resolveEffectiveReportStatus.
const REPORT_INCOMPLETE_THRESHOLD_MS = 10 * 60 * 1000; // 10 minutes

function formatDate(dateString: string): string {
  return new Date(dateString).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
}

type TestStatus = 'passed' | 'failed' | 'timed_out';
type ProgressStatus = 'in_progress' | 'completed' | 'timed_out' | 'incomplete';

/**
 * Effective progress status for a report group row.
 *
 * When an orchestration run is associated, its lifecycle drives the badge —
 * dispatch-mode shards upload at queue-empty, so the upload side can be
 * idle for long stretches while the run is still healthy.
 *
 * Without orchestration, the upload-side `in_progress` is promoted to
 * `incomplete` after a 10-minute gap past `last_upload_at` so a stuck
 * shard surfaces before the server-side reaper transitions it.
 */
export function resolveEffectiveReportStatus(
  status: ProgressStatus,
  lastUploadAt?: string,
  orchestration?: { status: 'in_progress' | 'completed' | 'timed_out' } | null,
): ProgressStatus {
  if (orchestration) {
    return orchestration.status;
  }
  if (status === 'in_progress' && lastUploadAt) {
    const idleMs = Date.now() - new Date(lastUploadAt).getTime();
    if (Number.isFinite(idleMs) && idleMs > REPORT_INCOMPLETE_THRESHOLD_MS) {
      return 'incomplete';
    }
  }
  return status;
}

export interface ReportSummaryProps {
  // Row 1: test result badge + optional name(s) with links
  /**
   * Pass/fail/flaky verdict for the run. Optional — when undefined the
   * badge is omitted, used while the run is still pending or in flight
   * and there are no completed test results to roll up yet.
   */
  testStatus?: TestStatus;
  name?: string;
  nameHref?: string;
  /** Multiple name links (used on commit/target pages with several report groups) */
  nameLinks?: { label: string; href: string }[];
  /** Secondary name shown after the main name with a separator (e.g., individual report shard) */
  secondaryName?: string;
  /** Whether the secondary name indicates a failure (shown with red background) */
  secondaryNameFailed?: boolean;

  // Row 2: stats + duration
  passed: number;
  failed: number;
  flaky: number;
  skipped: number;
  total: number;
  /**
   * When true, counts are unique test titles after cross-shard rollup
   * (consolidated report page). Labels say "unique" so they are not
   * confused with the table's per-shard failure sum.
   */
  uniqueTitleCounts?: boolean;
  /**
   * Wall-clock span between run begin and the first worker checkout —
   * cloud-init / start-server / playwright prepare, before any spec was
   * leased. Renders before `durationMs` as `(N setup) + ...`. Optional;
   * the consolidated rollup doesn't expose this, only the orchestration
   * tab populates it.
   */
  setupDurationMs?: number | null;
  durationMs?: number | null;
  /** Wall-clock span of retest shards alone. Renders after `durationMs` with a `+` separator. */
  retestDurationMs?: number | null;

  /**
   * Orchestration timeline anchors. When provided, a "begin → first test
   * → first retest → last test" row renders directly under the duration
   * pill. Same-day moments collapse to a single date headline + time
   * segments; cross-day timelines drop the headline and prefix each
   * segment with date + time.
   */
  beginAt?: string | null;
  firstTestAt?: string | null;
  firstRetestAt?: string | null;
  lastTestAt?: string | null;

  // Row 3: metadata
  createdAt?: string;
  framework?: string;
  reportCount?: number;
  /** Retest shard count. Rendered as `{reportCount}+{retestReportCount}` when > 0. */
  retestReportCount?: number;
  progressStatus?: ProgressStatus;
  /** Number of shards declared at /reports/begin. Renders as (N/M) on the incomplete badge. */
  totalReportsExpected?: number;
  /**
   * Dispatch units that have reached a terminal state (completed_pass /
   * completed_fail / completed_skipped / abandoned). Used by the Combine
   * and Dispatch tabs alongside `totalSpecs` to render an inline
   * `[check] N specs` (when all done) or `[!] M/N specs` (when some
   * are still pending or leased).
   */
  specsCount?: number;
  /** Total dispatch units declared at /orchestration/begin (run.total_units). */
  totalSpecs?: number;
  /**
   * Suppress the standalone Completed/Incomplete badge. Used on the
   * Reports tab where the report-count chip itself carries the
   * shard-completeness signal (warning icon + `M/N reports` when
   * uploaded < expected, otherwise check + `N reports`), making the
   * separate badge redundant.
   */
  hideProgressBadge?: boolean;

  // Row 4: git context badges
  repository?: string;
  branch?: string;
  commit?: string;
  ghPrNumber?: number;
  ghRunId?: string;
  ghJobId?: string;
}

function TestStatusBadge({ status }: { status: TestStatus }) {
  switch (status) {
    case 'passed':
      return (
        <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-sm font-semibold bg-green-100 text-green-800 dark:bg-green-900/50 dark:text-green-200">
          <CheckCircle className="h-4 w-4" />
          Passed
        </span>
      );
    case 'failed':
      return (
        <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-sm font-semibold bg-red-100 text-red-800 dark:bg-red-900/50 dark:text-red-200">
          <XCircle className="h-4 w-4" />
          Failed
        </span>
      );
    case 'timed_out':
      return (
        <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-sm font-semibold bg-orange-100 text-orange-800 dark:bg-orange-900/50 dark:text-orange-200">
          <AlertCircle className="h-4 w-4" />
          Timed Out
        </span>
      );
  }
}

function ProgressBadge({
  status,
  reportsCount,
  totalReportsExpected,
}: {
  status: ProgressStatus;
  reportsCount?: number;
  totalReportsExpected?: number;
}) {
  switch (status) {
    case 'timed_out':
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/50 dark:text-orange-300">
          <AlertCircle className="h-3 w-3" />
          Timed Out
        </span>
      );
    case 'incomplete': {
      const counts =
        totalReportsExpected && reportsCount != null
          ? ` (${reportsCount}/${totalReportsExpected})`
          : '';
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/50 dark:text-orange-300">
          <AlertCircle className="h-3 w-3" />
          {`Incomplete${counts}`}
        </span>
      );
    }
    case 'in_progress':
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-700 dark:bg-blue-900/50 dark:text-blue-300">
          <Loader2 className="h-3 w-3 animate-spin" />
          In Progress
        </span>
      );
    case 'completed':
    default:
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-700 dark:bg-green-900/50 dark:text-green-300">
          <CheckCircle className="h-3 w-3" />
          Completed
        </span>
      );
  }
}

export function ReportSummary(props: ReportSummaryProps) {
  const {
    testStatus,
    name,
    nameHref,
    nameLinks,
    secondaryName,
    secondaryNameFailed,
    passed,
    failed,
    flaky,
    skipped,
    total,
    uniqueTitleCounts,
    setupDurationMs,
    durationMs,
    retestDurationMs,
    beginAt,
    firstTestAt,
    firstRetestAt,
    lastTestAt,
    createdAt,
    framework,
    reportCount,
    retestReportCount,
    totalReportsExpected,
    progressStatus,
    hideProgressBadge,
    specsCount,
    totalSpecs,
    repository,
    branch,
    commit,
    ghPrNumber,
    ghRunId,
    ghJobId,
  } = props;

  const passRate = calculateRawPassRate({ passed, failed, flaky });
  const passRateColorClass = getPassRateColorClass(passRate);
  const failedLabel = uniqueTitleCounts ? `unique test${failed === 1 ? '' : 's'} failed` : 'failed';
  const totalLabel = uniqueTitleCounts ? `unique test${total === 1 ? '' : 's'}` : 'total';
  const statsTitle = uniqueTitleCounts
    ? `${passed} passed${failed > 0 ? `, ${failed} unique test${failed === 1 ? '' : 's'} failed` : ''}${flaky > 0 ? `, ${flaky} flaky` : ''}${skipped > 0 ? `, ${skipped} skipped` : ''} — ${total} unique test${total === 1 ? '' : 's'} (cross-shard / cross-platform titles rolled up)`
    : `${passed} passed${failed > 0 ? `, ${failed} failed` : ''}${flaky > 0 ? `, ${flaky} flaky` : ''}${skipped > 0 ? `, ${skipped} skipped` : ''} — ${total} total`;

  const hasMetadata =
    createdAt || framework || reportCount != null || totalSpecs != null || progressStatus;
  const hasGitContext = repository;

  return (
    <div>
      {/* Row 1: Test status badge + name(s) */}
      <div className="flex flex-wrap items-center gap-2 mt-3">
        {testStatus && <TestStatusBadge status={testStatus} />}
        {nameLinks && nameLinks.length > 0 ? (
          nameLinks.map((link) => (
            <a
              key={link.href}
              href={link.href}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-sm font-semibold text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300 transition-colors"
            >
              {link.label}
              <ExternalLink className="h-3.5 w-3.5 opacity-60" />
            </a>
          ))
        ) : name && nameHref ? (
          <a
            href={nameHref}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-sm font-semibold text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300 transition-colors"
          >
            {name}
            <ExternalLink className="h-3.5 w-3.5 opacity-60" />
          </a>
        ) : name ? (
          <span className="text-sm font-semibold text-gray-900 dark:text-white">{name}</span>
        ) : null}
        {secondaryName && (
          <>
            <span className="text-sm text-gray-400 dark:text-gray-500">/</span>
            <span
              className={`text-sm px-1.5 py-0.5 rounded ${
                secondaryNameFailed
                  ? 'bg-red-100 text-red-700 font-medium dark:bg-red-900/50 dark:text-red-300'
                  : 'text-gray-600 dark:text-gray-300'
              }`}
            >
              {secondaryName}
            </span>
          </>
        )}
      </div>

      {/* Row 2: Pass rate + stats + duration. Suppressed entirely
          when no specs have been recorded yet — "0 passed / 0 total"
          on a fresh in-progress run is noise, not signal. */}
      {total > 0 && (
        <div className="flex items-center gap-3 mt-2">
          {passRate !== null && (
            <span
              className={`rounded px-1.5 py-0.5 text-sm font-medium ${passRateColorClass}`}
              title={statsTitle}
            >
              {passRate}%
            </span>
          )}
          <span className="text-sm text-gray-600 dark:text-gray-300" title={statsTitle}>
            <span className="font-medium">{passed}</span> passed
            {failed > 0 && (
              <>
                {' / '}
                <span
                  className="font-medium px-1.5 py-0.5 rounded bg-red-100 text-red-700 dark:bg-red-900/50 dark:text-red-300"
                  title={
                    uniqueTitleCounts
                      ? 'Unique test titles still failing after cross-shard rollup'
                      : undefined
                  }
                >
                  {failed} {failedLabel}
                </span>
              </>
            )}
            {flaky > 0 && (
              <>
                {' / '}
                <span className="font-medium px-1.5 py-0.5 rounded bg-yellow-100 text-yellow-700 dark:bg-yellow-900/50 dark:text-yellow-300">
                  {flaky} flaky
                </span>
              </>
            )}
            {skipped > 0 && (
              <>
                {' / '}
                <span className="font-medium">{skipped}</span> skipped
              </>
            )}
            {' / '}
            <span className="font-medium">{total}</span> {totalLabel}
          </span>
          {durationMs != null &&
            (() => {
              const hasSetup = setupDurationMs != null && setupDurationMs > 0;
              const hasRetest = retestDurationMs != null && retestDurationMs > 0;
              const titleParts: string[] = [];
              if (hasSetup) {
                titleParts.push(
                  `${formatDuration(setupDurationMs!)} setup time (begin → first checkout)`,
                );
              }
              titleParts.push(`${formatDuration(durationMs)} first-pass`);
              if (hasRetest) titleParts.push(`${formatDuration(retestDurationMs!)} retest`);
              const title = hasSetup || hasRetest ? titleParts.join(' + ') : undefined;
              return (
                <span
                  className="inline-flex items-center gap-1 text-sm text-gray-500 dark:text-gray-400"
                  title={title}
                >
                  <Clock className="h-3.5 w-3.5" />
                  {hasSetup && (
                    <span className="text-gray-400 dark:text-gray-500">
                      {`(${formatDuration(setupDurationMs!)} setup) + `}
                    </span>
                  )}
                  {formatDuration(durationMs)}
                  {hasRetest && (
                    <span className="text-gray-400 dark:text-gray-500">
                      {' + '}
                      {formatDuration(retestDurationMs!)}
                    </span>
                  )}
                </span>
              );
            })()}
        </div>
      )}

      {/* Row 2b: orchestration timeline (begin → first test → first
          retest → last test). Same-day collapses to a single date
          headline + time segments; cross-day timelines drop the
          headline and prefix each segment with date+time. */}
      {(() => {
        const timeline = formatTimeline({
          beginAt,
          firstTestAt,
          firstRetestAt,
          lastTestAt,
        });
        if (!timeline.hasContent) return null;
        return (
          <div className="flex items-center gap-1.5 mt-1.5 text-xs text-gray-500 dark:text-gray-400">
            <Calendar className="h-3 w-3" aria-hidden="true" />
            {timeline.headline && (
              <span className="font-medium text-gray-600 dark:text-gray-300">
                {timeline.headline}
              </span>
            )}
            {timeline.segments.map((seg, i) => (
              <span key={seg.kind} className="inline-flex items-center gap-1">
                {(timeline.headline || i > 0) && (
                  <span aria-hidden="true" className="text-gray-300 dark:text-gray-600">
                    →
                  </span>
                )}
                <span className="text-gray-500 dark:text-gray-500">{seg.label}</span>
                <span className="text-gray-700 dark:text-gray-300">{seg.text}</span>
              </span>
            ))}
          </div>
        );
      })()}

      {/* Row 3: Metadata (date, framework, report count, progress status) */}
      {hasMetadata && (
        <div className="flex items-center gap-3 mt-2 text-sm text-gray-500 dark:text-gray-400">
          {createdAt && (
            <span className="inline-flex items-center gap-1">
              <Calendar className="h-3.5 w-3.5" />
              {formatDate(createdAt)}
            </span>
          )}
          {framework && (
            <span className="inline-flex items-center gap-1">
              <FlaskConical className="h-3.5 w-3.5" />
              {framework.charAt(0).toUpperCase() + framework.slice(1)}
            </span>
          )}
          {totalSpecs != null &&
            totalSpecs > 0 &&
            (() => {
              // Orchestration-driven view (Combine / Dispatch tabs):
              // mirror the report-count chip's shape so the user can
              // see at a glance how many specs are still in flight.
              // Blue while the run is mid-flight (the gap is expected),
              // orange after it has reached a terminal state with
              // unfinished units (the gap is a problem).
              const done = specsCount ?? 0;
              const hasMismatch = done < totalSpecs;
              const inProgress = progressStatus === 'in_progress';
              const Icon = hasMismatch ? AlertCircle : CheckCircle;
              const cls = hasMismatch
                ? inProgress
                  ? 'inline-flex items-center gap-1 text-blue-600 dark:text-blue-400'
                  : 'inline-flex items-center gap-1 text-orange-600 dark:text-orange-400'
                : 'inline-flex items-center gap-1';
              const label = hasMismatch ? `${done}/${totalSpecs}` : `${totalSpecs}`;
              return (
                <span
                  className={cls}
                  title={
                    hasMismatch
                      ? `${done} of ${totalSpecs} dispatch units have reached a terminal state`
                      : `${totalSpecs} dispatch units complete`
                  }
                >
                  <Icon className="h-3.5 w-3.5" />
                  {label} {totalSpecs === 1 ? 'spec' : 'specs'}
                </span>
              );
            })()}
          {reportCount != null &&
            reportCount + (retestReportCount ?? 0) > 0 &&
            (() => {
              // Numbered shards drive the "expected" check — retest shards
              // are best-effort extras outside the begin-time count.
              const hasShardMismatch =
                totalReportsExpected != null && reportCount < totalReportsExpected;
              const Icon = hasShardMismatch ? AlertCircle : CheckCircle;
              const cls = hasShardMismatch
                ? 'inline-flex items-center gap-1 text-orange-600 dark:text-orange-400'
                : 'inline-flex items-center gap-1';
              const label = hasShardMismatch
                ? `${reportCount}/${totalReportsExpected}`
                : `${reportCount}`;
              return (
                <span
                  className={cls}
                  title={
                    hasShardMismatch
                      ? `${reportCount} of ${totalReportsExpected} expected shards uploaded${retestReportCount ? `; ${retestReportCount} retest` : ''}`
                      : retestReportCount && retestReportCount > 0
                        ? `${reportCount} numbered shard${reportCount === 1 ? '' : 's'}, ${retestReportCount} retest`
                        : undefined
                  }
                >
                  <Icon className="h-3.5 w-3.5" />
                  {label}
                  {retestReportCount != null && retestReportCount > 0 && (
                    <>+{retestReportCount}</>
                  )}{' '}
                  {reportCount + (retestReportCount ?? 0) === 1 ? 'report' : 'reports'}
                </span>
              );
            })()}
          {progressStatus && !hideProgressBadge && (
            <ProgressBadge
              status={progressStatus}
              reportsCount={reportCount}
              totalReportsExpected={totalReportsExpected}
            />
          )}
        </div>
      )}

      {/* Row 4: Git context badges */}
      {hasGitContext && (
        <div className="flex flex-wrap items-center gap-1.5 mt-3">
          <a
            href={`https://github.com/${repository}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 px-2 py-1 bg-gray-100 rounded-md text-xs text-gray-700 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600 transition-colors"
          >
            <Folder className="h-3 w-3" />
            {repository}
          </a>
          {branch && !ghPrNumber && (
            <span className="inline-flex items-center gap-1.5 px-2 py-1 bg-blue-100 rounded-md text-xs text-blue-700 dark:bg-blue-900/50 dark:text-blue-300">
              <GitBranch className="h-3 w-3" />
              {branch.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '')}
            </span>
          )}
          {ghPrNumber && (
            <a
              href={`https://github.com/${repository}/pull/${ghPrNumber}`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 px-2 py-1 bg-purple-100 rounded-md text-xs text-purple-700 hover:bg-purple-200 dark:bg-purple-900/50 dark:text-purple-300 dark:hover:bg-purple-900/70 transition-colors"
            >
              <GitPullRequest className="h-3 w-3" />#{ghPrNumber}
            </a>
          )}
          {commit && (
            <a
              href={`https://github.com/${repository}/commit/${commit}`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 px-2 py-1 bg-gray-100 rounded-md text-xs font-mono text-gray-600 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600 transition-colors"
            >
              <GitCommit className="h-3 w-3" />
              {commit.slice(0, 7)}
            </a>
          )}
          {ghRunId && (
            <a
              href={`https://github.com/${repository}/actions/runs/${ghRunId}`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 px-2 py-1 bg-green-100 rounded-md text-xs text-green-700 hover:bg-green-200 dark:bg-green-900/50 dark:text-green-300 dark:hover:bg-green-900/70 transition-colors"
            >
              <Play className="h-3 w-3" />
              Run {ghRunId}
            </a>
          )}
          {ghJobId && ghRunId && repository && (
            <a
              href={`https://github.com/${repository}/actions/runs/${ghRunId}/job/${ghJobId}`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 px-2 py-1 bg-orange-100 rounded-md text-xs font-mono text-orange-700 hover:bg-orange-200 dark:bg-orange-900/50 dark:text-orange-300 dark:hover:bg-orange-900/70 transition-colors"
            >
              <Play className="h-3 w-3" />
              Job {ghJobId}
            </a>
          )}
        </div>
      )}
    </div>
  );
}
