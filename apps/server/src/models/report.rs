//! Report domain models and DTOs.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;
use utoipa::ToSchema;
use uuid::Uuid;

/// GitHub metadata for reports (stored as JSONB).
///
/// Field names are aligned with GitHub OIDC token claims.
/// See: <https://docs.github.com/en/actions/security-for-github-actions/security-hardening-your-deployments/about-security-hardening-with-openid-connect>
#[derive(Debug, Clone, Default, Serialize, Deserialize, ToSchema)]
pub struct GitHubMetadata {
    /// Subject (e.g., "repo:org/repo:ref:refs/heads/main").
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sub: Option<String>,
    /// Repository (e.g., "octo-org/octo-repo").
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository: Option<String>,
    /// Repository owner (e.g., "octo-org").
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_owner: Option<String>,
    /// Repository owner numeric ID.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_owner_id: Option<String>,
    /// Repository visibility ("public", "private", "internal").
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_visibility: Option<String>,
    /// Repository numeric ID.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_id: Option<String>,
    /// Actor (GitHub username who triggered the workflow).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub actor: Option<String>,
    /// Actor numeric ID.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub actor_id: Option<String>,
    /// Git ref (e.g., "refs/heads/main") — stored as-is.
    #[serde(rename = "ref", skip_serializing_if = "Option::is_none")]
    pub git_ref: Option<String>,
    /// Ref type ("branch" or "tag").
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ref_type: Option<String>,
    /// Commit SHA.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sha: Option<String>,
    /// Workflow name.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub workflow: Option<String>,
    /// Event name (e.g., "push", "pull_request", "workflow_dispatch").
    #[serde(skip_serializing_if = "Option::is_none")]
    pub event_name: Option<String>,
    /// Run ID.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_id: Option<String>,
    /// Run number (increments per workflow).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_number: Option<String>,
    /// Run attempt number.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_attempt: Option<String>,
    /// Runner environment (e.g., "github-hosted").
    #[serde(skip_serializing_if = "Option::is_none")]
    pub runner_environment: Option<String>,
    /// Head ref (source branch for PRs).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub head_ref: Option<String>,
    /// Base ref (target branch for PRs).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub base_ref: Option<String>,
    /// Full workflow ref (e.g., "org/repo/.github/workflows/ci.yml@refs/heads/main").
    #[serde(skip_serializing_if = "Option::is_none")]
    pub job_workflow_ref: Option<String>,
    /// PR number (not from OIDC — provided by the client).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr_number: Option<i32>,
}

impl GitHubMetadata {
    pub fn is_empty(&self) -> bool {
        self.sub.is_none()
            && self.repository.is_none()
            && self.repository_owner.is_none()
            && self.repository_owner_id.is_none()
            && self.repository_visibility.is_none()
            && self.repository_id.is_none()
            && self.actor.is_none()
            && self.actor_id.is_none()
            && self.git_ref.is_none()
            && self.sha.is_none()
            && self.workflow.is_none()
            && self.event_name.is_none()
            && self.run_id.is_none()
            && self.run_number.is_none()
            && self.run_attempt.is_none()
            && self.runner_environment.is_none()
            && self.head_ref.is_none()
            && self.base_ref.is_none()
            && self.job_workflow_ref.is_none()
            && self.pr_number.is_none()
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
    /// GitHub metadata (stored as JSONB).
    #[serde(default)]
    pub github_metadata: Option<GitHubMetadata>,
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
    /// GitHub metadata (caller-supplied).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub github_metadata: Option<GitHubMetadata>,
    /// OIDC claims (token-derived, stored separately).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub oidc_claims: Option<super::report_oidc_claim::ReportOidcClaimsResponse>,
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
    /// GitHub metadata (caller-supplied).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub github_metadata: Option<GitHubMetadata>,
    /// OIDC claims (token-derived, stored separately).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub oidc_claims: Option<super::report_oidc_claim::ReportOidcClaimsResponse>,
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
    /// Filter by GitHub repository (e.g., "org/repo").
    #[serde(default)]
    pub repository: Option<String>,
    /// Filter by git ref (e.g., "refs/heads/main").
    #[serde(rename = "ref", default)]
    pub git_ref: Option<String>,
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
