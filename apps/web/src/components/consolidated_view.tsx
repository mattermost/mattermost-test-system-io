import { useState } from 'react';
import {
  CheckCircle,
  XCircle,
  AlertTriangle,
  MinusCircle,
  ChevronDown,
  ChevronRight,
  GitCommit,
} from 'lucide-react';
import { SourceBadge } from '@/components/source_badge';
import type { ConsolidatedResultsResponse, ConsolidatedSpec } from '@/types';

function status_icon(status: string) {
  switch (status) {
    case 'passed':
      return <CheckCircle className="h-4 w-4 text-green-500" />;
    case 'failed':
    case 'timedOut':
      return <XCircle className="h-4 w-4 text-red-500" />;
    case 'flaky':
      return <AlertTriangle className="h-4 w-4 text-yellow-500" />;
    case 'skipped':
      return <MinusCircle className="h-4 w-4 text-gray-400" />;
    default:
      return <MinusCircle className="h-4 w-4 text-gray-400" />;
  }
}

function overall_status_style(status: string) {
  switch (status) {
    case 'passed':
      return 'bg-green-100 text-green-800 dark:bg-green-900/50 dark:text-green-300';
    case 'failed':
      return 'bg-red-100 text-red-800 dark:bg-red-900/50 dark:text-red-300';
    case 'flaky':
      return 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900/50 dark:text-yellow-300';
    default:
      return 'bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-300';
  }
}

function SpecRow({ spec }: { spec: ConsolidatedSpec }) {
  const [expanded, set_expanded] = useState(false);
  const has_history = spec.history && spec.history.length > 1;

  return (
    <div>
      <button
        type="button"
        onClick={() => has_history && set_expanded(!expanded)}
        className={`flex w-full items-center gap-3 px-4 py-2 text-sm text-left ${
          has_history ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800' : ''
        }`}
      >
        {has_history ? (
          expanded ? (
            <ChevronDown className="h-3 w-3 flex-shrink-0 text-gray-400" />
          ) : (
            <ChevronRight className="h-3 w-3 flex-shrink-0 text-gray-400" />
          )
        ) : (
          <span className="w-3" />
        )}
        {status_icon(spec.status)}
        <span className="flex-1 truncate text-gray-800 dark:text-gray-200">{spec.full_title}</span>
        {!spec.is_from_latest && (
          <SourceBadge commit_sha={spec.source_commit_sha} run_attempt={spec.source_run_attempt} />
        )}
        <span className="text-xs text-gray-400 dark:text-gray-500">{spec.duration_ms}ms</span>
      </button>

      {expanded && spec.history && (
        <div className="ml-10 border-l-2 border-gray-100 pl-4 pb-2 dark:border-gray-700">
          {spec.history.map((entry, i) => (
            <div
              key={`${entry.commit_sha}-${entry.run_attempt}`}
              className="flex items-center gap-2 py-1 text-xs text-gray-500 dark:text-gray-400"
            >
              {status_icon(entry.status)}
              <span className="inline-flex items-center gap-1 font-mono">
                <GitCommit className="h-3 w-3" />
                {entry.commit_sha.slice(0, 7)} #{entry.run_attempt}
              </span>
              <span>{entry.duration_ms}ms</span>
              {entry.error_message && (
                <span className="truncate max-w-[200px] text-red-500" title={entry.error_message}>
                  {entry.error_message}
                </span>
              )}
              {i === 0 && <span className="text-blue-500">(current)</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

interface ConsolidatedViewProps {
  data: ConsolidatedResultsResponse;
}

export function ConsolidatedView({ data }: ConsolidatedViewProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800/50">
      {/* Summary header */}
      <div className="flex items-center gap-3 border-b border-gray-100 px-4 py-3 dark:border-gray-700">
        <span
          className={`rounded-full px-3 py-1 text-sm font-medium ${overall_status_style(data.overall_status)}`}
        >
          {data.overall_status}
        </span>
        <span className="text-sm text-gray-500 dark:text-gray-400">
          {data.total_specs} specs:
          <span className="ml-1 text-green-600 dark:text-green-400">{data.passed} passed</span>
          {data.failed > 0 && (
            <span className="ml-1 text-red-600 dark:text-red-400">{data.failed} failed</span>
          )}
          {data.skipped > 0 && <span className="ml-1 text-gray-500">{data.skipped} skipped</span>}
          {data.flaky > 0 && (
            <span className="ml-1 text-yellow-600 dark:text-yellow-400">{data.flaky} flaky</span>
          )}
        </span>
      </div>

      {/* Spec list */}
      <div className="divide-y divide-gray-50 dark:divide-gray-700/50">
        {data.specs.map((spec) => (
          <SpecRow key={spec.full_title} spec={spec} />
        ))}
      </div>
    </div>
  );
}
