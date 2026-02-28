import {
  FolderGit2,
  GitBranch,
  GitCommit,
  CheckCircle,
  XCircle,
  Clock,
  Loader2,
} from 'lucide-react';
import { Link } from 'react-router-dom';
import type { RepositoryGroup, RunEntry } from '@/types';

function status_icon(status: string) {
  switch (status) {
    case 'complete':
      return <CheckCircle className="h-4 w-4 text-green-500" />;
    case 'failed':
      return <XCircle className="h-4 w-4 text-red-500" />;
    case 'processing':
    case 'uploading':
      return <Loader2 className="h-4 w-4 animate-spin text-blue-500" />;
    default:
      return <Clock className="h-4 w-4 text-gray-400" />;
  }
}

function framework_badge(framework: string) {
  const colors: Record<string, string> = {
    playwright: 'bg-green-100 text-green-700 dark:bg-green-900/50 dark:text-green-300',
    cypress: 'bg-teal-100 text-teal-700 dark:bg-teal-900/50 dark:text-teal-300',
    detox: 'bg-purple-100 text-purple-700 dark:bg-purple-900/50 dark:text-purple-300',
  };
  return (
    <span
      className={`rounded px-1.5 py-0.5 text-xs font-medium ${colors[framework] || 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-300'}`}
    >
      {framework}
    </span>
  );
}

function short_branch(branch: string): string {
  return branch.replace(/^refs\/heads\//, '').replace(/^refs\/tags\//, '');
}

function format_time(date_string: string): string {
  const date = new Date(date_string);
  const now = new Date();
  const diff_ms = now.getTime() - date.getTime();
  const diff_mins = Math.floor(diff_ms / 60000);

  if (diff_mins < 1) return 'just now';
  if (diff_mins < 60) return `${diff_mins}m ago`;
  const diff_hours = Math.floor(diff_mins / 60);
  if (diff_hours < 24) return `${diff_hours}h ago`;
  const diff_days = Math.floor(diff_hours / 24);
  if (diff_days < 7) return `${diff_days}d ago`;
  return date.toLocaleDateString();
}

function run_entry_row({ entry }: { entry: RunEntry }) {
  const branch = short_branch(entry.branch);

  return (
    <Link
      key={entry.report_id}
      to={entry.url_path}
      className="flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors hover:bg-gray-50 dark:hover:bg-gray-800"
    >
      {status_icon(entry.status)}
      {framework_badge(entry.framework)}
      <span className="inline-flex items-center gap-1 text-gray-600 dark:text-gray-400">
        <GitBranch className="h-3 w-3" />
        <span className="max-w-[120px] truncate">{branch}</span>
      </span>
      <span className="inline-flex items-center gap-1 font-mono text-xs text-gray-500 dark:text-gray-500">
        <GitCommit className="h-3 w-3" />
        {entry.short_sha}
      </span>
      {entry.test_stats && entry.test_stats.total > 0 && (
        <span className="ml-auto text-xs text-gray-500 dark:text-gray-500">
          <span className="text-green-600 dark:text-green-400">{entry.test_stats.passed}</span>
          {entry.test_stats.failed > 0 && (
            <>
              {' / '}
              <span className="text-red-600 dark:text-red-400">{entry.test_stats.failed}</span>
            </>
          )}
          {' / '}
          {entry.test_stats.total}
        </span>
      )}
      <span className="ml-auto text-xs text-gray-400 dark:text-gray-600">
        {format_time(entry.created_at)}
      </span>
    </Link>
  );
}

interface RepoGroupCardProps {
  group: RepositoryGroup;
}

export function RepoGroupCard({ group }: RepoGroupCardProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800/50">
      <div className="flex items-center gap-2 border-b border-gray-100 px-4 py-3 dark:border-gray-700">
        <FolderGit2 className="h-5 w-5 text-gray-400 dark:text-gray-500" />
        <h3 className="font-medium text-gray-900 dark:text-white">{group.repository_name}</h3>
        <span className="text-xs text-gray-400 dark:text-gray-500">{group.repository}</span>
      </div>
      <div className="divide-y divide-gray-50 dark:divide-gray-700/50">
        {group.runs.map((entry) => (
          <div key={entry.report_id}>{run_entry_row({ entry })}</div>
        ))}
      </div>
    </div>
  );
}
