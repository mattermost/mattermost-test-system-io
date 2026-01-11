import { useQuery } from "@tanstack/react-query";
import type {
  ReportListResponse,
  ReportDetail,
  TestSuiteListResponse,
} from "../types";

const API_URL = import.meta.env.VITE_API_URL || "/api/v1";

// Error handling
class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function handleResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const errorData = await response.json().catch(() => ({
      error: "UNKNOWN_ERROR",
      message: response.statusText,
    }));
    throw new ApiError(
      response.status,
      errorData.error || "UNKNOWN_ERROR",
      errorData.message || response.statusText,
    );
  }
  return response.json();
}

// API functions
export async function fetchReports(
  page = 1,
  limit = 100,
): Promise<ReportListResponse> {
  const response = await fetch(
    `${API_URL}/reports?page=${page}&limit=${limit}`,
  );
  return handleResponse<ReportListResponse>(response);
}

export async function fetchReport(id: string): Promise<ReportDetail> {
  const response = await fetch(`${API_URL}/reports/${id}`);
  return handleResponse<ReportDetail>(response);
}

export async function fetchReportSuites(
  id: string,
): Promise<TestSuiteListResponse> {
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
    queryKey: ["reports", page, limit],
    queryFn: () => fetchReports(page, limit),
  });
}

export function useReport(id: string) {
  return useQuery({
    queryKey: ["report", id],
    queryFn: () => fetchReport(id),
    enabled: !!id,
  });
}

export function useReportSuites(id: string) {
  return useQuery({
    queryKey: ["report", id, "suites"],
    queryFn: () => fetchReportSuites(id),
    enabled: !!id,
  });
}
