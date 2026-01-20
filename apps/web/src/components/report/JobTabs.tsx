import { CheckCircle, Clock, XCircle, Loader2 } from 'lucide-react';
import type { JobSummary, JobStatus } from '../../types';

interface JobTabsProps {
  jobs: JobSummary[];
  activeJobId: string | null;
  onSelectJob: (jobId: string) => void;
}

export function JobTabs({ jobs, activeJobId, onSelectJob }: JobTabsProps) {
  if (jobs.length === 0) {
    return (
      <div className="text-sm text-gray-500 dark:text-gray-400 py-2">
        No jobs available
      </div>
    );
  }

  return (
    <div className="flex gap-1 overflow-x-auto pb-2">
      {jobs.map((job) => (
        <button
          key={job.id}
          type="button"
          onClick={() => onSelectJob(job.id)}
          className={`inline-flex items-center gap-2 px-3 py-2 text-sm font-medium rounded-t-lg border-b-2 whitespace-nowrap transition-colors ${
            activeJobId === job.id
              ? 'border-blue-600 text-blue-600 bg-blue-50 dark:border-blue-400 dark:text-blue-400 dark:bg-blue-900/20'
              : 'border-transparent text-gray-600 hover:text-gray-800 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-gray-200 dark:hover:bg-gray-800'
          }`}
        >
          <JobStatusIcon status={job.status} />
          <span className="max-w-[200px] truncate">{job.display_name}</span>
        </button>
      ))}
    </div>
  );
}

interface JobStatusIconProps {
  status: JobStatus;
}

function JobStatusIcon({ status }: JobStatusIconProps) {
  switch (status) {
    case 'complete':
      return <CheckCircle className="h-4 w-4 text-green-500 dark:text-green-400" />;
    case 'failed':
      return <XCircle className="h-4 w-4 text-red-500 dark:text-red-400" />;
    case 'processing':
      return <Loader2 className="h-4 w-4 text-blue-500 dark:text-blue-400 animate-spin" />;
    case 'html_uploaded':
    case 'json_uploaded':
    default:
      return <Clock className="h-4 w-4 text-yellow-500 dark:text-yellow-400" />;
  }
}
