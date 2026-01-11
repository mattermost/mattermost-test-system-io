//! Cypress mochawesome report extraction service.
//!
//! This module handles parsing and extracting test data from Cypress mochawesome
//! JSON reports (all.json or mochawesome.json) into the database schema.

use chrono::{DateTime, Utc};
use rusqlite::Connection;
use serde::Deserialize;
use std::path::Path;
use tracing::{info, warn};
use uuid::Uuid;

use crate::db::queries;
use crate::error::{AppError, AppResult};
use crate::models::{ExtractionStatus, ReportStats, TestResult, TestSpec, TestStatus, TestSuite};

/// Root structure of a mochawesome report JSON file.
#[derive(Debug, Deserialize)]
pub struct MochawesomeReport {
    pub stats: MochawesomeStats,
    pub results: Vec<MochawesomeResult>,
}

/// Statistics section of the mochawesome report.
#[derive(Debug, Deserialize)]
#[allow(dead_code)]
pub struct MochawesomeStats {
    pub suites: i32,
    pub tests: i32,
    pub passes: i32,
    pub pending: i32,
    pub failures: i32,
    #[serde(default)]
    pub skipped: i32,
    pub start: String,
    pub end: String,
    pub duration: i64,
}

/// A result entry representing a spec file with its suites.
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
#[allow(dead_code)]
pub struct MochawesomeResult {
    pub uuid: String,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub full_file: String,
    #[serde(default)]
    pub file: String,
    #[serde(default)]
    pub suites: Vec<MochawesomeSuite>,
}

/// A test suite (describe block) in the mochawesome report.
#[derive(Debug, Deserialize)]
#[allow(dead_code)]
pub struct MochawesomeSuite {
    pub uuid: String,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub tests: Vec<MochawesomeTest>,
    #[serde(default)]
    pub suites: Vec<MochawesomeSuite>,
}

/// An individual test case (it block) in the mochawesome report.
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
#[allow(dead_code)]
pub struct MochawesomeTest {
    pub title: String,
    #[serde(default)]
    pub full_title: String,
    #[serde(default)]
    pub duration: i64,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub pass: bool,
    #[serde(default)]
    pub fail: bool,
    #[serde(default)]
    pub pending: bool,
    #[serde(default)]
    pub context: Option<serde_json::Value>,
    #[serde(default)]
    pub err: serde_json::Value,
    #[serde(default)]
    pub uuid: String,
}

/// Extract Cypress test results from a mochawesome JSON file.
///
/// Parses the JSON file and inserts data into the database:
/// - ReportStats: Summary statistics (passes, failures, pending, skipped, duration)
/// - TestSuites: Test suites (describe blocks)
/// - TestSpecs: Individual test cases (it blocks)
/// - TestResults: Execution results with status and duration
///
/// Returns the framework version if found in the report metadata.
pub fn extract_cypress_results(
    conn: &Connection,
    report_id: Uuid,
    json_path: &Path,
) -> AppResult<Option<String>> {
    info!(
        "Starting Cypress extraction for report {} from {:?}",
        report_id, json_path
    );

    // Read and parse the JSON file
    let json_content = std::fs::read_to_string(json_path)
        .map_err(|e| AppError::FileSystem(format!("Failed to read mochawesome JSON: {}", e)))?;

    let report: MochawesomeReport = serde_json::from_str(&json_content)
        .map_err(|e| AppError::InvalidInput(format!("Failed to parse mochawesome JSON: {}", e)))?;

    info!(
        "Parsed mochawesome report: {} suites, {} tests, {} passes, {} failures",
        report.stats.suites, report.stats.tests, report.stats.passes, report.stats.failures
    );

    // Parse start time
    let start_time = DateTime::parse_from_rfc3339(&report.stats.start)
        .map(|dt| dt.with_timezone(&Utc))
        .unwrap_or_else(|_| Utc::now());

    // Map stats: passes→expected, failures→unexpected, pending+skipped→skipped, flaky=0
    let stats = ReportStats {
        report_id,
        start_time,
        duration_ms: report.stats.duration,
        expected: report.stats.passes,
        skipped: report.stats.pending + report.stats.skipped,
        unexpected: report.stats.failures,
        flaky: 0, // Cypress doesn't track flaky tests
    };

    // Insert stats
    queries::insert_report_stats(conn, &stats)?;
    info!("Inserted report stats for {}", report_id);

    // Process each result (spec file)
    let mut total_suites = 0;
    let mut total_specs = 0;

    for result in &report.results {
        let file_path = if !result.full_file.is_empty() {
            &result.full_file
        } else {
            &result.file
        };

        // Process nested suites
        for suite in &result.suites {
            let (suite_count, spec_count) =
                process_mochawesome_suite(conn, report_id, file_path, suite, &start_time)?;
            total_suites += suite_count;
            total_specs += spec_count;
        }
    }

    info!(
        "Extraction complete for report {}: {} suites, {} specs",
        report_id, total_suites, total_specs
    );

    // Update report status to completed
    queries::update_report_status(
        conn,
        report_id,
        ExtractionStatus::Completed,
        Some("cypress"),
        None, // Version could be extracted from meta field if present
        None,
    )?;

    // Return None for version as mochawesome doesn't include Cypress version by default
    Ok(None)
}

/// Process a mochawesome suite and its nested suites recursively.
///
/// Returns (suite_count, spec_count) for tracking.
fn process_mochawesome_suite(
    conn: &Connection,
    report_id: Uuid,
    file_path: &str,
    suite: &MochawesomeSuite,
    start_time: &DateTime<Utc>,
) -> AppResult<(i32, i32)> {
    let mut suite_count = 0;
    let mut spec_count = 0;

    // Use suite title or file path as fallback
    let suite_title = if !suite.title.is_empty() {
        &suite.title
    } else {
        file_path
    };

    // Insert the test suite
    let test_suite = TestSuite {
        id: None,
        report_id,
        title: suite_title.to_string(),
        file_path: file_path.to_string(),
        specs_count: None,
        passed_count: None,
        failed_count: None,
        flaky_count: None,
        skipped_count: None,
        duration_ms: None,
    };

    let suite_id = queries::insert_test_suite(conn, &test_suite)?;
    suite_count += 1;

    // Process tests in this suite
    for test in &suite.tests {
        process_mochawesome_test(conn, suite_id, file_path, test, start_time)?;
        spec_count += 1;
    }

    // Process nested suites recursively
    for nested_suite in &suite.suites {
        let (nested_suites, nested_specs) =
            process_mochawesome_suite(conn, report_id, file_path, nested_suite, start_time)?;
        suite_count += nested_suites;
        spec_count += nested_specs;
    }

    Ok((suite_count, spec_count))
}

/// Process a single mochawesome test and insert spec + result.
fn process_mochawesome_test(
    conn: &Connection,
    suite_id: i64,
    file_path: &str,
    test: &MochawesomeTest,
    start_time: &DateTime<Utc>,
) -> AppResult<()> {
    // Determine test status
    let status = parse_cypress_test_status(test);
    let ok = test.pass && !test.fail;

    // Create test spec
    let spec = TestSpec {
        id: None,
        suite_id,
        title: test.title.clone(),
        ok,
        full_title: test.full_title.clone(), // Full title including describe blocks
        file_path: file_path.to_string(),
        line: 0,   // Mochawesome doesn't provide line numbers
        column: 0, // Mochawesome doesn't provide column numbers
    };

    let spec_id = queries::insert_test_spec(conn, &spec)?;

    // Extract error details if test failed
    let errors_json = if test.fail && !test.err.is_null() {
        Some(serde_json::to_string(&test.err).unwrap_or_default())
    } else {
        None
    };

    // Create test result
    let result = TestResult {
        id: None,
        spec_id,
        status,
        duration_ms: test.duration,
        retry: 0, // Cypress doesn't track retries in mochawesome format
        start_time: *start_time,
        project_id: String::new(),   // Not applicable for Cypress
        project_name: String::new(), // Not applicable for Cypress
        errors_json,
    };

    queries::insert_test_result(conn, &result)?;

    Ok(())
}

/// Parse Cypress test state to TestStatus enum.
fn parse_cypress_test_status(test: &MochawesomeTest) -> TestStatus {
    // Check state field first
    match test.state.as_str() {
        "passed" => TestStatus::Passed,
        "failed" => TestStatus::Failed,
        "pending" => TestStatus::Skipped,
        _ => {
            // Fallback to boolean flags
            if test.pass {
                TestStatus::Passed
            } else if test.fail {
                TestStatus::Failed
            } else if test.pending {
                TestStatus::Skipped
            } else {
                // Default to failed if unknown
                warn!(
                    "Unknown test state for '{}', defaulting to failed",
                    test.title
                );
                TestStatus::Failed
            }
        }
    }
}
