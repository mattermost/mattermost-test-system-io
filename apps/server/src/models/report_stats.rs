//! Report statistics model.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use utoipa::ToSchema;
use uuid::Uuid;

/// Summary statistics for a report.
#[derive(Debug, Clone, Serialize, Deserialize, ToSchema)]
pub struct ReportStats {
    /// Associated report ID
    #[serde(skip_serializing)]
    pub report_id: Uuid,
    /// Test run start time
    pub start_time: DateTime<Utc>,
    /// Total duration in milliseconds
    pub duration_ms: i64,
    /// Count of expected (passed) tests
    pub expected: i32,
    /// Count of skipped tests
    pub skipped: i32,
    /// Count of unexpected (failed) tests
    pub unexpected: i32,
    /// Count of flaky tests
    pub flaky: i32,
}

impl ReportStats {
    /// Create new report stats.
    pub fn new(
        report_id: Uuid,
        start_time: DateTime<Utc>,
        duration_ms: i64,
        expected: i32,
        skipped: i32,
        unexpected: i32,
        flaky: i32,
    ) -> Self {
        ReportStats {
            report_id,
            start_time,
            duration_ms,
            expected,
            skipped,
            unexpected,
            flaky,
        }
    }
}
