// TypeScript types matching API schemas

// Client configuration from server
export interface ClientConfig {
  upload_timeout_ms: number;
  html_view_enabled: boolean;
  search_min_length: number;
  github_oauth_enabled?: boolean;
}

// Server info (version, build, and repository metadata)
export interface ServerInfo {
  server_version: string;
  environment: string;
  repo_url: string;
  commit_sha: string;
  build_time: string;
}

// User from /auth/me
export interface AuthUser {
  id: string;
  username: string;
  display_name?: string;
  avatar_url?: string;
  role: string;
}

/** OIDC claims stored separately from report metadata (token-derived). */
export interface ReportOidcClaims {
  sub?: string;
  repository?: string;
  repository_owner?: string;
  actor?: string;
  sha?: string;
  ref?: string;
  ref_type?: string;
  workflow?: string;
  event_name?: string;
  run_id?: string;
  run_number?: string;
  run_attempt?: string;
  head_ref?: string;
  base_ref?: string;
  resolved_role: string;
  api_path: string;
  http_method: string;
  created_at: string;
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

/** Environment metadata — tool and server info for the test run. */
export interface ReportEnvironmentMetadata {
  tool?: Record<string, unknown>;
  server?: Record<string, unknown>;
}

// Report summary (current API)
export interface ReportSummary {
  id: string;
  short_id: string;
  name: string;
  status: ReportStatus;
  framework: Framework;
  test_stats?: TestStats;
  repository: string;
  branch: string;
  commit: string;
  gh_run_id: string;
  gh_pr_number?: number;
  gh_run_attempt: string;
  environment_metadata?: ReportEnvironmentMetadata;
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
  report_id?: string;
  report_name?: string;
  report_number?: number;
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

export interface ReportEntryInfo {
  report_id: string;
  report_name: string;
  report_number: number;
}

export interface TestSuiteListResponse {
  suites: TestSuite[];
  reports?: ReportEntryInfo[];
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

// Report types
export type ProcessingStatus = 'pending' | 'processing' | 'complete' | 'failed';
export type ReportStatus = 'in_progress' | 'completed';
export type Framework = 'playwright' | 'cypress' | 'detox';

export interface ReportEnvironment {
  os?: string;
  browser?: string;
  device?: string;
  tags?: string[];
}

export interface ReportEntry {
  id: string;
  short_id: string;
  gh_job_id?: string;
  gh_job_name?: string;
  display_name: string;
  status: ProcessingStatus;
  environment?: ReportEnvironment;
  created_at?: string;
  updated_at?: string;
}

export interface ReportDetail {
  id: string;
  name: string;
  framework: Framework;
  status: ReportStatus;
  repository: string;
  branch: string;
  commit: string;
  gh_run_id: string;
  gh_pr_number?: number;
  gh_run_attempt: string;
  environment_metadata?: ReportEnvironmentMetadata;
  created_at: string;
  updated_at: string;
  reports: ReportEntry[];
  error_message?: string;
}

// --- Grouped Reports (landing page) ---

export interface RunEntry {
  report_id: string;
  framework: Framework;
  name: string;
  status: ReportStatus;
  branch: string;
  commit: string;
  short_sha: string;
  run_number?: string;
  gh_run_attempt: string;
  gh_run_id?: string;
  gh_pr_number?: number;
  test_stats?: TestStats;
  created_at: string;
  url_path: string;
}

export interface RepositoryGroup {
  repository: string;
  repository_name: string;
  latest_run_at: string;
  runs: RunEntry[];
}

export interface GroupedReportsResponse {
  groups: RepositoryGroup[];
}

// --- Consolidated Results (filtered view) ---

export interface SpecHistoryEntry {
  commit_sha: string;
  run_attempt: number;
  status: string;
  duration_ms: number;
  error_message?: string;
  created_at: string;
}

export interface ConsolidatedSpec {
  full_title: string;
  status: string;
  source_commit_sha: string;
  source_run_attempt: number;
  is_from_latest: boolean;
  duration_ms: number;
  error_message?: string;
  history?: SpecHistoryEntry[];
}

export interface ConsolidatedFilters {
  repository: string;
  target_name: string;
  commit_sha: string;
  tool_name: string;
}

export interface ConsolidatedResultsResponse {
  filters: ConsolidatedFilters;
  overall_status: string;
  total_specs: number;
  passed: number;
  failed: number;
  skipped: number;
  flaky: number;
  contributing_reports: string[];
  latest_commit_sha: string;
  latest_run_attempt: number;
  available_run_attempts: number[];
  duration_ms?: number;
  specs: ConsolidatedSpec[];
}
