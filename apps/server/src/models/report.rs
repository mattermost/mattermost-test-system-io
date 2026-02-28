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
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub enum ReportStatus {
    Initializing,
    Uploading,
    Processing,
    Complete,
    Failed,
}

impl ReportStatus {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Initializing => "initializing",
            Self::Uploading => "uploading",
            Self::Processing => "processing",
            Self::Complete => "complete",
            Self::Failed => "failed",
        }
    }

    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "initializing" => Some(Self::Initializing),
            "uploading" => Some(Self::Uploading),
            "processing" => Some(Self::Processing),
            "complete" => Some(Self::Complete),
            "failed" => Some(Self::Failed),
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

/// Request to register a new report.
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct RegisterReportRequest {
    /// Number of parallel jobs expected (1-100).
    pub expected_jobs: i32,
    /// Test framework.
    pub framework: Framework,
    /// Repository (e.g., "org/repo"). Required.
    pub repository: String,
    /// Branch or tag name (e.g., "main", "release-1.0"). Required.
    pub branch: String,
    /// Commit hash (e.g., "776e302abc..."). Required.
    pub commit: String,
    /// GitHub Actions run ID. Optional (empty string if not provided).
    #[serde(default)]
    pub run_id: Option<String>,
    /// PR number. Required when event is pull_request.
    #[serde(default)]
    pub pr_number: Option<i32>,
    /// Environment metadata — tool and server info (stored as JSONB).
    #[serde(default)]
    pub environment_metadata: Option<ReportEnvironmentMetadata>,
}

/// Response after registering a report.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct RegisterReportResponse {
    /// Report UUID.
    pub report_id: Uuid,
    /// Report status.
    pub status: ReportStatus,
    /// Expected number of jobs.
    pub expected_jobs: i32,
    /// Test framework.
    pub framework: Framework,
    /// Creation timestamp.
    pub created_at: DateTime<Utc>,
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
    /// Expected number of jobs.
    pub expected_jobs: i32,
    /// Number of completed jobs.
    pub jobs_complete: i32,
    /// Test statistics aggregated from all jobs.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub test_stats: Option<TestStats>,
    /// Repository (e.g., "org/repo").
    pub repository: String,
    /// Branch name (stored as-is, e.g., "main").
    pub branch: String,
    /// Commit SHA.
    pub commit: String,
    /// GitHub Actions run ID.
    pub run_id: String,
    /// PR number.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr_number: Option<i32>,
    /// OIDC claims (token-derived, stored separately).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub oidc_claims: Option<super::report_oidc_claim::ReportOidcClaimsResponse>,
    /// Environment metadata (tool + server info).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment_metadata: Option<ReportEnvironmentMetadata>,
    /// Creation timestamp.
    pub created_at: DateTime<Utc>,
}

/// Report detail response including jobs.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ReportDetailResponse {
    /// Report UUID.
    pub id: Uuid,
    /// Report status.
    pub status: ReportStatus,
    /// Test framework.
    pub framework: Framework,
    /// Expected number of jobs.
    pub expected_jobs: i32,
    /// Repository (e.g., "org/repo").
    pub repository: String,
    /// Branch name (stored as-is, e.g., "main").
    pub branch: String,
    /// Commit SHA.
    pub commit: String,
    /// GitHub Actions run ID.
    pub run_id: String,
    /// PR number.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr_number: Option<i32>,
    /// OIDC claims (token-derived, stored separately).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub oidc_claims: Option<super::report_oidc_claim::ReportOidcClaimsResponse>,
    /// Environment metadata (tool + server info).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment_metadata: Option<ReportEnvironmentMetadata>,
    /// Creation timestamp.
    pub created_at: DateTime<Utc>,
    /// Last update timestamp.
    pub updated_at: DateTime<Utc>,
    /// Jobs in this report (ordered by creation time).
    pub jobs: Vec<super::job::JobSummary>,
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

/// Query parameters for listing reports.
#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct ListReportsQuery {
    /// Filter by framework.
    #[serde(default)]
    pub framework: Option<Framework>,
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
    pub status: ReportStatus,
    pub branch: String,
    pub commit: String,
    pub short_sha: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_number: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_attempt: Option<String>,
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
    pub specs: Vec<ConsolidatedSpec>,
}
