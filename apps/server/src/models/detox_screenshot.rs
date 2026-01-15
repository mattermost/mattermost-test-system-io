//! Detox screenshot model representing screenshots captured during test execution.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

/// Screenshot type captured during test execution.
/// Only high-priority types are saved: testFnFailure (failure evidence) and testStart (initial state).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum ScreenshotType {
    /// Screenshot taken on test failure - highest priority
    TestFnFailure,
    /// Screenshot taken at test start - shows initial state
    TestStart,
}

impl ScreenshotType {
    /// Convert from database string representation.
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "testFnFailure" => Some(Self::TestFnFailure),
            "testStart" => Some(Self::TestStart),
            _ => None,
        }
    }

    /// Convert to database string representation.
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::TestFnFailure => "testFnFailure",
            Self::TestStart => "testStart",
        }
    }
}

impl std::fmt::Display for ScreenshotType {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// Detox screenshot entity representing a screenshot captured during test execution.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DetoxScreenshot {
    /// Internal auto-increment ID
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<i64>,
    /// Parent job ID (foreign key to detox_jobs)
    pub job_id: Uuid,
    /// Full test name for matching (e.g., "Account - Settings - About should display version")
    pub test_full_name: String,
    /// Type of screenshot (testFnFailure or testStart)
    pub screenshot_type: ScreenshotType,
    /// Relative path within report files
    pub file_path: String,
    /// Creation timestamp
    pub created_at: DateTime<Utc>,
    /// Soft delete timestamp (None if file still exists)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub deleted_at: Option<DateTime<Utc>>,
}

impl DetoxScreenshot {
    /// Create a new Detox screenshot.
    pub fn new(
        job_id: Uuid,
        test_full_name: String,
        screenshot_type: ScreenshotType,
        file_path: String,
    ) -> Self {
        DetoxScreenshot {
            id: None,
            job_id,
            test_full_name,
            screenshot_type,
            file_path,
            created_at: Utc::now(),
            deleted_at: None,
        }
    }
}
