import { useReports } from "../services/api";
import { ReportCard } from "./report_card";
import { EmptyState } from "./empty_state";
import { useState } from "react";
import { Loader2 } from "lucide-react";

export function ReportList() {
  const [page, setPage] = useState(1);
  const limit = 100;
  const { data, isLoading, error } = useReports(page, limit);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-8 w-8 animate-spin text-blue-600 dark:text-blue-400" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-900/20 dark:text-red-400">
        <p className="font-medium">Error loading reports</p>
        <p className="text-sm">{error.message}</p>
      </div>
    );
  }

  if (!data || data.reports.length === 0) {
    return (
      <EmptyState
        title="No reports yet"
        description="Upload your first test report to get started."
      />
    );
  }

  const { reports, pagination } = data;

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        {reports.map((report, index) => (
          <ReportCard
            key={report.id}
            report={report}
            rowNumber={(page - 1) * limit + index + 1}
          />
        ))}
      </div>

      {pagination.total_pages > 1 && (
        <div className="flex items-center justify-between border-t border-gray-200 pt-4 dark:border-gray-700">
          <div className="text-sm text-gray-500 dark:text-gray-400">
            Showing {(page - 1) * limit + 1} to{" "}
            {Math.min(page * limit, pagination.total)} of {pagination.total}{" "}
            reports
          </div>
          <div className="flex gap-2">
            <button
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page === 1}
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
            >
              Previous
            </button>
            <button
              onClick={() =>
                setPage((p) => Math.min(pagination.total_pages, p + 1))
              }
              disabled={page >= pagination.total_pages}
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
            >
              Next
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
