import { useQuery } from '@tanstack/react-query';
import type {
  ClientConfig,
  ServerInfo,
  ReportListResponse,
  RawReportListResponse,
  TestSuiteListResponse,
  ReportDetail,
  GroupedReportsResponse,
  ConsolidatedResultsResponse,
} from '@/types';

export const API_URL = import.meta.env.VITE_API_URL || '/api/v1';

// Error handling
class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

async function handleResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const errorData = await response.json().catch(() => ({
      error: 'UNKNOWN_ERROR',
      message: response.statusText,
    }));
    throw new ApiError(
      response.status,
      errorData.error || 'UNKNOWN_ERROR',
      errorData.message || response.statusText,
    );
  }
  return response.json();
}

// Client config
export function useClientConfig() {
  return useQuery({
    queryKey: ['client-config'],
    queryFn: async () => {
      const response = await fetch(`${API_URL}/config`);
      return handleResponse<ClientConfig>(response);
    },
    staleTime: 5 * 60 * 1000, // 5 minutes
  });
}

// Server info (version, build metadata)
export function useServerInfo() {
  return useQuery({
    queryKey: ['server-info'],
    queryFn: async () => {
      const response = await fetch(`${API_URL}/info`);
      return handleResponse<ServerInfo>(response);
    },
    staleTime: 5 * 60 * 1000, // 5 minutes
  });
}

// Grouped reports for landing page
export function useGroupedReports() {
  return useQuery({
    queryKey: ['reports-grouped'],
    queryFn: async () => {
      const response = await fetch(`${API_URL}/reports/grouped`);
      return handleResponse<GroupedReportsResponse>(response);
    },
  });
}

// Consolidated results for filtered view
export function useConsolidatedResults(
  repo: string,
  branch: string,
  commit: string,
  name: string,
  runAttempt?: number,
  gid?: string,
) {
  return useQuery({
    queryKey: ['reports-consolidated', repo, branch, commit, name, runAttempt, gid],
    queryFn: async () => {
      const params = new URLSearchParams({
        repository: repo,
        branch,
        commit,
        name,
      });
      if (runAttempt !== undefined) {
        params.set('run_attempt', String(runAttempt));
      }
      if (gid) {
        params.set('gid', gid);
      }
      const response = await fetch(`${API_URL}/reports/consolidated?${params}`);
      return handleResponse<ConsolidatedResultsResponse>(response);
    },
    enabled: !!repo && !!branch && !!commit && !!name,
  });
}

// Individual reports list API
export interface IndividualReportSummary {
  id: string;
  short_id: string;
  report_group_id: string;
  name: string;
  status: string;
  gh_job_id?: string;
  gh_job_name?: string;
  repository?: string;
  branch?: string;
  commit?: string;
  test_stats?: {
    total: number;
    passed: number;
    failed: number;
    skipped: number;
    flaky: number;
    duration_ms?: number;
    wall_clock_ms?: number;
  };
  duration_ms?: number;
  created_at: string;
}

export interface IndividualReportListResponse {
  reports: IndividualReportSummary[];
  total: number;
  limit: number;
  offset: number;
}

export function useIndividualReports(page = 1, limit = 100) {
  return useQuery({
    queryKey: ['reports-individual', page, limit],
    queryFn: async () => {
      const offset = (page - 1) * limit;
      const response = await fetch(`${API_URL}/reports/individual?limit=${limit}&offset=${offset}`);
      return handleResponse<IndividualReportListResponse>(response);
    },
  });
}

// Reports list API (report groups)
export function useReports(page = 1, limit = 100) {
  return useQuery({
    queryKey: ['reports', page, limit],
    queryFn: async () => {
      const offset = (page - 1) * limit;
      const response = await fetch(`${API_URL}/reports?limit=${limit}&offset=${offset}`);
      const rawData = await handleResponse<RawReportListResponse>(response);

      // Transform to expected format with pagination object
      const totalPages = Math.ceil(rawData.total / limit);
      const currentPage = Math.floor(rawData.offset / limit) + 1;

      return {
        reports: rawData.reports,
        pagination: {
          page: currentPage,
          limit: rawData.limit,
          total: rawData.total,
          total_pages: totalPages,
        },
      } as ReportListResponse;
    },
  });
}

export async function fetchReportSuites(id: string): Promise<TestSuiteListResponse> {
  const response = await fetch(`${API_URL}/reports/${id}/suites`);
  return handleResponse<TestSuiteListResponse>(response);
}

export function useReportSuites(id: string) {
  return useQuery({
    queryKey: ['report', id, 'suites'],
    queryFn: () => fetchReportSuites(id),
    enabled: !!id,
  });
}

// Report detail API
export async function fetchReportDetail(id: string): Promise<ReportDetail> {
  const response = await fetch(`${API_URL}/reports/${id}`);
  return handleResponse<ReportDetail>(response);
}

export function useReportDetail(id: string) {
  return useQuery({
    queryKey: ['report-detail', id],
    queryFn: () => fetchReportDetail(id),
    enabled: !!id,
  });
}

// Search types - grouped by suite
export interface SearchMatchedTestCase {
  test_case_id: string;
  title: string;
  full_title: string;
  status: string;
  match_tokens: string[];
}

export interface SearchSuiteResult {
  suite_id: string;
  suite_title: string;
  suite_file_path: string | null;
  report_id: string;
  matches: SearchMatchedTestCase[];
}

export interface SearchResponse {
  query: string;
  search_min_length: number;
  total_matches: number;
  results: SearchSuiteResult[];
}

// Search API function
export async function searchTestCases(
  reportId: string,
  query: string,
  limit = 100,
): Promise<SearchResponse> {
  const params = new URLSearchParams({
    q: query,
    limit: String(limit),
  });
  const response = await fetch(`${API_URL}/reports/${reportId}/search?${params}`);
  return handleResponse<SearchResponse>(response);
}

// Search React Query hook
// Note: Debouncing should be done in the component before calling this hook
export function useSearchTestCases(
  reportId: string,
  query: string,
  minSearchLength: number,
  limit = 100,
) {
  return useQuery({
    queryKey: ['search-test-cases', reportId, query, limit],
    queryFn: () => searchTestCases(reportId, query, limit),
    enabled: !!reportId && query.length >= minSearchLength,
    staleTime: 60 * 1000, // 1 minute cache
  });
}
