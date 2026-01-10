//! JSON extraction service for parsing Playwright results.json.

use chrono::{DateTime, Utc};
use rusqlite::Connection;
use serde::Deserialize;
use std::path::Path;
use tracing::{info, warn};
use uuid::Uuid;

use crate::db::queries;
use crate::error::{AppError, AppResult};
use crate::models::{ExtractionStatus, ReportStats, TestResult, TestSpec, TestStatus, TestSuite};

// ============================================================================
// Playwright JSON Schema Structs
// ============================================================================

/// Root structure of Playwright results.json.
#[derive(Debug, Deserialize)]
pub struct PlaywrightReport {
    pub config: PlaywrightConfig,
    pub suites: Vec<PlaywrightSuite>,
    #[serde(default, rename = "errors")]
    pub _errors: Vec<serde_json::Value>,
    pub stats: PlaywrightStats,
}

/// Playwright configuration section.
#[derive(Debug, Deserialize)]
pub struct PlaywrightConfig {
    pub version: Option<String>,
}

/// Playwright stats section.
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaywrightStats {
    pub start_time: String,
    pub duration: f64,
    pub expected: i32,
    pub skipped: i32,
    pub unexpected: i32,
    pub flaky: i32,
}

/// Playwright test suite.
#[derive(Debug, Deserialize)]
pub struct PlaywrightSuite {
    pub title: String,
    pub file: String,
    #[serde(default)]
    pub specs: Vec<PlaywrightSpec>,
    #[serde(default)]
    pub suites: Vec<PlaywrightSuite>,
}

/// Playwright test specification.
#[derive(Debug, Deserialize)]
pub struct PlaywrightSpec {
    pub title: String,
    pub ok: bool,
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub file: String,
    #[serde(default)]
    pub line: i32,
    #[serde(default)]
    pub column: i32,
    #[serde(default)]
    pub tests: Vec<PlaywrightTest>,
}

/// Playwright test (can have multiple results due to retries).
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaywrightTest {
    pub project_id: String,
    pub project_name: String,
    #[serde(default)]
    pub results: Vec<PlaywrightResult>,
}

/// Playwright test result.
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PlaywrightResult {
    pub status: String,
    pub duration: i64,
    pub retry: i32,
    pub start_time: String,
    #[serde(default)]
    pub errors: Vec<serde_json::Value>,
}

// ============================================================================
// Extraction Logic
// ============================================================================

/// Extract test results from a results.json file into the database.
pub fn extract_results(conn: &Connection, report_id: Uuid, results_path: &Path) -> AppResult<()> {
    info!(
        "Starting extraction for report {} from {:?}",
        report_id, results_path
    );

    // Read and parse JSON (synchronous file read since we're in sync context)
    let content = std::fs::read_to_string(results_path)
        .map_err(|e| AppError::FileSystem(format!("Failed to read results.json: {}", e)))?;
    let report: PlaywrightReport = serde_json::from_str(&content)
        .map_err(|e| AppError::ExtractionFailed(format!("Failed to parse results.json: {}", e)))?;

    // Parse start time
    let start_time = DateTime::parse_from_rfc3339(&report.stats.start_time)
        .map(|dt| dt.with_timezone(&Utc))
        .unwrap_or_else(|_| Utc::now());

    // Insert stats
    let stats = ReportStats::new(
        report_id,
        start_time,
        report.stats.duration as i64,
        report.stats.expected,
        report.stats.skipped,
        report.stats.unexpected,
        report.stats.flaky,
    );

    queries::insert_report_stats(conn, &stats)?;
    info!("Inserted stats for report {}", report_id);

    // Process suites recursively
    let mut suite_count = 0;
    let mut spec_count = 0;
    let mut result_count = 0;

    for suite in &report.suites {
        let (s, sp, r) = process_suite(conn, report_id, suite)?;
        suite_count += s;
        spec_count += sp;
        result_count += r;
    }

    // Update report status with framework detection
    // For Playwright reports, we set framework to "playwright"
    queries::update_report_status(
        conn,
        report_id,
        ExtractionStatus::Completed,
        Some("playwright"),
        report.config.version.as_deref(),
        None,
    )?;

    info!(
        "Extraction complete for report {}: {} suites, {} specs, {} results",
        report_id, suite_count, spec_count, result_count
    );

    Ok(())
}

/// Process a suite and its nested suites recursively.
fn process_suite(
    conn: &Connection,
    report_id: Uuid,
    suite: &PlaywrightSuite,
) -> AppResult<(usize, usize, usize)> {
    let mut suite_count = 0;
    let mut spec_count = 0;
    let mut result_count = 0;

    // Only insert suite if it has specs
    if !suite.specs.is_empty() {
        let db_suite = TestSuite::new(report_id, suite.title.clone(), suite.file.clone());
        let suite_id = queries::insert_test_suite(conn, &db_suite)?;
        suite_count += 1;

        // Process specs
        for spec in &suite.specs {
            let (sp, r) = process_spec(conn, suite_id, spec)?;
            spec_count += sp;
            result_count += r;
        }
    }

    // Process nested suites
    for nested_suite in &suite.suites {
        let (s, sp, r) = process_suite(conn, report_id, nested_suite)?;
        suite_count += s;
        spec_count += sp;
        result_count += r;
    }

    Ok((suite_count, spec_count, result_count))
}

/// Process a spec and its test results.
fn process_spec(
    conn: &Connection,
    suite_id: i64,
    spec: &PlaywrightSpec,
) -> AppResult<(usize, usize)> {
    let db_spec = TestSpec::new(
        suite_id,
        spec.title.clone(),
        spec.ok,
        spec.id.clone(),
        spec.file.clone(),
        spec.line,
        spec.column,
    );

    let spec_id = queries::insert_test_spec(conn, &db_spec)?;
    let mut result_count = 0;

    // Process tests and their results
    for test in &spec.tests {
        for result in &test.results {
            let status = parse_test_status(&result.status);
            let start_time = DateTime::parse_from_rfc3339(&result.start_time)
                .map(|dt| dt.with_timezone(&Utc))
                .unwrap_or_else(|_| Utc::now());

            let errors_json = if result.errors.is_empty() {
                None
            } else {
                Some(serde_json::to_string(&result.errors).unwrap_or_default())
            };

            let db_result = TestResult::new(
                spec_id,
                status,
                result.duration,
                result.retry,
                start_time,
                test.project_id.clone(),
                test.project_name.clone(),
                errors_json,
            );

            queries::insert_test_result(conn, &db_result)?;
            result_count += 1;
        }
    }

    Ok((1, result_count))
}

/// Parse test status string to enum.
fn parse_test_status(status: &str) -> TestStatus {
    match status {
        "passed" => TestStatus::Passed,
        "failed" => TestStatus::Failed,
        "skipped" => TestStatus::Skipped,
        "timedOut" => TestStatus::TimedOut,
        _ => {
            warn!("Unknown test status: {}, treating as failed", status);
            TestStatus::Failed
        }
    }
}
