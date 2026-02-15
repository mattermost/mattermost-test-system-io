// TypeScript types matching API schemas

// Client configuration from server
export interface ClientConfig {
  upload_timeout_ms: number;
  html_view_enabled: boolean;
  search_min_length: number;
  github_oauth_enabled?: boolean;
}

// User from /auth/me
export interface AuthUser {
  id: string;
  username: string;
  display_name?: string;
  avatar_url?: string;
  role: string;
}

/** GitHub metadata stored with reports — field names match GitHub OIDC claims. */
export interface GitHubMetadata {
  sub?: string;
  repository?: string;
  repository_owner?: string;
  repository_owner_id?: string;
  repository_visibility?: string;
  repository_id?: string;
  actor?: string;
  actor_id?: string;
  ref?: string;
  ref_type?: string;
  sha?: string;
  workflow?: string;
  event_name?: string;
  run_id?: string;
  run_number?: string;
  run_attempt?: string;
  runner_environment?: string;
  head_ref?: string;
  base_ref?: string;
  job_workflow_ref?: string;
  pr_number?: number;
}

export type TestStatus = 'passed' | 'failed' | 'skipped' | 'timedOut' | 'flaky';

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
  github_metadata?: GitHubMetadata;
  created_at: string;
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
  github_metadata?: GitHubMetadata;
  created_at: string;
  updated_at: string;
  jobs: JobSummary[];
  error_message?: string;
}
