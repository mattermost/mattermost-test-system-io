import { useGroupedReports } from '@/services/api';
import { RepoGroupCard } from '@/components/repo_group_card';
import { Loader2, Inbox } from 'lucide-react';

export function HomePage() {
  const { data, isLoading, error } = useGroupedReports();

  return (
    <div>
      <div className="mb-6">
        <h2 className="text-lg font-medium text-gray-900 dark:text-white">Reports</h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          Test reports grouped by repository
        </p>
      </div>

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
        </div>
      )}

      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800 dark:bg-red-950/30 dark:text-red-400">
          Failed to load reports: {error instanceof Error ? error.message : 'Unknown error'}
        </div>
      )}

      {data && data.groups.length === 0 && (
        <div className="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-500">
          <Inbox className="h-12 w-12 mb-3" />
          <p className="text-sm">No reports yet</p>
          <p className="text-xs mt-1">Upload test reports to see them here</p>
        </div>
      )}

      {data && data.groups.length > 0 && (
        <div className="space-y-4">
          {data.groups.map((group) => (
            <RepoGroupCard key={group.repository_name} group={group} />
          ))}
        </div>
      )}
    </div>
  );
}
