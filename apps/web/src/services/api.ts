import { useQuery } from '@tanstack/react-query';
import type {
  ClientConfig,
  ReportListResponse,
  RawReportListResponse,
  TestSuiteListResponse,
  ReportWithJobs,
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

// Reports list API
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

export function useReportSuites(id: string) {
  return useQuery({
    queryKey: ['report', id, 'suites'],
    queryFn: async () => {
      const response = await fetch(`${API_URL}/reports/${id}/suites`);
      return handleResponse<TestSuiteListResponse>(response);
    },
    enabled: !!id,
  });
}

// Report with jobs API
async function fetchReportWithJobs(id: string): Promise<ReportWithJobs> {
  const response = await fetch(`${API_URL}/reports/${id}`);
  return handleResponse<ReportWithJobs>(response);
}

export function useReportWithJobs(id: string) {
  return useQuery({
    queryKey: ['report-with-jobs', id],
    queryFn: () => fetchReportWithJobs(id),
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
  job_id: string;
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
