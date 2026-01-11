import { Link } from "react-router-dom";
import {
  GitBranch,
  GitCommit,
  GitPullRequest,
  Clock,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  AlertCircle,
  MinusCircle,
  ChevronRight,
  Folder,
  FlaskConical,
  FileCheck,
  Loader2,
} from "lucide-react";
import type { ReportSummary } from "../types";

interface ReportCardProps {
  report: ReportSummary;
  rowNumber?: number;
}

export function ReportCard({ report, rowNumber }: ReportCardProps) {
  const formatDate = (dateString: string) => {
    const date = new Date(dateString);
    return date.toLocaleDateString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  };

  const formatDuration = (ms: number) => {
    const seconds = Math.floor(ms / 1000);
    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = seconds % 60;
    if (minutes > 0) {
      return `${minutes}m ${remainingSeconds}s`;
    }
    return `${remainingSeconds}s`;
  };

  const stats = report.stats;
  // Pass rate excludes skipped tests: (passed + flaky) / (passed + flaky + failed)
  const countedTotal = stats
    ? stats.expected + stats.flaky + stats.unexpected
    : 0;
  const passRate =
    stats && countedTotal > 0
      ? Math.round(((stats.expected + stats.flaky) / countedTotal) * 100)
      : null;

  // Extraction status icon (different from pass/fail icons)
  const statusIcon = (() => {
    switch (report.extraction_status) {
      case "completed":
        return <FileCheck className="h-4 w-4 text-blue-500" />;
      case "failed":
        return <AlertCircle className="h-4 w-4 text-orange-500" />;
      case "pending":
        return <Loader2 className="h-4 w-4 text-gray-400 animate-spin" />;
      default:
        return <MinusCircle className="h-4 w-4 text-gray-400" />;
    }
  })();

  return (
    <Link
      to={`/reports/${report.id}`}
      className="group flex items-center gap-4 rounded-lg border border-gray-200 bg-white px-4 py-3 transition-colors hover:bg-gray-50 dark:border-gray-700 dark:bg-gray-800 dark:hover:bg-gray-700/50"
    >
      {/* Row Number */}
      {rowNumber !== undefined && (
        <span className="w-8 text-right text-xs font-medium text-gray-400 dark:text-gray-500">
          {rowNumber}
        </span>
      )}

      {/* Status Icon */}
      {statusIcon}

      {/* Repo + Framework */}
      <div className="min-w-0 flex-shrink-0 w-36">
        {report.github_context?.repository ? (
          <div className="flex items-center gap-1.5 text-sm font-medium text-gray-700 group-hover:text-blue-600 truncate dark:text-gray-200 dark:group-hover:text-blue-400">
            <Folder className="h-3.5 w-3.5 flex-shrink-0 text-gray-400 dark:text-gray-500" />
            <span className="truncate">
              {report.github_context.repository.split("/").pop()}
            </span>
          </div>
        ) : (
          <div className="text-sm text-gray-400 dark:text-gray-500">—</div>
        )}
        {report.framework && (
          <div className="flex items-center gap-1.5 text-xs text-gray-500 truncate dark:text-gray-400">
            <FlaskConical className="h-3 w-3 flex-shrink-0" />
            <span className="truncate">
              {report.framework}
              {report.framework_version && ` v${report.framework_version}`}
            </span>
          </div>
        )}
      </div>

      {/* ID + Date */}
      <div className="min-w-0 flex-shrink-0">
        <span className="font-mono text-xs text-gray-500 group-hover:text-blue-600 dark:text-gray-400 dark:group-hover:text-blue-400">
          {report.id.slice(0, 8)}
        </span>
        <div className="text-xs text-gray-400 dark:text-gray-500">
          {formatDate(report.created_at)}
        </div>
      </div>

      {/* Git context */}
      {report.github_context && (
        <div className="flex items-center gap-1.5 text-xs">
          {report.github_context.branch && (
            <span className="inline-flex items-center gap-1 rounded-md bg-blue-50 px-1.5 py-0.5 text-blue-700 dark:bg-blue-900/50 dark:text-blue-300">
              <GitBranch className="h-3 w-3" />
              <span className="max-w-16 truncate">
                {report.github_context.branch}
              </span>
            </span>
          )}
          {report.github_context.pr_number && (
            <span className="inline-flex items-center gap-1 rounded-md bg-purple-50 px-1.5 py-0.5 text-purple-700 dark:bg-purple-900/50 dark:text-purple-300">
              <GitPullRequest className="h-3 w-3" />
              {report.github_context.pr_number}
            </span>
          )}
          {report.github_context.commit_sha && (
            <span className="inline-flex items-center gap-1 rounded-md bg-gray-100 px-1.5 py-0.5 font-mono text-gray-600 dark:bg-gray-700 dark:text-gray-300">
              <GitCommit className="h-3 w-3" />
              {report.github_context.commit_sha.slice(0, 7)}
            </span>
          )}
        </div>
      )}

      {/* Spacer */}
      <div className="flex-1" />

      {/* Test stats */}
      {stats && (
        <div className="flex items-center gap-3 text-xs">
          <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
            <CheckCircle2 className="h-3.5 w-3.5" />
            {stats.expected}
          </span>
          {stats.unexpected > 0 && (
            <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
              <XCircle className="h-3.5 w-3.5" />
              {stats.unexpected}
            </span>
          )}
          {stats.flaky > 0 && (
            <span className="inline-flex items-center gap-1 text-yellow-600 dark:text-yellow-400">
              <AlertTriangle className="h-3.5 w-3.5" />
              {stats.flaky}
            </span>
          )}
          {stats.skipped > 0 && (
            <span className="inline-flex items-center gap-1 text-gray-400 dark:text-gray-500">
              <MinusCircle className="h-3.5 w-3.5" />
              {stats.skipped}
            </span>
          )}
        </div>
      )}

      {/* Pass rate badge */}
      {passRate !== null && (
        <span
          className={`inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-semibold ${
            passRate === 100
              ? "bg-green-100 text-green-700 dark:bg-green-900/50 dark:text-green-300"
              : "bg-red-100 text-red-700 dark:bg-red-900/50 dark:text-red-300"
          }`}
        >
          {passRate === 100 ? (
            <CheckCircle2 className="h-3 w-3" />
          ) : (
            <XCircle className="h-3 w-3" />
          )}
          {passRate}%
        </span>
      )}

      {/* Duration */}
      {stats && (
        <span className="inline-flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400">
          <Clock className="h-3.5 w-3.5" />
          {formatDuration(stats.duration_ms)}
        </span>
      )}

      {/* Arrow */}
      <ChevronRight className="h-4 w-4 text-gray-400 transition-transform group-hover:translate-x-0.5 group-hover:text-blue-500 dark:text-gray-500 dark:group-hover:text-blue-400" />
    </Link>
  );
}
