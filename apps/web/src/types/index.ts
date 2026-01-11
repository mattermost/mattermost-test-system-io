// TypeScript types matching API schemas

export type ExtractionStatus = "pending" | "completed" | "failed";
export type TestStatus = "passed" | "failed" | "skipped" | "timedOut";

export interface GitHubContext {
  repository?: string;
  branch?: string;
  commit_sha?: string;
  pr_number?: number;
  pr_author?: string;
  run_id?: number;
  run_attempt?: number;
}

export interface ReportStats {
  start_time: string;
  duration_ms: number;
  expected: number;
  skipped: number;
  unexpected: number;
  flaky: number;
}

export interface ReportSummary {
  id: string;
  created_at: string;
  extraction_status: ExtractionStatus;
  framework?: string;
  framework_version?: string;
  stats?: ReportStats;
  github_context?: GitHubContext;
}

export interface ReportDetail {
  id: string;
  created_at: string;
  extraction_status: ExtractionStatus;
  error_message?: string;
  file_path: string;
  framework?: string;
  framework_version?: string;
  stats?: ReportStats;
  has_files: boolean;
  files_deleted_at?: string;
  github_context?: GitHubContext;
}

export interface Pagination {
  page: number;
  limit: number;
  total: number;
  total_pages: number;
}

export interface ReportListResponse {
  reports: ReportSummary[];
  pagination: Pagination;
}

export interface TestSuite {
  id: number;
  title: string;
  file_path: string;
  specs_count: number;
  passed_count: number;
  failed_count: number;
  flaky_count?: number;
  skipped_count?: number;
  duration_ms?: number;
}

export interface TestSuiteListResponse {
  suites: TestSuite[];
}

export interface TestResult {
  id: number;
  status: TestStatus;
  duration_ms: number;
  retry: number;
  start_time: string;
  project_id: string;
  project_name: string;
  errors_json?: string;
}

export interface TestSpec {
  id: number;
  title: string;
  ok: boolean;
  spec_id: string;
  file_path: string;
  line: number;
  column: number;
  results: TestResult[];
}

export interface TestSpecListResponse {
  specs: TestSpec[];
}
