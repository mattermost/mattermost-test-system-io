import { useQuery } from '@tanstack/react-query';
import type {
  ReportListResponse,
  ReportDetail,
  TestSuiteListResponse,
  DetoxRunListResponse,
  DetoxRunDetail,
  DetoxCombinedTestsResponse,
  DetoxJobDetail,
  DetoxScreenshotsListResponse,
} from '../types';

const API_URL = import.meta.env.VITE_API_URL || '/api/v1';

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

// API functions
export async function fetchReports(page = 1, limit = 100): Promise<ReportListResponse> {
  const response = await fetch(`${API_URL}/reports?page=${page}&limit=${limit}`);
  return handleResponse<ReportListResponse>(response);
}

export async function fetchReport(id: string): Promise<ReportDetail> {
  const response = await fetch(`${API_URL}/reports/${id}`);
  return handleResponse<ReportDetail>(response);
}

export async function fetchReportSuites(id: string): Promise<TestSuiteListResponse> {
  const response = await fetch(`${API_URL}/reports/${id}/suites`);
  return handleResponse<TestSuiteListResponse>(response);
}

// Get URL for HTML report viewer
export function getReportHtmlUrl(id: string): string {
  return `${API_URL}/reports/${id}/html`;
}

// React Query hooks
export function useReports(page = 1, limit = 100) {
  return useQuery({
    queryKey: ['reports', page, limit],
    queryFn: () => fetchReports(page, limit),
  });
}

export function useReport(id: string) {
  return useQuery({
    queryKey: ['report', id],
    queryFn: () => fetchReport(id),
    enabled: !!id,
  });
}

export function useReportSuites(id: string) {
  return useQuery({
    queryKey: ['report', id, 'suites'],
    queryFn: () => fetchReportSuites(id),
    enabled: !!id,
  });
}

// Detox API functions (T025)
export async function fetchDetoxRuns(
  page = 1,
  limit = 20,
  platform?: string,
): Promise<DetoxRunListResponse> {
  const params = new URLSearchParams({
    page: String(page),
    limit: String(limit),
  });
  if (platform) {
    params.set('platform', platform);
  }
  const response = await fetch(`${API_URL}/detox-runs?${params}`);
  return handleResponse<DetoxRunListResponse>(response);
}

export async function fetchDetoxRun(id: string): Promise<DetoxRunDetail> {
  const response = await fetch(`${API_URL}/detox-runs/${id}`);
  return handleResponse<DetoxRunDetail>(response);
}

export async function fetchDetoxRunTests(
  id: string,
  page = 1,
  limit = 50,
  status?: string,
  search?: string,
): Promise<DetoxCombinedTestsResponse> {
  const params = new URLSearchParams({
    page: String(page),
    limit: String(limit),
  });
  if (status) {
    params.set('status', status);
  }
  if (search) {
    params.set('search', search);
  }
  const response = await fetch(`${API_URL}/detox-runs/${id}/tests?${params}`);
  return handleResponse<DetoxCombinedTestsResponse>(response);
}

export async function fetchDetoxJob(id: string): Promise<DetoxJobDetail> {
  const response = await fetch(`${API_URL}/detox-jobs/${id}`);
  return handleResponse<DetoxJobDetail>(response);
}

export function getDetoxJobHtmlUrl(id: string): string {
  return `${API_URL}/detox-jobs/${id}/html`;
}

export async function fetchDetoxTestScreenshots(
  jobId: string,
  testFullName: string,
): Promise<DetoxScreenshotsListResponse> {
  const encodedTestName = encodeURIComponent(testFullName);
  const response = await fetch(
    `${API_URL}/detox-jobs/${jobId}/tests/${encodedTestName}/screenshots`,
  );
  return handleResponse<DetoxScreenshotsListResponse>(response);
}

// Detox React Query hooks
export function useDetoxRuns(page = 1, limit = 20, platform?: string) {
  return useQuery({
    queryKey: ['detox-runs', page, limit, platform],
    queryFn: () => fetchDetoxRuns(page, limit, platform),
  });
}

export function useDetoxRun(id: string) {
  return useQuery({
    queryKey: ['detox-run', id],
    queryFn: () => fetchDetoxRun(id),
    enabled: !!id,
  });
}

export function useDetoxRunTests(
  id: string,
  page = 1,
  limit = 50,
  status?: string,
  search?: string,
) {
  return useQuery({
    queryKey: ['detox-run', id, 'tests', page, limit, status, search],
    queryFn: () => fetchDetoxRunTests(id, page, limit, status, search),
    enabled: !!id,
  });
}

export function useDetoxJob(id: string) {
  return useQuery({
    queryKey: ['detox-job', id],
    queryFn: () => fetchDetoxJob(id),
    enabled: !!id,
  });
}

export function useDetoxTestScreenshots(jobId: string, testFullName: string) {
  return useQuery({
    queryKey: ['detox-screenshots', jobId, testFullName],
    queryFn: () => fetchDetoxTestScreenshots(jobId, testFullName),
    enabled: !!jobId && !!testFullName,
  });
}
