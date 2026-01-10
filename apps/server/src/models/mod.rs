//! Domain models for the Rust Report Viewer.

pub mod github_context;
pub mod report;
pub mod report_stats;
pub mod test_result;
pub mod test_spec;
pub mod test_suite;

// Re-export commonly used types
pub use github_context::GitHubContext;
pub use report::{ExtractionStatus, Report, ReportDetail, ReportSummary};
pub use report_stats::ReportStats;
pub use test_result::{TestResult, TestStatus};
pub use test_spec::{TestSpec, TestSpecListResponse, TestSpecWithResults};
pub use test_suite::{TestSuite, TestSuiteListResponse};

/// Pagination parameters.
#[derive(Debug, Clone, serde::Deserialize)]
pub struct PaginationParams {
    #[serde(default = "default_page")]
    pub page: u32,
    #[serde(default = "default_limit")]
    pub limit: u32,
}

fn default_page() -> u32 {
    1
}

fn default_limit() -> u32 {
    100
}

impl PaginationParams {
    /// Calculate the offset for database queries.
    pub fn offset(&self) -> u32 {
        (self.page.saturating_sub(1)) * self.limit
    }

    /// Clamp limit to maximum allowed value.
    pub fn clamped_limit(&self) -> u32 {
        self.limit.min(100)
    }
}

/// Pagination metadata for responses.
#[derive(Debug, Clone, serde::Serialize)]
pub struct Pagination {
    pub page: u32,
    pub limit: u32,
    pub total: u64,
    pub total_pages: u32,
}

impl Pagination {
    /// Create pagination metadata.
    pub fn new(page: u32, limit: u32, total: u64) -> Self {
        let total_pages = if total == 0 {
            0
        } else {
            ((total as f64) / (limit as f64)).ceil() as u32
        };

        Pagination {
            page,
            limit,
            total,
            total_pages,
        }
    }
}
