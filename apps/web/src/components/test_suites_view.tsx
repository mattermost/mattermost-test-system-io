import { useState, useEffect, useRef, useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  ChevronRight,
  ChevronDown,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  MinusCircle,
  Clock,
  FileCode,
  Loader2,
  RotateCcw,
  Filter,
  Search,
  X,
} from 'lucide-react';
import type { TestSuite, ReportStats, TestSpec, TestSpecListResponse, TestAttachment, JobInfo } from '../types';
import { ScreenshotGallery } from './ui/screenshot-gallery';
import { useSearchTestCases, useClientConfig, type SearchSuiteResult } from '../services/api';

const API_BASE = '/api/v1';
const SEARCH_DEBOUNCE_MS = 1000; // 1 second debounce for search API

type StatusFilter = 'all' | 'passed' | 'failed' | 'flaky' | 'skipped';

interface TestSuitesViewProps {
  reportId: string;
  suites: TestSuite[];
  stats?: ReportStats;
  title?: string;
  jobs?: JobInfo[];
}

export function TestSuitesView({ reportId, suites, stats, title, jobs }: TestSuitesViewProps) {
  const [expandedSuiteIds, setExpandedSuiteIds] = useState<Set<number>>(new Set());
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selectedJobs, setSelectedJobs] = useState<Set<string>>(new Set()); // empty = all jobs
  const [jobDropdownOpen, setJobDropdownOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const jobDropdownRef = useRef<HTMLDivElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);

  // Get client config for min_search_length
  const { data: clientConfig } = useClientConfig();
  const minSearchLength = clientConfig?.min_search_length ?? 3;

  // Debounce search query for API calls (1 second)
  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedSearch(searchQuery.trim());
    }, SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [searchQuery]);

  // Search API - only calls when search query meets min length
  const { data: searchData, isLoading: isSearching } = useSearchTestCases(
    reportId,
    debouncedSearch,
    minSearchLength,
    500 // Get more results for better grouping
  );

  // Build a map of suite_id -> SearchSuiteResult from API response
  const searchResultsBySuite = useMemo(() => {
    if (!searchData?.results) return new Map<string, SearchSuiteResult>();
    const map = new Map<string, SearchSuiteResult>();
    for (const suiteResult of searchData.results) {
      map.set(suiteResult.suite_id, suiteResult);
    }
    return map;
  }, [searchData?.results]);

  // Check if we have active API search results
  const hasApiSearchResults = debouncedSearch.length >= minSearchLength && searchData?.results && searchData.results.length > 0;

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (jobDropdownRef.current && !jobDropdownRef.current.contains(event.target as Node)) {
        setJobDropdownOpen(false);
      }
    };

    if (jobDropdownOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [jobDropdownOpen]);

  const handleSuiteClick = (suiteId: number) => {
    setExpandedSuiteIds((prev) => {
      const next = new Set(prev);
      if (next.has(suiteId)) {
        next.delete(suiteId);
      } else {
        next.add(suiteId);
      }
      return next;
    });
  };

  // Normalize search query for case-insensitive client-side matching
  const normalizedSearch = searchQuery.toLowerCase().trim();

  // Filter and sort suites by start_time (actual test execution time)
  // Two-tier search:
  // 1. Client-side: Always filter by suite title/file_path (immediate)
  // 2. API-side: When query >= minSearchLength, also include suites with matching test cases
  const filteredSuites = useMemo(() => {
    return suites
      .filter((suite) => {
        // Job filter
        if (selectedJobs.size > 0 && suite.job_id && !selectedJobs.has(suite.job_id)) {
          return false;
        }

        // Search filter - two-tier approach
        if (normalizedSearch) {
          // Tier 1: Client-side suite title/file_path match (always)
          const titleMatch = suite.title?.toLowerCase().includes(normalizedSearch);
          const filePathMatch = suite.file_path?.toLowerCase().includes(normalizedSearch);
          const suiteMatches = titleMatch || filePathMatch;

          // Tier 2: API-side test case match (when query meets min length)
          const suiteIdStr = String(suite.id);
          const hasTestCaseMatches = hasApiSearchResults && searchResultsBySuite.has(suiteIdStr);

          // Include suite if it matches either tier
          if (!suiteMatches && !hasTestCaseMatches) {
            return false;
          }
        }

        // Status filter
        if (statusFilter === 'all') return true;
        switch (statusFilter) {
          case 'passed':
            return suite.passed_count > 0;
          case 'failed':
            return suite.failed_count > 0;
          case 'flaky':
            return (suite.flaky_count ?? 0) > 0;
          case 'skipped':
            return (suite.skipped_count ?? 0) > 0;
          default:
            return true;
        }
      })
      .sort((a, b) => {
        // Sort by start_time (actual test execution time), fallback to created_at
        const aTime = a.start_time || a.created_at;
        const bTime = b.start_time || b.created_at;
        if (aTime && bTime) {
          return new Date(aTime).getTime() - new Date(bTime).getTime();
        }
        return 0;
      });
  }, [suites, selectedJobs, normalizedSearch, statusFilter, hasApiSearchResults, searchResultsBySuite]);

  // Toggle job selection
  const toggleJob = (jobId: string) => {
    setSelectedJobs((prev) => {
      const next = new Set(prev);
      if (next.has(jobId)) {
        next.delete(jobId);
      } else {
        next.add(jobId);
      }
      return next;
    });
  };

  // Select all jobs (clear selection = show all)
  const selectAllJobs = () => {
    setSelectedJobs(new Set());
    setJobDropdownOpen(false);
  };

  // Calculate totals from suites (use filtered suites for accurate counts)
  const suitesForTotals = selectedJobs.size > 0
    ? suites.filter((s) => s.job_id && selectedJobs.has(s.job_id))
    : suites;
  const totals = suitesForTotals.reduce(
    (acc, suite) => ({
      passed: acc.passed + (suite.passed_count ?? 0),
      failed: acc.failed + (suite.failed_count ?? 0),
      flaky: acc.flaky + (suite.flaky_count ?? 0),
      skipped: acc.skipped + (suite.skipped_count ?? 0),
    }),
    { passed: 0, failed: 0, flaky: 0, skipped: 0 }
  );
  const totalTests = totals.passed + totals.failed + totals.flaky + totals.skipped;

  return (
    <div className="space-y-3">
      {/* Stats Header */}
      {stats && (
        <div className="rounded-lg border border-gray-200 bg-white px-4 py-2.5 dark:border-gray-700 dark:bg-gray-800">
          {/* Row 1: Title, stats */}
          <div className="flex items-center gap-4">
            {/* Left: Title + Pass rate + Duration */}
            <div className="flex items-center gap-2 min-w-0">
              <h2 className="text-sm font-medium text-gray-900 dark:text-white truncate">
                {title || 'Test Report'}
              </h2>
              <span className="text-xs text-gray-400 dark:text-gray-500">•</span>
              <span
                className={`inline-flex items-center gap-1 text-xs font-semibold whitespace-nowrap ${
                  calcPassRate(stats) === '100.0'
                    ? 'text-green-600 dark:text-green-400'
                    : 'text-red-600 dark:text-red-400'
                }`}
              >
                {calcPassRate(stats) === '100.0' ? (
                  <CheckCircle2 className="h-3 w-3" />
                ) : (
                  <XCircle className="h-3 w-3" />
                )}
                {calcPassRate(stats)}%
              </span>
              <span className="text-xs text-gray-400 dark:text-gray-500">•</span>
              <span className="text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap">
                {formatDuration(stats.duration_ms)}
              </span>
            </div>

            <div className="flex-1" />

            {/* Right: Stat pills */}
            <div className="flex items-center gap-1">
              <StatPill
                label="Total"
                value={stats.expected + stats.unexpected + stats.flaky + stats.skipped}
                variant="default"
                isActive={statusFilter === 'all'}
                onClick={() => setStatusFilter('all')}
              />
              <StatPill
                label="Passed"
                value={stats.expected}
                variant="success"
                isActive={statusFilter === 'passed'}
                onClick={() => setStatusFilter('passed')}
              />
              {stats.unexpected > 0 && (
                <StatPill
                  label="Failed"
                  value={stats.unexpected}
                  variant="error"
                  isActive={statusFilter === 'failed'}
                  onClick={() => setStatusFilter('failed')}
                />
              )}
              {stats.flaky > 0 && (
                <StatPill
                  label="Flaky"
                  value={stats.flaky}
                  variant="warning"
                  isActive={statusFilter === 'flaky'}
                  onClick={() => setStatusFilter('flaky')}
                />
              )}
              {stats.skipped > 0 && (
                <StatPill
                  label="Skipped"
                  value={stats.skipped}
                  variant="muted"
                  isActive={statusFilter === 'skipped'}
                  onClick={() => setStatusFilter('skipped')}
                />
              )}
            </div>
          </div>

          {/* Row 2: Full-width progress bar */}
          <div className="mt-2">
            <ProgressBar stats={stats} />
          </div>
        </div>
      )}

      {/* Suites Summary */}
      <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
        <div className="mb-4 flex items-center gap-4">
          {/* Section 1: Title (fixed width) */}
          <h3 className="w-40 flex-shrink-0 text-sm font-medium text-gray-900 dark:text-white">
            Test Suites ({filteredSuites.length}
            {(statusFilter !== 'all' || normalizedSearch) ? ` of ${suites.length}` : ''})
          </h3>

          {/* Section 2: Search input (fixed width, centered) */}
          <div className="relative flex-shrink-0">
            {isSearching ? (
              <Loader2 className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-blue-500 animate-spin" />
            ) : (
              <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-gray-400 dark:text-gray-500" />
            )}
            <input
              ref={searchInputRef}
              type="text"
              placeholder="Search tests..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="h-7 w-56 rounded border border-gray-200 bg-white pl-7 pr-7 text-xs text-gray-900 placeholder-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-gray-600 dark:bg-gray-700 dark:text-white dark:placeholder-gray-500 dark:focus:border-blue-400 dark:focus:ring-blue-400"
            />
            {searchQuery && (
              <button
                type="button"
                onClick={() => {
                  setSearchQuery('');
                  setDebouncedSearch('');
                  searchInputRef.current?.focus();
                }}
                className="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-0.5 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:text-gray-500 dark:hover:bg-gray-600 dark:hover:text-gray-300"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>

          {/* Spacer */}
          <div className="flex-1" />

          {/* Section 3: Filters (status + job dropdown) */}
          <div className="flex flex-shrink-0 items-center gap-2">
            {/* Status filter buttons */}
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => setStatusFilter('all')}
                className={`cursor-pointer rounded px-2 py-0.5 text-xs font-medium transition-colors ${
                  statusFilter === 'all'
                    ? 'bg-gray-200 text-gray-900 dark:bg-gray-600 dark:text-white'
                    : 'text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700'
                }`}
              >
                All ({totalTests})
              </button>
              <button
                type="button"
                onClick={() => setStatusFilter('passed')}
                className={`cursor-pointer rounded px-2 py-0.5 text-xs font-medium transition-colors ${
                  statusFilter === 'passed'
                    ? 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-400'
                    : 'text-green-600 hover:bg-green-50 dark:text-green-500 dark:hover:bg-green-900/20'
                }`}
              >
                <span className="inline-flex items-center gap-1">
                  <CheckCircle2 className="h-3 w-3" />
                  {totals.passed}
                </span>
              </button>
              {totals.failed > 0 && (
                <button
                  type="button"
                  onClick={() => setStatusFilter('failed')}
                  className={`cursor-pointer rounded px-2 py-0.5 text-xs font-medium transition-colors ${
                    statusFilter === 'failed'
                      ? 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400'
                      : 'text-red-600 hover:bg-red-50 dark:text-red-500 dark:hover:bg-red-900/20'
                  }`}
                >
                  <span className="inline-flex items-center gap-1">
                    <XCircle className="h-3 w-3" />
                    {totals.failed}
                  </span>
                </button>
              )}
              {totals.flaky > 0 && (
                <button
                  type="button"
                  onClick={() => setStatusFilter('flaky')}
                  className={`cursor-pointer rounded px-2 py-0.5 text-xs font-medium transition-colors ${
                    statusFilter === 'flaky'
                      ? 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-400'
                      : 'text-yellow-600 hover:bg-yellow-50 dark:text-yellow-500 dark:hover:bg-yellow-900/20'
                  }`}
                >
                  <span className="inline-flex items-center gap-1">
                    <AlertTriangle className="h-3 w-3" />
                    {totals.flaky}
                  </span>
                </button>
              )}
              {totals.skipped > 0 && (
                <button
                  type="button"
                  onClick={() => setStatusFilter('skipped')}
                  className={`cursor-pointer rounded px-2 py-0.5 text-xs font-medium transition-colors ${
                    statusFilter === 'skipped'
                      ? 'bg-gray-200 text-gray-700 dark:bg-gray-600 dark:text-gray-300'
                      : 'text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700'
                  }`}
                >
                  <span className="inline-flex items-center gap-1">
                    <MinusCircle className="h-3 w-3" />
                    {totals.skipped}
                  </span>
                </button>
              )}
            </div>

            {/* Job filter dropdown - only show when multiple jobs */}
            {jobs && jobs.length > 1 && (
            <div ref={jobDropdownRef} className="relative">
              <button
                type="button"
                onClick={() => setJobDropdownOpen(!jobDropdownOpen)}
                className={`cursor-pointer inline-flex items-center gap-1.5 rounded px-2 py-1 text-xs font-medium transition-colors w-28 justify-center ${
                  selectedJobs.size > 0
                    ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-400'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-400 dark:hover:bg-gray-600'
                }`}
              >
                <Filter className="h-3 w-3" />
                {selectedJobs.size > 0 ? `${selectedJobs.size} job${selectedJobs.size > 1 ? 's' : ''}` : 'All Jobs'}
                <ChevronDown className={`h-3 w-3 transition-transform ${jobDropdownOpen ? 'rotate-180' : ''}`} />
              </button>

              {jobDropdownOpen && (
                <div className="absolute right-0 z-10 mt-1 w-80 max-w-[90vw] rounded-md border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800">
                  <div className="p-2 max-h-64 overflow-y-auto">
                    <button
                      type="button"
                      onClick={selectAllJobs}
                      className={`w-full rounded px-2 py-1.5 text-left text-xs transition-colors ${
                        selectedJobs.size === 0
                          ? 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
                          : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700'
                      }`}
                    >
                      All Jobs
                    </button>
                    <div className="my-1 border-t border-gray-200 dark:border-gray-700" />
                    {[...jobs].sort((a, b) => a.job_number - b.job_number).map((job) => (
                      <button
                        key={job.job_id}
                        type="button"
                        onClick={() => toggleJob(job.job_id)}
                        className={`w-full rounded px-2 py-1.5 text-left text-xs transition-colors ${
                          selectedJobs.has(job.job_id)
                            ? 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
                            : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700'
                        }`}
                      >
                        <span className="flex items-center gap-2 min-w-0">
                          <span className="inline-flex h-5 min-w-5 items-center justify-center rounded bg-gray-200 px-1 text-[10px] font-semibold text-gray-600 dark:bg-gray-600 dark:text-gray-300">
                            {job.job_number}
                          </span>
                          <span className="truncate" title={job.job_name}>{job.job_name}</span>
                        </span>
                      </button>
                    ))}
                  </div>
                </div>
              )}
            </div>
            )}
          </div>
        </div>

        {filteredSuites.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400">
            {statusFilter === 'all'
              ? 'No test suites found'
              : `No suites with ${statusFilter} tests`}
          </p>
        ) : (
          <div className="divide-y divide-gray-100 dark:divide-gray-700">
            {filteredSuites.map((suite, index) => (
              <SuiteRow
                key={suite.id}
                suite={suite}
                reportId={reportId}
                isExpanded={expandedSuiteIds.has(suite.id)}
                onToggle={() => handleSuiteClick(suite.id)}
                statusFilter={statusFilter}
                rowNumber={index + 1}
                hasMultipleJobs={!!jobs && jobs.length > 1}
                searchQuery={normalizedSearch}
                searchSuiteResult={searchResultsBySuite.get(String(suite.id))}
              />
            ))}
          </div>
        )}

        {/* Totals - use stats for consistency with header */}
        {suites.length > 0 && stats && (
          <div className="mt-4 flex items-center justify-between border-t border-gray-200 pt-4 text-xs dark:border-gray-700">
            <span className="font-medium text-gray-900 dark:text-white">Total</span>
            <div className="flex items-center gap-3">
              <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-400">
                <Clock className="h-3 w-3" />
                {formatDuration(stats.duration_ms)}
              </span>
              <span className="text-gray-600 dark:text-gray-300">
                {stats.expected + stats.unexpected + stats.flaky + stats.skipped} specs
              </span>
              <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
                <CheckCircle2 className="h-3 w-3" />
                {stats.expected}
              </span>
              {stats.flaky > 0 && (
                <span className="inline-flex items-center gap-1 text-yellow-600 dark:text-yellow-400">
                  <AlertTriangle className="h-3 w-3" />
                  {stats.flaky}
                </span>
              )}
              {stats.unexpected > 0 && (
                <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
                  <XCircle className="h-3 w-3" />
                  {stats.unexpected}
                </span>
              )}
              {stats.skipped > 0 && (
                <span className="inline-flex items-center gap-1 text-gray-400 dark:text-gray-500">
                  <MinusCircle className="h-3 w-3" />
                  {stats.skipped}
                </span>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

interface SuiteRowProps {
  suite: TestSuite;
  reportId: string;
  isExpanded: boolean;
  onToggle: () => void;
  statusFilter: StatusFilter;
  rowNumber: number;
  hasMultipleJobs: boolean;
  searchQuery: string;
  searchSuiteResult?: SearchSuiteResult;
}

function SuiteRow({
  suite,
  reportId,
  isExpanded,
  onToggle,
  statusFilter,
  rowNumber,
  hasMultipleJobs,
  searchQuery,
  searchSuiteResult,
}: SuiteRowProps) {
  const hasFlaky = (suite.flaky_count ?? 0) > 0;
  const hasFailed = suite.failed_count > 0;
  const hasSkipped = (suite.skipped_count ?? 0) > 0;
  const hasPassed = suite.passed_count > 0;
  // Suite is skipped-only if it has skipped tests but no passed, failed, or flaky
  const isSkippedOnly = hasSkipped && !hasPassed && !hasFailed && !hasFlaky;

  // Fetch specs when expanded
  const { data: specsData, isLoading, isFetched } = useQuery<TestSpecListResponse>({
    queryKey: ['suite-specs', reportId, suite.id],
    queryFn: async () => {
      const res = await fetch(`${API_BASE}/reports/${reportId}/suites/${suite.id}/specs`);
      if (!res.ok) throw new Error('Failed to fetch specs');
      return res.json();
    },
    enabled: isExpanded,
    staleTime: 60000,
  });

  // Only show expanded content when data is ready (not loading)
  const showExpanded = isExpanded && isFetched && !isLoading;

  // Build a set of matched test case IDs and match tokens from search results
  const matchedTestCaseIds = useMemo(() => {
    if (!searchSuiteResult?.matches || searchSuiteResult.matches.length === 0) return null;
    return new Set(searchSuiteResult.matches.map(tc => tc.test_case_id));
  }, [searchSuiteResult]);

  // Filter specs based on status filter and search query
  const filteredSpecs =
    specsData?.specs?.filter((spec) => {
      // If we have matched test cases from search API, filter by those
      if (matchedTestCaseIds) {
        // Check if this spec's ID matches any of the search results
        const specIdStr = spec.id.toString();
        if (!matchedTestCaseIds.has(specIdStr)) {
          // Also match by title for cases where IDs might not align
          const matchedByTitle = searchSuiteResult?.matches?.some(tc =>
            tc.title.toLowerCase() === spec.title?.toLowerCase() ||
            tc.full_title.toLowerCase() === spec.file_path?.toLowerCase()
          );
          if (!matchedByTitle) return false;
        }
      } else if (searchQuery) {
        // Fallback to local search for short queries
        const titleMatch = spec.title?.toLowerCase().includes(searchQuery);
        if (!titleMatch) return false;
      }

      if (statusFilter === 'all') return true;
      if (spec.results.length === 0) return false;

      // Check for flaky: passed eventually but had at least one failure
      const hasFailure = spec.results.some((r) => r.status === 'failed');
      const hasPassed = spec.results.some((r) => r.status === 'passed');
      const isFlaky = spec.ok && hasFailure && hasPassed;

      // Get the final result (highest retry number)
      const finalResult = spec.results.reduce((latest, r) =>
        r.retry > (latest?.retry ?? -1) ? r : latest,
      );

      switch (statusFilter) {
        case 'passed':
          // All specs that ultimately passed (including flaky)
          return spec.ok;
        case 'failed':
          return !spec.ok && finalResult?.status !== 'skipped';
        case 'flaky':
          return isFlaky;
        case 'skipped':
          return finalResult?.status === 'skipped';
        default:
          return true;
      }
    }) || [];

  // Status icon based on suite state (priority: failed > flaky > skipped-only > passed)
  const StatusIcon = hasFailed
    ? XCircle
    : hasFlaky
      ? AlertTriangle
      : isSkippedOnly
        ? MinusCircle
        : CheckCircle2;
  const statusIconColor = hasFailed
    ? 'text-red-500'
    : hasFlaky
      ? 'text-yellow-500'
      : isSkippedOnly
        ? 'text-gray-400'
        : 'text-green-500';

  return (
    <div
      className={`-mx-2 px-2 rounded-lg transition-colors ${showExpanded ? 'bg-blue-50 dark:bg-blue-900/20' : ''}`}
    >
      <button
        type="button"
        onClick={onToggle}
        className={`w-full cursor-pointer py-2.5 text-left transition-colors ${
          showExpanded
            ? 'hover:bg-blue-100/50 dark:hover:bg-blue-900/30'
            : 'hover:bg-gray-50 dark:hover:bg-gray-700/50'
        }`}
      >
        <div className="flex items-center justify-between">
          <div className="flex min-w-0 items-center gap-2">
            <span className="w-6 text-xs text-gray-400 dark:text-gray-500 text-right flex-shrink-0">
              {rowNumber}
            </span>
            {isLoading ? (
              <Loader2 className="h-4 w-4 flex-shrink-0 text-blue-500 animate-spin" />
            ) : (
              <ChevronRight
                className={`h-4 w-4 flex-shrink-0 text-gray-400 transition-transform dark:text-gray-500 ${
                  showExpanded ? 'rotate-90' : ''
                }`}
              />
            )}
            <StatusIcon className={`h-4 w-4 flex-shrink-0 ${statusIconColor}`} />
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-gray-900 dark:text-white flex items-center gap-1.5">
                <FileCode className="h-3.5 w-3.5 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                <span className="truncate">
                  {suite.file_path ? (
                    <HighlightText text={suite.file_path} search={searchQuery} />
                  ) : (
                    <span className="text-red-500 italic">Missing file path</span>
                  )}
                </span>
                {suite.job_number !== undefined && hasMultipleJobs && (
                  <span
                    className="ml-1 inline-flex h-4 w-4 items-center justify-center rounded bg-gray-200 text-[10px] font-semibold text-gray-600 dark:bg-gray-600 dark:text-gray-300 flex-shrink-0"
                    title={suite.job_name || `Job ${suite.job_number}`}
                  >
                    {suite.job_number}
                  </span>
                )}
              </p>
              {suite.title !== suite.file_path && (
                <p className="truncate text-xs text-gray-500 dark:text-gray-400">
                  <HighlightText text={suite.title} search={searchQuery} />
                </p>
              )}
            </div>
          </div>
          <div className="flex flex-shrink-0 items-center gap-3 text-xs">
            <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-400">
              <Clock className="h-3 w-3" />
              {formatDuration(suite.duration_ms || 0)}
            </span>
            <span className="text-gray-600 dark:text-gray-300">{suite.specs_count} specs</span>
            {suite.passed_count > 0 && (
              <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
                <CheckCircle2 className="h-3 w-3" />
                {suite.passed_count}
              </span>
            )}
            {hasFlaky && (
              <span className="inline-flex items-center gap-1 text-yellow-600 dark:text-yellow-400">
                <AlertTriangle className="h-3 w-3" />
                {suite.flaky_count}
              </span>
            )}
            {hasFailed && (
              <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
                <XCircle className="h-3 w-3" />
                {suite.failed_count}
              </span>
            )}
            {hasSkipped && (
              <span className="inline-flex items-center gap-1 text-gray-400 dark:text-gray-500">
                <MinusCircle className="h-3 w-3" />
                {suite.skipped_count}
              </span>
            )}
          </div>
        </div>
      </button>

      {/* Expanded specs list - only show when data is ready */}
      {showExpanded && (
        <div className="mb-3 ml-6 border-l-2 border-gray-200 pl-4 dark:border-gray-600">
          {filteredSpecs.length > 0 ? (
            <div className="space-y-2 py-2">
              {filteredSpecs.map((spec, specIndex) => (
                <SpecRow
                  key={spec.id}
                  spec={spec}
                  rowLabel={`${rowNumber}.${specIndex + 1}`}
                  searchQuery={searchQuery}
                />
              ))}
            </div>
          ) : (
            <p className="py-2 text-sm text-gray-500 dark:text-gray-400">
              {statusFilter === 'all' ? 'No specs found' : `No ${statusFilter} specs`}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

interface SpecRowProps {
  spec: TestSpec;
  rowLabel: string;
  searchQuery: string;
}

function SpecRow({ spec, rowLabel, searchQuery }: SpecRowProps) {
  const latestResult = spec.results[spec.results.length - 1];

  // Determine status icon based on actual status
  const isSkipped = latestResult?.status === 'skipped';
  const latestPassed = latestResult?.status === 'passed';
  const hadFailedAttempt = spec.results.some((r) => r.status === 'failed');
  // Flaky conditions:
  // 1. Has multiple attempts with at least one failure and eventually passed
  // 2. spec.ok is true (server says passed) but we have a failed result (retry data may be incomplete)
  const isFlaky = (spec.ok && hadFailedAttempt) || (latestPassed && hadFailedAttempt);

  let StatusIcon = CheckCircle2;
  let statusColor = 'text-green-500';

  if (isSkipped) {
    StatusIcon = MinusCircle;
    statusColor = 'text-gray-400';
  } else if (isFlaky) {
    // Check flaky BEFORE failed - flaky tests should show warning, not error
    StatusIcon = AlertTriangle;
    statusColor = 'text-yellow-500';
  } else if (!spec.ok) {
    StatusIcon = XCircle;
    statusColor = 'text-red-500';
  }

  // Show individual attempts for flaky tests (multiple results)
  const hasMultipleAttempts = spec.results.length > 1;

  // Check if single-attempt test has attachments or errors to display
  const singleResultHasContent =
    !hasMultipleAttempts &&
    latestResult &&
    (latestResult.errors_json ||
      (latestResult.attachments && latestResult.attachments.length > 0));

  // Determine if this spec has expandable content (failed, flaky, or skipped with details)
  const hasExpandableContent =
    hasMultipleAttempts ||
    singleResultHasContent ||
    (spec.screenshots && spec.screenshots.length > 0);

  // Only make expandable if not passed (failed, flaky, or skipped)
  const isExpandable = hasExpandableContent && (!spec.ok || isFlaky || isSkipped);

  const [isExpanded, setIsExpanded] = useState(false);

  const handleToggle = () => {
    if (isExpandable) {
      setIsExpanded(!isExpanded);
    }
  };

  const ExpandIcon = isExpanded ? ChevronDown : ChevronRight;

  return (
    <div className="text-sm">
      <div
        className={`flex items-center gap-2 py-1 ${isExpandable ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800 rounded -mx-1 px-1' : ''}`}
        onClick={handleToggle}
        role={isExpandable ? 'button' : undefined}
        tabIndex={isExpandable ? 0 : undefined}
        onKeyDown={isExpandable ? (e) => e.key === 'Enter' && handleToggle() : undefined}
      >
        {isExpandable ? (
          <ExpandIcon className="h-3.5 w-3.5 flex-shrink-0 text-gray-400 dark:text-gray-500" />
        ) : (
          <span className="w-3.5 flex-shrink-0" />
        )}
        <span className="w-10 text-xs font-medium text-gray-400 dark:text-gray-500 flex-shrink-0 text-right">
          {rowLabel}
        </span>
        <StatusIcon className={`h-3.5 w-3.5 flex-shrink-0 ${statusColor}`} />
        <span className="flex-1 truncate text-gray-900 dark:text-gray-100">
          <HighlightText text={spec.title} search={searchQuery} />
        </span>
        {latestResult && !hasMultipleAttempts && (
          <>
            {latestResult.project_name && latestResult.project_name !== 'default' && (
              <span className="text-xs text-gray-600 dark:text-gray-400">
                {latestResult.project_name}
              </span>
            )}
            <span className="inline-flex items-center gap-1 text-xs text-gray-500 dark:text-gray-500">
              <Clock className="h-3 w-3" />
              {formatDuration(latestResult.duration_ms)}
            </span>
          </>
        )}
        {hasMultipleAttempts && (
          <span className="inline-flex items-center gap-1 text-xs text-orange-600 dark:text-orange-400">
            <RotateCcw className="h-3 w-3" />
            {spec.results.length} attempts
          </span>
        )}
      </div>
      {/* Show all attempts for flaky tests with inline errors */}
      {isExpanded && hasMultipleAttempts && (
        <div className="ml-16 mt-1 space-y-2 border-l-2 border-gray-200 pl-3 dark:border-gray-600">
          {spec.results.map((result, idx) => {
            // 'flaky' status means this attempt passed (after retries)
            const isPassed = result.status === 'passed' || result.status === 'flaky';
            const isSkipped = result.status === 'skipped';
            const AttemptIcon = isSkipped ? MinusCircle : isPassed ? CheckCircle2 : XCircle;
            const attemptColor = isSkipped
              ? 'text-gray-400'
              : isPassed
                ? 'text-green-500'
                : 'text-red-500';
            const statusLabel = isPassed ? 'passed' : isSkipped ? 'skipped' : 'failed';
            return (
              <div key={idx} className="space-y-1">
                <div className="flex items-center gap-2 text-xs">
                  <AttemptIcon className={`h-3 w-3 flex-shrink-0 ${attemptColor}`} />
                  <span className="font-medium text-gray-700 dark:text-gray-300">
                    Attempt {result.retry + 1}
                  </span>
                  <span className={`text-xs ${attemptColor}`}>({statusLabel})</span>
                  <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-500">
                    <Clock className="h-3 w-3" />
                    {formatDuration(result.duration_ms)}
                  </span>
                  {result.project_name && result.project_name !== 'default' && (
                    <span className="text-gray-600 dark:text-gray-400">
                      {result.project_name}
                    </span>
                  )}
                </div>
                {/* Inline error display for this attempt */}
                {result.errors_json && (
                  <InlineErrorDisplay errorsJson={result.errors_json} />
                )}
                {/* Attachments (screenshots) for this attempt */}
                <AttachmentsDisplay attachments={result.attachments} />
              </div>
            );
          })}
        </div>
      )}
      {/* Show errors and attachments for single-attempt tests */}
      {isExpanded && singleResultHasContent && (
        <div className="ml-16 mt-1 space-y-2">
          {latestResult.errors_json && (
            <InlineErrorDisplay errorsJson={latestResult.errors_json} />
          )}
          <AttachmentsDisplay attachments={latestResult.attachments} />
        </div>
      )}
      {isExpanded && spec.screenshots && spec.screenshots.length > 0 && (
        <div className="ml-[5.25rem] mt-2">
          <p className="text-xs text-gray-500 dark:text-gray-400 mb-1">
            Screenshots ({spec.screenshots.length})
          </p>
          <ScreenshotGallery
            screenshots={spec.screenshots.map((screenshot, idx) => ({
              path: screenshot.file_path,
              s3_key: screenshot.file_path,
              content_type: 'image/png',
              retry: 0,
              missing: false,
              sequence: idx,
            }))}
          />
        </div>
      )}
    </div>
  );
}

interface ErrorInfo {
  message?: string;
  estack?: string;
  diff?: string | null;
}

/** Compact inline error display for individual attempt errors */
function InlineErrorDisplay({ errorsJson }: { errorsJson: string }) {
  let errorText = '';

  try {
    const parsed = JSON.parse(errorsJson);
    if (Array.isArray(parsed) && parsed.length > 0) {
      if (typeof parsed[0] === 'string') {
        // Jest-stare/Detox/Playwright JUnit format: array of full error strings
        errorText = parsed.join('\n\n');
      } else {
        // Playwright format: array of error objects
        errorText = parsed
          .map((e: ErrorInfo) => {
            const parts = [e.message];
            if (e.estack) parts.push(e.estack);
            return parts.join('\n');
          })
          .join('\n\n');
      }
    } else if (parsed && typeof parsed === 'object' && parsed.message) {
      // Cypress format: single error object
      const parts = [parsed.message];
      if (parsed.estack) parts.push(parsed.estack);
      errorText = parts.join('\n');
    }
  } catch {
    // Invalid JSON
  }

  if (!errorText) return null;

  return (
    <div className="ml-5 rounded border border-red-200 bg-gray-900 dark:border-red-800 overflow-hidden">
      <pre className="p-3 overflow-x-auto text-xs font-mono text-gray-100 whitespace-pre-wrap">
        {errorText}
      </pre>
    </div>
  );
}

/** Display attachments (screenshots) for a test result */
function AttachmentsDisplay({ attachments }: { attachments?: TestAttachment[] }) {
  if (!attachments || attachments.length === 0) return null;

  // Filter to only show image attachments that have s3_key (found in storage)
  const imageAttachments = attachments.filter(
    (a) => a.content_type?.startsWith('image/') && a.s3_key && !a.missing
  );

  if (imageAttachments.length === 0) return null;

  // Sort by sequence to preserve original JUnit XML order
  const sortedAttachments = [...imageAttachments].sort((a, b) => a.sequence - b.sequence);

  return (
    <div className="ml-5 mt-2">
      <p className="text-xs text-gray-500 dark:text-gray-400 mb-1">
        Screenshots ({sortedAttachments.length})
      </p>
      <ScreenshotGallery screenshots={sortedAttachments} />
    </div>
  );
}

type StatVariant = 'default' | 'success' | 'error' | 'warning' | 'muted';

interface StatPillProps {
  label: string;
  value: number;
  variant: StatVariant;
  isActive: boolean;
  onClick: () => void;
}

function StatPill({ label, value, variant, isActive, onClick }: StatPillProps) {
  const variants: Record<StatVariant, string> = {
    default: 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-200',
    success: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-400',
    error: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400',
    warning: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-400',
    muted: 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400',
  };

  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium transition-colors ${variants[variant]} ${
        isActive
          ? 'ring-1 ring-blue-500 ring-offset-1 dark:ring-offset-gray-800'
          : 'hover:opacity-80'
      }`}
    >
      <span className="font-semibold">{value}</span>
      <span className="opacity-70">{label}</span>
    </button>
  );
}

function ProgressBar({ stats }: { stats: ReportStats }) {
  const total = stats.expected + stats.unexpected + stats.flaky + stats.skipped;
  if (total === 0) return <div className="h-2 rounded-full bg-gray-200 dark:bg-gray-700" />;

  return (
    <div className="flex h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
      {stats.expected > 0 && (
        <div
          className="h-full bg-green-500"
          style={{ width: `${(stats.expected / total) * 100}%` }}
        />
      )}
      {stats.flaky > 0 && (
        <div
          className="h-full bg-yellow-500"
          style={{ width: `${(stats.flaky / total) * 100}%` }}
        />
      )}
      {stats.unexpected > 0 && (
        <div
          className="h-full bg-red-500"
          style={{ width: `${(stats.unexpected / total) * 100}%` }}
        />
      )}
      {stats.skipped > 0 && (
        <div
          className="h-full bg-gray-400 dark:bg-gray-500"
          style={{ width: `${(stats.skipped / total) * 100}%` }}
        />
      )}
    </div>
  );
}

function calcPassRate(stats: ReportStats): string {
  // Pass rate excludes skipped tests: (passed + flaky) / (passed + flaky + failed)
  const countedTotal = stats.expected + stats.flaky + stats.unexpected;
  if (countedTotal === 0) return '0';
  return (((stats.expected + stats.flaky) / countedTotal) * 100).toFixed(1);
}

function formatDuration(ms: number): string {
  const totalSeconds = ms / 1000;
  const minutes = Math.floor(totalSeconds / 60);
  const hours = Math.floor(minutes / 60);

  if (hours > 0) {
    return `${hours}h ${minutes % 60}m ${Math.floor(totalSeconds % 60)}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${Math.floor(totalSeconds % 60)}s`;
  }
  return `${totalSeconds.toFixed(2)}s`;
}

/** Highlight matching text in a string */
function HighlightText({ text, search }: { text: string; search: string }) {
  if (!search || !text) {
    return <>{text}</>;
  }

  const lowerText = text.toLowerCase();
  const lowerSearch = search.toLowerCase();
  const index = lowerText.indexOf(lowerSearch);

  if (index === -1) {
    return <>{text}</>;
  }

  const before = text.slice(0, index);
  const match = text.slice(index, index + search.length);
  const after = text.slice(index + search.length);

  return (
    <>
      {before}
      <mark className="bg-yellow-200 text-yellow-900 dark:bg-yellow-500/40 dark:text-yellow-100">
        {match}
      </mark>
      {after && <HighlightText text={after} search={search} />}
    </>
  );
}
