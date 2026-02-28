import { useParams, useSearchParams } from 'react-router-dom';
import { useConsolidatedResults } from '@/services/api';
import { Breadcrumb } from '@/components/breadcrumb';
import { ConsolidatedView } from '@/components/consolidated_view';
import { RunAttemptSelector } from '@/components/run_attempt_selector';
import { Loader2, Inbox, AlertTriangle } from 'lucide-react';

export function FilteredReportPage() {
  const { repo, target_name, commit_sha, tool_name } = useParams<{
    repo: string;
    target_name: string;
    commit_sha: string;
    tool_name: string;
  }>();

  const [search_params] = useSearchParams();
  const run_attempt_param = search_params.get('run_attempt');
  const run_attempt = run_attempt_param ? parseInt(run_attempt_param, 10) : undefined;

  const { data, isLoading, error } = useConsolidatedResults(
    repo || '',
    target_name || '',
    commit_sha || '',
    tool_name || '',
    run_attempt,
  );

  return (
    <div>
      <div className="mb-4">
        <Breadcrumb
          repo={repo || ''}
          target_name={target_name || ''}
          commit_sha={commit_sha || ''}
          tool_name={tool_name || ''}
        />
      </div>

      {/* Run attempt selector */}
      {data && data.available_run_attempts.length > 1 && (
        <div className="mb-4">
          <RunAttemptSelector
            available_attempts={data.available_run_attempts}
            current_attempt={run_attempt}
          />
        </div>
      )}

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
        </div>
      )}

      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-800 dark:bg-red-950/30">
          <div className="flex items-center gap-2 text-sm text-red-700 dark:text-red-400">
            <AlertTriangle className="h-4 w-4" />
            {error instanceof Error && error.message.includes('ambiguous')
              ? 'The short SHA is ambiguous — please use the full 40-character SHA.'
              : `Failed to load results: ${error instanceof Error ? error.message : 'Unknown error'}`}
          </div>
        </div>
      )}

      {data && data.total_specs === 0 && (
        <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
          <Inbox className="h-12 w-12 mb-3" />
          <p className="text-sm">No matching reports</p>
          <p className="text-xs mt-1">
            No test results found for {repo}/{target_name}/{commit_sha}/{tool_name}
          </p>
        </div>
      )}

      {data && data.total_specs > 0 && <ConsolidatedView data={data} />}
    </div>
  );
}
