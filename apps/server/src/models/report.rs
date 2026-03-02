//! Report domain models and DTOs.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use utoipa::ToSchema;
use uuid::Uuid;

/// Environment metadata for reports (stored as JSONB).
///
/// Flexible key-value structure with namespaced top-level keys.
/// Currently supports `tool` and `server` namespaces.
#[derive(Debug, Clone, Default, Serialize, Deserialize, ToSchema)]
pub struct ReportEnvironmentMetadata {
    /// Tool-specific info (name, version, browser, config).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tool: Option<serde_json::Value>,
    /// Server-under-test info (version, type, edition, build).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub server: Option<serde_json::Value>,
}

impl ReportEnvironmentMetadata {
    pub fn is_empty(&self) -> bool {
        self.tool.is_none() && self.server.is_none()
    }

    #[allow(dead_code)]
    pub fn to_json(&self) -> Option<JsonValue> {
        if self.is_empty() {
            None
        } else {
            serde_json::to_value(self).ok()
        }
    }

    pub fn from_json(value: Option<&JsonValue>) -> Self {
        value
            .and_then(|v| serde_json::from_value(v.clone()).ok())
            .unwrap_or_default()
    }
}

/// Report status enum.
///
/// Only two states: a report is either still receiving data or fully done.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub enum ReportStatus {
    InProgress,
    Completed,
}

impl ReportStatus {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::InProgress => "in_progress",
            Self::Completed => "completed",
        }
    }

    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "in_progress" => Some(Self::InProgress),
            "completed" => Some(Self::Completed),
            _ => None,
        }
    }
}

impl std::fmt::Display for ReportStatus {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// Test framework enum.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "lowercase")]
pub enum Framework {
    Playwright,
    Cypress,
    Detox,
}

impl Framework {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Playwright => "playwright",
            Self::Cypress => "cypress",
            Self::Detox => "detox",
        }
    }

    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "playwright" => Some(Self::Playwright),
            "cypress" => Some(Self::Cypress),
            "detox" => Some(Self::Detox),
            _ => None,
        }
    }
}

impl std::fmt::Display for Framework {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// Test statistics for a report.
#[derive(Debug, Clone, Default, Serialize, ToSchema)]
pub struct TestStats {
    /// Total number of tests.
    pub total: i32,
    /// Number of passed tests.
    pub passed: i32,
    /// Number of failed tests.
    pub failed: i32,
    /// Number of skipped tests.
    pub skipped: i32,
    /// Number of flaky tests.
    pub flaky: i32,
    /// Total duration in milliseconds (from JSON stats, null if not available).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub duration_ms: Option<i64>,
    /// Wall clock duration in milliseconds (parallel execution time).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub wall_clock_ms: Option<i64>,
}

/// Report summary for list responses.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ReportSummary {
    /// Report UUID.
    pub id: Uuid,
    /// Short ID for display (timestamp portion of UUIDv7).
    pub short_id: String,
    /// Report status.
    pub status: ReportStatus,
    /// Test framework.
    pub framework: Framework,
    /// User-defined report name for grouping (e.g., "playwright-full-enterprise-master").
    pub name: String,
    /// Number of completed reports.
    pub reports_complete: i32,
    /// Test statistics aggregated from all reports.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub test_stats: Option<TestStats>,
    /// Repository (e.g., "org/repo").
    pub repository: String,
    /// Branch name (stored as-is, e.g., "main").
    pub branch: String,
    /// Commit SHA.
    pub commit: String,
    /// GitHub Actions run ID.
    pub gh_run_id: String,
    /// GitHub Actions run attempt.
    pub gh_run_attempt: String,
    /// PR number.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_pr_number: Option<i32>,
    /// Environment metadata (tool + server info).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment_metadata: Option<ReportEnvironmentMetadata>,
    /// Creation timestamp.
    pub created_at: DateTime<Utc>,
}

/// Report group detail response including reports.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ReportDetailResponse {
    /// Report UUID.
    pub id: Uuid,
    /// Report status.
    pub status: ReportStatus,
    /// Test framework.
    pub framework: Framework,
    /// User-defined report name.
    pub name: String,
    /// Repository (e.g., "org/repo").
    pub repository: String,
    /// Branch name (stored as-is, e.g., "main").
    pub branch: String,
    /// Commit SHA.
    pub commit: String,
    /// GitHub Actions run ID.
    pub gh_run_id: String,
    /// GitHub Actions run attempt.
    pub gh_run_attempt: String,
    /// PR number.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_pr_number: Option<i32>,
    /// Environment metadata (tool + server info).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment_metadata: Option<ReportEnvironmentMetadata>,
    /// Creation timestamp.
    pub created_at: DateTime<Utc>,
    /// Last update timestamp.
    pub updated_at: DateTime<Utc>,
    /// Individual reports in this report group (ordered by creation time).
    pub reports: Vec<UploadSummary>,
}

/// Report list response with pagination.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ReportListResponse {
    /// List of reports.
    pub reports: Vec<ReportSummary>,
    /// Total number of reports matching filter.
    pub total: i64,
    /// Limit used.
    pub limit: i32,
    /// Offset used.
    pub offset: i32,
}

/// Individual report summary (single upload, not grouped).
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct IndividualReportSummary {
    /// Report UUID.
    pub id: Uuid,
    /// Short ID for display.
    pub short_id: String,
    /// Report group UUID.
    pub report_group_id: Uuid,
    /// User-defined report name.
    pub name: String,
    /// Processing status.
    pub status: String,
    /// GitHub Actions job ID.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_job_id: Option<String>,
    /// GitHub Actions job name / display name.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_job_name: Option<String>,
    /// Repository (from parent report group).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository: Option<String>,
    /// Branch (from parent report group).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub branch: Option<String>,
    /// Commit SHA (from parent report group).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub commit: Option<String>,
    /// Test statistics.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub test_stats: Option<TestStats>,
    /// Duration in milliseconds.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub duration_ms: Option<i64>,
    /// Creation timestamp.
    pub created_at: DateTime<Utc>,
}

/// Response for individual reports list.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct IndividualReportListResponse {
    pub reports: Vec<IndividualReportSummary>,
    pub total: i64,
    pub limit: i32,
    pub offset: i32,
}

/// Query parameters for listing reports.
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct ListReportsQuery {
    /// Filter by framework.
    #[serde(default)]
    pub framework: Option<Framework>,
    /// Filter by report name.
    #[serde(default)]
    pub name: Option<String>,
    /// Filter by status.
    #[serde(default)]
    pub status: Option<ReportStatus>,
    /// Filter by repository (e.g., "org/repo").
    #[serde(default)]
    pub repository: Option<String>,
    /// Filter by branch (e.g., "main").
    #[serde(default)]
    pub branch: Option<String>,
    /// Filter by commit SHA (prefix-matched, min 7 chars).
    #[serde(default)]
    pub commit: Option<String>,
    /// Filter by GitHub run attempt.
    #[serde(default)]
    pub gh_run_attempt: Option<String>,
    /// Maximum results to return.
    #[serde(default = "default_limit")]
    pub limit: i32,
    /// Offset for pagination.
    #[serde(default)]
    pub offset: i32,
}

fn default_limit() -> i32 {
    20
}

/// Maximum number of runs shown per repository on the grouped landing page.
pub const MAX_RUNS_PER_REPO: usize = 10;

// --- Grouped Reports (landing page) ---

/// A single run entry within a repository group.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct RunEntry {
    pub report_id: Uuid,
    pub framework: Framework,
    pub name: String,
    pub status: ReportStatus,
    pub branch: String,
    pub commit: String,
    pub short_sha: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_number: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_attempt: Option<String>,
    pub gh_run_attempt: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_run_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_pr_number: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub test_stats: Option<TestStats>,
    pub created_at: DateTime<Utc>,
    pub url_path: String,
}

/// A repository group with its recent runs.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct RepositoryGroup {
    pub repository: String,
    pub repository_name: String,
    pub latest_run_at: DateTime<Utc>,
    pub runs: Vec<RunEntry>,
}

/// Response for the grouped reports landing page.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct GroupedReportsResponse {
    pub groups: Vec<RepositoryGroup>,
}

// --- Consolidated Results (filtered view) ---

/// Filter parameters for the consolidated view.
#[derive(Debug, Clone, Serialize, Deserialize, ToSchema)]
pub struct ConsolidatedFilters {
    pub repository: String,
    pub target_name: String,
    pub commit_sha: String,
    pub tool_name: String,
}

/// A single historical result for a spec across commits/attempts.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct SpecHistoryEntry {
    pub commit_sha: String,
    pub run_attempt: i32,
    pub status: String,
    pub duration_ms: i32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_message: Option<String>,
    pub created_at: String,
}

/// A single spec in the consolidated view.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ConsolidatedSpec {
    pub full_title: String,
    pub status: String,
    pub source_commit_sha: String,
    pub source_run_attempt: i32,
    pub is_from_latest: bool,
    pub duration_ms: i32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_message: Option<String>,
    /// Full result history for this spec, ordered newest first.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub history: Vec<SpecHistoryEntry>,
}

// --- Begin / Complete Endpoints ---

/// Request body for `POST /reports/begin` and `POST /reports/complete`.
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct ReportGroupingRequest {
    /// Repository (e.g., "org/repo"). Required.
    pub repository: String,
    /// Commit SHA. Required.
    pub commit: String,
    /// GitHub Actions run ID. Required.
    pub gh_run_id: String,
    /// Test framework. Required.
    pub framework: Framework,
    /// User-defined report name for grouping (e.g., "playwright-full-enterprise"). Required.
    pub name: String,
    /// PR number (required for pull_request events).
    #[serde(default)]
    pub gh_pr_number: Option<i32>,
}

/// Response from `POST /reports/begin`.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct BeginResponse {
    /// Report UUID.
    pub report_id: Uuid,
    /// Current report status (always `in_progress` after begin).
    pub status: ReportStatus,
    /// `true` if this call created the report; `false` if it already existed.
    pub created: bool,
}

/// Response from `POST /reports/complete`.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct CompleteResponse {
    /// Report UUID.
    pub report_id: Uuid,
    /// Current report status (always `completed` after complete).
    pub status: ReportStatus,
    /// Number of individual reports in this group.
    pub reports_count: i64,
}

/// Response for the consolidated results endpoint.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ConsolidatedResultsResponse {
    pub filters: ConsolidatedFilters,
    pub overall_status: String,
    pub total_specs: usize,
    pub passed: usize,
    pub failed: usize,
    pub skipped: usize,
    pub flaky: usize,
    pub contributing_reports: Vec<Uuid>,
    pub latest_commit_sha: String,
    pub latest_run_attempt: i32,
    pub available_run_attempts: Vec<i32>,
    /// Wall-clock duration in milliseconds (earliest start to latest end across all reports).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub duration_ms: Option<i64>,
    pub specs: Vec<ConsolidatedSpec>,
}

// ============================================================================
// Report Registration and Upload Types
// ============================================================================

/// Environment metadata for individual reports (stored as JSONB).
#[derive(Debug, Clone, Default, Serialize, Deserialize, ToSchema)]
pub struct EnvironmentMetadata {
    /// Operating system (e.g., linux, macos, windows).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub os: Option<String>,
    /// Browser name (e.g., chrome, firefox, safari).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub browser: Option<String>,
    /// Device name for mobile testing.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub device: Option<String>,
    /// Custom tags for categorization.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub tags: Vec<String>,
}

impl EnvironmentMetadata {
    pub fn is_empty(&self) -> bool {
        self.os.is_none() && self.browser.is_none() && self.device.is_none() && self.tags.is_empty()
    }

    pub fn to_json(&self) -> Option<JsonValue> {
        if self.is_empty() {
            None
        } else {
            serde_json::to_value(self).ok()
        }
    }
}

/// Report processing status enum.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub enum UploadStatus {
    /// Report initialized, waiting for uploads and/or JSON data.
    Pending,
    /// Test data extraction in progress.
    Processing,
    /// Extraction complete.
    Complete,
    /// Report failed.
    Failed,
}

impl UploadStatus {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::Processing => "processing",
            Self::Complete => "complete",
            Self::Failed => "failed",
        }
    }

    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "pending" => Some(Self::Pending),
            "processing" => Some(Self::Processing),
            "complete" => Some(Self::Complete),
            "failed" => Some(Self::Failed),
            _ => None,
        }
    }
}

impl std::fmt::Display for UploadStatus {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// File that was rejected during validation.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct RejectedFile {
    /// File path.
    pub path: String,
    /// Rejection reason.
    pub reason: String,
}

/// Upload summary for report detail responses.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct UploadSummary {
    /// Upload UUID.
    pub id: Uuid,
    /// Short ID for display (timestamp portion of UUIDv7).
    pub short_id: String,
    /// GitHub Actions job ID (for idempotency).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_job_id: Option<String>,
    /// GitHub Actions job name.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_job_name: Option<String>,
    /// UI display name (gh_job_name or "Report N").
    pub display_name: String,
    /// Upload processing status.
    pub status: UploadStatus,
}

/// Screenshot file to upload (in register request).
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct ScreenshotToUpload {
    /// Relative file path (e.g., "test-name/screenshot1.png").
    pub path: String,
    /// Expected file size in bytes.
    #[serde(default)]
    pub size: Option<i64>,
    /// MIME content type (optional, will be inferred from extension).
    #[serde(default)]
    pub content_type: Option<String>,
}

/// Screenshot that was accepted for upload.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct AcceptedScreenshot {
    /// Relative file path.
    pub path: String,
    /// S3 object key.
    pub s3_key: String,
    /// Test name extracted from path.
    pub test_name: String,
}

/// Response after uploading screenshots.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ScreenshotUploadResponse {
    /// Report UUID.
    pub report_id: Uuid,
    /// Number of screenshots uploaded in this request.
    pub files_uploaded: u64,
    /// Total screenshots uploaded so far.
    pub total_uploaded: u64,
    /// Total screenshots expected for this report.
    pub total_expected: u64,
    /// True if all screenshots have been uploaded.
    pub all_uploaded: bool,
}

/// JSON file to upload (in register request).
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct JsonFileToUpload {
    /// Relative file path (e.g., "results.json", "test-output/report.json").
    pub path: String,
    /// Expected file size in bytes.
    #[serde(default)]
    pub size: Option<i64>,
    /// MIME content type (optional, defaults to application/json).
    #[serde(default)]
    pub content_type: Option<String>,
}

/// JSON file that was accepted for upload.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct AcceptedJsonFile {
    /// Relative file path.
    pub path: String,
    /// S3 object key.
    pub s3_key: String,
}

/// Response after uploading JSON files.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct JsonUploadResponse {
    /// Report UUID.
    pub report_id: Uuid,
    /// Number of JSON files uploaded in this request.
    pub files_uploaded: u64,
    /// Total JSON files uploaded so far.
    pub total_uploaded: u64,
    /// Total JSON files expected for this report.
    pub total_expected: u64,
    /// True if all JSON files have been uploaded.
    pub all_uploaded: bool,
    /// True if extraction has been triggered (all JSON files uploaded).
    pub extraction_triggered: bool,
}

/// Request body for report registration.
///
/// Auto-creates a report group (by grouping key) and a report in a single call.
/// Optionally declares files for upload.
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct RegisterReportRequest {
    /// Repository (e.g., "org/repo"). Required.
    pub repository: String,
    /// Commit SHA. Required.
    pub commit: String,
    /// GitHub Actions run ID. Required.
    pub gh_run_id: String,
    /// Test framework. Required.
    pub framework: Framework,
    /// User-defined report name for grouping (e.g., "playwright-full-enterprise"). Required.
    pub name: String,
    /// GitHub Actions job ID (for idempotency). Required.
    pub gh_job_id: String,
    /// Human-readable name for UI display. Required.
    pub gh_job_name: String,
    /// Branch name (optional — derived from OIDC claims for PR events).
    #[serde(default)]
    pub branch: Option<String>,
    /// PR number (required for pull_request events).
    #[serde(default)]
    pub gh_pr_number: Option<i32>,
    /// Report-level environment metadata (tool + server info).
    #[serde(default)]
    pub environment_metadata: Option<ReportEnvironmentMetadata>,
    /// Report-level environment metadata (os, browser, device, tags).
    #[serde(default)]
    pub environment: Option<EnvironmentMetadata>,
    /// JSON files to upload.
    #[serde(default)]
    pub json_files: Option<Vec<JsonFileToUpload>>,
    /// Screenshot files to upload.
    #[serde(default)]
    pub screenshots: Option<Vec<ScreenshotToUpload>>,
}

/// Response from report registration.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct RegisterReportResponse {
    /// Report UUID.
    pub report_id: Uuid,
    /// Upload ID (opaque, used for upload endpoints).
    pub upload_id: Uuid,
    /// True if returning an existing report (idempotent replay).
    pub is_existing: bool,
    /// Current report status.
    pub report_status: ReportStatus,
    /// Number of reports in this group (including the one just created).
    pub reports_in_group: i64,
    /// JSON files accepted for upload.
    pub accepted_json_files: Vec<AcceptedJsonFile>,
    /// JSON files that were rejected.
    pub rejected_json_files: Vec<RejectedFile>,
    /// Screenshots accepted for upload.
    pub accepted_screenshots: Vec<AcceptedScreenshot>,
    /// Screenshots that were rejected.
    pub rejected_screenshots: Vec<RejectedFile>,
}
