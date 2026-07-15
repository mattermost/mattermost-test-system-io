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
import type {
  TestSuite,
  ReportStats,
  TestSpec,
  TestSpecListResponse,
  ReportEntryInfo,
  CrossShardAttempt,
} from '@/types';
import type { Divergence, SnapshotUnit } from '@/types/orchestration';
import { PaginationBar } from '@/components/ui/pagination_bar';
import { ScreenshotGallery } from '@/components/ui/screenshot-gallery';
import { useSearchTestCases, useClientConfig, type SearchSuiteResult } from '@/services/api';
import { DivergenceBadge } from '@/components/orchestration/divergence_badge';
import {
  StatPill,
  ProgressBar,
  HighlightText,
  InlineErrorDisplay,
  AttachmentsDisplay,
  calcPassRate,
  formatDuration,
  workerSlot,
  type StatusFilter,
} from './test_suites';

const API_BASE = '/api/v1';
const SEARCH_DEBOUNCE_MS = 500; // 500ms debounce for both client and API search
const PAGE_SIZE = 100;

interface TestSuitesViewProps {
  reportId: string;
  suites: TestSuite[];
  stats?: ReportStats;
  title?: string;
  reports?: ReportEntryInfo[];
  /**
   * Per-spec list of attempts across every shard that ran the same test
   * title, keyed by `full_title` = `${suite.title} › ${spec.title}`.
   * Populated from the consolidated view; omitted in single-report contexts.
   */
  crossShardHistory?: Map<string, CrossShardAttempt[]>;
  /**
   * Per-spec disagreements between the orchestration view and the canonical
   * artifact view, keyed by `spec.file_path`. Populated only when both data
   * sources are available; the badge renders next to any matching spec row.
   */
  divergencesBySpecPath?: Map<string, Divergence>;
  /**
   * Optional orchestration snapshot units, used solely to display the
   * canonical repo-root-relative spec path (e.g. `tests/login.spec.ts`)
   * instead of the framework's `testDir`-relative `file_path`. When the
   * lookup misses, `file_path` is rendered as-is.
   */
  orchestrationUnits?: SnapshotUnit[];
}

export function TestSuitesView({
  reportId,
  suites,
  stats,
  title,
  reports,
  crossShardHistory,
  divergencesBySpecPath,
  orchestrationUnits,
}: TestSuitesViewProps) {
  const [expandedSuiteIds, setExpandedSuiteIds] = useState<Set<number>>(new Set());
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selectedReports, setSelectedReports] = useState<Set<string>>(new Set()); // empty = all reports
  const [reportDropdownOpen, setReportDropdownOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [effectiveSearch, setEffectiveSearch] = useState(''); // Search ready for rendering
  const [currentPage, setCurrentPage] = useState(1);
  const reportDropdownRef = useRef<HTMLDivElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);

  // Get client config for search_min_length
  const { data: clientConfig } = useClientConfig();
  const minSearchLength = clientConfig?.search_min_length ?? 2;

  // Map a suite's framework-reported file_path to the orchestration's
  // canonical repo-root-relative spec_path. Match priorities: exact
  // equality, then suite_path that ends with `/<file_path>` (the
  // common testDir-relative case), then falls back to no override.
  const canonicalSpecPathByFilePath = useMemo(() => {
    const out = new Map<string, string>();
    if (!orchestrationUnits || orchestrationUnits.length === 0) return out;
    const unitPaths = orchestrationUnits.map((u) => u.spec_path).filter((p): p is string => !!p);
    for (const suite of suites) {
      const fp = suite.file_path;
      if (!fp || out.has(fp)) continue;
      const exact = unitPaths.find((p) => p === fp);
      if (exact) {
        out.set(fp, exact);
        continue;
      }
      const suffix = unitPaths.find((p) => p.endsWith('/' + fp));
      if (suffix) out.set(fp, suffix);
    }
    return out;
  }, [orchestrationUnits, suites]);

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
    500, // Get more results for better grouping
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
  const hasApiSearchResults =
    effectiveSearch.length >= minSearchLength &&
    searchData?.results &&
    searchData.results.length > 0;

  // Reset to page 1 when filters change
  useEffect(() => {
    setCurrentPage(1);
  }, [statusFilter, selectedReports, effectiveSearch]);

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (reportDropdownRef.current && !reportDropdownRef.current.contains(event.target as Node)) {
        setReportDropdownOpen(false);
      }
    };

    if (reportDropdownOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [reportDropdownOpen]);

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
  const normalizedSearch = useMemo(() => effectiveSearch.toLowerCase(), [effectiveSearch]);

  // Step 1: Base filter (empty suites, report filter, search) + sort + deduplicate
  // Dedup happens BEFORE status filter so the latest result wins (e.g., a retest pass
  // replaces an earlier failure, and the failed entry won't appear in "failed" filter).
  const deduplicatedSuites = useMemo(() => {
    const sorted = suites
      .filter((suite) => {
        // Skip empty suites (no specs extracted)
        if (suite.tests_count === 0) return false;

        // Report filter
        if (selectedReports.size > 0 && suite.report_id && !selectedReports.has(suite.report_id)) {
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

        return true;
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

    // Deduplicate by file_path: keep the latest entry (last created wins)
    if (reports && reports.length > 1) {
      const seen = new Map<string, number>();
      // Walk in reverse so later entries (latest) overwrite earlier ones
      for (let i = sorted.length - 1; i >= 0; i--) {
        const fp = sorted[i]!.file_path;
        if (fp && !seen.has(fp)) {
          seen.set(fp, i);
        }
      }
      return sorted.filter((suite, i) => {
        if (!suite.file_path) return true;
        return seen.get(suite.file_path) === i;
      });
    }

    return sorted;
  }, [
    suites,
    selectedReports,
    normalizedSearch,
    hasApiSearchResults,
    searchResultsBySuite,
    reports,
  ]);

  // Step 2: Apply status filter on deduplicated suites
  const filteredSuites = useMemo(() => {
    if (statusFilter === 'all') return deduplicatedSuites;
    return deduplicatedSuites.filter((suite) => {
      switch (statusFilter) {
        // Suite-file-level: the suite's overall outcome equals the chip
        case 'spec_passed':
          return (
            (suite.failed_count ?? 0) === 0 &&
            (suite.passed_count ?? 0) + (suite.flaky_count ?? 0) > 0
          );
        case 'spec_failed':
          return (suite.failed_count ?? 0) > 0;
        // Test-case-level: at least one test in the suite has the status
        case 'test_passed':
          return (suite.passed_count ?? 0) > 0;
        case 'test_failed':
          return (suite.failed_count ?? 0) > 0;
        case 'test_flaky':
          return (suite.flaky_count ?? 0) > 0;
        case 'test_skipped':
          return (suite.skipped_count ?? 0) > 0;
        default:
          return true;
      }
    });
  }, [deduplicatedSuites, statusFilter]);

  // Step 3: Paginate filtered suites
  const totalPages = Math.ceil(filteredSuites.length / PAGE_SIZE);
  const paginatedSuites = useMemo(() => {
    const start = (currentPage - 1) * PAGE_SIZE;
    return filteredSuites.slice(start, start + PAGE_SIZE);
  }, [filteredSuites, currentPage]);

  // Toggle report selection
  const toggleReport = (reportId: string) => {
    setSelectedReports((prev) => {
      const next = new Set(prev);
      if (next.has(reportId)) {
        next.delete(reportId);
      } else {
        next.add(reportId);
      }
      return next;
    });
  };

  // Select all reports (clear selection = show all)
  const selectAllReports = () => {
    setSelectedReports(new Set());
    setReportDropdownOpen(false);
  };

  // Calculate totals from deduplicated suites (latest results only)
  const { totals, totalTests } = useMemo(() => {
    const calculated = deduplicatedSuites.reduce(
      (acc, suite) => ({
        passed: acc.passed + (suite.passed_count ?? 0),
        failed: acc.failed + (suite.failed_count ?? 0),
        flaky: acc.flaky + (suite.flaky_count ?? 0),
        skipped: acc.skipped + (suite.skipped_count ?? 0),
      }),
      { passed: 0, failed: 0, flaky: 0, skipped: 0 },
    );
    return {
      totals: calculated,
      totalTests: calculated.passed + calculated.failed + calculated.flaky + calculated.skipped,
    };
  }, [deduplicatedSuites]);

  // Suite-file-level pass/fail counts for the title-bar chips. A suite is
  // considered passed when none of its tests failed (flaky still counts
  // as passed, mirroring the run-level rule). Used by the chips next to
  // the "Test Suites (N)" title to filter at the suite level.
  const { specPassed, specFailed } = useMemo(() => {
    let passed = 0;
    let failed = 0;
    for (const s of deduplicatedSuites) {
      if ((s.failed_count ?? 0) > 0) failed++;
      else if ((s.passed_count ?? 0) + (s.flaky_count ?? 0) > 0) passed++;
    }
    return { specPassed: passed, specFailed: failed };
  }, [deduplicatedSuites]);

  // Build map: file_path -> list of report badge info (ordered by created_at) for all reports that tested this file
  type ReportBadge = {
    report_number: number;
    report_name: string;
    passed: boolean;
    created_at: string;
  };
  const filePathReportsMap = useMemo(() => {
    if (!reports || reports.length <= 1) return new Map<string, ReportBadge[]>();
    const map = new Map<string, ReportBadge[]>();
    for (const suite of suites) {
      if (!suite.file_path || suite.tests_count === 0) continue;
      const entry: ReportBadge = {
        report_number: suite.report_number ?? 0,
        report_name: suite.report_name || '',
        passed: suite.failed_count === 0,
        created_at: suite.created_at || '',
      };
      const existing = map.get(suite.file_path);
      if (existing) {
        if (!existing.some((e) => e.report_number === entry.report_number)) {
          existing.push(entry);
        }
      } else {
        map.set(suite.file_path, [entry]);
      }
    }
    // Sort each entry by created_at (chronological order)
    for (const entries of map.values()) {
      entries.sort((a, b) => (a.created_at || '').localeCompare(b.created_at || ''));
    }
    return map;
  }, [suites, reports]);

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
                isActive={statusFilter === 'test_passed'}
                onClick={() => setStatusFilter('test_passed')}
              />
              {stats.unexpected > 0 && (
                <StatPill
                  label="Failed"
                  value={stats.unexpected}
                  variant="error"
                  isActive={statusFilter === 'test_failed'}
                  onClick={() => setStatusFilter('test_failed')}
                />
              )}
              {stats.flaky > 0 && (
                <StatPill
                  label="Flaky"
                  value={stats.flaky}
                  variant="warning"
                  isActive={statusFilter === 'test_flaky'}
                  onClick={() => setStatusFilter('test_flaky')}
                />
              )}
              {stats.skipped > 0 && (
                <StatPill
                  label="Skipped"
                  value={stats.skipped}
                  variant="muted"
                  isActive={statusFilter === 'test_skipped'}
                  onClick={() => setStatusFilter('test_skipped')}
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
          {/* Section 1: Title + suite-level chips */}
          <h3 className="flex flex-shrink-0 items-center gap-2 text-sm font-medium text-gray-900 dark:text-white">
            <button
              type="button"
              onClick={() => {
                setStatusFilter('all');
                setSearchQuery('');
                setDebouncedSearch('');
                setEffectiveSearch('');
              }}
              title="Show all suites"
              className={`cursor-pointer rounded px-1 py-0.5 transition-colors hover:bg-gray-100 dark:hover:bg-gray-700 ${
                statusFilter === 'all' && !normalizedSearch ? 'bg-gray-200 dark:bg-gray-600' : ''
              }`}
            >
              {deduplicatedSuites.length} {deduplicatedSuites.length === 1 ? 'spec' : 'specs'}
            </button>
            {specPassed > 0 && (
              <button
                type="button"
                onClick={() =>
                  setStatusFilter(statusFilter === 'spec_passed' ? 'all' : 'spec_passed')
                }
                title="Filter passed suites"
                className={`inline-flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-xs text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20 ${
                  statusFilter === 'spec_passed' ? 'bg-green-100 dark:bg-green-900/40' : ''
                }`}
              >
                <CheckCircle2 className="h-3 w-3" />
                {specPassed}
              </button>
            )}
            {specFailed > 0 && (
              <button
                type="button"
                onClick={() =>
                  setStatusFilter(statusFilter === 'spec_failed' ? 'all' : 'spec_failed')
                }
                title="Filter failed suites"
                className={`inline-flex cursor-pointer items-center gap-1 rounded px-1.5 py-0.5 text-xs text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20 ${
                  statusFilter === 'spec_failed' ? 'bg-red-100 dark:bg-red-900/40' : ''
                }`}
              >
                <XCircle className="h-3 w-3" />
                {specFailed}
              </button>
            )}
          </h3>

          {/* Spacer */}
          <div className="flex-1" />

          {/* Section 2: Test-case-level pills + report dropdown */}
          <div className="flex flex-shrink-0 items-center gap-2">
            {/* Status filter buttons */}
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => setStatusFilter('all')}
                className={`cursor-pointer rounded px-2 py-0.5 text-sm font-medium transition-colors ${
                  statusFilter === 'all'
                    ? 'bg-gray-200 text-gray-900 dark:bg-gray-600 dark:text-white'
                    : 'text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700'
                }`}
              >
                {totalTests} {totalTests === 1 ? 'test' : 'tests'}
              </button>
              <button
                type="button"
                onClick={() => setStatusFilter('test_passed')}
                className={`cursor-pointer rounded px-2 py-0.5 text-xs transition-colors ${
                  statusFilter === 'test_passed'
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
                  onClick={() => setStatusFilter('test_failed')}
                  className={`cursor-pointer rounded px-2 py-0.5 text-xs transition-colors ${
                    statusFilter === 'test_failed'
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
                  onClick={() => setStatusFilter('test_flaky')}
                  className={`cursor-pointer rounded px-2 py-0.5 text-xs transition-colors ${
                    statusFilter === 'test_flaky'
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
                  onClick={() => setStatusFilter('test_skipped')}
                  className={`cursor-pointer rounded px-2 py-0.5 text-xs transition-colors ${
                    statusFilter === 'test_skipped'
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

            {/* Report filter dropdown - only show when multiple reports */}
            {reports && reports.length > 1 && (
              <div ref={reportDropdownRef} className="relative">
                <button
                  type="button"
                  onClick={() => setReportDropdownOpen(!reportDropdownOpen)}
                  className={`cursor-pointer inline-flex items-center gap-1.5 rounded px-2 py-1 text-xs font-medium transition-colors w-28 justify-center ${
                    selectedReports.size > 0
                      ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-400'
                      : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-400 dark:hover:bg-gray-600'
                  }`}
                >
                  <Filter className="h-3 w-3" />
                  {selectedReports.size > 0
                    ? `${selectedReports.size} report${selectedReports.size > 1 ? 's' : ''}`
                    : 'All Reports'}
                  <ChevronDown
                    className={`h-3 w-3 transition-transform ${reportDropdownOpen ? 'rotate-180' : ''}`}
                  />
                </button>

                {reportDropdownOpen && (
                  <div className="absolute right-0 z-10 mt-1 w-80 max-w-[90vw] rounded-md border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800">
                    <div className="p-2 max-h-64 overflow-y-auto">
                      <button
                        type="button"
                        onClick={selectAllReports}
                        className={`w-full rounded px-2 py-1.5 text-left text-xs transition-colors ${
                          selectedReports.size === 0
                            ? 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
                            : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700'
                        }`}
                      >
                        All Reports
                      </button>
                      <div className="my-1 border-t border-gray-200 dark:border-gray-700" />
                      {[...reports]
                        .sort(
                          (a, b) =>
                            workerSlot(a.report_name, a.report_number) -
                            workerSlot(b.report_name, b.report_number),
                        )
                        .map((entry) => (
                          <button
                            key={entry.report_id}
                            type="button"
                            onClick={() => toggleReport(entry.report_id)}
                            className={`w-full rounded px-2 py-1.5 text-left text-xs transition-colors ${
                              selectedReports.has(entry.report_id)
                                ? 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
                                : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700'
                            }`}
                          >
                            <span className="flex items-center gap-2 min-w-0">
                              <span className="inline-flex h-5 min-w-5 items-center justify-center rounded bg-gray-200 px-1 text-[10px] font-semibold text-gray-600 dark:bg-gray-600 dark:text-gray-300">
                                {workerSlot(entry.report_name, entry.report_number)}
                              </span>
                              <span className="truncate" title={entry.report_name}>
                                {entry.report_name}
                              </span>
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

        {/* Search row (own line, below the header) */}
        <div className="relative mb-4 inline-block w-[21rem]">
          {isSearching ? (
            <Loader2 className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 animate-spin text-blue-500" />
          ) : (
            <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-gray-400 dark:text-gray-500" />
          )}
          <input
            ref={searchInputRef}
            type="text"
            placeholder="Search tests..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="h-7 w-full rounded border border-gray-200 bg-white pl-7 pr-7 text-xs text-gray-900 placeholder-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-gray-600 dark:bg-gray-700 dark:text-white dark:placeholder-gray-500 dark:focus:border-blue-400 dark:focus:ring-blue-400"
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

        {totalPages > 1 && (
          <PaginationBar
            currentPage={currentPage}
            totalPages={totalPages}
            totalItems={filteredSuites.length}
            pageSize={PAGE_SIZE}
            onPageChange={setCurrentPage}
            itemLabel="suite"
            position="top"
          />
        )}

        {filteredSuites.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400">
            {statusFilter === 'all'
              ? 'No test suites found'
              : `No suites match the ${statusFilter.replace(/^(spec|test)_/, '')} filter`}
          </p>
        ) : (
          <div className="divide-y divide-gray-100 dark:divide-gray-700">
            {paginatedSuites.map((suite, index) => {
              // Check if suite itself matched by title/file_path (vs matched by API test cases)
              const suiteMatchedByPath = normalizedSearch
                ? suite.title?.toLowerCase().includes(normalizedSearch) ||
                  suite.file_path?.toLowerCase().includes(normalizedSearch)
                : false;
              return (
                <SuiteRow
                  key={suite.id}
                  suite={suite}
                  reportId={reportId}
                  isExpanded={expandedSuiteIds.has(suite.id)}
                  onToggle={() => handleSuiteClick(suite.id)}
                  statusFilter={statusFilter}
                  rowNumber={(currentPage - 1) * PAGE_SIZE + index + 1}
                  hasMultipleReports={!!reports && reports.length > 1}
                  searchQuery={normalizedSearch}
                  suiteMatchedByPath={suiteMatchedByPath}
                  allReportsForFile={filePathReportsMap.get(suite.file_path) || []}
                  crossShardHistory={crossShardHistory}
                  divergencesBySpecPath={divergencesBySpecPath}
                  displayPath={canonicalSpecPathByFilePath.get(suite.file_path) ?? suite.file_path}
                />
              );
            })}
          </div>
        )}

        {totalPages > 1 && (
          <PaginationBar
            currentPage={currentPage}
            totalPages={totalPages}
            totalItems={filteredSuites.length}
            pageSize={PAGE_SIZE}
            onPageChange={setCurrentPage}
            itemLabel="suite"
            position="bottom"
          />
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
                {stats.expected + stats.unexpected + stats.flaky + stats.skipped} tests
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
  hasMultipleReports: boolean;
  searchQuery: string;
  /** True if suite matched by its own title/file_path, false if matched only by API test cases */
  suiteMatchedByPath: boolean;
  /** All reports that tested this same file_path (for multi-report badge display) */
  allReportsForFile: {
    report_number: number;
    report_name: string;
    passed: boolean;
    created_at: string;
  }[];
  /** Per-spec cross-shard attempt history keyed by `${suite.title} › ${spec.title}`. */
  crossShardHistory?: Map<string, CrossShardAttempt[]>;
  /** Per-spec orchestration/artifact divergences keyed by `spec.file_path`. */
  divergencesBySpecPath?: Map<string, Divergence>;
  /**
   * The path text rendered next to the file icon. Defaults to
   * `suite.file_path`; the parent overrides with the orchestration's
   * canonical repo-root-relative `spec_path` when one is known.
   */
  displayPath: string;
}

const LOADING_DELAY_MS = 1000;

const SuiteRow = memo(function SuiteRow({
  suite,
  reportId,
  isExpanded,
  onToggle,
  statusFilter,
  rowNumber,
  hasMultipleReports,
  searchQuery,
  suiteMatchedByPath,
  allReportsForFile,
  crossShardHistory,
  divergencesBySpecPath,
  displayPath,
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
  const {
    data: specsData,
    isLoading,
    isFetched,
  } = useQuery<TestSpecListResponse>({
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
        case 'test_passed':
          // All specs that ultimately passed (including flaky)
          return spec.ok;
        case 'test_failed':
          return !spec.ok && finalResult?.status !== 'skipped';
        case 'test_flaky':
          return isFlaky;
        case 'test_skipped':
          return finalResult?.status === 'skipped';
        // Suite-file-level filters don't drill into individual specs —
        // when a suite matches, every spec in it is shown.
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
                    <HighlightText text={displayPath} search={searchQuery} />
                  ) : (
                    <span className="text-red-500 italic">Missing file path</span>
                  )}
                </span>
                {hasMultipleReports && allReportsForFile.length > 0 && (
                  <span className="ml-1 inline-flex items-center gap-0.5 flex-shrink-0">
                    {allReportsForFile.map((j) => {
                      const isCurrent = j.report_number === suite.report_number;
                      const colorClass = j.passed
                        ? isCurrent
                          ? 'bg-green-200 text-green-700 dark:bg-green-800 dark:text-green-200'
                          : 'bg-green-100 text-green-400 dark:bg-green-900/40 dark:text-green-500'
                        : isCurrent
                          ? 'bg-red-200 text-red-700 dark:bg-red-800 dark:text-red-200'
                          : 'bg-red-100 text-red-400 dark:bg-red-900/40 dark:text-red-500';
                      return (
                        <span
                          key={j.report_number}
                          className={`inline-flex h-4 min-w-4 items-center justify-center rounded px-0.5 text-[10px] font-semibold ${colorClass}`}
                          title={j.report_name || `Report ${j.report_number}`}
                        >
                          {workerSlot(j.report_name, j.report_number)}
                        </span>
                      );
                    })}
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
            <span className="text-gray-600 dark:text-gray-300">
              {suite.tests_count} {suite.tests_count === 1 ? 'test' : 'tests'}
            </span>
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
              {filteredSpecs.map((spec, specIndex) => {
                const fullTitle = `${suite.title} › ${spec.title}`;
                return (
                  <SpecRow
                    key={spec.id}
                    spec={spec}
                    rowLabel={`${rowNumber}.${specIndex + 1}`}
                    searchQuery={searchQuery}
                    crossShardAttempts={crossShardHistory?.get(fullTitle)}
                    divergence={divergencesBySpecPath?.get(spec.file_path)}
                  />
                );
              })}
            </div>
          ) : (
            <p className="py-2 text-sm text-gray-500 dark:text-gray-400">
              {statusFilter === 'all' ? 'No tests found' : `No ${statusFilter} tests`}
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
  /**
   * Attempts for this spec across every shard that ran it (from the
   * consolidated view). Populated only on filtered-report pages; single-
   * report contexts pass undefined.
   */
  crossShardAttempts?: CrossShardAttempt[];
  /**
   * Disagreement between this spec's orchestration verdict and its
   * artifact verdict, if any. Surfaces a small inline pill so reviewers
   * spot divergences at a glance.
   */
  divergence?: Divergence;
}

const SpecRow = memo(function SpecRow({
  spec,
  rowLabel,
  searchQuery,
  crossShardAttempts,
  divergence,
}: SpecRowProps) {
  const [isExpanded, setIsExpanded] = useState(false);

  // Memoize all derived status values
  const {
    latestResult,
    StatusIcon,
    statusColor,
    hasMultipleAttempts,
    singleResultHasContent,
    hasCrossShardDivergence,
    shardSummaries,
    failedShardCount,
    isExpandable,
  } = useMemo(() => {
    const latest = spec.results[spec.results.length - 1];
    const skipped = latest?.status === 'skipped';
    const latestPassed = latest?.status === 'passed';
    const hadFailedAttempt = spec.results.some((r) => r.status === 'failed');
    const flakyFromRetries = (spec.ok && hadFailedAttempt) || (latestPassed && hadFailedAttempt);

    // Roll each shard's attempts up to one summary entry (final status,
    // attempt count, earliest timestamp). Native retries within a single
    // shard become `attempts_count`, not extra rows in the "Across shards"
    // list.
    type ShardSummary = {
      report_id: string;
      display_name: string;
      final_status: string;
      final_duration_ms: number;
      final_error?: string;
      attempts_count: number;
      created_at: string;
    };
    const summaries: ShardSummary[] = [];
    if (crossShardAttempts && crossShardAttempts.length > 0) {
      const byShard = new Map<string, CrossShardAttempt[]>();
      for (const a of crossShardAttempts) {
        const arr = byShard.get(a.report_id) ?? [];
        arr.push(a);
        byShard.set(a.report_id, arr);
      }
      for (const [reportId, attempts] of byShard) {
        const sorted = [...attempts].sort((a, b) => (a.created_at < b.created_at ? -1 : 1));
        const last = sorted[sorted.length - 1]!;
        summaries.push({
          report_id: reportId,
          display_name: last.display_name,
          final_status: last.status,
          final_duration_ms: last.duration_ms,
          final_error: last.error_message,
          attempts_count: sorted.length,
          created_at: sorted[0]!.created_at,
        });
      }
      summaries.sort((a, b) => (a.created_at < b.created_at ? -1 : 1));
    }

    // Cross-shard divergence: two or more distinct shards with differing
    // final statuses. In-shard retries don't count — those are native
    // flakiness, rendered by the `spec.results` path above.
    const finalStatuses = new Set(summaries.map((s) => s.final_status));
    const crossShardDivergence = summaries.length > 1 && finalStatuses.size > 1;
    const failedShards = summaries.filter((s) => s.final_status === 'failed').length;

    const flaky = flakyFromRetries || crossShardDivergence;

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
      (latest.errors_json || (latest.attachments && latest.attachments.length > 0));
    const hasExpandable =
      multipleAttempts ||
      singleHasContent ||
      (spec.screenshots && spec.screenshots.length > 0) ||
      crossShardDivergence;
    const expandable = hasExpandable && (!spec.ok || flaky || skipped);

    return {
      latestResult: latest,
      StatusIcon: Icon,
      statusColor: color,
      hasMultipleAttempts: multipleAttempts,
      singleResultHasContent: singleHasContent,
      hasCrossShardDivergence: crossShardDivergence,
      shardSummaries: summaries,
      failedShardCount: failedShards,
      isExpandable: expandable,
    };
  }, [spec, crossShardAttempts]);

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
        {hasCrossShardDivergence && (
          <span
            className="inline-flex items-center gap-1 text-xs text-yellow-700 dark:text-yellow-400"
            title="This test produced different final results across shards — expand to see each shard's outcome."
          >
            <AlertTriangle className="h-3 w-3" />
            {failedShardCount} of {shardSummaries.length} shards failed
          </span>
        )}
        {divergence && (
          <DivergenceBadge
            orchestrationStatus={divergence.orchestration_status}
            artifactStatus={divergence.artifact_status}
          />
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
                    {/* Position-index, not result.retry+1: orchestration
                        retests produce a fresh Playwright process whose
                        internal retry counter resets to 0, so two retest
                        survivors would otherwise both render as
                        "Attempt 1". The list-position index is monotonic
                        across leases AND retries, matching the Dispatch
                        tab's labeling. */}
                    Attempt {idx + 1}
                  </span>
                  <span className={`text-xs ${attemptColor}`}>({statusLabel})</span>
                  <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-500">
                    <Clock className="h-3 w-3" />
                    {formatDuration(result.duration_ms)}
                  </span>
                  {result.project_name && result.project_name !== 'default' && (
                    <span className="text-gray-600 dark:text-gray-400">{result.project_name}</span>
                  )}
                </div>
                {/* Inline error display for this attempt */}
                {result.errors_json && <InlineErrorDisplay errorsJson={result.errors_json} />}
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
          {latestResult.errors_json && <InlineErrorDisplay errorsJson={latestResult.errors_json} />}
          <AttachmentsDisplay attachments={latestResult.attachments} />
        </div>
      )}
      {(() => {
        // Spec-level aggregate gallery. Dedupe against paths already shown
        // in any per-attempt AttachmentsDisplay so screenshots that exist
        // in BOTH test_cases.attachments (Playwright JSON path) and
        // report_screenshots (multipart upload path) don't render twice.
        if (!isExpanded || !spec.screenshots || spec.screenshots.length === 0) {
          return null;
        }
        // Spec-level Screenshots use the S3 key as `file_path`. Per-attempt
        // attachments carry both the original local Playwright path AND the
        // resolved s3_key — match on s3_key (or basename when s3_key is
        // missing) so the dedup actually catches duplicates.
        const seenInAttempts = new Set<string>();
        const baseName = (p: string) => {
          const i = p.lastIndexOf('/');
          return i >= 0 ? p.slice(i + 1) : p;
        };
        for (const r of spec.results ?? []) {
          for (const a of r.attachments ?? []) {
            const s3 = (a as { s3_key?: string | null }).s3_key;
            if (typeof s3 === 'string' && s3.length > 0) {
              seenInAttempts.add(s3);
              seenInAttempts.add(baseName(s3));
              continue;
            }
            const path = (a as { path?: string }).path;
            if (typeof path === 'string' && path.length > 0) {
              seenInAttempts.add(baseName(path));
            }
          }
        }
        const matchesSeen = (filePath: string) =>
          seenInAttempts.has(filePath) || seenInAttempts.has(baseName(filePath));
        const extras = spec.screenshots.filter((s) => !matchesSeen(s.file_path));
        if (extras.length === 0) return null;
        return (
          <div className="ml-16 mt-2 border-l-2 border-gray-200 pl-3 dark:border-gray-600">
            <p className="text-xs text-gray-500 dark:text-gray-400 mb-1">
              Screenshots ({extras.length})
            </p>
            <ScreenshotGallery
              screenshots={extras.map((screenshot, idx) => ({
                path: screenshot.file_path,
                s3_key: screenshot.file_path,
                content_type: 'image/png',
                retry: 0,
                missing: false,
                sequence: idx,
              }))}
            />
          </div>
        );
      })()}
      {isExpanded && hasCrossShardDivergence && (
        <div className="ml-16 mt-2 space-y-3 border-l-2 border-yellow-300 pl-3 dark:border-yellow-700">
          <p className="text-xs font-medium text-yellow-700 dark:text-yellow-400">Across shards</p>
          {shardSummaries.map((s) => {
            // Pull this shard's full attempt list back out of crossShardAttempts
            // so every attempt (failed + passing) renders individually, not
            // just the rollup. Sort chronologically.
            const shardAttempts = (crossShardAttempts ?? [])
              .filter((a) => a.report_id === s.report_id)
              .slice()
              .sort((a, b) => (a.created_at < b.created_at ? -1 : 1));
            const headerPassed = s.final_status === 'passed' || s.final_status === 'flaky';
            const headerSkipped = s.final_status === 'skipped';
            const HeaderIcon = headerSkipped ? MinusCircle : headerPassed ? CheckCircle2 : XCircle;
            const headerColor = headerSkipped
              ? 'text-gray-400'
              : headerPassed
                ? 'text-green-500'
                : 'text-red-500';
            return (
              <div key={s.report_id} className="space-y-1">
                <div className="flex items-center gap-2 text-xs">
                  <HeaderIcon className={`h-3 w-3 flex-shrink-0 ${headerColor}`} />
                  <span className="font-medium text-gray-700 dark:text-gray-300">
                    {s.display_name || s.report_id.slice(0, 8)}
                  </span>
                  <span className={`text-xs ${headerColor}`}>(final: {s.final_status})</span>
                  {s.attempts_count > 1 && (
                    <span className="text-gray-500 dark:text-gray-500">
                      {s.attempts_count} attempts
                    </span>
                  )}
                </div>
                {shardAttempts.map((a, idx) => (
                  <CrossShardAttemptRow key={`${a.report_id}-${idx}`} attempt={a} index={idx} />
                ))}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
});

interface CrossShardAttemptRowProps {
  attempt: CrossShardAttempt;
  index: number;
}

/**
 * One attempt row inside the "Across shards" expansion. The attempt's error
 * + screenshots are collapsed by default; clicking the row reveals them.
 * Rows with no error/screenshot content are non-interactive.
 */
const CrossShardAttemptRow = memo(function CrossShardAttemptRow({
  attempt,
  index,
}: CrossShardAttemptRowProps) {
  const [isExpanded, setIsExpanded] = useState(false);
  const hasErrors = !!attempt.errors_json;
  const hasScreenshots = !!(attempt.screenshots && attempt.screenshots.length > 0);
  const hasContent = hasErrors || hasScreenshots;

  const isPassed = attempt.status === 'passed' || attempt.status === 'flaky';
  const isSkipped = attempt.status === 'skipped';
  const AttemptIcon = isSkipped ? MinusCircle : isPassed ? CheckCircle2 : XCircle;
  const color = isSkipped ? 'text-gray-400' : isPassed ? 'text-green-500' : 'text-red-500';

  const toggle = () => hasContent && setIsExpanded((v) => !v);
  const ExpandIcon = isExpanded ? ChevronDown : ChevronRight;

  return (
    <div className="ml-5 space-y-1">
      <div
        className={`flex items-center gap-2 text-xs ${
          hasContent
            ? 'cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800 rounded -mx-1 px-1'
            : ''
        }`}
        role={hasContent ? 'button' : undefined}
        tabIndex={hasContent ? 0 : undefined}
        onClick={hasContent ? toggle : undefined}
        onKeyDown={hasContent ? (e) => (e.key === 'Enter' || e.key === ' ') && toggle() : undefined}
      >
        {hasContent ? (
          <ExpandIcon className="h-3 w-3 flex-shrink-0 text-gray-400 dark:text-gray-500" />
        ) : (
          <span className="w-3 flex-shrink-0" />
        )}
        <AttemptIcon className={`h-3 w-3 flex-shrink-0 ${color}`} />
        <span className="text-gray-600 dark:text-gray-400">Attempt {index + 1}</span>
        <span className={`text-xs ${color}`}>({attempt.status})</span>
        {attempt.duration_ms > 0 && (
          <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-500">
            <Clock className="h-3 w-3" />
            {formatDuration(attempt.duration_ms)}
          </span>
        )}
      </div>
      {isExpanded && hasErrors && <InlineErrorDisplay errorsJson={attempt.errors_json!} />}
      {isExpanded && hasScreenshots && (
        <div className="ml-5">
          <ScreenshotGallery
            screenshots={attempt.screenshots!.map((sh, i) => ({
              path: sh.file_path,
              s3_key: sh.file_path,
              content_type: 'image/png',
              retry: 0,
              missing: false,
              sequence: i,
            }))}
          />
        </div>
      )}
    </div>
  );
});
