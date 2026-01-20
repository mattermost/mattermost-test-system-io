import { Code, ExternalLink, FileWarning, Clock, AlertCircle } from 'lucide-react';
import type { JobSummary } from '../../types';

interface JobPanelProps {
  job: JobSummary | null;
}

export function JobPanel({ job }: JobPanelProps) {
  if (!job) {
    return (
      <div className="flex items-center justify-center h-[500px] text-gray-500 dark:text-gray-400">
        <div className="text-center">
          <FileWarning className="h-12 w-12 mx-auto mb-3 opacity-50" />
          <p>Select a job to view its HTML report</p>
        </div>
      </div>
    );
  }

  // Job is still processing
  if (job.status === 'processing') {
    return (
      <div className="flex items-center justify-center h-[500px]">
        <div className="text-center">
          <Clock className="h-12 w-12 mx-auto mb-3 text-blue-500 dark:text-blue-400 animate-pulse" />
          <p className="text-gray-700 dark:text-gray-300 font-medium">Processing</p>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
            This job is still being processed...
          </p>
        </div>
      </div>
    );
  }

  // Job failed
  if (job.status === 'failed') {
    return (
      <div className="flex items-center justify-center h-[500px]">
        <div className="text-center">
          <AlertCircle className="h-12 w-12 mx-auto mb-3 text-red-500 dark:text-red-400" />
          <p className="text-gray-700 dark:text-gray-300 font-medium">Job Failed</p>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
            This job encountered an error during processing
          </p>
        </div>
      </div>
    );
  }

  // Job has no HTML report
  if (!job.html_url) {
    return (
      <div className="flex items-center justify-center h-[500px]">
        <div className="text-center">
          <FileWarning className="h-12 w-12 mx-auto mb-3 text-yellow-500 dark:text-yellow-400" />
          <p className="text-gray-700 dark:text-gray-300 font-medium">No HTML Report</p>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
            This job does not have an HTML report available
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-600 dark:bg-gray-800 overflow-hidden">
      <div className="border-b border-gray-200 bg-gray-50 px-4 py-3 flex items-center justify-between dark:border-gray-600 dark:bg-gray-700">
        <h3 className="text-sm font-medium text-gray-900 dark:text-gray-100 inline-flex items-center gap-2">
          <Code className="h-4 w-4 text-gray-500 dark:text-gray-300" />
          {job.display_name}
        </h3>
        <a
          href={job.html_url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1.5 text-sm text-blue-600 hover:text-blue-700 dark:text-blue-300 dark:hover:text-blue-200 transition-colors"
        >
          Open in new tab
          <ExternalLink className="h-3.5 w-3.5" />
        </a>
      </div>
      <iframe
        src={job.html_url}
        className="h-[700px] w-full bg-white"
        title={`HTML Report - ${job.display_name}`}
      />
    </div>
  );
}
