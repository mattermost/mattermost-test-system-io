//! Report model representing uploaded test reports.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use utoipa::ToSchema;
use uuid::Uuid;

use super::github_context::GitHubContext;

/// Detox platform (iOS or Android).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "lowercase")]
pub enum DetoxPlatform {
    Ios,
    Android,
}

impl DetoxPlatform {
    /// Convert to string representation.
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Ios => "ios",
            Self::Android => "android",
        }
    }
}

impl std::fmt::Display for DetoxPlatform {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// Extraction status for a report.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "lowercase")]
pub enum ExtractionStatus {
    Pending,
    Completed,
    Failed,
}

impl ExtractionStatus {
    /// Convert from database string representation.
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "pending" => Some(Self::Pending),
            "completed" => Some(Self::Completed),
            "failed" => Some(Self::Failed),
            _ => None,
        }
    }

    /// Convert to database string representation.
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::Completed => "completed",
            Self::Failed => "failed",
        }
    }
}

impl std::fmt::Display for ExtractionStatus {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// Report entity representing an uploaded test report.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Report {
    /// Unique identifier (UUID v4)
    pub id: Uuid,
    /// Upload timestamp
    pub created_at: DateTime<Utc>,
    /// Soft delete timestamp (None if active)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub deleted_at: Option<DateTime<Utc>>,
    /// Current extraction status
    pub extraction_status: ExtractionStatus,
    /// Relative path to report directory
    pub file_path: String,
    /// Test framework name (playwright, cypress, detox, etc.)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub framework: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub framework_version: Option<String>,
    /// Platform for Detox reports (ios/android)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub platform: Option<String>,
    /// Error message if extraction failed
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_message: Option<String>,
    /// Whether the report files still exist on disk
    pub has_files: bool,
    /// Timestamp when files were deleted (None if files still exist)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub files_deleted_at: Option<DateTime<Utc>>,
    /// GitHub Actions context metadata
    #[serde(skip_serializing_if = "Option::is_none")]
    pub github_context: Option<GitHubContext>,
}

impl Report {
    /// Create a new report with pending extraction status.
    /// The ID is auto-generated and used as the file_path (directory name).
    pub fn new_with_id() -> Self {
        let id = Uuid::now_v7();
        Report {
            id,
            created_at: Utc::now(),
            deleted_at: None,
            extraction_status: ExtractionStatus::Pending,
            file_path: id.to_string(),
            framework: None,
            framework_version: None,
            platform: None,
            error_message: None,
            has_files: true,
            files_deleted_at: None,
            github_context: None,
        }
    }
}

/// Summary view of a report for list responses.
#[derive(Debug, Clone, Serialize, Deserialize, ToSchema)]
pub struct ReportSummary {
    pub id: Uuid,
    pub created_at: DateTime<Utc>,
    pub extraction_status: ExtractionStatus,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub framework: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub framework_version: Option<String>,
    /// Platform for Detox reports (ios/android)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub platform: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stats: Option<super::report_stats::ReportStats>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub github_context: Option<GitHubContext>,
}

impl From<Report> for ReportSummary {
    fn from(report: Report) -> Self {
        ReportSummary {
            id: report.id,
            created_at: report.created_at,
            extraction_status: report.extraction_status,
            framework: report.framework,
            framework_version: report.framework_version,
            platform: report.platform,
            stats: None,
            github_context: report.github_context,
        }
    }
}

/// Detailed view of a report.
#[derive(Debug, Clone, Serialize, Deserialize, ToSchema)]
pub struct ReportDetail {
    pub id: Uuid,
    pub created_at: DateTime<Utc>,
    pub extraction_status: ExtractionStatus,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error_message: Option<String>,
    pub file_path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub framework: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub framework_version: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stats: Option<super::report_stats::ReportStats>,
    /// Whether the report originally had files from upload
    pub has_files: bool,
    /// Timestamp when files were deleted (None if files still exist)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub files_deleted_at: Option<DateTime<Utc>>,
    /// GitHub Actions context metadata
    #[serde(skip_serializing_if = "Option::is_none")]
    pub github_context: Option<GitHubContext>,
}

impl From<Report> for ReportDetail {
    fn from(report: Report) -> Self {
        ReportDetail {
            id: report.id,
            created_at: report.created_at,
            extraction_status: report.extraction_status,
            error_message: report.error_message,
            file_path: report.file_path,
            framework: report.framework,
            framework_version: report.framework_version,
            stats: None,
            has_files: report.has_files,
            files_deleted_at: report.files_deleted_at,
            github_context: report.github_context,
        }
    }
}
