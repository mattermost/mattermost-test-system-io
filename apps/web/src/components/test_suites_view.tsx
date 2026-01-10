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
    <div>
      <button
        type="button"
        onClick={onToggle}
        className="w-full cursor-pointer py-2.5 text-left transition-colors hover:bg-gray-50 dark:hover:bg-gray-700/50"
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
              {filteredSpecs.map((spec) => (
                <SpecRow key={spec.id} spec={spec} />
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
}

function SpecRow({ spec }: SpecRowProps) {
  const latestResult = spec.results[spec.results.length - 1];

  // Determine icon based on actual status, not just spec.ok
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
        <StatusIcon className={`h-3.5 w-3.5 flex-shrink-0 ${statusColor}`} />
        <span className="flex-1 truncate text-gray-900 dark:text-gray-100">
          {spec.title}
        </span>
        {latestResult && (
          <>
            <StatusBadge status={latestResult.status} />
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
        <div className="ml-5 mt-1">
          {spec.results
            .filter((r) => r.errors_json)
            .map((r, idx) => (
              <ErrorDisplay key={idx} errorsJson={r.errors_json!} />
            ))}
        </div>
      )}
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    passed:
      "bg-green-100 text-green-800 dark:bg-green-900/50 dark:text-green-300",
    failed: "bg-red-100 text-red-800 dark:bg-red-900/50 dark:text-red-300",
    skipped: "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-300",
    timedOut:
      "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/50 dark:text-yellow-300",
  };

  return (
    <span
      className={`rounded px-1.5 py-0.5 text-xs font-medium ${colors[status] || colors.failed}`}
    >
      {status}
    </span>
  );
}

function ErrorDisplay({ errorsJson }: { errorsJson: string }) {
  let errors: { message?: string }[] | null = null;
  try {
    const parsed = JSON.parse(errorsJson);
    if (Array.isArray(parsed) && parsed.length > 0) {
      errors = parsed;
    }
  } catch {
    // Invalid JSON, errors stays null
  }

  if (!errors) return null;

  return (
    <div className="rounded-md border border-red-200 bg-red-50 p-2 text-xs dark:border-red-800 dark:bg-red-900/20">
      <p className="mb-1 font-medium text-red-800 dark:text-red-300 flex items-center gap-1">
        <XCircle className="h-3 w-3" />
        Error
      </p>
      {errors.map((error: { message?: string }, idx: number) => (
        <pre
          key={idx}
          className="overflow-x-auto whitespace-pre-wrap text-red-700 dark:text-red-400"
        >
          {error.message || JSON.stringify(error, null, 2)}
        </pre>
      ))}
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
