import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ChevronRight,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  MinusCircle,
  Clock,
  FileCode,
  Loader2,
  RotateCcw,
} from "lucide-react";
import type {
  TestSuite,
  ReportStats,
  TestSpec,
  TestSpecListResponse,
} from "../types";

const API_BASE = "/api/v1";

type StatusFilter = "all" | "passed" | "failed" | "flaky" | "skipped";

interface TestSuitesViewProps {
  reportId: string;
  suites: TestSuite[];
  stats?: ReportStats;
  title?: string;
}

export function TestSuitesView({
  reportId,
  suites,
  stats,
  title,
}: TestSuitesViewProps) {
  const [expandedSuiteId, setExpandedSuiteId] = useState<number | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");

  const handleSuiteClick = (suiteId: number) => {
    setExpandedSuiteId(expandedSuiteId === suiteId ? null : suiteId);
  };

  // Filter suites based on status filter
  const filteredSuites = suites.filter((suite) => {
    if (statusFilter === "all") return true;
    switch (statusFilter) {
      case "passed":
        return suite.passed_count > 0;
      case "failed":
        return suite.failed_count > 0;
      case "flaky":
        return (suite.flaky_count ?? 0) > 0;
      case "skipped":
        return (suite.skipped_count ?? 0) > 0;
      default:
        return true;
    }
  });

  return (
    <div className="space-y-3">
      {/* Stats Header */}
      {stats && (
        <div className="rounded-lg border border-gray-200 bg-white px-4 py-2.5 dark:border-gray-700 dark:bg-gray-800">
          {/* Row 1: Title, stats */}
          <div className="flex items-center gap-4">
            {/* Left: Title + Pass rate + Duration */}
            <div className="flex items-center gap-2 min-w-0">
              <h2 className="text-sm font-medium text-gray-900 dark:text-white truncate">
                {title || "Test Report"}
              </h2>
              <span className="text-xs text-gray-400 dark:text-gray-500">
                •
              </span>
              <span
                className={`inline-flex items-center gap-1 text-xs font-semibold whitespace-nowrap ${
                  calcPassRate(stats) === "100.0"
                    ? "text-green-600 dark:text-green-400"
                    : "text-red-600 dark:text-red-400"
                }`}
              >
                {calcPassRate(stats) === "100.0" ? (
                  <CheckCircle2 className="h-3 w-3" />
                ) : (
                  <XCircle className="h-3 w-3" />
                )}
                {calcPassRate(stats)}%
              </span>
              <span className="text-xs text-gray-400 dark:text-gray-500">
                •
              </span>
              <span className="text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap">
                {formatDuration(stats.duration_ms)}
              </span>
            </div>

            <div className="flex-1" />

            {/* Right: Stat pills */}
            <div className="flex items-center gap-1">
              <StatPill
                label="Total"
                value={
                  stats.expected +
                  stats.unexpected +
                  stats.flaky +
                  stats.skipped
                }
                variant="default"
                isActive={statusFilter === "all"}
                onClick={() => setStatusFilter("all")}
              />
              <StatPill
                label="Passed"
                value={stats.expected}
                variant="success"
                isActive={statusFilter === "passed"}
                onClick={() => setStatusFilter("passed")}
              />
              {stats.unexpected > 0 && (
                <StatPill
                  label="Failed"
                  value={stats.unexpected}
                  variant="error"
                  isActive={statusFilter === "failed"}
                  onClick={() => setStatusFilter("failed")}
                />
              )}
              {stats.flaky > 0 && (
                <StatPill
                  label="Flaky"
                  value={stats.flaky}
                  variant="warning"
                  isActive={statusFilter === "flaky"}
                  onClick={() => setStatusFilter("flaky")}
                />
              )}
              {stats.skipped > 0 && (
                <StatPill
                  label="Skipped"
                  value={stats.skipped}
                  variant="muted"
                  isActive={statusFilter === "skipped"}
                  onClick={() => setStatusFilter("skipped")}
                />
              )}
            </div>
          </div>

          {/* Row 2: Full-width progress bar */}
          <div className="mt-2">
            <ProgressBar stats={stats} />
          </div>
        </div>
      )}

      {/* Suites Summary */}
      <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
        <h3 className="mb-4 text-sm font-medium text-gray-900 dark:text-white">
          Test Suites ({filteredSuites.length}
          {statusFilter !== "all" ? ` of ${suites.length}` : ""})
        </h3>

        {filteredSuites.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400">
            {statusFilter === "all"
              ? "No test suites found"
              : `No suites with ${statusFilter} tests`}
          </p>
        ) : (
          <div className="divide-y divide-gray-100 dark:divide-gray-700">
            {filteredSuites.map((suite, index) => (
              <SuiteRow
                key={suite.id}
                suite={suite}
                reportId={reportId}
                isExpanded={expandedSuiteId === suite.id}
                onToggle={() => handleSuiteClick(suite.id)}
                statusFilter={statusFilter}
                rowNumber={index + 1}
              />
            ))}
          </div>
        )}

        {/* Totals - use stats for consistency with header */}
        {suites.length > 0 && stats && (
          <div className="mt-4 flex items-center justify-between border-t border-gray-200 pt-4 text-xs dark:border-gray-700">
            <span className="font-medium text-gray-900 dark:text-white">
              Total
            </span>
            <div className="flex items-center gap-3">
              <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-400">
                <Clock className="h-3 w-3" />
                {formatDuration(stats.duration_ms)}
              </span>
              <span className="text-gray-600 dark:text-gray-300">
                {stats.expected +
                  stats.unexpected +
                  stats.flaky +
                  stats.skipped}{" "}
                specs
              </span>
              <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
                <CheckCircle2 className="h-3 w-3" />
                {stats.expected}
              </span>
              {stats.flaky > 0 && (
                <span className="inline-flex items-center gap-1 text-yellow-600 dark:text-yellow-400">
                  <AlertTriangle className="h-3 w-3" />
                  {stats.flaky}
                </span>
              )}
              {stats.unexpected > 0 && (
                <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
                  <XCircle className="h-3 w-3" />
                  {stats.unexpected}
                </span>
              )}
              {stats.skipped > 0 && (
                <span className="inline-flex items-center gap-1 text-gray-400 dark:text-gray-500">
                  <MinusCircle className="h-3 w-3" />
                  {stats.skipped}
                </span>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

interface SuiteRowProps {
  suite: TestSuite;
  reportId: string;
  isExpanded: boolean;
  onToggle: () => void;
  statusFilter: StatusFilter;
  rowNumber: number;
}

function SuiteRow({
  suite,
  reportId,
  isExpanded,
  onToggle,
  statusFilter,
  rowNumber,
}: SuiteRowProps) {
  const hasFlaky = (suite.flaky_count ?? 0) > 0;
  const hasFailed = suite.failed_count > 0;

  // Fetch specs when expanded
  const { data: specsData, isLoading } = useQuery<TestSpecListResponse>({
    queryKey: ["suite-specs", reportId, suite.id],
    queryFn: async () => {
      const res = await fetch(
        `${API_BASE}/reports/${reportId}/suites/${suite.id}/specs`,
      );
      if (!res.ok) throw new Error("Failed to fetch specs");
      return res.json();
    },
    enabled: isExpanded,
    staleTime: 60000,
  });

  // Filter specs based on status filter
  const filteredSpecs =
    specsData?.specs?.filter((spec) => {
      if (statusFilter === "all") return true;
      if (spec.results.length === 0) return false;

      // Check for flaky: passed eventually but had at least one failure
      const hasFailure = spec.results.some((r) => r.status === "failed");
      const hasPassed = spec.results.some((r) => r.status === "passed");
      const isFlaky = spec.ok && hasFailure && hasPassed;

      // Get the final result (highest retry number)
      const finalResult = spec.results.reduce((latest, r) =>
        r.retry > (latest?.retry ?? -1) ? r : latest,
      );

      switch (statusFilter) {
        case "passed":
          // All specs that ultimately passed (including flaky)
          return spec.ok;
        case "failed":
          return !spec.ok && finalResult?.status !== "skipped";
        case "flaky":
          return isFlaky;
        case "skipped":
          return finalResult?.status === "skipped";
        default:
          return true;
      }
    }) || [];

  // Status icon based on suite state
  const StatusIcon = hasFailed
    ? XCircle
    : hasFlaky
      ? AlertTriangle
      : CheckCircle2;
  const statusIconColor = hasFailed
    ? "text-red-500"
    : hasFlaky
      ? "text-yellow-500"
      : "text-green-500";

  return (
    <div
      className={`-mx-2 px-2 rounded-lg transition-colors ${
        isExpanded ? "bg-blue-50 dark:bg-blue-900/20" : ""
      }`}
    >
      <button
        type="button"
        onClick={onToggle}
        className={`w-full cursor-pointer py-2.5 text-left transition-colors ${
          isExpanded
            ? "hover:bg-blue-100/50 dark:hover:bg-blue-900/30"
            : "hover:bg-gray-50 dark:hover:bg-gray-700/50"
        }`}
      >
        <div className="flex items-center justify-between">
          <div className="flex min-w-0 items-center gap-2">
            <span className="w-6 text-xs text-gray-400 dark:text-gray-500 text-right flex-shrink-0">
              {rowNumber}
            </span>
            <ChevronRight
              className={`h-4 w-4 flex-shrink-0 text-gray-400 transition-transform dark:text-gray-500 ${
                isExpanded ? "rotate-90" : ""
              }`}
            />
            <StatusIcon
              className={`h-4 w-4 flex-shrink-0 ${statusIconColor}`}
            />
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-gray-900 dark:text-white flex items-center gap-1.5">
                <FileCode className="h-3.5 w-3.5 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                {suite.file_path}
              </p>
              {suite.title !== suite.file_path && (
                <p className="truncate text-xs text-gray-500 dark:text-gray-400">
                  {suite.title}
                </p>
              )}
            </div>
          </div>
          <div className="flex flex-shrink-0 items-center gap-3 text-xs">
            <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-400">
              <Clock className="h-3 w-3" />
              {formatDuration(suite.duration_ms || 0)}
            </span>
            <span className="text-gray-600 dark:text-gray-300">
              {suite.specs_count} specs
            </span>
            <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
              <CheckCircle2 className="h-3 w-3" />
              {suite.passed_count}
            </span>
            {hasFlaky && (
              <span className="inline-flex items-center gap-1 text-yellow-600 dark:text-yellow-400">
                <AlertTriangle className="h-3 w-3" />
                {suite.flaky_count}
              </span>
            )}
            {hasFailed && (
              <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
                <XCircle className="h-3 w-3" />
                {suite.failed_count}
              </span>
            )}
          </div>
        </div>
      </button>

      {/* Expanded specs list */}
      {isExpanded && (
        <div className="mb-3 ml-6 border-l-2 border-gray-200 pl-4 dark:border-gray-600">
          {isLoading ? (
            <div className="flex items-center gap-2 py-2 text-sm text-gray-500 dark:text-gray-400">
              <Loader2 className="h-4 w-4 animate-spin" />
              Loading specs...
            </div>
          ) : filteredSpecs.length > 0 ? (
            <div className="space-y-2 py-2">
              {filteredSpecs.map((spec, specIndex) => (
                <SpecRow
                  key={spec.id}
                  spec={spec}
                  reportId={reportId}
                  rowLabel={`${rowNumber}.${specIndex + 1}`}
                />
              ))}
            </div>
          ) : (
            <p className="py-2 text-sm text-gray-500 dark:text-gray-400">
              {statusFilter === "all"
                ? "No specs found"
                : `No ${statusFilter} specs`}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

interface SpecRowProps {
  spec: TestSpec;
  reportId: string;
  rowLabel: string;
}

function SpecRow({ spec, reportId, rowLabel }: SpecRowProps) {
  const latestResult = spec.results[spec.results.length - 1];

  // Determine status icon based on actual status
  const isSkipped = latestResult?.status === "skipped";
  const isFlaky = spec.ok && spec.results.some((r) => r.status === "failed");

  let StatusIcon = CheckCircle2;
  let statusColor = "text-green-500";

  if (isSkipped) {
    StatusIcon = MinusCircle;
    statusColor = "text-gray-400";
  } else if (!spec.ok) {
    StatusIcon = XCircle;
    statusColor = "text-red-500";
  } else if (isFlaky) {
    StatusIcon = AlertTriangle;
    statusColor = "text-yellow-500";
  }

  return (
    <div className="text-sm">
      <div className="flex items-center gap-2 py-1">
        <span className="w-10 text-xs font-medium text-gray-400 dark:text-gray-500 flex-shrink-0 text-right">
          {rowLabel}
        </span>
        <StatusIcon className={`h-3.5 w-3.5 flex-shrink-0 ${statusColor}`} />
        <span className="flex-1 truncate text-gray-900 dark:text-gray-100">
          {spec.title}
        </span>
        {latestResult && (
          <>
            <span className="text-xs text-gray-600 dark:text-gray-400">
              {latestResult.project_name}
            </span>
            <span className="inline-flex items-center gap-1 text-xs text-gray-500 dark:text-gray-500">
              <Clock className="h-3 w-3" />
              {formatDuration(latestResult.duration_ms)}
            </span>
            {latestResult.retry > 0 && (
              <span className="inline-flex items-center gap-1 text-xs text-orange-600 dark:text-orange-400">
                <RotateCcw className="h-3 w-3" />#{latestResult.retry}
              </span>
            )}
          </>
        )}
      </div>
      {spec.results.some((r) => r.errors_json) && (
        <div className="ml-5 mt-1 space-y-1">
          {spec.results
            .filter((r) => r.errors_json)
            .map((r, idx) => (
              <ErrorDisplay
                key={idx}
                errorsJson={r.errors_json!}
                attempt={r.retry + 1}
                totalAttempts={spec.results.length}
              />
            ))}
        </div>
      )}
      {spec.screenshots && spec.screenshots.length > 0 && (
        <div className="ml-5 mt-2 space-y-2">
          <p className="text-xs text-gray-500 dark:text-gray-400">
            Screenshots ({spec.screenshots.length})
          </p>
          <div className="flex flex-wrap gap-2">
            {spec.screenshots.map((screenshot, idx) => (
              <a
                key={idx}
                href={`${API_BASE}/reports/${reportId}/data/${screenshot.file_path}`}
                target="_blank"
                rel="noopener noreferrer"
                className="group relative"
              >
                <img
                  src={`${API_BASE}/reports/${reportId}/data/${screenshot.file_path}`}
                  alt={`${screenshot.screenshot_type} screenshot`}
                  className="h-80 w-auto rounded border border-gray-200 object-cover hover:border-blue-500 dark:border-gray-700"
                  loading="lazy"
                />
                <span className="absolute bottom-0 left-0 right-0 rounded-b bg-black/60 px-1 py-0.5 text-center text-xs text-white">
                  {screenshot.screenshot_type}
                </span>
              </a>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

interface ErrorInfo {
  message?: string;
  estack?: string;
  diff?: string | null;
}

interface ErrorDisplayProps {
  errorsJson: string;
  attempt: number;
  totalAttempts: number;
}

function ErrorDisplay({
  errorsJson,
  attempt,
  totalAttempts,
}: ErrorDisplayProps) {
  const [isExpanded, setIsExpanded] = useState(false);
  let errors: ErrorInfo[] = [];

  try {
    const parsed = JSON.parse(errorsJson);
    if (Array.isArray(parsed) && parsed.length > 0) {
      // Check if it's an array of strings (jest-stare/Detox format)
      if (typeof parsed[0] === "string") {
        // Jest-stare format: array of full error strings
        // Parse each string to extract message and stack
        errors = parsed.map((errorStr: string) => {
          // Split on first "at " to separate message from stack
          const atIndex = errorStr.indexOf("\n    at ");
          if (atIndex > 0) {
            return {
              message: errorStr.substring(0, atIndex).trim(),
              estack: errorStr.substring(atIndex + 1).trim(),
            };
          }
          // No stack trace found, use entire string as message
          return { message: errorStr.trim() };
        });
      } else {
        // Playwright format: array of error objects
        errors = parsed;
      }
    } else if (parsed && typeof parsed === "object" && parsed.message) {
      // Cypress format: single error object with message and estack
      errors = [parsed];
    }
  } catch {
    // Invalid JSON, errors stays empty
  }

  if (errors.length === 0) return null;

  // Extract file location from stack trace (for Cypress)
  const extractLocation = (estack?: string): string | null => {
    if (!estack) return null;
    // Match patterns like "at Context.eval (webpack://cypress/./tests/.../file.js:77:49)"
    // or "at file.js:77:49"
    const match = estack.match(/at\s+(?:[\w.]+\s+)?\(?(.+?):(\d+):(\d+)\)?/);
    if (match && match[1] && match[2] && match[3]) {
      const filePath = match[1];
      const line = match[2];
      const col = match[3];
      // Simplify webpack paths
      const simplePath = filePath.replace(/^webpack:\/\/[^/]+\/\.\//, "");
      return `${simplePath}:${line}:${col}`;
    }
    return null;
  };

  const showAttempt = totalAttempts > 1;

  return (
    <div className="rounded-md border border-red-200 bg-red-50 p-2 text-xs dark:border-red-800 dark:bg-red-900/20">
      {showAttempt && (
        <p className="mb-1 text-red-600 dark:text-red-400 font-medium flex items-center gap-1">
          <RotateCcw className="h-3 w-3" />
          Attempt {attempt} of {totalAttempts}
        </p>
      )}
      {errors.map((error, idx) => {
        const location = extractLocation(error.estack);
        return (
          <div key={idx} className="space-y-1">
            <p className="font-medium text-red-800 dark:text-red-300 flex items-center gap-1">
              <XCircle className="h-3 w-3 flex-shrink-0" />
              <span className="break-all">
                {error.message || "Unknown error"}
              </span>
            </p>
            {location && (
              <p className="ml-4 font-mono text-red-600 dark:text-red-400">
                at {location}
              </p>
            )}
            {error.estack && (
              <div className="ml-4">
                <button
                  type="button"
                  onClick={() => setIsExpanded(!isExpanded)}
                  className="text-red-600 hover:text-red-800 dark:text-red-400 dark:hover:text-red-300 underline"
                >
                  {isExpanded ? "Hide stack trace" : "Show stack trace"}
                </button>
                {isExpanded && (
                  <pre className="mt-1 overflow-x-auto whitespace-pre-wrap text-red-600 dark:text-red-400 bg-red-100 dark:bg-red-900/30 p-2 rounded">
                    {error.estack}
                  </pre>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

type StatVariant = "default" | "success" | "error" | "warning" | "muted";

interface StatPillProps {
  label: string;
  value: number;
  variant: StatVariant;
  isActive: boolean;
  onClick: () => void;
}

function StatPill({ label, value, variant, isActive, onClick }: StatPillProps) {
  const variants: Record<StatVariant, string> = {
    default: "bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-200",
    success:
      "bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-400",
    error: "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400",
    warning:
      "bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-400",
    muted: "bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400",
  };

  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium transition-colors ${variants[variant]} ${
        isActive
          ? "ring-1 ring-blue-500 ring-offset-1 dark:ring-offset-gray-800"
          : "hover:opacity-80"
      }`}
    >
      <span className="font-semibold">{value}</span>
      <span className="opacity-70">{label}</span>
    </button>
  );
}

function ProgressBar({ stats }: { stats: ReportStats }) {
  const total = stats.expected + stats.unexpected + stats.flaky + stats.skipped;
  if (total === 0)
    return <div className="h-2 rounded-full bg-gray-200 dark:bg-gray-700" />;

  return (
    <div className="flex h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
      {stats.expected > 0 && (
        <div
          className="h-full bg-green-500"
          style={{ width: `${(stats.expected / total) * 100}%` }}
        />
      )}
      {stats.flaky > 0 && (
        <div
          className="h-full bg-yellow-500"
          style={{ width: `${(stats.flaky / total) * 100}%` }}
        />
      )}
      {stats.unexpected > 0 && (
        <div
          className="h-full bg-red-500"
          style={{ width: `${(stats.unexpected / total) * 100}%` }}
        />
      )}
      {stats.skipped > 0 && (
        <div
          className="h-full bg-gray-400 dark:bg-gray-500"
          style={{ width: `${(stats.skipped / total) * 100}%` }}
        />
      )}
    </div>
  );
}

function calcPassRate(stats: ReportStats): string {
  // Pass rate excludes skipped tests: (passed + flaky) / (passed + flaky + failed)
  const countedTotal = stats.expected + stats.flaky + stats.unexpected;
  if (countedTotal === 0) return "0";
  return (((stats.expected + stats.flaky) / countedTotal) * 100).toFixed(1);
}

function formatDuration(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);

  if (hours > 0) {
    return `${hours}h ${minutes % 60}m ${seconds % 60}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds % 60}s`;
  }
  return `${seconds}s`;
}
