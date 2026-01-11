//! Detox job model representing an individual job within a test report.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

/// Detox job entity representing a single parallel test execution job.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DetoxJob {
    /// Unique identifier (UUID v7)
    pub id: Uuid,
    /// Associated report ID (foreign key to reports)
    pub report_id: Uuid,
    /// Job name as identifier (e.g., "ios-results-vf2ywez97e-1")
    pub job_name: String,
    /// Creation timestamp
    pub created_at: DateTime<Utc>,
    /// Number of tests in this job
    pub tests_count: i32,
    /// Number of passed tests
    pub passed_count: i32,
    /// Number of failed tests
    pub failed_count: i32,
    /// Number of skipped tests
    pub skipped_count: i32,
    /// Duration in milliseconds
    pub duration_ms: i64,
}

impl DetoxJob {
    /// Create a new Detox job.
    pub fn new(report_id: Uuid, job_name: String) -> Self {
        DetoxJob {
            id: Uuid::now_v7(),
            report_id,
            job_name,
            created_at: Utc::now(),
            tests_count: 0,
            passed_count: 0,
            failed_count: 0,
            skipped_count: 0,
            duration_ms: 0,
        }
    }
}
