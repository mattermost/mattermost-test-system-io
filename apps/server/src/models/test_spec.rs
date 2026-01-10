//! Test spec model representing an individual test specification.

use serde::{Deserialize, Serialize};

use super::TestResult;

/// Test specification representing an individual test.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TestSpec {
    /// Internal ID
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<i64>,
    /// Associated suite ID
    #[serde(skip_serializing)]
    pub suite_id: i64,
    /// Test title
    pub title: String,
    /// Whether the test passed (ok = true)
    pub ok: bool,
    /// Playwright's spec ID
    pub spec_id: String,
    /// Path to test file
    pub file_path: String,
    /// Line number in file
    pub line: i32,
    /// Column number in file
    pub column: i32,
}

impl TestSpec {
    /// Create a new test spec.
    pub fn new(
        suite_id: i64,
        title: String,
        ok: bool,
        spec_id: String,
        file_path: String,
        line: i32,
        column: i32,
    ) -> Self {
        TestSpec {
            id: None,
            suite_id,
            title,
            ok,
            spec_id,
            file_path,
            line,
            column,
        }
    }
}

/// Test spec with its results for API response.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TestSpecWithResults {
    pub id: i64,
    pub title: String,
    pub ok: bool,
    pub spec_id: String,
    pub file_path: String,
    pub line: i32,
    pub column: i32,
    pub results: Vec<TestResult>,
}

/// Response for test spec list with results.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TestSpecListResponse {
    pub specs: Vec<TestSpecWithResults>,
}
