//! JSON extraction service for parsing Playwright results.json.

use chrono::{DateTime, Utc};
use sea_orm::DatabaseConnection;
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
#[allow(dead_code)]
pub async fn extract_results(
    conn: &DatabaseConnection,
    report_id: Uuid,
    results_path: &Path,
) -> AppResult<()> {
    info!(
        "Starting extraction for report {} from {:?}",
        report_id, results_path
    );

    // Read and parse JSON
    let content = tokio::fs::read_to_string(results_path)
        .await
        .map_err(|e| AppError::FileSystem(format!("Failed to read results.json: {}", e)))?;

    extract_results_from_string(conn, report_id, &content).await
}

/// Extract test results from JSON bytes (for S3 storage).
pub async fn extract_results_from_bytes(
    conn: &DatabaseConnection,
    report_id: Uuid,
    data: &[u8],
) -> AppResult<()> {
    let content = String::from_utf8(data.to_vec())
        .map_err(|e| AppError::ExtractionFailed(format!("Invalid UTF-8 in results.json: {}", e)))?;

    extract_results_from_string(conn, report_id, &content).await
}

/// Extract test results from JSON string.
async fn extract_results_from_string(
    conn: &DatabaseConnection,
    report_id: Uuid,
    content: &str,
) -> AppResult<()> {
    info!("Starting extraction for report {}", report_id);

    let report: PlaywrightReport = serde_json::from_str(content)
        .map_err(|e| AppError::ExtractionFailed(format!("Failed to parse results.json: {}", e)))?;

    extract_playwright_report(conn, report_id, report).await
}

/// Extract test results from JSON Value (from database JSONB).
pub async fn extract_results_from_json(
    conn: &DatabaseConnection,
    report_id: Uuid,
    json: serde_json::Value,
) -> AppResult<()> {
    info!(
        "Starting extraction for report {} from JSON value",
        report_id
    );

    let report: PlaywrightReport = serde_json::from_value(json)
        .map_err(|e| AppError::ExtractionFailed(format!("Failed to parse JSON value: {}", e)))?;

    extract_playwright_report(conn, report_id, report).await
}

/// Extract test results from parsed Playwright report.
async fn extract_playwright_report(
    conn: &DatabaseConnection,
    report_id: Uuid,
    report: PlaywrightReport,
) -> AppResult<()> {
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

    queries::insert_report_stats(conn, &stats).await?;
    info!("Inserted stats for report {}", report_id);

    // Process suites recursively
    let mut suite_count = 0;
    let mut spec_count = 0;
    let mut result_count = 0;

    for suite in &report.suites {
        let (s, sp, r) = process_suite(conn, report_id, suite).await?;
        suite_count += s;
        spec_count += sp;
        result_count += r;
    }

    // Update report status with framework detection
    queries::update_report_status(
        conn,
        report_id,
        ExtractionStatus::Completed,
        Some("playwright"),
        report.config.version.as_deref(),
        None,
    )
    .await?;

    info!(
        "Extraction complete for report {}: {} suites, {} specs, {} results",
        report_id, suite_count, spec_count, result_count
    );

    Ok(())
}

/// Process a suite and its nested suites recursively.
async fn process_suite(
    conn: &DatabaseConnection,
    report_id: Uuid,
    suite: &PlaywrightSuite,
) -> AppResult<(usize, usize, usize)> {
    let mut suite_count = 0;
    let mut spec_count = 0;
    let mut result_count = 0;

    // Only insert suite if it has specs
    if !suite.specs.is_empty() {
        let db_suite = TestSuite::new(report_id, suite.title.clone(), suite.file.clone());
        let suite_id = queries::insert_test_suite(conn, &db_suite).await?;
        suite_count += 1;

        // Process specs
        for spec in &suite.specs {
            let (sp, r) = process_spec(conn, suite_id, spec).await?;
            spec_count += sp;
            result_count += r;
        }
    }

    // Process nested suites
    for nested_suite in &suite.suites {
        let (s, sp, r) = Box::pin(process_suite(conn, report_id, nested_suite)).await?;
        suite_count += s;
        spec_count += sp;
        result_count += r;
    }

    Ok((suite_count, spec_count, result_count))
}

/// Process a spec and its test results.
async fn process_spec(
    conn: &DatabaseConnection,
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

    let spec_id = queries::insert_test_spec(conn, &db_spec).await?;
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

            queries::insert_test_result(conn, &db_result).await?;
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

// ============================================================================
// Detox/Jest-Stare JSON Schema Structs (T010)
// ============================================================================

/// Root structure of jest-stare JSON data file.
#[allow(dead_code)]
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JestStareReport {
    /// Total number of tests
    #[serde(default)]
    pub num_total_tests: i32,
    /// Number of passed tests
    #[serde(default)]
    pub num_passed_tests: i32,
    /// Number of failed tests
    #[serde(default)]
    pub num_failed_tests: i32,
    /// Number of pending/skipped tests
    #[serde(default)]
    pub num_pending_tests: i32,
    /// Test results by suite
    #[serde(default)]
    pub test_results: Vec<JestStareSuiteResult>,
    /// Whether all tests passed
    #[serde(default)]
    pub success: bool,
    /// Start time in milliseconds
    #[serde(default)]
    pub start_time: i64,
}

/// Jest-stare suite result (corresponds to a test file).
#[allow(dead_code)]
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JestStareSuiteResult {
    /// Path to the test file
    #[serde(default)]
    pub test_file_path: String,
    /// Suite name from the test file
    #[serde(default)]
    pub name: String,
    /// Individual test results
    #[serde(default)]
    pub test_results: Vec<JestStareTestResult>,
    /// Performance stats
    #[serde(default)]
    pub perf_stats: Option<JestStarePerfStats>,
}

/// Jest-stare individual test result.
#[allow(dead_code)]
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JestStareTestResult {
    /// Test title (short name)
    #[serde(default)]
    pub title: String,
    /// Full test name including describe blocks
    #[serde(default)]
    pub full_name: String,
    /// Ancestor titles (describe blocks)
    #[serde(default)]
    pub ancestor_titles: Vec<String>,
    /// Test status: "passed", "failed", "pending"
    #[serde(default)]
    pub status: String,
    /// Duration in milliseconds
    #[serde(default)]
    pub duration: i64,
    /// Failure messages
    #[serde(default)]
    pub failure_messages: Vec<String>,
    /// Number of invocations (retries)
    #[serde(default)]
    pub invocations: i32,
}

/// Jest-stare performance statistics.
#[allow(dead_code)]
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JestStarePerfStats {
    /// Start time
    #[serde(default)]
    pub start: i64,
    /// End time
    #[serde(default)]
    pub end: i64,
    /// Runtime in milliseconds
    #[serde(default)]
    pub runtime: i64,
}

// ============================================================================
// Screenshot Folder Scanner (T012)
// ============================================================================

/// Information about discovered screenshots for a test.
#[derive(Debug, Clone)]
#[allow(dead_code)]
pub struct DiscoveredScreenshot {
    /// Full test name (for matching)
    pub test_full_name: String,
    /// Screenshot type
    pub screenshot_type: crate::models::ScreenshotType,
    /// Relative path to screenshot file
    pub file_path: String,
}

/// Scan report directory for Detox screenshots.
/// Screenshots are stored in folders named after the test: {device}/{testFullName}/{type}.png
/// Results are sorted with testFnFailure first (highest priority), then testStart.
#[allow(dead_code)]
pub fn scan_detox_screenshots(report_path: &Path) -> Vec<DiscoveredScreenshot> {
    let mut screenshots = Vec::new();

    // Look for device folders (e.g., "android.emu.debug.2026-01-09 01-04-31Z")
    let entries = match std::fs::read_dir(report_path) {
        Ok(e) => e,
        Err(_) => return screenshots,
    };

    for entry in entries.flatten() {
        let path = entry.path();
        if path.is_dir() {
            // Check if this looks like a device folder (contains timestamp pattern)
            let folder_name = match path.file_name().and_then(|n| n.to_str()) {
                Some(n) => n,
                None => continue,
            };

            // Device folders typically contain "debug" or "release" and a timestamp
            if folder_name.contains("debug") || folder_name.contains("release") {
                // Scan this device folder for test screenshots
                let test_screenshots = scan_device_folder(&path, folder_name);
                screenshots.extend(test_screenshots);
            }
        }
    }

    // Sort for upload/save: testFnFailure first (priority 0), then testStart (priority 1)
    // This ensures failure screenshots are saved before the upload limit is reached
    screenshots.sort_by_key(|s| match s.screenshot_type {
        crate::models::ScreenshotType::TestFnFailure => 0,
        crate::models::ScreenshotType::TestStart => 1,
    });

    screenshots
}

/// Scan a device folder for test screenshot folders.
fn scan_device_folder(device_path: &Path, device_name: &str) -> Vec<DiscoveredScreenshot> {
    let mut screenshots = Vec::new();

    let entries = match std::fs::read_dir(device_path) {
        Ok(e) => e,
        Err(_) => return screenshots,
    };

    for entry in entries.flatten() {
        let path = entry.path();
        if path.is_dir() {
            // This folder name is the test full name
            let test_full_name = match path.file_name().and_then(|n| n.to_str()) {
                Some(n) => n.to_string(),
                None => continue,
            };

            // Look for screenshot files in this folder
            let test_screenshots = scan_test_folder(&path, &test_full_name, device_name);
            screenshots.extend(test_screenshots);
        }
    }

    screenshots
}

/// Scan a test folder for screenshot files.
fn scan_test_folder(
    test_path: &Path,
    test_full_name: &str,
    device_name: &str,
) -> Vec<DiscoveredScreenshot> {
    let mut screenshots = Vec::new();

    let entries = match std::fs::read_dir(test_path) {
        Ok(e) => e,
        Err(_) => return screenshots,
    };

    for entry in entries.flatten() {
        let path = entry.path();
        if path.is_file()
            && let Some(ext) = path.extension().and_then(|e| e.to_str())
            && (ext == "png" || ext == "jpg" || ext == "jpeg")
        {
            // Parse screenshot type from filename
            let file_name = match path.file_name().and_then(|s| s.to_str()) {
                Some(n) => n,
                None => continue,
            };
            let file_stem = match path.file_stem().and_then(|s| s.to_str()) {
                Some(n) => n,
                None => continue,
            };

            // Only save high-priority screenshots: testFnFailure (highest) and testStart
            // Skip testDone and afterAllFailure to save storage
            let screenshot_type = match file_stem {
                "testFnFailure" => Some(crate::models::ScreenshotType::TestFnFailure),
                "testStart" => Some(crate::models::ScreenshotType::TestStart),
                _ => None,
            };

            if let Some(s_type) = screenshot_type {
                // Build relative path: {device}/{testFullName}/{filename}
                let file_path = format!("{}/{}/{}", device_name, test_full_name, file_name);

                screenshots.push(DiscoveredScreenshot {
                    test_full_name: test_full_name.to_string(),
                    screenshot_type: s_type,
                    file_path,
                });
            }
        }
    }

    screenshots
}

// ============================================================================
// Jest-Stare JSON Extraction
// ============================================================================

/// Extract test results from jest-stare JSON file.
#[allow(dead_code)]
pub async fn extract_jest_stare_results(
    conn: &DatabaseConnection,
    report_id: Uuid,
    data_json_path: &Path,
) -> AppResult<JestStareStats> {
    info!(
        "Extracting jest-stare results for report {} from {:?}",
        report_id, data_json_path
    );

    let content = tokio::fs::read_to_string(data_json_path)
        .await
        .map_err(|e| AppError::FileSystem(format!("Failed to read jest-stare data: {}", e)))?;

    extract_jest_stare_results_from_string(conn, report_id, &content).await
}

/// Extract test results from jest-stare JSON bytes (for S3 storage).
pub async fn extract_jest_stare_results_from_bytes(
    conn: &DatabaseConnection,
    report_id: Uuid,
    data: &[u8],
) -> AppResult<JestStareStats> {
    let content = String::from_utf8(data.to_vec()).map_err(|e| {
        AppError::ExtractionFailed(format!("Invalid UTF-8 in jest-stare data: {}", e))
    })?;

    extract_jest_stare_results_from_string(conn, report_id, &content).await
}

/// Extract test results from jest-stare JSON string.
async fn extract_jest_stare_results_from_string(
    conn: &DatabaseConnection,
    report_id: Uuid,
    content: &str,
) -> AppResult<JestStareStats> {
    info!("Extracting jest-stare results for report {}", report_id);

    let report: JestStareReport = serde_json::from_str(content).map_err(|e| {
        AppError::ExtractionFailed(format!("Failed to parse jest-stare JSON: {}", e))
    })?;

    // Calculate total duration from all test results
    let total_duration: i64 = report
        .test_results
        .iter()
        .flat_map(|suite| suite.test_results.iter())
        .map(|test| test.duration)
        .sum();

    // Process each suite
    let mut suite_count = 0;
    let mut spec_count = 0;
    let mut result_count = 0;

    for suite_result in &report.test_results {
        if suite_result.test_results.is_empty() {
            continue;
        }

        // Create suite from test file path
        let suite_title = if !suite_result.name.is_empty() {
            suite_result.name.clone()
        } else if !suite_result.test_file_path.is_empty() {
            std::path::Path::new(&suite_result.test_file_path)
                .file_name()
                .map(|n| n.to_string_lossy().to_string())
                .unwrap_or_else(|| "Unknown Suite".to_string())
        } else {
            "Unknown Suite".to_string()
        };

        let db_suite = TestSuite::new(report_id, suite_title, suite_result.test_file_path.clone());
        let suite_id = queries::insert_test_suite(conn, &db_suite).await?;
        suite_count += 1;

        // Process each test in the suite
        for test_result in &suite_result.test_results {
            let (sp, r) = process_jest_stare_test(conn, suite_id, test_result).await?;
            spec_count += sp;
            result_count += r;
        }
    }

    info!(
        "Jest-stare extraction complete: {} suites, {} specs, {} results",
        suite_count, spec_count, result_count
    );

    Ok(JestStareStats {
        total_tests: report.num_total_tests,
        passed_tests: report.num_passed_tests,
        failed_tests: report.num_failed_tests,
        skipped_tests: report.num_pending_tests,
        duration_ms: total_duration,
    })
}

/// Statistics extracted from jest-stare report.
#[derive(Debug, Clone)]
pub struct JestStareStats {
    pub total_tests: i32,
    pub passed_tests: i32,
    pub failed_tests: i32,
    pub skipped_tests: i32,
    pub duration_ms: i64,
}

/// Process a single jest-stare test result.
async fn process_jest_stare_test(
    conn: &DatabaseConnection,
    suite_id: i64,
    test: &JestStareTestResult,
) -> AppResult<(usize, usize)> {
    // Map jest-stare status to our TestStatus
    let status = match test.status.as_str() {
        "passed" => TestStatus::Passed,
        "failed" => TestStatus::Failed,
        "pending" | "skipped" => TestStatus::Skipped,
        _ => {
            warn!(
                "Unknown jest-stare status: {}, treating as failed",
                test.status
            );
            TestStatus::Failed
        }
    };

    let ok = status == TestStatus::Passed;

    // Create spec (use full_name as the full_title)
    let db_spec = TestSpec::new(
        suite_id,
        test.title.clone(),
        ok,
        test.full_name.clone(), // Full title including suite/describe blocks
        String::new(),          // No file path in jest-stare
        0,                      // No line info
        0,                      // No column info
    );

    let spec_id = queries::insert_test_spec(conn, &db_spec).await?;

    // Create test result
    let errors_json = if test.failure_messages.is_empty() {
        None
    } else {
        Some(serde_json::to_string(&test.failure_messages).unwrap_or_default())
    };

    let db_result = TestResult::new(
        spec_id,
        status,
        test.duration,
        test.invocations.saturating_sub(1).max(0), // Convert invocations to retry count
        Utc::now(),                                // No start time in jest-stare
        String::new(),                             // No project_id
        String::new(),                             // No project_name
        errors_json,
    );

    queries::insert_test_result(conn, &db_result).await?;

    Ok((1, 1))
}

// ============================================================================
// Screenshot Path Parsing (for S3 storage)
// ============================================================================

/// Parsed screenshot information from S3 path.
pub struct ParsedScreenshot {
    pub test_full_name: String,
    pub screenshot_type: crate::models::ScreenshotType,
}

/// Parse Detox screenshot information from a file path.
/// Path format: {job}/{device}/{testFullName}/{screenshotType}.png
pub fn parse_detox_screenshot_path(file_path: &str) -> Option<ParsedScreenshot> {
    let parts: Vec<&str> = file_path.split('/').collect();

    // Minimum: job/device/testName/screenshot.png = 4 parts
    if parts.len() < 4 {
        return None;
    }

    // Get filename and parse screenshot type
    let filename = parts.last()?;
    let file_stem = filename.strip_suffix(".png")?;

    let screenshot_type = match file_stem {
        "testFnFailure" => crate::models::ScreenshotType::TestFnFailure,
        "testStart" => crate::models::ScreenshotType::TestStart,
        _ => return None, // Only save high-priority screenshots
    };

    // Test full name is the folder before the filename
    // Path: job/device/testFullName/screenshot.png
    let test_full_name = parts[parts.len() - 2].to_string();

    Some(ParsedScreenshot {
        test_full_name,
        screenshot_type,
    })
}
