// TypeScript types matching API schemas

// Client configuration from server
export interface ClientConfig {
  upload_timeout_ms: number;
  enable_html_view: boolean;
  min_search_length: number;
}

export type ExtractionStatus = 'pending' | 'completed' | 'failed';
export type TestStatus = 'passed' | 'failed' | 'skipped' | 'timedOut' | 'flaky';

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

// Test stats for a report
export interface TestStats {
  total: number;
  passed: number;
  failed: number;
  skipped: number;
  flaky: number;
  duration_ms?: number;
  wall_clock_ms?: number;
}

// New job-based report summary (current API)
export interface ReportSummary {
  id: string;
  short_id: string;
  status: ReportStatus;
  framework: Framework;
  expected_jobs: number;
  jobs_complete: number;
  test_stats?: TestStats;
  github_metadata?: {
    repo?: string;
    branch?: string;
    commit?: string;
    pr_number?: number;
    workflow?: string;
    run_id?: number;
    run_attempt?: number;
  };
  created_at: string;
}

// Legacy report summary (deprecated)
export interface LegacyReportSummary {
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

// New API response format
export interface ReportListResponse {
  reports: ReportSummary[];
  pagination: Pagination;
}

// Raw API response (for transformation)
export interface RawReportListResponse {
  reports: ReportSummary[];
  total: number;
  limit: number;
  offset: number;
}

export interface TestSuite {
  id: number;
  title: string;
  file_path: string;
  job_id?: string;
  job_name?: string;
  job_number?: number;
  specs_count: number;
  passed_count: number;
  failed_count: number;
  flaky_count?: number;
  skipped_count?: number;
  duration_ms?: number;
  /** Actual test execution start time from framework JSON. */
  start_time?: string;
  created_at?: string;
}

export interface JobInfo {
  job_id: string;
  job_name: string;
  job_number: number;
}

export interface TestSuiteListResponse {
  suites: TestSuite[];
  jobs?: JobInfo[];
}

export interface TestAttachment {
  path: string;
  content_type?: string;
  retry: number;
  s3_key?: string;
  missing: boolean;
  sequence: number;
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
  attachments?: TestAttachment[];
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

// Job-based report types (Phase 6)
export type JobStatus = 'html_uploaded' | 'json_uploaded' | 'processing' | 'complete' | 'failed';
export type ReportStatus = 'initializing' | 'uploading' | 'processing' | 'complete' | 'failed';
export type Framework = 'playwright' | 'cypress' | 'detox';

export interface JobEnvironment {
  os?: string;
  browser?: string;
  device?: string;
  tags?: string[];
}

export interface JobSummary {
  id: string;
  github_job_id?: string;
  github_job_name?: string;
  display_name: string;
  status: JobStatus;
  html_url?: string;
  environment?: JobEnvironment;
  created_at: string;
  updated_at: string;
}

export interface ReportWithJobs {
  id: string;
  framework: Framework;
  status: ReportStatus;
  expected_jobs: number;
  github_repo?: string;
  github_branch?: string;
  github_commit?: string;
  github_pr_number?: number;
  github_workflow?: string;
  github_run_id?: string;
  created_at: string;
  updated_at: string;
  jobs: JobSummary[];
  error_message?: string;
}

// Detox types (T024)
export type DetoxPlatform = 'android' | 'ios';

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
