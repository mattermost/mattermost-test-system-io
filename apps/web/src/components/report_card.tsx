import { memo, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { FlaskConical, ChevronRight, Clock, Timer } from 'lucide-react';
import type { ReportSummary } from '@/types';
import {
  StatusIcon,
  JobsStatusIcon,
  JobsProgress,
  TestStatsDisplay,
  GitMetadataMobile,
  GitMetadataDesktop,
  formatDateShort,
  formatDateFull,
  formatDuration,
  calculatePassRate,
  getPassRateColorClass,
} from './report_card_parts';

interface ReportCardProps {
  report: ReportSummary;
  rowNumber?: number;
  uploadTimeoutMs?: number;
  /** Current timestamp for timeout calculation - pass from parent to avoid re-renders */
  now?: number;
}

const DEFAULT_UPLOAD_TIMEOUT_MS = 60 * 60 * 1000; // 1 hour default

export const ReportCard = memo(function ReportCard({
  report,
  rowNumber,
  uploadTimeoutMs,
  now,
}: ReportCardProps) {
  // Memoize derived values
  const { frameworkDisplay, allJobsComplete, isTimedOut, passRate, passRateColorClass } =
    useMemo(() => {
      // Framework display name
      const framework =
        {
          playwright: 'Playwright',
          cypress: 'Cypress',
          detox: 'Detox',
        }[report.framework] || report.framework;

      // Jobs progress and upload status
      const complete = report.jobs_complete >= report.expected_jobs;

      // Check if report is timed out (not complete after timeout period)
      const timeoutMs = uploadTimeoutMs ?? DEFAULT_UPLOAD_TIMEOUT_MS;
      const createdAt = new Date(report.created_at).getTime();
      const currentTime = now ?? Date.now();
      const timedOut = !complete && currentTime - createdAt > timeoutMs;

      // Pass rate calculation
      const rate = report.test_stats ? calculatePassRate(report.test_stats) : null;
      const rateColorClass = getPassRateColorClass(rate);

      return {
        frameworkDisplay: framework,
        allJobsComplete: complete,
        isTimedOut: timedOut,
        passRate: rate,
        passRateColorClass: rateColorClass,
      };
    }, [report, uploadTimeoutMs, now]);

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
            <StatusIcon testStats={report.test_stats} />
          </div>
          <div className="min-w-0">
            <span className="text-sm font-medium text-gray-700 group-hover:text-blue-600 dark:text-gray-200 dark:group-hover:text-blue-400 truncate block">
              {frameworkDisplay}
            </span>
            <div className="text-xs text-gray-500 dark:text-gray-400">
              <JobsProgress
                complete={report.jobs_complete}
                expected={report.expected_jobs}
                allComplete={allJobsComplete}
                isTimedOut={isTimedOut}
              />
            </div>
          </div>
        </div>

        {/* Column 2: Repo + PR */}
        <div className="flex-1 min-w-0 text-center">
          {report.github_metadata && <GitMetadataMobile metadata={report.github_metadata} />}
        </div>

        {/* Column 3: Results + Pass Rate + Arrow */}
        <div className="flex-1 min-w-0 flex items-center justify-end gap-2">
          <div className="flex flex-col items-end gap-0.5">
            {report.test_stats && <TestStatsDisplay stats={report.test_stats} compact />}
            {passRate !== null && (
              <span
                className={`text-center rounded-md px-1.5 py-0.5 text-xs font-medium ${passRateColorClass}`}
              >
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
            <StatusIcon testStats={report.test_stats} />
          </div>

          {/* Framework + Jobs */}
          <div className="flex-shrink-0 w-28">
            <div className="flex items-center gap-1.5 text-sm font-medium text-gray-700 group-hover:text-blue-600 dark:text-gray-200 dark:group-hover:text-blue-400">
              <FlaskConical className="h-3.5 w-3.5 flex-shrink-0 text-gray-400 dark:text-gray-500" />
              <span className="truncate">{frameworkDisplay}</span>
            </div>
            <div className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
              <JobsStatusIcon allComplete={allJobsComplete} isTimedOut={isTimedOut} />
              <JobsProgress
                complete={report.jobs_complete}
                expected={report.expected_jobs}
                allComplete={allJobsComplete}
                isTimedOut={isTimedOut}
                showLabel
              />
            </div>
          </div>

          {/* Duration (sm to <lg) + Short Date */}
          <div className="flex-shrink-0 lg:hidden">
            {report.test_stats &&
            report.test_stats.duration_ms != null &&
            report.test_stats.duration_ms > 0 ? (
              <div className="flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400">
                <Timer className="h-3 w-3 text-gray-400 dark:text-gray-500" />
                {report.expected_jobs > 1 &&
                report.test_stats.wall_clock_ms &&
                report.test_stats.wall_clock_ms > 1000
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
            {report.test_stats &&
            report.test_stats.duration_ms != null &&
            report.test_stats.duration_ms > 0 ? (
              <div className="flex flex-col gap-0.5 text-xs text-gray-500 dark:text-gray-400">
                {report.expected_jobs > 1 &&
                  report.test_stats.wall_clock_ms &&
                  report.test_stats.wall_clock_ms > 1000 && (
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
          {report.github_metadata &&
            (report.github_metadata.repository || report.github_metadata.pr_number) && (
              <GitMetadataDesktop metadata={report.github_metadata} />
            )}
        </div>

        {/* Right section */}
        <div className="flex items-center gap-3 flex-shrink-0 flex-nowrap">
          <div className="flex flex-col items-end gap-0.5 md:flex-row md:items-center md:gap-2 flex-nowrap">
            {report.test_stats && <TestStatsDisplay stats={report.test_stats} />}
            {passRate !== null && (
              <span
                className={`w-12 text-center rounded-md px-2 py-0.5 text-xs font-medium ${passRateColorClass}`}
              >
                {passRate}%
              </span>
            )}
          </div>
          <ChevronRight className="h-4 w-4 flex-shrink-0 text-gray-400 transition-transform group-hover:translate-x-0.5 group-hover:text-blue-500 dark:text-gray-500 dark:group-hover:text-blue-400" />
        </div>
      </div>
    </Link>
  );
});
