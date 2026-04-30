import { ChevronLeft, ChevronRight } from 'lucide-react';

export interface PaginationBarProps {
  /** 1-based current page index. */
  currentPage: number;
  /** Total page count. The bar is meant to be conditionally rendered by callers when this is > 1. */
  totalPages: number;
  /** Total filtered item count, used for the "Showing X to Y of Z" label. */
  totalItems: number;
  /** Items per page. Used to compute the visible window. */
  pageSize: number;
  /** Callback invoked with the next 1-based page index. */
  onPageChange: (next: number) => void;
  /**
   * Singular label for the item kind being paginated (e.g. "spec",
   * "suite", "report"). Pluralized with a naive `s` suffix when count
   * differs from 1. Defaults to "item".
   */
  itemLabel?: string;
  /**
   * `top` puts the bar above the list (border-bottom + bottom margin);
   * `bottom` mirrors below (border-top + top margin). Defaults to `top`.
   */
  position?: 'top' | 'bottom';
}

export function PaginationBar({
  currentPage,
  totalPages,
  totalItems,
  pageSize,
  onPageChange,
  itemLabel = 'item',
  position = 'top',
}: PaginationBarProps) {
  const start = (currentPage - 1) * pageSize + 1;
  const end = Math.min(currentPage * pageSize, totalItems);
  const noun = totalItems === 1 ? itemLabel : `${itemLabel}s`;
  const wrapperClass =
    position === 'top'
      ? 'flex items-center justify-between border-b border-gray-200 pb-4 mb-4 dark:border-gray-700'
      : 'flex items-center justify-between border-t border-gray-200 pt-4 mt-4 dark:border-gray-700';
  return (
    <div className={wrapperClass}>
      <div className="text-sm text-gray-500 dark:text-gray-400">
        Showing {start} to {end} of {totalItems} {noun}
      </div>
      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => onPageChange(currentPage - 1)}
          disabled={currentPage === 1}
          className="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
        >
          <ChevronLeft className="h-4 w-4" />
          Previous
        </button>
        <button
          type="button"
          onClick={() => onPageChange(currentPage + 1)}
          disabled={currentPage >= totalPages}
          className="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
        >
          Next
          <ChevronRight className="h-4 w-4" />
        </button>
      </div>
    </div>
  );
}
