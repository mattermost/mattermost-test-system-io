//! Job domain models and DTOs.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use utoipa::ToSchema;
use uuid::Uuid;

/// GitHub metadata for jobs (stored as JSONB).
#[derive(Debug, Clone, Default, Serialize, Deserialize, ToSchema)]
pub struct JobGitHubMetadata {
    /// GitHub Actions job ID (for idempotency).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub job_id: Option<String>,
    /// Human-readable job name for UI display.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub job_name: Option<String>,
}

impl JobGitHubMetadata {
    pub fn is_empty(&self) -> bool {
        self.job_id.is_none() && self.job_name.is_none()
    }

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

/// Environment metadata for jobs (stored as JSONB).
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

    pub fn from_json(value: Option<&JsonValue>) -> Self {
        value
            .and_then(|v| serde_json::from_value(v.clone()).ok())
            .unwrap_or_default()
    }
}

/// Job status enum.
///
/// Upload completion is tracked separately via `html_uploaded` and `screenshots_uploaded` fields.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub enum JobStatus {
    /// Job initialized, waiting for uploads and/or JSON data.
    Pending,
    /// Test data extraction in progress.
    Processing,
    /// Extraction complete.
    Complete,
    /// Job failed.
    Failed,
}

impl JobStatus {
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

impl std::fmt::Display for JobStatus {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// Upload status for HTML files and screenshots.
///
/// Tracks the progress of file uploads independently of job processing status.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub enum UploadStatus {
    /// Upload has been initiated (init endpoint called).
    Started,
    /// All files have been successfully uploaded.
    Completed,
    /// Upload failed due to an error.
    Failed,
    /// Upload timed out (files not received within expected time).
    Timedout,
}

impl UploadStatus {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Started => "started",
            Self::Completed => "completed",
            Self::Failed => "failed",
            Self::Timedout => "timedout",
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

/// Request to initialize a new job (without files).
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct InitJobRequest {
    /// GitHub metadata (job_id, job_name).
    #[serde(default)]
    pub github_metadata: Option<JobGitHubMetadata>,
    /// Environment metadata.
    #[serde(default)]
    pub environment: Option<EnvironmentMetadata>,
}

/// Response after initializing a job.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct InitJobResponse {
    /// Job UUID (UUIDv7, time-ordered).
    pub job_id: Uuid,
    /// True if returning existing job (idempotent).
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub is_existing: bool,
}

// ============================================================================
// HTML File Upload Types (request-then-transfer pattern)
// ============================================================================

/// HTML file to upload (in init request).
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct HtmlFileToUpload {
    /// Relative file path (e.g., "index.html", "assets/style.css").
    pub path: String,
    /// Expected file size in bytes.
    #[serde(default)]
    pub size: Option<i64>,
    /// MIME content type (optional, will be inferred from extension).
    #[serde(default)]
    pub content_type: Option<String>,
}

/// Request to initialize HTML file uploads for a job.
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct InitHtmlRequest {
    /// List of HTML files to upload.
    pub files: Vec<HtmlFileToUpload>,
}

/// HTML file that was accepted for upload.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct AcceptedHtmlFile {
    /// Relative file path.
    pub path: String,
    /// S3 object key.
    pub s3_key: String,
}

/// Response after initializing HTML file uploads.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct InitHtmlResponse {
    /// Job UUID.
    pub job_id: Uuid,
    /// HTML files accepted for upload.
    pub accepted_files: Vec<AcceptedHtmlFile>,
    /// HTML files that were rejected (invalid extension, etc.).
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub rejected_files: Vec<RejectedFile>,
}

/// Response after uploading HTML files.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct HtmlUploadResponse {
    /// Job UUID.
    pub job_id: Uuid,
    /// Number of HTML files uploaded in this request.
    pub files_uploaded: u64,
    /// Total HTML files uploaded so far.
    pub total_uploaded: u64,
    /// Total HTML files expected for this job.
    pub total_expected: u64,
    /// True if all HTML files have been uploaded.
    pub all_uploaded: bool,
}

/// HTML upload progress response.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct HtmlUploadProgress {
    /// Job UUID.
    pub job_id: Uuid,
    /// Number of HTML files uploaded.
    pub uploaded: u64,
    /// Total HTML files expected.
    pub total: u64,
    /// True if all HTML files have been uploaded.
    pub all_uploaded: bool,
}

/// Job status response.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct JobStatusResponse {
    /// Job UUID.
    pub job_id: Uuid,
    /// Job status.
    pub status: JobStatus,
    /// Error message if failed.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_message: Option<String>,
    /// Number of files uploaded for this job.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub files_uploaded: Option<u64>,
    /// Total number of files expected for this job.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub files_total: Option<u64>,
    /// Current report status.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub report_status: Option<super::report::ReportStatus>,
    /// Number of jobs with status=complete.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub jobs_complete: Option<i32>,
    /// Expected number of jobs.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub jobs_expected: Option<i32>,
}


/// Job summary for report detail responses.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct JobSummary {
    /// Job UUID.
    pub id: Uuid,
    /// Short ID for display (timestamp portion of UUIDv7).
    pub short_id: String,
    /// GitHub metadata (job_id, job_name).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub github_metadata: Option<JobGitHubMetadata>,
    /// UI display name (github_job_name or "Job N").
    pub display_name: String,
    /// Job status.
    pub status: JobStatus,
    /// URL to view HTML report.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub html_url: Option<String>,
}

/// Detailed job response.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct JobDetailResponse {
    /// Job UUID.
    pub id: Uuid,
    /// Report UUID.
    pub report_id: Uuid,
    /// GitHub metadata (job_id, job_name).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub github_metadata: Option<JobGitHubMetadata>,
    /// Job status.
    pub status: JobStatus,
    /// URL to view HTML report.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub html_url: Option<String>,
    /// Environment metadata.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment: Option<EnvironmentMetadata>,
    /// Error message if failed.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_message: Option<String>,
    /// Creation timestamp.
    pub created_at: DateTime<Utc>,
    /// Last update timestamp.
    pub updated_at: DateTime<Utc>,
}

/// Job list response with pagination.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct JobListResponse {
    /// List of jobs.
    pub jobs: Vec<JobDetailResponse>,
    /// Total number of jobs matching filter.
    pub total: i64,
    /// Limit used.
    pub limit: i32,
    /// Offset used.
    pub offset: i32,
}

/// Query parameters for listing jobs.
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct QueryJobsParams {
    /// Filter by report ID.
    #[serde(default)]
    pub report_id: Option<Uuid>,
    /// Filter by status.
    #[serde(default)]
    pub status: Option<JobStatus>,
    /// Filter by GitHub job name.
    #[serde(default)]
    pub github_job_name: Option<String>,
    /// Filter from date.
    #[serde(default)]
    pub from_date: Option<DateTime<Utc>>,
    /// Filter to date.
    #[serde(default)]
    pub to_date: Option<DateTime<Utc>>,
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

/// Screenshot file to upload (in init request).
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

/// Request to initialize screenshot uploads for a job.
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct InitScreenshotsRequest {
    /// List of screenshot files to upload.
    pub files: Vec<ScreenshotToUpload>,
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

/// Response after initializing screenshot uploads.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct InitScreenshotsResponse {
    /// Job UUID.
    pub job_id: Uuid,
    /// Screenshots accepted for upload.
    pub accepted_files: Vec<AcceptedScreenshot>,
    /// Screenshots that were rejected (invalid extension, etc.).
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub rejected_files: Vec<RejectedFile>,
}

/// Response after uploading screenshots.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ScreenshotUploadResponse {
    /// Job UUID.
    pub job_id: Uuid,
    /// Number of screenshots uploaded in this request.
    pub files_uploaded: u64,
    /// Total screenshots uploaded so far.
    pub total_uploaded: u64,
    /// Total screenshots expected for this job.
    pub total_expected: u64,
    /// True if all screenshots have been uploaded.
    pub all_uploaded: bool,
}

// ============================================================================
// JSON File Upload Types (request-then-transfer pattern)
// ============================================================================

/// JSON file to upload (in init request).
/// Supports rich test data from Cypress, Detox, and Playwright JSON formats.
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

/// Request to initialize JSON file uploads for a job.
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct InitJsonRequest {
    /// List of JSON files to upload.
    pub files: Vec<JsonFileToUpload>,
}

/// JSON file that was accepted for upload.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct AcceptedJsonFile {
    /// Relative file path.
    pub path: String,
    /// S3 object key.
    pub s3_key: String,
}

/// Response after initializing JSON file uploads.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct InitJsonResponse {
    /// Job UUID.
    pub job_id: Uuid,
    /// JSON files accepted for upload.
    pub accepted_files: Vec<AcceptedJsonFile>,
    /// JSON files that were rejected (invalid extension, etc.).
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub rejected_files: Vec<RejectedFile>,
}

/// Response after uploading JSON files.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct JsonUploadResponse {
    /// Job UUID.
    pub job_id: Uuid,
    /// Number of JSON files uploaded in this request.
    pub files_uploaded: u64,
    /// Total JSON files uploaded so far.
    pub total_uploaded: u64,
    /// Total JSON files expected for this job.
    pub total_expected: u64,
    /// True if all JSON files have been uploaded.
    pub all_uploaded: bool,
    /// True if extraction has been triggered (all JSON files uploaded).
    pub extraction_triggered: bool,
}

/// JSON upload progress response.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct JsonUploadProgress {
    /// Job UUID.
    pub job_id: Uuid,
    /// Number of JSON files uploaded.
    pub uploaded: u64,
    /// Total JSON files expected.
    pub total: u64,
    /// True if all JSON files have been uploaded.
    pub all_uploaded: bool,
}
