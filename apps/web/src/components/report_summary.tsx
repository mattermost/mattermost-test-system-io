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
import { useEffect, useState } from 'react';
import {
  formatDuration,
  calculatePassRate,
  getPassRateColorClass,
} from '@/components/report_card_parts';

const TIMED_OUT_THRESHOLD_MS = 3_600_000; // 1 hour

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
type ProgressStatus = 'in_progress' | 'completed' | 'timed_out';

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
  durationMs?: number | null;
  /** Wall-clock span of retest shards alone. Renders after `durationMs` with a `+` separator. */
  retestDurationMs?: number | null;

  // Row 3: metadata
  createdAt?: string;
  framework?: string;
  reportCount?: number;
  /** Retest shard count. Rendered as `{reportCount}+{retestReportCount}` when > 0. */
  retestReportCount?: number;
  progressStatus?: ProgressStatus;
  /**
   * For orchestrated runs in progress: when the run was begun. Combined
   * with `firstCheckoutAt` (or now, when no spec has been leased yet) to
   * surface a live "setup + running" duration next to the In Progress
   * badge so users can tell how long the run has been going.
   */
  runStartedAt?: string;
  /**
   * For orchestrated runs in progress: when the first spec was checked
   * out by a worker (= earliest unit attempt). When undefined the run is
   * still in the setup phase (cloud-init / start-server / prepare).
   */
  firstCheckoutAt?: string | null;

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

function ProgressDurations({
  runStartedAt,
  firstCheckoutAt,
}: {
  runStartedAt: string;
  firstCheckoutAt?: string | null;
}) {
  // Live tick so the running counter updates without a refetch. Cheap —
  // a single 1s interval scoped to the in-progress badge only.
  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);
  const startMs = Date.parse(runStartedAt);
  if (!Number.isFinite(startMs)) return null;
  const checkoutMs = firstCheckoutAt ? Date.parse(firstCheckoutAt) : NaN;
  const hasCheckout = Number.isFinite(checkoutMs);
  const setupMs = hasCheckout ? Math.max(0, checkoutMs - startMs) : Math.max(0, nowMs - startMs);
  const runningMs = hasCheckout ? Math.max(0, nowMs - checkoutMs) : 0;
  const label = hasCheckout
    ? `(${formatDuration(setupMs)} setup) + ${formatDuration(runningMs)} running`
    : `(${formatDuration(setupMs)} setup)`;
  return (
    <span
      className="text-xs text-gray-500 dark:text-gray-400 tabular-nums"
      title="Setup = run begin → first spec checkout. Running = first checkout → now."
    >
      {label}
    </span>
  );
}

function ProgressBadge({ status, createdAt }: { status: ProgressStatus; createdAt?: string }) {
  // Check for timed-out in_progress
  if (status === 'in_progress' && createdAt) {
    const isTimedOut = new Date(createdAt).getTime() < Date.now() - TIMED_OUT_THRESHOLD_MS;
    if (isTimedOut) {
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/50 dark:text-orange-300">
          <AlertCircle className="h-3 w-3" />
          Timed Out
        </span>
      );
    }
  }

  switch (status) {
    case 'timed_out':
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/50 dark:text-orange-300">
          <AlertCircle className="h-3 w-3" />
          Timed Out
        </span>
      );
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
    durationMs,
    retestDurationMs,
    createdAt,
    framework,
    reportCount,
    retestReportCount,
    progressStatus,
    runStartedAt,
    firstCheckoutAt,
    repository,
    branch,
    commit,
    ghPrNumber,
    ghRunId,
    ghJobId,
  } = props;

  const passRate = calculatePassRate({ passed, failed, flaky });
  const passRateColorClass = getPassRateColorClass(passRate);
  const statsTitle = `${passed} passed${failed > 0 ? `, ${failed} failed` : ''}${flaky > 0 ? `, ${flaky} flaky` : ''}${skipped > 0 ? `, ${skipped} skipped` : ''} — ${total} total`;

  const hasMetadata = createdAt || framework || reportCount != null || progressStatus;
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
                <span className="font-medium px-1.5 py-0.5 rounded bg-red-100 text-red-700 dark:bg-red-900/50 dark:text-red-300">
                  {failed} failed
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
            <span className="font-medium">{total}</span> total
          </span>
          {durationMs != null && (
            <span
              className="inline-flex items-center gap-1 text-sm text-gray-500 dark:text-gray-400"
              title={
                retestDurationMs != null && retestDurationMs > 0
                  ? 'Parallel shard batch, then separate retest run'
                  : undefined
              }
            >
              <Clock className="h-3.5 w-3.5" />
              {formatDuration(durationMs)}
              {retestDurationMs != null && retestDurationMs > 0 && (
                <span className="text-gray-400 dark:text-gray-500">
                  {' + '}
                  {formatDuration(retestDurationMs)}
                </span>
              )}
            </span>
          )}
        </div>
      )}

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
          {reportCount != null && reportCount + (retestReportCount ?? 0) > 0 && (
            <span
              className="inline-flex items-center gap-1"
              title={
                retestReportCount && retestReportCount > 0
                  ? `${reportCount} numbered shard${reportCount === 1 ? '' : 's'}, ${retestReportCount} retest`
                  : undefined
              }
            >
              <CheckCircle className="h-3.5 w-3.5" />
              {reportCount}
              {retestReportCount != null && retestReportCount > 0 && <>+{retestReportCount}</>}{' '}
              {reportCount + (retestReportCount ?? 0) === 1 ? 'report' : 'reports'}
            </span>
          )}
          {progressStatus && <ProgressBadge status={progressStatus} createdAt={createdAt} />}
          {progressStatus === 'in_progress' && runStartedAt && (
            <ProgressDurations runStartedAt={runStartedAt} firstCheckoutAt={firstCheckoutAt} />
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
