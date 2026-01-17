//! Test result model representing individual test execution results.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use utoipa::ToSchema;

/// Test execution status.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "lowercase")]
pub enum TestStatus {
    Passed,
    Failed,
    Skipped,
    #[serde(rename = "timedOut")]
    TimedOut,
}

impl TestStatus {
    /// Convert to database string representation.
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Passed => "passed",
            Self::Failed => "failed",
            Self::Skipped => "skipped",
            Self::TimedOut => "timedOut",
        }
    }

    /// Parse from string representation.
    pub fn parse(s: &str) -> Self {
        match s {
            "passed" => Self::Passed,
            "failed" => Self::Failed,
            "skipped" => Self::Skipped,
            "timedOut" => Self::TimedOut,
            _ => Self::Failed, // Default to failed for unknown statuses
        }
    }
}

impl std::fmt::Display for TestStatus {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// Individual test execution result.
#[derive(Debug, Clone, Serialize, Deserialize, ToSchema)]
pub struct TestResult {
    /// Internal ID
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<i64>,
    /// Associated spec ID
    #[serde(skip_serializing)]
    pub spec_id: i64,
    /// Execution status
    pub status: TestStatus,
    /// Execution duration in milliseconds
    pub duration_ms: i64,
    /// Retry attempt number (0 = first attempt)
    pub retry: i32,
    /// Execution start time
    pub start_time: DateTime<Utc>,
    /// Playwright project ID
    pub project_id: String,
    /// Playwright project name
    pub project_name: String,
    /// JSON array of error objects (if any)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub errors_json: Option<String>,
}

impl TestResult {
    /// Create a new test result.
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        spec_id: i64,
        status: TestStatus,
        duration_ms: i64,
        retry: i32,
        start_time: DateTime<Utc>,
        project_id: String,
        project_name: String,
        errors_json: Option<String>,
    ) -> Self {
        TestResult {
            id: None,
            spec_id,
            status,
            duration_ms,
            retry,
            start_time,
            project_id,
            project_name,
            errors_json,
        }
    }
}
