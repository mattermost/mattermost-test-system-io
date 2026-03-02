//! WebSocket event types for real-time updates.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

/// WebSocket event sent to connected clients.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", content = "payload")]
#[serde(rename_all = "snake_case")]
pub enum WsEvent {
    /// A new report was created.
    ReportCreated(ReportCreatedPayload),
    /// A report was updated (status change, stats update).
    ReportUpdated(ReportUpdatedPayload),
    /// A new individual report was registered.
    ReportRegistered(ReportRegisteredPayload),
    /// An individual report was updated (status change).
    ReportEntryUpdated(ReportEntryUpdatedPayload),
    /// Test suites are now available for a report.
    SuitesAvailable(SuitesAvailablePayload),
}

/// Payload for report_created event.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReportCreatedPayload {
    pub report_id: Uuid,
    pub framework: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository: Option<String>,
    #[serde(rename = "ref", skip_serializing_if = "Option::is_none")]
    pub git_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sha: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub actor: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_run_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_pr_number: Option<i32>,
    pub created_at: DateTime<Utc>,
}

/// Payload for report_updated event.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReportUpdatedPayload {
    pub report_id: Uuid,
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub completed_reports: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub test_stats: Option<TestStatsPayload>,
    pub updated_at: DateTime<Utc>,
}

/// Payload for report_registered event.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReportRegisteredPayload {
    pub report_group_id: Uuid,
    pub report_id: Uuid,
    pub display_name: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_job_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gh_job_name: Option<String>,
    pub status: String,
    pub created_at: DateTime<Utc>,
}

/// Payload for report_entry_updated event.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReportEntryUpdatedPayload {
    pub report_group_id: Uuid,
    pub report_id: Uuid,
    pub status: String,
    pub updated_at: DateTime<Utc>,
}

/// Payload for suites_available event.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SuitesAvailablePayload {
    pub report_id: Uuid,
    pub suite_count: i32,
}

/// Test statistics included in report_updated events.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TestStatsPayload {
    pub passed: i32,
    pub failed: i32,
    pub skipped: i32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub flaky: Option<i32>,
    pub total: i32,
}

/// Wrapper that includes timestamp with every event.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WsEventMessage {
    #[serde(flatten)]
    pub event: WsEvent,
    pub timestamp: DateTime<Utc>,
}

impl WsEventMessage {
    /// Create a new event message with the current timestamp.
    pub fn new(event: WsEvent) -> Self {
        Self {
            event,
            timestamp: Utc::now(),
        }
    }
}

impl WsEvent {
    /// Create a simple report_created event with minimal fields.
    #[allow(dead_code)]
    pub fn report_created(report_id: Uuid) -> Self {
        WsEvent::ReportCreated(ReportCreatedPayload {
            report_id,
            framework: "playwright".to_string(),
            repository: None,
            git_ref: None,
            sha: None,
            actor: None,
            gh_run_id: None,
            gh_pr_number: None,
            created_at: Utc::now(),
        })
    }

    /// Create a report_updated event with status.
    pub fn report_updated(report_id: Uuid) -> Self {
        WsEvent::ReportUpdated(ReportUpdatedPayload {
            report_id,
            status: "updated".to_string(),
            completed_reports: None,
            test_stats: None,
            updated_at: Utc::now(),
        })
    }

    /// Create a report_registered event.
    pub fn report_registered(report_group_id: Uuid, report_id: Uuid) -> Self {
        WsEvent::ReportRegistered(ReportRegisteredPayload {
            report_group_id,
            report_id,
            display_name: format!("Report {}", &report_id.to_string()[..8]),
            gh_job_id: None,
            gh_job_name: None,
            status: "pending".to_string(),
            created_at: Utc::now(),
        })
    }

    /// Create a report_entry_updated event with status.
    pub fn report_entry_updated(report_group_id: Uuid, report_id: Uuid, status: String) -> Self {
        WsEvent::ReportEntryUpdated(ReportEntryUpdatedPayload {
            report_group_id,
            report_id,
            status,
            updated_at: Utc::now(),
        })
    }

    /// Create a suites_available event.
    pub fn suites_available(report_id: Uuid, suite_count: i32) -> Self {
        WsEvent::SuitesAvailable(SuitesAvailablePayload {
            report_id,
            suite_count,
        })
    }
}
