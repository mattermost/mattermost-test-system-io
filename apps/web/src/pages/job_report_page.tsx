import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useReportWithJobs, useReportSuites, useClientConfig } from '@/services/api';
import { JobPanel } from '@/components/report/job_panel';
import { TestSuitesView } from '@/components/test_suites_view';
import {
  Loader2,
  ChevronRight,
  ChevronLeft,
  GitBranch,
  GitCommit,
  GitPullRequest,
  Folder,
  Play,
  Calendar,
  FlaskConical,
  AlertCircle,
  CheckCircle,
  Clock,
  FileText,
  Code,
} from 'lucide-react';
import { OidcClaimsSection } from '@/components/report_card_parts/oidc_claims';
import type { ReportStatus, JobSummary } from '@/types';

type MainTab = 'results' | 'html';

// Move formatDate outside component to avoid recreation
function formatDate(dateString: string): string {
  const date = new Date(dateString);
  return date.toLocaleString();
}

// Sort key extractor for job names
function getJobSortKey(name: string): { num: number; str: string } {
  const lastDash = name.lastIndexOf('-');
  if (lastDash === -1) return { num: NaN, str: name };
  const suffix = name.slice(lastDash + 1);
  const num = parseInt(suffix, 10);
  return { num, str: suffix };
}

// Job comparator for sorting
function compareJobs(a: JobSummary, b: JobSummary): number {
  const keyA = getJobSortKey(a.display_name);
  const keyB = getJobSortKey(b.display_name);
  if (!isNaN(keyA.num) && !isNaN(keyB.num)) {
    return keyA.num - keyB.num;
  }
  return keyA.str.localeCompare(keyB.str);
}

export function JobReportPage() {
  const { id } = useParams<{ id: string }>();
  const { data: report, isLoading, error } = useReportWithJobs(id || '');
  const {
    data: suitesData,
    isLoading: isLoadingSuites,
    error: suitesError,
  } = useReportSuites(id || '');
  const { data: config } = useClientConfig();
  const [mainTab, setMainTab] = useState<MainTab>('results');
  const [activeJobId, setActiveJobId] = useState<string | null>(null);
  const [canScrollLeft, setCanScrollLeft] = useState(false);
  const [canScrollRight, setCanScrollRight] = useState(false);
  const navRef = useRef<HTMLElement>(null);

  // Get html_view_enabled from config (default: true)
  const enableHtmlView = config?.html_view_enabled ?? true;

  // Check scroll position and update indicators
  const updateScrollIndicators = useCallback(() => {
    const nav = navRef.current;
    if (!nav) return;

    const { scrollLeft, scrollWidth, clientWidth } = nav;
    setCanScrollLeft(scrollLeft > 0);
    setCanScrollRight(scrollLeft + clientWidth < scrollWidth - 1);
  }, []);

  // Scroll nav left or right
  const scrollNav = useCallback((direction: 'left' | 'right') => {
    const nav = navRef.current;
    if (!nav) return;

    const scrollAmount = 256; // ~2 tabs
    nav.scrollBy({
      left: direction === 'left' ? -scrollAmount : scrollAmount,
      behavior: 'smooth',
    });
  }, []);

  // Auto-select first job when report loads
  useEffect(() => {
    const firstJob = report?.jobs?.[0];
    if (firstJob && !activeJobId) {
      setActiveJobId(firstJob.id);
    }
  }, [report, activeJobId]);

  // Update scroll indicators on mount and when jobs change
  useEffect(() => {
    updateScrollIndicators();
    // Also check on resize
    window.addEventListener('resize', updateScrollIndicators);
    return () => window.removeEventListener('resize', updateScrollIndicators);
  }, [updateScrollIndicators, report?.jobs]);

  // Memoize derived values - must be before any early returns
  const activeJob = useMemo(
    () => report?.jobs.find((j) => j.id === activeJobId) || null,
    [report?.jobs, activeJobId],
  );

  const completedJobs = useMemo(
    () => report?.jobs.filter((j) => j.status === 'complete').length ?? 0,
    [report?.jobs],
  );

  // Memoize sorted jobs for tabs
  const sortedJobs = useMemo(
    () => (report?.jobs ? [...report.jobs].sort(compareJobs) : []),
    [report?.jobs],
  );

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
            <span className="inline-flex items-center gap-1">
              <FlaskConical className="h-3.5 w-3.5" />
              {report.framework.charAt(0).toUpperCase() + report.framework.slice(1)}
            </span>
            <ReportStatusBadge status={report.status} />
            <span className="inline-flex items-center gap-1">
              <CheckCircle className="h-3.5 w-3.5" />
              {completedJobs}/{report.expected_jobs} {report.expected_jobs === 1 ? 'job' : 'jobs'}
            </span>
          </div>

          {/* GitHub Context Badges */}
          {report.github_metadata && (
            <div className="flex flex-wrap items-center gap-1.5 mt-3">
              {report.github_metadata.repository && (
                <a
                  href={`https://github.com/${report.github_metadata.repository}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 px-2 py-1 bg-gray-100 rounded-md text-xs text-gray-700 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600 transition-colors"
                >
                  <Folder className="h-3 w-3" />
                  {report.github_metadata.repository}
                </a>
              )}
              {report.github_metadata.ref && (
                <span className="inline-flex items-center gap-1.5 px-2 py-1 bg-blue-100 rounded-md text-xs text-blue-700 dark:bg-blue-900/50 dark:text-blue-300">
                  <GitBranch className="h-3 w-3" />
                  {report.github_metadata.ref
                    .replace(/^refs\/heads\//, '')
                    .replace(/^refs\/tags\//, '')}
                </span>
              )}
              {report.github_metadata.pr_number && (
                <a
                  href={
                    report.github_metadata.repository
                      ? `https://github.com/${report.github_metadata.repository}/pull/${report.github_metadata.pr_number}`
                      : '#'
                  }
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 px-2 py-1 bg-purple-100 rounded-md text-xs text-purple-700 hover:bg-purple-200 dark:bg-purple-900/50 dark:text-purple-300 dark:hover:bg-purple-900/70 transition-colors"
                >
                  <GitPullRequest className="h-3 w-3" />#{report.github_metadata.pr_number}
                </a>
              )}
              {report.github_metadata.sha && (
                <a
                  href={
                    report.github_metadata.repository
                      ? `https://github.com/${report.github_metadata.repository}/commit/${report.github_metadata.sha}`
                      : '#'
                  }
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 px-2 py-1 bg-gray-100 rounded-md text-xs font-mono text-gray-600 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600 transition-colors"
                >
                  <GitCommit className="h-3 w-3" />
                  {report.github_metadata.sha.slice(0, 7)}
                </a>
              )}
              {report.github_metadata.run_id && (
                <a
                  href={
                    report.github_metadata.repository
                      ? `https://github.com/${report.github_metadata.repository}/actions/runs/${report.github_metadata.run_id}`
                      : '#'
                  }
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 px-2 py-1 bg-green-100 rounded-md text-xs text-green-700 hover:bg-green-200 dark:bg-green-900/50 dark:text-green-300 dark:hover:bg-green-900/70 transition-colors"
                >
                  <Play className="h-3 w-3" />
                  Run {report.github_metadata.run_id}
                </a>
              )}
            </div>
          )}
        </div>
      </div>

      {/* OIDC Provenance (shown when report was uploaded via OIDC) */}
      {report.oidc_claims && (
        <div className="px-4 sm:px-6 pb-4">
          <OidcClaimsSection claims={report.oidc_claims} />
        </div>
      )}

      {/* Main Tabs: Test Results vs HTML Views */}
      <div className="relative border-b border-gray-200 dark:border-gray-700">
        {/* Left scroll indicator */}
        {canScrollLeft && (
          <button
            type="button"
            onClick={() => scrollNav('left')}
            className="absolute left-0 top-0 bottom-0 z-10 flex items-center justify-center w-8 bg-gradient-to-r from-white via-white to-transparent dark:from-gray-900 dark:via-gray-900 dark:to-transparent"
            aria-label="Scroll left"
          >
            <ChevronLeft className="h-5 w-5 text-gray-500 dark:text-gray-400" />
          </button>
        )}

        {/* Right scroll indicator */}
        {canScrollRight && (
          <button
            type="button"
            onClick={() => scrollNav('right')}
            className="absolute right-0 top-0 bottom-0 z-10 flex items-center justify-center w-8 bg-gradient-to-l from-white via-white to-transparent dark:from-gray-900 dark:via-gray-900 dark:to-transparent"
            aria-label="Scroll right"
          >
            <ChevronRight className="h-5 w-5 text-gray-500 dark:text-gray-400" />
          </button>
        )}

        <nav
          ref={navRef}
          onScroll={updateScrollIndicators}
          className="flex gap-1 overflow-x-auto scrollbar-hide"
          aria-label="Tabs"
        >
          <button
            type="button"
            onClick={() => setMainTab('results')}
            className={`cursor-pointer inline-flex items-center justify-center gap-2 px-3 py-2.5 text-sm font-medium border-b-2 transition-colors w-[128px] flex-shrink-0 ${
              mainTab === 'results'
                ? 'border-blue-600 text-blue-600 dark:border-blue-400 dark:text-blue-400'
                : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-200 dark:hover:border-gray-600'
            }`}
          >
            <FileText className="h-4 w-4" />
            Test Results
          </button>
          {enableHtmlView &&
            sortedJobs.map((job) => (
              <button
                key={job.id}
                type="button"
                onClick={() => {
                  setMainTab('html');
                  setActiveJobId(job.id);
                }}
                className={`cursor-pointer inline-flex items-center justify-center gap-2 px-3 py-2.5 text-sm font-medium border-b-2 transition-colors w-[128px] flex-shrink-0 ${
                  mainTab === 'html' && activeJobId === job.id
                    ? 'border-blue-600 text-blue-600 dark:border-blue-400 dark:text-blue-400'
                    : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-200 dark:hover:border-gray-600'
                }`}
                title={job.display_name}
              >
                <Code className="h-4 w-4" />
                <span className="whitespace-nowrap">
                  View{' '}
                  <span className="opacity-75">
                    {job.display_name.length > 8
                      ? `..${job.display_name.slice(-4)}`
                      : job.display_name}
                  </span>
                </span>
              </button>
            ))}
        </nav>
      </div>

      {/* Tab Content */}
      {mainTab === 'results' && (
        <div>
          {isLoadingSuites ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-8 w-8 animate-spin text-blue-600 dark:text-blue-400" />
            </div>
          ) : suitesError ? (
            <div className="rounded-lg bg-yellow-50 border border-yellow-200 p-4 text-yellow-800 dark:bg-yellow-900/20 dark:border-yellow-800 dark:text-yellow-300">
              <p className="font-medium">Unable to load test results</p>
              <p className="text-sm mt-1">{suitesError?.message}</p>
            </div>
          ) : (
            <TestSuitesView
              reportId={report.id}
              suites={suitesData?.suites || []}
              title={`Report ${report.id.slice(0, 8)}`}
              jobs={suitesData?.jobs}
            />
          )}
        </div>
      )}

      {mainTab === 'html' && enableHtmlView && <JobPanel job={activeJob} />}

      {/* Error Message */}
      {report.error_message && (
        <div className="rounded-lg bg-red-50 border border-red-200 p-4 dark:bg-red-900/20 dark:border-red-800">
          <div className="flex items-start gap-3">
            <AlertCircle className="h-5 w-5 text-red-500 dark:text-red-400 flex-shrink-0 mt-0.5" />
            <div>
              <p className="text-sm font-medium text-red-800 dark:text-red-300">Report Error</p>
              <p className="mt-1 text-sm text-red-700 dark:text-red-400">{report.error_message}</p>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

interface ReportStatusBadgeProps {
  status: ReportStatus;
}

function ReportStatusBadge({ status }: ReportStatusBadgeProps) {
  switch (status) {
    case 'complete':
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-700 dark:bg-green-900/50 dark:text-green-300">
          <CheckCircle className="h-3 w-3" />
          Complete
        </span>
      );
    case 'failed':
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-700 dark:bg-red-900/50 dark:text-red-300">
          <AlertCircle className="h-3 w-3" />
          Failed
        </span>
      );
    case 'processing':
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-700 dark:bg-blue-900/50 dark:text-blue-300">
          <Loader2 className="h-3 w-3 animate-spin" />
          Processing
        </span>
      );
    case 'uploading':
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-yellow-100 text-yellow-700 dark:bg-yellow-900/50 dark:text-yellow-300">
          <Clock className="h-3 w-3" />
          Uploading
        </span>
      );
    case 'initializing':
    default:
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-300">
          <Clock className="h-3 w-3" />
          Initializing
        </span>
      );
  }
}
