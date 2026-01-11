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
  platform?: string; // For Detox reports: "ios" or "android"
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

export interface ScreenshotInfo {
  file_path: string;
  screenshot_type: string;
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
  screenshots?: ScreenshotInfo[];
}

export interface TestSpecListResponse {
  specs: TestSpec[];
}

// Detox types (T024)
export type DetoxPlatform = "android" | "ios";

export interface DetoxRunSummary {
  id: string;
  run_id: string;
  platform: string;
  total_jobs: number;
  total_tests: number;
  passed_tests: number;
  failed_tests: number;
  skipped_tests: number;
  duration_ms: number;
  created_at: string;
}

export interface DetoxRunListResponse {
  runs: DetoxRunSummary[];
  pagination: Pagination;
}

export interface DetoxJobSummary {
  id: string;
  job_number: number;
  folder_name: string;
  tests_count: number;
  passed_count: number;
  failed_count: number;
  skipped_count: number;
  duration_ms: number;
  report_id: string;
  created_at: string;
}

export interface DetoxRunDetail extends DetoxRunSummary {
  jobs: DetoxJobSummary[];
}

export interface DetoxJobListResponse {
  jobs: DetoxJobSummary[];
}

export interface DetoxCombinedTestResult {
  id: number;
  title: string;
  full_title: string;
  status: string;
  duration_ms: number;
  error_message?: string;
  job_id: string;
  job_number: number;
  folder_name: string;
  suite_title?: string;
  has_screenshots: boolean;
}

export interface DetoxCombinedTestsResponse {
  tests: DetoxCombinedTestResult[];
  pagination: Pagination;
}

export interface DetoxJobDetail {
  id: string;
  run_id: string;
  job_number: number;
  folder_name: string;
  tests_count: number;
  passed_count: number;
  failed_count: number;
  skipped_count: number;
  duration_ms: number;
  report_id: string;
  created_at: string;
}

export interface DetoxScreenshot {
  id: number;
  file_path: string;
  screenshot_type: string;
  test_full_name: string;
  available: boolean;
}

export interface DetoxScreenshotsListResponse {
  screenshots: DetoxScreenshot[];
}
