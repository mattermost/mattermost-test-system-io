import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useReport, useReportSuites, getReportHtmlUrl } from '../services/api';
import { TestSuitesView } from '../components/test_suites_view';
import {
  Loader2,
  ChevronRight,
  GitBranch,
  GitCommit,
  GitPullRequest,
  Folder,
  Play,
  FileText,
  Code,
  ExternalLink,
  AlertCircle,
  Calendar,
  FlaskConical,
} from 'lucide-react';

type ViewTab = 'report' | 'html';

export function ReportPage() {
  const { id } = useParams<{ id: string }>();
  const [activeTab, setActiveTab] = useState<ViewTab>('report');
  const { data: report, isLoading, error } = useReport(id || '');
  const {
    data: suitesData,
    isLoading: isLoadingSuites,
    error: suitesError,
  } = useReportSuites(id || '');

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-8 w-8 animate-spin text-blue-600 dark:text-blue-400" />
      </div>
    );
  }

  if (error || !report) {
    return (
      <div className="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-900/20 dark:text-red-400">
        <p className="font-medium">Error loading report</p>
        <p className="text-sm">{error?.message || 'Report not found'}</p>
        <Link
          to="/"
          className="mt-4 inline-block text-sm text-red-600 hover:text-red-500 dark:text-red-400 dark:hover:text-red-300"
        >
          Back to reports
        </Link>
      </div>
    );
  }

  const formatDate = (dateString: string) => {
    const date = new Date(dateString);
    return date.toLocaleString();
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          {/* Breadcrumb */}
          <nav className="flex items-center gap-1 text-sm">
            <Link
              to="/"
              className="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
            >
              Reports
            </Link>
            <ChevronRight className="h-4 w-4 text-gray-400 dark:text-gray-500" />
            <span className="font-mono text-gray-700 dark:text-gray-300">
              {report.id.slice(0, 8)}
            </span>
          </nav>

          {/* Title + Meta */}
          <h1 className="mt-2 text-xl font-semibold text-gray-900 dark:text-white">
            Report Details
          </h1>
          <div className="flex items-center gap-3 mt-1 text-sm text-gray-500 dark:text-gray-400">
            <span className="inline-flex items-center gap-1">
              <Calendar className="h-3.5 w-3.5" />
              {formatDate(report.created_at)}
            </span>
            {report.framework && report.framework_version && (
              <span className="inline-flex items-center gap-1">
                <FlaskConical className="h-3.5 w-3.5" />
                {report.framework.charAt(0).toUpperCase() + report.framework.slice(1)} v
                {report.framework_version}
              </span>
            )}
          </div>

          {/* GitHub Context Badges */}
          {report.github_context && (
            <div className="flex flex-wrap items-center gap-1.5 mt-3">
              {report.github_context.repository && (
                <a
                  href={`https://github.com/${report.github_context.repository}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 px-2 py-1 bg-gray-100 rounded-md text-xs text-gray-700 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600 transition-colors"
                >
                  <Folder className="h-3 w-3" />
                  {report.github_context.repository}
                </a>
              )}
              {report.github_context.branch && (
                <span className="inline-flex items-center gap-1.5 px-2 py-1 bg-blue-100 rounded-md text-xs text-blue-700 dark:bg-blue-900/50 dark:text-blue-300">
                  <GitBranch className="h-3 w-3" />
                  {report.github_context.branch}
                </span>
              )}
              {report.github_context.pr_number && (
                <a
                  href={
                    report.github_context.repository
                      ? `https://github.com/${report.github_context.repository}/pull/${report.github_context.pr_number}`
                      : '#'
                  }
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 px-2 py-1 bg-purple-100 rounded-md text-xs text-purple-700 hover:bg-purple-200 dark:bg-purple-900/50 dark:text-purple-300 dark:hover:bg-purple-900/70 transition-colors"
                >
                  <GitPullRequest className="h-3 w-3" />#{report.github_context.pr_number}
                </a>
              )}
              {report.github_context.commit_sha && (
                <a
                  href={
                    report.github_context.repository
                      ? `https://github.com/${report.github_context.repository}/commit/${report.github_context.commit_sha}`
                      : '#'
                  }
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 px-2 py-1 bg-gray-100 rounded-md text-xs font-mono text-gray-600 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600 transition-colors"
                >
                  <GitCommit className="h-3 w-3" />
                  {report.github_context.commit_sha.slice(0, 7)}
                </a>
              )}
              {report.github_context.run_id && (
                <a
                  href={
                    report.github_context.repository
                      ? `https://github.com/${report.github_context.repository}/actions/runs/${report.github_context.run_id}`
                      : '#'
                  }
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 px-2 py-1 bg-green-100 rounded-md text-xs text-green-700 hover:bg-green-200 dark:bg-green-900/50 dark:text-green-300 dark:hover:bg-green-900/70 transition-colors"
                >
                  <Play className="h-3 w-3" />
                  Run {report.github_context.run_id}
                  {report.github_context.run_attempt &&
                    report.github_context.run_attempt > 1 &&
                    ` #${report.github_context.run_attempt}`}
                </a>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-gray-200 dark:border-gray-700">
        <nav className="flex gap-1" aria-label="Tabs">
          <TabButton
            active={activeTab === 'report'}
            onClick={() => setActiveTab('report')}
            icon={<FileText className="h-4 w-4" />}
          >
            Test Results
          </TabButton>
          {report.has_files && !report.files_deleted_at && (
            <TabButton
              active={activeTab === 'html'}
              onClick={() => setActiveTab('html')}
              icon={<Code className="h-4 w-4" />}
            >
              HTML Report
            </TabButton>
          )}
        </nav>
      </div>

      {/* Tab Content */}
      {activeTab === 'report' && (
        <div>
          {isLoadingSuites ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-8 w-8 animate-spin text-blue-600 dark:text-blue-400" />
            </div>
          ) : suitesError ? (
            <div className="rounded-lg bg-yellow-50 border border-yellow-200 p-4 text-yellow-800 dark:bg-yellow-900/20 dark:border-yellow-800 dark:text-yellow-300">
              <p className="font-medium">Unable to load test results</p>
              <p className="text-sm mt-1">{suitesError?.message}</p>
              {report.has_files && !report.files_deleted_at && (
                <p className="text-sm mt-2">
                  Try viewing the{' '}
                  <button
                    type="button"
                    className="text-yellow-700 underline hover:text-yellow-600 dark:text-yellow-400 dark:hover:text-yellow-300"
                    onClick={() => setActiveTab('html')}
                  >
                    HTML Report
                  </button>{' '}
                  instead.
                </p>
              )}
            </div>
          ) : (
            <TestSuitesView
              reportId={report.id}
              suites={suitesData?.suites || []}
              stats={report.stats}
              title={`Report ${report.id.slice(0, 8)}`}
              jobs={suitesData?.jobs}
            />
          )}
        </div>
      )}

      {activeTab === 'html' && report.has_files && !report.files_deleted_at && (
        <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-600 dark:bg-gray-800 overflow-hidden">
          <div className="border-b border-gray-200 bg-gray-50 px-4 py-3 flex items-center justify-between dark:border-gray-600 dark:bg-gray-700">
            <h3 className="text-sm font-medium text-gray-900 dark:text-gray-100 inline-flex items-center gap-2">
              <Code className="h-4 w-4 text-gray-500 dark:text-gray-300" />
              HTML Report
            </h3>
            <a
              href={getReportHtmlUrl(report.id)}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 text-sm text-blue-600 hover:text-blue-700 dark:text-blue-300 dark:hover:text-blue-200 transition-colors"
            >
              Open in new tab
              <ExternalLink className="h-3.5 w-3.5" />
            </a>
          </div>
          <iframe
            src={getReportHtmlUrl(report.id)}
            className="h-[700px] w-full bg-white dark:invert dark:hue-rotate-180"
            title="HTML Report"
          />
        </div>
      )}

      {/* Error Message */}
      {report.error_message && (
        <div className="rounded-lg bg-red-50 border border-red-200 p-4 dark:bg-red-900/20 dark:border-red-800">
          <div className="flex items-start gap-3">
            <AlertCircle className="h-5 w-5 text-red-500 dark:text-red-400 flex-shrink-0 mt-0.5" />
            <div>
              <p className="text-sm font-medium text-red-800 dark:text-red-300">Extraction Error</p>
              <p className="mt-1 text-sm text-red-700 dark:text-red-400">{report.error_message}</p>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

interface TabButtonProps {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
  icon?: React.ReactNode;
}

function TabButton({ active, onClick, children, icon }: TabButtonProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-2 px-3 py-2.5 text-sm font-medium border-b-2 transition-colors ${
        active
          ? 'border-blue-600 text-blue-600 dark:border-blue-400 dark:text-blue-400'
          : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-200 dark:hover:border-gray-600'
      }`}
    >
      {icon}
      {children}
    </button>
  );
}
