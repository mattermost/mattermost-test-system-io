import { useState, useEffect, useRef, useMemo, useCallback, memo } from 'react';
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
import type { TestSuite, ReportStats, TestSpec, TestSpecListResponse, JobInfo } from '../types';
import { ScreenshotGallery } from './ui/screenshot-gallery';
import { useSearchTestCases, useClientConfig, type SearchSuiteResult } from '../services/api';
import {
  StatPill,
  ProgressBar,
  HighlightText,
  InlineErrorDisplay,
  AttachmentsDisplay,
  calcPassRate,
  formatDuration,
  type StatusFilter,
} from './test_suites';

const API_BASE = '/api/v1';
const SEARCH_DEBOUNCE_MS = 500; // 500ms debounce for both client and API search

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
  const [effectiveSearch, setEffectiveSearch] = useState(''); // Search ready for rendering
  const jobDropdownRef = useRef<HTMLDivElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);

  // Get client config for min_search_length
  const { data: clientConfig } = useClientConfig();
  const minSearchLength = clientConfig?.min_search_length ?? 2;

  // Single debounce for both client-side filtering and API calls (500ms)
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

  // Update effectiveSearch only when ready to render:
  // - Immediately for searches below minSearchLength (client-side only)
  // - After API returns for searches >= minSearchLength (consolidated render)
  useEffect(() => {
    const needsApiSearch = debouncedSearch.length >= minSearchLength;

    if (!needsApiSearch) {
      // Below min length - client-side filtering only, update immediately
      setEffectiveSearch(debouncedSearch);
    } else if (!isSearching) {
      // API search complete - safe to update for consolidated render
      setEffectiveSearch(debouncedSearch);
    }
  }, [debouncedSearch, minSearchLength, isSearching]);

  // Build a map of suite_id -> SearchSuiteResult from API response
  const searchResultsBySuite = useMemo(() => {
    if (!searchData?.results) return new Map<string, SearchSuiteResult>();
    const map = new Map<string, SearchSuiteResult>();
    for (const suiteResult of searchData.results) {
      map.set(suiteResult.suite_id, suiteResult);
    }
    return map;
  }, [searchData?.results]);

  // Check if we have active API search results (use effectiveSearch for consistency)
  const hasApiSearchResults = effectiveSearch.length >= minSearchLength && searchData?.results && searchData.results.length > 0;

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

  const handleSuiteClick = useCallback((suiteId: number) => {
    setExpandedSuiteIds((prev) => {
      const next = new Set(prev);
      if (next.has(suiteId)) {
        next.delete(suiteId);
      } else {
        next.add(suiteId);
      }
      return next;
    });
  }, []);

  // Normalize search query for case-insensitive client-side matching
  // Uses effectiveSearch which only updates when API is ready (single render)
  const normalizedSearch = useMemo(
    () => effectiveSearch.toLowerCase(),
    [effectiveSearch]
  );

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
  const { totals, totalTests } = useMemo(() => {
    const suitesForTotals = selectedJobs.size > 0
      ? suites.filter((s) => s.job_id && selectedJobs.has(s.job_id))
      : suites;
    const calculated = suitesForTotals.reduce(
      (acc, suite) => ({
        passed: acc.passed + (suite.passed_count ?? 0),
        failed: acc.failed + (suite.failed_count ?? 0),
        flaky: acc.flaky + (suite.flaky_count ?? 0),
        skipped: acc.skipped + (suite.skipped_count ?? 0),
      }),
      { passed: 0, failed: 0, flaky: 0, skipped: 0 }
    );
    return {
      totals: calculated,
      totalTests: calculated.passed + calculated.failed + calculated.flaky + calculated.skipped,
    };
  }, [suites, selectedJobs]);

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
                  setEffectiveSearch('');
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
            {filteredSuites.map((suite, index) => {
              // Check if suite itself matched by title/file_path (vs matched by API test cases)
              const suiteMatchedByPath = normalizedSearch
                ? (suite.title?.toLowerCase().includes(normalizedSearch) ||
                   suite.file_path?.toLowerCase().includes(normalizedSearch))
                : false;
              return (
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
                  suiteMatchedByPath={suiteMatchedByPath}
                />
              );
            })}
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
  /** True if suite matched by its own title/file_path, false if matched only by API test cases */
  suiteMatchedByPath: boolean;
}

const LOADING_DELAY_MS = 1000;

const SuiteRow = memo(function SuiteRow({
  suite,
  reportId,
  isExpanded,
  onToggle,
  statusFilter,
  rowNumber,
  hasMultipleJobs,
  searchQuery,
  suiteMatchedByPath,
}: SuiteRowProps) {
  // Memoize status calculations
  const { hasFlaky, hasFailed, hasSkipped, StatusIcon, statusIconColor } = useMemo(() => {
    const flaky = (suite.flaky_count ?? 0) > 0;
    const failed = suite.failed_count > 0;
    const skipped = (suite.skipped_count ?? 0) > 0;
    const passed = suite.passed_count > 0;
    const skippedOnly = skipped && !passed && !failed && !flaky;

    // Status icon based on suite state (priority: failed > flaky > skipped-only > passed)
    const Icon = failed
      ? XCircle
      : flaky
        ? AlertTriangle
        : skippedOnly
          ? MinusCircle
          : CheckCircle2;
    const iconColor = failed
      ? 'text-red-500'
      : flaky
        ? 'text-yellow-500'
        : skippedOnly
          ? 'text-gray-400'
          : 'text-green-500';

    return {
      hasFlaky: flaky,
      hasFailed: failed,
      hasSkipped: skipped,
      StatusIcon: Icon,
      statusIconColor: iconColor,
    };
  }, [suite.flaky_count, suite.failed_count, suite.skipped_count, suite.passed_count]);

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

  // Delay showing loader by 1 second - if data arrives faster, skip the loader entirely
  const [showLoader, setShowLoader] = useState(false);
  useEffect(() => {
    if (isLoading) {
      const timer = setTimeout(() => setShowLoader(true), LOADING_DELAY_MS);
      return () => clearTimeout(timer);
    } else {
      setShowLoader(false);
    }
  }, [isLoading]);

  // Only show expanded content when data is ready (not loading)
  const showExpanded = isExpanded && isFetched && !isLoading;

  // Filter specs based on status filter and search query - memoized
  const filteredSpecs = useMemo(() => {
    if (!specsData?.specs) return [];

    return specsData.specs.filter((spec) => {
      // Search filter - only filter specs if suite was matched by API test cases
      // If suite matched by its own title/file_path, show all specs in that suite
      if (searchQuery && !suiteMatchedByPath) {
        const specTitleLower = spec.title?.toLowerCase() || '';
        if (!specTitleLower.includes(searchQuery)) {
          return false;
        }
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
    });
  }, [specsData?.specs, searchQuery, suiteMatchedByPath, statusFilter]);

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
            {showLoader ? (
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
});

interface SpecRowProps {
  spec: TestSpec;
  rowLabel: string;
  searchQuery: string;
}

const SpecRow = memo(function SpecRow({ spec, rowLabel, searchQuery }: SpecRowProps) {
  const [isExpanded, setIsExpanded] = useState(false);

  // Memoize all derived status values
  const {
    latestResult,
    StatusIcon,
    statusColor,
    hasMultipleAttempts,
    singleResultHasContent,
    isExpandable,
  } = useMemo(() => {
    const latest = spec.results[spec.results.length - 1];
    const skipped = latest?.status === 'skipped';
    const latestPassed = latest?.status === 'passed';
    const hadFailedAttempt = spec.results.some((r) => r.status === 'failed');
    const flaky = (spec.ok && hadFailedAttempt) || (latestPassed && hadFailedAttempt);

    let Icon = CheckCircle2;
    let color = 'text-green-500';

    if (skipped) {
      Icon = MinusCircle;
      color = 'text-gray-400';
    } else if (flaky) {
      Icon = AlertTriangle;
      color = 'text-yellow-500';
    } else if (!spec.ok) {
      Icon = XCircle;
      color = 'text-red-500';
    }

    const multipleAttempts = spec.results.length > 1;
    const singleHasContent =
      !multipleAttempts &&
      latest &&
      (latest.errors_json ||
        (latest.attachments && latest.attachments.length > 0));
    const hasExpandable =
      multipleAttempts ||
      singleHasContent ||
      (spec.screenshots && spec.screenshots.length > 0);
    const expandable = hasExpandable && (!spec.ok || flaky || skipped);

    return {
      latestResult: latest,
      StatusIcon: Icon,
      statusColor: color,
      hasMultipleAttempts: multipleAttempts,
      singleResultHasContent: singleHasContent,
      isExpandable: expandable,
    };
  }, [spec]);

  const handleToggle = useCallback(() => {
    if (isExpandable) {
      setIsExpanded((prev) => !prev);
    }
  }, [isExpandable]);

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
      {isExpanded && singleResultHasContent && latestResult && (
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
});
