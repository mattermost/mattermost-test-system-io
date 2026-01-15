//! Test suite model representing a test file.

use serde::{Deserialize, Serialize};
use utoipa::ToSchema;
use uuid::Uuid;

/// Test suite representing a test file from results.json.
#[derive(Debug, Clone, Serialize, Deserialize, ToSchema)]
pub struct TestSuite {
    /// Internal ID
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<i64>,
    /// Associated report ID
    #[serde(skip_serializing)]
    pub report_id: Uuid,
    /// Suite title (usually file name)
    pub title: String,
    /// Path to test file
    pub file_path: String,
    /// Number of specs in this suite (computed)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub specs_count: Option<i32>,
    /// Number of passed specs (computed)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub passed_count: Option<i32>,
    /// Number of failed specs (computed)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub failed_count: Option<i32>,
    /// Number of flaky specs (passed but had failures, computed)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub flaky_count: Option<i32>,
    /// Number of skipped specs (computed)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub skipped_count: Option<i32>,
    /// Total duration in milliseconds (computed from test results)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub duration_ms: Option<i64>,
}

impl TestSuite {
    /// Create a new test suite.
    pub fn new(report_id: Uuid, title: String, file_path: String) -> Self {
        TestSuite {
            id: None,
            report_id,
            title,
            file_path,
            specs_count: None,
            passed_count: None,
            failed_count: None,
            flaky_count: None,
            skipped_count: None,
            duration_ms: None,
        }
    }
}

/// Response for test suite list.
#[derive(Debug, Clone, Serialize, Deserialize, ToSchema)]
pub struct TestSuiteListResponse {
    pub suites: Vec<TestSuite>,
}
