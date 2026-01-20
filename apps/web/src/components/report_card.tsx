import { Link } from 'react-router-dom';
import {
  GitBranch,
  GitCommit,
  GitPullRequest,
  CheckCircle2,
  XCircle,
  ChevronRight,
  FlaskConical,
  Loader2,
  Clock,
  SkipForward,
  CircleDot,
  Timer,
  AlertCircle,
  FolderGit2,
} from 'lucide-react';
import type { ReportSummary } from '../types';

interface ReportCardProps {
  report: ReportSummary;
  rowNumber?: number;
  uploadTimeoutMs?: number;
}

const DEFAULT_UPLOAD_TIMEOUT_MS = 60 * 60 * 1000; // 1 hour default

export function ReportCard({ report, rowNumber, uploadTimeoutMs }: ReportCardProps) {
  const formatDateShort = (dateString: string) => {
    const date = new Date(dateString);
    return date.toLocaleDateString(undefined, {
      month: 'short',
      day: 'numeric',
    });
  };

  const formatDateFull = (dateString: string) => {
    const date = new Date(dateString);
    return date.toLocaleDateString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  };

  const formatDuration = (ms: number) => {
    const seconds = Math.floor(ms / 1000);
    const minutes = Math.floor(seconds / 60);
    const hours = Math.floor(minutes / 60);

    if (hours > 0) {
      const remainingMinutes = minutes % 60;
      return remainingMinutes > 0 ? `${hours}h ${remainingMinutes}m` : `${hours}h`;
    }
    if (minutes > 0) {
      const remainingSeconds = seconds % 60;
      return remainingSeconds > 0 ? `${minutes}m ${remainingSeconds}s` : `${minutes}m`;
    }
    return `${seconds}s`;
  };

  // Status icon based on test results (pass/fail)
  const statusIcon = (() => {
    if (!report.test_stats) {
      return <Clock className="h-4 w-4 text-gray-400" />;
    }
    // No failures = green, any failures = red
    return report.test_stats.failed === 0
      ? <CheckCircle2 className="h-4 w-4 text-green-500" />
      : <XCircle className="h-4 w-4 text-red-500" />;
  })();

  // Framework display name
  const frameworkDisplay = {
    playwright: 'Playwright',
    cypress: 'Cypress',
    detox: 'Detox',
  }[report.framework] || report.framework;

  // Jobs progress and upload status
  const jobsProgress = `${report.jobs_complete}/${report.expected_jobs}`;
  const allJobsComplete = report.jobs_complete >= report.expected_jobs;

  // Check if report is timed out (not complete after timeout period)
  const timeoutMs = uploadTimeoutMs ?? DEFAULT_UPLOAD_TIMEOUT_MS;
  const createdAt = new Date(report.created_at).getTime();
  const isTimedOut = !allJobsComplete && (Date.now() - createdAt > timeoutMs);

  // Jobs status icon
  const jobsStatusIcon = (() => {
    if (allJobsComplete) {
      return <Loader2 className="h-3 w-3 flex-shrink-0 text-green-500" />;
    }
    if (isTimedOut) {
      return <AlertCircle className="h-3 w-3 flex-shrink-0 text-red-500" />;
    }
    return <Loader2 className="h-3 w-3 flex-shrink-0 text-blue-500 animate-spin" />;
  })();

  // Pass rate calculation
  const passRate = (() => {
    if (!report.test_stats) return null;
    const passed = report.test_stats.passed + report.test_stats.flaky;
    const failed = report.test_stats.failed;
    const total = passed + failed;
    if (total === 0) return null;
    return Math.round((passed * 100) / total);
  })();

  const passRateColorClass = passRate === 100
    ? 'bg-green-100 text-green-700 dark:bg-green-900/50 dark:text-green-300'
    : 'bg-red-100 text-red-700 dark:bg-red-900/50 dark:text-red-300';

  return (
    <Link
      to={`/reports/${report.id}`}
      className="group block rounded-lg border border-gray-200 bg-white px-3 sm:px-4 py-3 transition-colors hover:bg-gray-50 dark:border-gray-700 dark:bg-gray-800 dark:hover:bg-gray-700/50"
    >
      {/* Mobile layout (<sm): 3 equal columns */}
      <div className="flex sm:hidden items-center">
        {/* Column 1: Status + Framework + Jobs */}
        <div className="flex-1 min-w-0 flex items-center gap-2">
          <div className="flex-shrink-0 self-center">
            {statusIcon}
          </div>
          <div className="min-w-0">
            <span className="text-sm font-medium text-gray-700 group-hover:text-blue-600 dark:text-gray-200 dark:group-hover:text-blue-400 truncate block">
              {frameworkDisplay}
            </span>
            <div className="text-xs text-gray-500 dark:text-gray-400">
              <span className={allJobsComplete ? 'text-green-600 dark:text-green-400' : isTimedOut ? 'text-red-600 dark:text-red-400' : ''}>
                {jobsProgress}
              </span>
            </div>
          </div>
        </div>

        {/* Column 2: Repo + PR */}
        <div className="flex-1 min-w-0 text-center">
          {report.github_metadata && (
            <div className="flex flex-col items-center gap-0.5 text-xs">
              {report.github_metadata.repo && (
                <span className="inline-flex items-center gap-1 font-medium text-gray-700 dark:text-gray-300 min-w-0">
                  <FolderGit2 className="hidden min-[480px]:inline h-3 w-3 flex-shrink-0 text-gray-400 dark:text-gray-500" />
                  <span className="truncate max-w-[80px] min-[480px]:max-w-[100px]">
                    {report.github_metadata.repo.split('/').pop()}
                  </span>
                </span>
              )}
              {report.github_metadata.pr_number && (
                <span className="inline-flex items-center gap-1 rounded-md bg-purple-50 px-1.5 py-0.5 text-purple-700 dark:bg-purple-900/50 dark:text-purple-300">
                  <GitPullRequest className="hidden min-[480px]:inline h-3 w-3" />
                  #{report.github_metadata.pr_number}
                </span>
              )}
            </div>
          )}
        </div>

        {/* Column 3: Results + Pass Rate + Arrow */}
        <div className="flex-1 min-w-0 flex items-center justify-end gap-2">
          <div className="flex flex-col items-end gap-0.5">
            {report.test_stats && (
              <div className="flex items-center gap-1.5 text-xs">
                <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
                  <CheckCircle2 className="hidden min-[480px]:inline h-3 w-3" />
                  {report.test_stats.passed}
                </span>
                {report.test_stats.failed > 0 && (
                  <>
                    <span className="min-[480px]:hidden text-gray-300 dark:text-gray-600">|</span>
                    <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
                      <XCircle className="hidden min-[480px]:inline h-3 w-3" />
                      {report.test_stats.failed}
                    </span>
                  </>
                )}
              </div>
            )}
            {passRate !== null && (
              <span className={`text-center rounded-md px-1.5 py-0.5 text-xs font-medium ${passRateColorClass}`}>
                {passRate}%
              </span>
            )}
          </div>
          <ChevronRight className="h-4 w-4 flex-shrink-0 text-gray-400 transition-transform group-hover:translate-x-0.5 group-hover:text-blue-500 dark:text-gray-500 dark:group-hover:text-blue-400" />
        </div>
      </div>

      {/* Desktop layout (sm+) */}
      <div className="hidden sm:flex items-center gap-4">
        {/* Left section */}
        <div className="flex items-center gap-4 min-w-0 flex-1 overflow-hidden">
          {/* Row Number */}
          {rowNumber !== undefined && (
            <span className="w-8 flex-shrink-0 text-right text-xs font-medium text-gray-400 dark:text-gray-500">
              {rowNumber}
            </span>
          )}

          {/* Status Icon */}
          <div className="w-4 flex-shrink-0">
            {statusIcon}
          </div>

          {/* Framework + Jobs */}
          <div className="flex-shrink-0 w-28">
            <div className="flex items-center gap-1.5 text-sm font-medium text-gray-700 group-hover:text-blue-600 dark:text-gray-200 dark:group-hover:text-blue-400">
              <FlaskConical className="h-3.5 w-3.5 flex-shrink-0 text-gray-400 dark:text-gray-500" />
              <span className="truncate">{frameworkDisplay}</span>
            </div>
            <div className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
              {jobsStatusIcon}
              <span className={allJobsComplete ? 'text-green-600 dark:text-green-400' : isTimedOut ? 'text-red-600 dark:text-red-400' : ''}>
                {jobsProgress}<span className="hidden lg:inline"> {report.expected_jobs === 1 ? 'job' : 'jobs'}</span>
              </span>
            </div>
          </div>

          {/* Duration (sm to <lg) + Short Date */}
          <div className="flex-shrink-0 lg:hidden">
            {report.test_stats && report.test_stats.duration_ms != null && report.test_stats.duration_ms > 0 ? (
              <div className="flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400">
                <Timer className="h-3 w-3 text-gray-400 dark:text-gray-500" />
                {report.expected_jobs > 1 && report.test_stats.wall_clock_ms && report.test_stats.wall_clock_ms > 1000
                  ? formatDuration(report.test_stats.wall_clock_ms)
                  : formatDuration(report.test_stats.duration_ms)}
              </div>
            ) : (
              <div className="text-xs text-gray-400 dark:text-gray-500">--</div>
            )}
            <div className="text-xs text-gray-400 dark:text-gray-500">
              {formatDateShort(report.created_at)}
            </div>
          </div>

          {/* Wall Clock + Duration (lg+) + Full Date */}
          <div className="hidden lg:block flex-shrink-0">
            {report.test_stats && report.test_stats.duration_ms != null && report.test_stats.duration_ms > 0 ? (
              <div className="flex flex-col gap-0.5 text-xs text-gray-500 dark:text-gray-400">
                {report.expected_jobs > 1 && report.test_stats.wall_clock_ms && report.test_stats.wall_clock_ms > 1000 && (
                  <div className="flex items-center gap-1">
                    <Clock className="h-3 w-3 text-gray-400 dark:text-gray-500" />
                    {formatDuration(report.test_stats.wall_clock_ms)}
                  </div>
                )}
                <div className="flex items-center gap-1">
                  <Timer className="h-3 w-3 text-gray-400 dark:text-gray-500" />
                  {formatDuration(report.test_stats.duration_ms)}
                </div>
              </div>
            ) : (
              <div className="text-xs text-gray-400 dark:text-gray-500">--</div>
            )}
            <div className="text-xs text-gray-400 dark:text-gray-500">
              {formatDateFull(report.created_at)}
            </div>
          </div>

          {/* Git context */}
          {report.github_metadata && (report.github_metadata.repo || report.github_metadata.pr_number) && (
            <>
              {/* sm to md: inline layout */}
              <div className="flex md:hidden flex-wrap items-center gap-x-2 gap-y-0.5 text-xs min-w-0">
                {report.github_metadata.repo && (
                  <span className="inline-flex items-center gap-1 font-medium text-gray-700 dark:text-gray-300 min-w-0">
                    <FolderGit2 className="h-3 w-3 flex-shrink-0 text-gray-400 dark:text-gray-500" />
                    <span className="truncate">{report.github_metadata.repo.split('/').pop()}</span>
                  </span>
                )}
                {report.github_metadata.pr_number && (
                  <span className="inline-flex items-center gap-1 rounded-md bg-purple-50 px-1.5 py-0.5 text-purple-700 dark:bg-purple-900/50 dark:text-purple-300">
                    <GitPullRequest className="h-3 w-3" />
                    #{report.github_metadata.pr_number}
                  </span>
                )}
              </div>
              {/* md+: two stacks side by side (Repo+PR | Branch+Commit) */}
              <div className="hidden md:flex items-start gap-3 text-xs min-w-0 overflow-hidden">
                {/* Repo + PR stack */}
                {(report.github_metadata.repo || report.github_metadata.pr_number) && (
                  <div className="flex flex-col gap-0.5 min-w-0 overflow-hidden lg:w-48">
                    {report.github_metadata.repo && (
                      <span className="inline-flex items-center gap-1 font-medium text-gray-700 dark:text-gray-300 min-w-0">
                        <FolderGit2 className="h-3 w-3 flex-shrink-0 text-gray-400 dark:text-gray-500" />
                        <span className="truncate">{report.github_metadata.repo}</span>
                      </span>
                    )}
                    {report.github_metadata.pr_number && (
                      <span className="inline-flex items-center gap-1 rounded-md bg-purple-50 px-1.5 py-0.5 text-purple-700 dark:bg-purple-900/50 dark:text-purple-300 w-fit">
                        <GitPullRequest className="h-3 w-3" />
                        #{report.github_metadata.pr_number}
                      </span>
                    )}
                  </div>
                )}
                {/* Branch + Commit stack */}
                {(report.github_metadata.branch || report.github_metadata.commit) && (
                  <div className="flex flex-col gap-0.5 flex-shrink-0">
                    {report.github_metadata.branch && (
                      <span className="inline-flex items-center gap-1 rounded-md bg-blue-50 px-1.5 py-0.5 text-blue-700 dark:bg-blue-900/50 dark:text-blue-300 min-w-0">
                        <GitBranch className="h-3 w-3 flex-shrink-0" />
                        <span className="truncate max-w-24">{report.github_metadata.branch}</span>
                      </span>
                    )}
                    {report.github_metadata.commit && (
                      <span className="hidden lg:inline-flex items-center gap-1 rounded-md bg-gray-100 px-1.5 py-0.5 font-mono text-gray-600 dark:bg-gray-700 dark:text-gray-300">
                        <GitCommit className="h-3 w-3" />
                        {report.github_metadata.commit.slice(0, 7)}
                      </span>
                    )}
                  </div>
                )}
              </div>
            </>
          )}

        </div>

        {/* Right section */}
        <div className="flex items-center gap-3 flex-shrink-0 flex-nowrap">
          <div className="flex flex-col items-end gap-0.5 md:flex-row md:items-center md:gap-2 flex-nowrap">
            {report.test_stats && (
              <div className="flex items-center gap-2 text-xs">
                <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
                  <CheckCircle2 className="h-3 w-3" />
                  {report.test_stats.passed}
                </span>
                {report.test_stats.failed > 0 && (
                  <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
                    <XCircle className="h-3 w-3" />
                    {report.test_stats.failed}
                  </span>
                )}
                {report.test_stats.skipped > 0 && (
                  <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-400">
                    <SkipForward className="h-3 w-3" />
                    {report.test_stats.skipped}
                  </span>
                )}
                {report.test_stats.flaky > 0 && (
                  <span className="inline-flex items-center gap-1 text-yellow-600 dark:text-yellow-400">
                    <CircleDot className="h-3 w-3" />
                    {report.test_stats.flaky}
                  </span>
                )}
              </div>
            )}

            {passRate !== null && (
              <span className={`w-12 text-center rounded-md px-2 py-0.5 text-xs font-medium ${passRateColorClass}`}>
                {passRate}%
              </span>
            )}
          </div>

          <ChevronRight className="h-4 w-4 flex-shrink-0 text-gray-400 transition-transform group-hover:translate-x-0.5 group-hover:text-blue-500 dark:text-gray-500 dark:group-hover:text-blue-400" />
        </div>
      </div>
    </Link>
  );
}
