//! Extraction service for processing JSON test result files.
//!
//! Extracts test suites and test cases from framework-specific JSON formats
//! (Cypress/mochawesome, Detox/Jest, Playwright) and stores them in the database.

use chrono::{DateTime, Utc};
use serde::Deserialize;
use serde_json::{json, Value as JsonValue};
use tracing::{error, info, warn};
use uuid::Uuid;

use crate::db::test_results::{NewTestCase, NewTestSuite};
use crate::db::DbPool;
use crate::models::JobStatus;
use crate::services::Storage;

// ============================================================================
// Cypress / Mochawesome JSON Structures
// ============================================================================

#[derive(Debug, Deserialize)]
struct CypressStats {
    /// ISO 8601 timestamp when test run started.
    #[serde(default)]
    start: Option<String>,
}

#[derive(Debug, Deserialize)]
struct CypressReport {
    #[serde(default)]
    stats: Option<CypressStats>,
    results: Vec<CypressResult>,
}

#[derive(Debug, Deserialize)]
struct CypressResult {
    #[serde(default)]
    title: String,
    #[serde(default)]
    file: String,
    #[serde(rename = "fullFile", default)]
    full_file: String,
    #[serde(default)]
    tests: Vec<CypressTest>,
    #[serde(default)]
    suites: Vec<CypressSuite>,
}

#[derive(Debug, Deserialize)]
struct CypressSuite {
    #[serde(default)]
    title: String,
    #[serde(default)]
    file: String,
    #[serde(default)]
    tests: Vec<CypressTest>,
    #[serde(default)]
    suites: Vec<CypressSuite>,
}

#[derive(Debug, Deserialize)]
struct CypressTest {
    #[serde(default)]
    title: String,
    #[serde(rename = "fullTitle", default)]
    full_title: String,
    #[serde(default)]
    duration: i64,
    #[serde(default)]
    state: String,
    #[serde(default)]
    pass: bool,
    #[serde(default)]
    fail: bool,
    #[serde(default)]
    pending: bool,
    #[serde(default)]
    skipped: bool,
    #[serde(default)]
    context: Option<String>,
    #[serde(default)]
    err: Option<CypressError>,
}

#[derive(Debug, Deserialize)]
struct CypressError {
    #[serde(default)]
    message: Option<String>,
    #[serde(default)]
    estack: Option<String>,
}

// ============================================================================
// Detox / Jest JSON Structures
// ============================================================================

#[derive(Debug, Deserialize)]
struct DetoxPerfStats {
    /// Unix timestamp in milliseconds when test file started.
    #[serde(default)]
    start: Option<i64>,
}

#[derive(Debug, Deserialize)]
struct DetoxReport {
    #[serde(rename = "testResults", default)]
    test_results: Vec<DetoxTestFile>,
}

#[derive(Debug, Deserialize)]
struct DetoxTestFile {
    #[serde(rename = "testFilePath", default)]
    test_file_path: String,
    #[serde(rename = "perfStats", default)]
    perf_stats: Option<DetoxPerfStats>,
    #[serde(rename = "testResults", default)]
    test_results: Vec<DetoxTestResult>,
}

#[derive(Debug, Deserialize)]
struct DetoxTestResult {
    #[serde(rename = "ancestorTitles", default)]
    ancestor_titles: Vec<String>,
    #[serde(default)]
    duration: Option<i64>,
    #[serde(rename = "failureMessages", default)]
    failure_messages: Vec<String>,
    #[serde(rename = "fullName", default)]
    full_name: String,
    #[serde(default)]
    status: String,
    #[serde(default)]
    title: String,
}

// ============================================================================
// Playwright JSON Structures
// ============================================================================

#[derive(Debug, Deserialize)]
struct PlaywrightReport {
    #[serde(default)]
    suites: Vec<PlaywrightSuite>,
}

#[derive(Debug, Deserialize)]
struct PlaywrightSuite {
    #[serde(default)]
    title: String,
    #[serde(default)]
    file: String,
    #[serde(default)]
    specs: Vec<PlaywrightSpec>,
    #[serde(default)]
    suites: Vec<PlaywrightSuite>,
}

#[derive(Debug, Deserialize)]
struct PlaywrightSpec {
    #[serde(default)]
    title: String,
    #[serde(default)]
    tests: Vec<PlaywrightTest>,
}

#[derive(Debug, Deserialize)]
struct PlaywrightTest {
    #[serde(rename = "projectName", default)]
    project_name: String,
    #[serde(default)]
    results: Vec<PlaywrightTestResult>,
    #[serde(default)]
    status: String,
}

#[derive(Debug, Deserialize)]
struct PlaywrightTestResult {
    #[serde(default)]
    status: String,
    #[serde(default)]
    duration: i64,
    #[serde(default)]
    errors: Vec<PlaywrightError>,
    #[serde(default)]
    retry: i32,
    #[serde(default)]
    attachments: Vec<PlaywrightAttachment>,
    /// ISO 8601 timestamp when test started.
    #[serde(rename = "startTime", default)]
    start_time: Option<String>,
}

#[derive(Debug, Deserialize)]
struct PlaywrightError {
    #[serde(default)]
    message: Option<String>,
    #[serde(default)]
    stack: Option<String>,
}

#[derive(Debug, Deserialize)]
struct PlaywrightAttachment {
    #[serde(rename = "contentType", default)]
    content_type: String,
    #[serde(default)]
    path: Option<String>,
}

// ============================================================================
// Extracted Data Structures
// ============================================================================

/// Extracted test case data before inserting.
struct ExtractedTestCase {
    title: String,
    full_title: String,
    status: String,
    duration_ms: i32,
    retry_count: i32,
    error_message: Option<String>,
    sequence: i32,
    attachments: Vec<ExtractedAttachment>,
    /// Test execution start time (ISO 8601).
    start_time: Option<DateTime<Utc>>,
}

/// Attachment extracted from test results.
#[derive(Debug, Clone)]
struct ExtractedAttachment {
    path: String,
    content_type: Option<String>,
    retry: i32,
    s3_key: Option<String>,
    missing: bool,
    sequence: i32,
}

/// Extracted test suite with its test cases.
struct ExtractedTestSuite {
    title: String,
    file_path: Option<String>,
    test_cases: Vec<ExtractedTestCase>,
    /// Earliest test start time in this suite (computed from test cases).
    start_time: Option<DateTime<Utc>>,
}

// ============================================================================
// Main Extraction Function
// ============================================================================

/// Extract data from a job's JSON files and update the database.
///
/// This function is called asynchronously after JSON files are uploaded.
/// It fetches the files from S3, parses them, and stores the results.
pub async fn extract_job(pool: &DbPool, storage: &Storage, job_id: Uuid, framework: &str) {
    info!("Starting JSON extraction for job_id={}, framework={}", job_id, framework);

    // Get job to verify it exists and get report_id
    let job = match pool.get_job_by_id(job_id).await {
        Ok(Some(j)) => j,
        Ok(None) => {
            error!("Job {} not found during extraction", job_id);
            return;
        }
        Err(e) => {
            error!("Failed to get job {} during extraction: {}", job_id, e);
            return;
        }
    };

    // Get uploaded JSON files for this job
    let json_files = match pool.get_uploaded_json_files(job_id).await {
        Ok(files) => files,
        Err(e) => {
            error!("Failed to get JSON files for job {}: {}", job_id, e);
            if let Err(e) = pool
                .update_job_status(job_id, JobStatus::Failed, Some(format!("Failed to get JSON files: {}", e)))
                .await
            {
                error!("Failed to update job status: {}", e);
            }
            return;
        }
    };

    if json_files.is_empty() {
        error!("Job {} has no uploaded JSON files", job_id);
        if let Err(e) = pool
            .update_job_status(job_id, JobStatus::Failed, Some("No JSON files uploaded".to_string()))
            .await
        {
            error!("Failed to update job status: {}", e);
        }
        return;
    }

    let mut all_suites: Vec<ExtractedTestSuite> = Vec::new();
    let mut total_duration_ms: i64 = 0;
    let earliest_time: Option<DateTime<Utc>> = None;
    let mut global_sequence = 0;

    // Fetch uploaded screenshots for validation
    let uploaded_screenshots = match pool.get_screenshots_by_job_id(job_id).await {
        Ok(screenshots) => screenshots,
        Err(e) => {
            warn!("Failed to get screenshots for job {}: {}", job_id, e);
            Vec::new()
        }
    };

    // Build a lookup map: filename -> s3_key
    let screenshot_map: std::collections::HashMap<String, String> = uploaded_screenshots
        .iter()
        .map(|s| (s.filename.clone(), s.s3_key.clone()))
        .collect();

    // Fetch and parse each JSON file from S3
    for file in &json_files {
        info!("Fetching JSON file from S3: {}", file.s3_key);

        let json_content = match storage.get(&file.s3_key).await {
            Ok((data, _content_type)) => match String::from_utf8(data) {
                Ok(s) => s,
                Err(e) => {
                    warn!("JSON file {} is not valid UTF-8: {}", file.s3_key, e);
                    continue;
                }
            },
            Err(e) => {
                warn!("Failed to fetch JSON file {}: {}", file.s3_key, e);
                continue;
            }
        };

        // Parse JSON and extract test data based on framework
        let suites = match framework.to_lowercase().as_str() {
            "cypress" => extract_cypress(&json_content, &screenshot_map, &mut global_sequence),
            "detox" => extract_detox(&json_content, &screenshot_map, &mut global_sequence),
            "playwright" => extract_playwright(&json_content, &screenshot_map, &mut global_sequence),
            _ => {
                // Try to auto-detect format
                if let Some(suites) = try_auto_detect(&json_content, &screenshot_map, &mut global_sequence) {
                    suites
                } else {
                    warn!("Unknown framework '{}' and auto-detection failed for {}", framework, file.s3_key);
                    continue;
                }
            }
        };

        for suite in suites {
            total_duration_ms += suite.test_cases.iter().map(|tc| tc.duration_ms as i64).sum::<i64>();
            all_suites.push(suite);
        }

        // Mark file as extracted
        if let Err(e) = pool.mark_json_file_extracted(file.id).await {
            warn!("Failed to mark JSON file {} as extracted: {}", file.id, e);
        }
    }

    info!(
        "Parsed JSON files for job_id={}: {} suites, {} total duration ms",
        job_id, all_suites.len(), total_duration_ms
    );

    // Insert test suites and cases into database
    let mut suite_count = 0;
    let mut case_count = 0;
    for suite in all_suites {
        let (passed, failed, skipped, flaky, unique_count) = count_statuses(&suite.test_cases);
        let duration_ms = suite.test_cases.iter().map(|tc| tc.duration_ms).sum();

        let new_suite = NewTestSuite {
            job_id,
            title: suite.title,
            file_path: suite.file_path,
            total_count: unique_count,
            passed_count: passed,
            failed_count: failed,
            skipped_count: skipped,
            flaky_count: flaky,
            duration_ms,
            start_time: suite.start_time,
        };

        match pool.insert_test_suite(new_suite).await {
            Ok(inserted_suite) => {
                suite_count += 1;
                let suite_id = inserted_suite.id;

                for test_case in suite.test_cases {
                    // Convert attachments to JSON with validation status
                    let attachments_json = if test_case.attachments.is_empty() {
                        None
                    } else {
                        Some(json!(test_case.attachments.iter().map(|a| {
                            let mut obj = json!({
                                "path": a.path,
                                "content_type": a.content_type,
                                "retry": a.retry,
                                "missing": a.missing,
                                "sequence": a.sequence
                            });
                            if let Some(ref key) = a.s3_key {
                                obj["s3_key"] = json!(key);
                            }
                            obj
                        }).collect::<Vec<_>>()))
                    };

                    let new_case = NewTestCase {
                        suite_id,
                        job_id,
                        title: test_case.title,
                        full_title: test_case.full_title,
                        status: test_case.status,
                        duration_ms: test_case.duration_ms,
                        retry_count: test_case.retry_count,
                        error_message: test_case.error_message,
                        sequence: test_case.sequence,
                        attachments: attachments_json,
                    };

                    if let Err(e) = pool.insert_test_case(new_case).await {
                        error!("Failed to insert test case for job {}: {}", job_id, e);
                    } else {
                        case_count += 1;
                    }
                }
            }
            Err(e) => {
                error!("Failed to insert test suite for job {}: {}", job_id, e);
            }
        }
    }

    // Update job duration if we have stats
    if (total_duration_ms > 0 || earliest_time.is_some())
        && let Err(e) = pool.update_job_duration(job_id, Some(total_duration_ms), earliest_time).await
    {
        error!("Failed to update job {} duration: {}", job_id, e);
    }

    // Link screenshots to test cases
    if let Err(e) = link_screenshots_to_test_cases(pool, job_id).await {
        error!("Failed to link screenshots for job {}: {}", job_id, e);
        // Don't fail the job - screenshots linking is not critical
    }

    // Update job status to complete
    if let Err(e) = pool
        .update_job_status(job_id, JobStatus::Complete, None)
        .await
    {
        error!("Failed to update job {} status to complete: {}", job_id, e);
        return;
    }

    // Check if all jobs for the report are complete
    let report_id = job.test_report_id;
    if let Err(e) = check_report_completion(pool, report_id).await {
        error!("Failed to check report {} completion: {}", report_id, e);
    }

    info!(
        "Extraction complete for job_id={}: {} suites, {} test cases",
        job_id, suite_count, case_count
    );
}

// ============================================================================
// Screenshot Linking
// ============================================================================

/// Link screenshots to their matching test cases.
///
/// This function can be called:
/// 1. After JSON extraction - to link screenshots that were uploaded first
/// 2. After screenshot upload - to link screenshots to existing test cases
///
/// Matching logic:
/// - Screenshot's test_name (from path) is compared with test case's full_title
/// - Handles path normalization (e.g., "/" vs " > ")
/// - Handles project suffix in full_title (e.g., "Title [chromium]")
pub async fn link_screenshots_to_test_cases(
    pool: &DbPool,
    job_id: Uuid,
) -> Result<(), crate::error::AppError> {
    // Get all screenshots for this job (only unlinked ones need processing)
    let screenshots = pool.get_screenshots_by_job_id(job_id).await?;
    let unlinked_screenshots: Vec<_> = screenshots
        .iter()
        .filter(|s| s.test_case_id.is_none())
        .collect();

    if unlinked_screenshots.is_empty() {
        // All screenshots are already linked or no screenshots exist
        return Ok(());
    }

    // Get all test cases for this job
    let test_cases = pool.get_test_cases_by_job_id(job_id).await?;
    if test_cases.is_empty() {
        // No test cases yet - screenshots will be linked when JSON is extracted
        return Ok(());
    }

    // Build a mapping: (screenshot_id, test_case_id)
    let mut mappings: Vec<(Uuid, Uuid)> = Vec::new();

    for screenshot in &unlinked_screenshots {
        // Screenshot test_name comes from folder path (e.g., "Suite > Spec" or "Suite/Spec")
        let normalized_test_name = screenshot.test_name.replace('/', " > ");

        // Find matching test case
        for test_case in &test_cases {
            let full_title = &test_case.full_title;

            // Match conditions:
            // 1. Exact match
            // 2. Normalized match (/ -> " > ")
            // 3. Match with project suffix (e.g., "Title [chromium]")
            let is_match = full_title == &screenshot.test_name
                || full_title == &normalized_test_name
                || full_title.starts_with(&format!("{} [", screenshot.test_name))
                || full_title.starts_with(&format!("{} [", normalized_test_name));

            if is_match {
                mappings.push((screenshot.id, test_case.id));
                break; // Found a match, move to next screenshot
            }
        }
    }

    let total_unlinked = unlinked_screenshots.len();
    let linked_count = mappings.len();
    let unmapped_count = total_unlinked - linked_count;

    // Link the screenshots
    if !mappings.is_empty() {
        pool.link_screenshots_to_test_cases(&mappings).await?;
    }

    if linked_count > 0 {
        if unmapped_count > 0 {
            warn!(
                "Screenshot linking for job_id={}: {} linked, {} still unmapped",
                job_id, linked_count, unmapped_count
            );
        } else {
            info!(
                "Screenshot linking for job_id={}: {} linked",
                job_id, linked_count
            );
        }
    }

    Ok(())
}

// ============================================================================
// Framework-Specific Extractors
// ============================================================================

/// Extract test data from Cypress/mochawesome JSON format.
fn extract_cypress(
    json_content: &str,
    screenshot_map: &std::collections::HashMap<String, String>,
    global_sequence: &mut i32,
) -> Vec<ExtractedTestSuite> {
    let report: CypressReport = match serde_json::from_str(json_content) {
        Ok(r) => r,
        Err(e) => {
            warn!("Failed to parse Cypress JSON: {}", e);
            return Vec::new();
        }
    };

    // Parse start time from stats (applies to all suites in this report)
    let report_start_time = report
        .stats
        .as_ref()
        .and_then(|s| s.start.as_ref())
        .and_then(|s| {
            DateTime::parse_from_rfc3339(s)
                .ok()
                .map(|dt| dt.with_timezone(&Utc))
        });

    let mut suites = Vec::new();

    for result in report.results {
        // Process root-level tests
        let file_path = if !result.full_file.is_empty() {
            Some(result.full_file.clone())
        } else if !result.file.is_empty() {
            Some(result.file.clone())
        } else {
            None
        };

        // Process nested suites
        for suite in result.suites {
            let extracted = extract_cypress_suite(&suite, file_path.as_deref(), screenshot_map, global_sequence, report_start_time);
            suites.push(extracted);
        }

        // Process root-level tests if any
        if !result.tests.is_empty() {
            let test_cases: Vec<ExtractedTestCase> = result
                .tests
                .iter()
                .map(|t| {
                    let seq = *global_sequence;
                    *global_sequence += 1;
                    extract_cypress_test(t, screenshot_map, seq, report_start_time)
                })
                .collect();

            if !test_cases.is_empty() {
                suites.push(ExtractedTestSuite {
                    title: result.title.clone(),
                    file_path: file_path.clone(),
                    test_cases,
                    start_time: report_start_time,
                });
            }
        }
    }

    suites
}

fn extract_cypress_suite(
    suite: &CypressSuite,
    parent_file: Option<&str>,
    screenshot_map: &std::collections::HashMap<String, String>,
    global_sequence: &mut i32,
    report_start_time: Option<DateTime<Utc>>,
) -> ExtractedTestSuite {
    let file_path = if !suite.file.is_empty() {
        Some(suite.file.clone())
    } else {
        parent_file.map(|s| s.to_string())
    };

    let mut test_cases: Vec<ExtractedTestCase> = suite
        .tests
        .iter()
        .map(|t| {
            let seq = *global_sequence;
            *global_sequence += 1;
            extract_cypress_test(t, screenshot_map, seq, report_start_time)
        })
        .collect();

    // Recursively process nested suites
    for nested in &suite.suites {
        let nested_suite = extract_cypress_suite(nested, file_path.as_deref(), screenshot_map, global_sequence, report_start_time);
        test_cases.extend(nested_suite.test_cases);
    }

    ExtractedTestSuite {
        title: suite.title.clone(),
        file_path,
        test_cases,
        start_time: report_start_time,
    }
}

fn extract_cypress_test(
    test: &CypressTest,
    screenshot_map: &std::collections::HashMap<String, String>,
    sequence: i32,
    report_start_time: Option<DateTime<Utc>>,
) -> ExtractedTestCase {
    let status = if test.pending || test.skipped {
        "skipped".to_string()
    } else if test.fail {
        "failed".to_string()
    } else if test.pass {
        "passed".to_string()
    } else {
        test.state.clone()
    };

    let error_message = test.err.as_ref().and_then(|e| {
        e.message.clone().or_else(|| e.estack.clone())
    });

    // Parse screenshot context if available
    let attachments = parse_cypress_context(&test.context, screenshot_map, sequence);

    ExtractedTestCase {
        title: test.title.clone(),
        full_title: if test.full_title.is_empty() {
            test.title.clone()
        } else {
            test.full_title.clone()
        },
        status,
        duration_ms: test.duration as i32,
        retry_count: 0,
        error_message,
        sequence,
        attachments,
        start_time: report_start_time, // From stats.start at report level
    }
}

fn parse_cypress_context(
    context: &Option<String>,
    screenshot_map: &std::collections::HashMap<String, String>,
    _sequence: i32,
) -> Vec<ExtractedAttachment> {
    let Some(ctx) = context else {
        return Vec::new();
    };

    // Cypress context is JSON: {"title": "...", "value": "screenshot/path.png"}
    let parsed: Result<JsonValue, _> = serde_json::from_str(ctx);
    let Ok(value) = parsed else {
        return Vec::new();
    };

    let mut attachments = Vec::new();
    let mut seq = 0;

    if let Some(path) = value.get("value").and_then(|v| v.as_str()) {
        let decoded_path = urlencoding::decode(path).unwrap_or_else(|_| path.into()).to_string();
        let (s3_key, missing) = lookup_screenshot(&decoded_path, screenshot_map);

        attachments.push(ExtractedAttachment {
            path: decoded_path,
            content_type: Some("image/png".to_string()),
            retry: 0,
            s3_key,
            missing,
            sequence: seq,
        });
        seq += 1;
    }

    // Handle array of contexts
    if let Some(arr) = value.as_array() {
        for item in arr {
            if let Some(path) = item.get("value").and_then(|v| v.as_str()) {
                let decoded_path = urlencoding::decode(path).unwrap_or_else(|_| path.into()).to_string();
                let (s3_key, missing) = lookup_screenshot(&decoded_path, screenshot_map);

                attachments.push(ExtractedAttachment {
                    path: decoded_path,
                    content_type: Some("image/png".to_string()),
                    retry: 0,
                    s3_key,
                    missing,
                    sequence: seq,
                });
                seq += 1;
            }
        }
    }

    attachments
}

/// Extract test data from Detox/Jest JSON format.
fn extract_detox(
    json_content: &str,
    screenshot_map: &std::collections::HashMap<String, String>,
    global_sequence: &mut i32,
) -> Vec<ExtractedTestSuite> {
    let report: DetoxReport = match serde_json::from_str(json_content) {
        Ok(r) => r,
        Err(e) => {
            warn!("Failed to parse Detox JSON: {}", e);
            return Vec::new();
        }
    };

    let mut suites = Vec::new();

    for file_result in report.test_results {
        let file_path = if file_result.test_file_path.is_empty() {
            None
        } else {
            // Extract relative path from full path
            let path = &file_result.test_file_path;
            let relative = path.rsplit_once("/e2e/").map(|(_, rest)| rest.to_string())
                .unwrap_or_else(|| path.clone());
            Some(relative)
        };

        // Parse start time from perfStats (Unix timestamp in milliseconds)
        let file_start_time = file_result
            .perf_stats
            .as_ref()
            .and_then(|ps| ps.start)
            .and_then(DateTime::from_timestamp_millis)
            .map(|dt| dt.with_timezone(&Utc));

        // Group tests by ancestor titles (suite name)
        let mut suite_tests: std::collections::HashMap<String, Vec<ExtractedTestCase>> =
            std::collections::HashMap::new();

        for test in &file_result.test_results {
            let suite_name = test.ancestor_titles.join(" > ");

            let status = match test.status.as_str() {
                "passed" => "passed".to_string(),
                "failed" => "failed".to_string(),
                "pending" | "skipped" | "todo" => "skipped".to_string(),
                other => other.to_string(),
            };

            let error_message = if test.failure_messages.is_empty() {
                None
            } else {
                Some(test.failure_messages.join("\n"))
            };

            let seq = *global_sequence;
            *global_sequence += 1;

            let test_case = ExtractedTestCase {
                title: test.title.clone(),
                full_title: test.full_name.clone(),
                status,
                duration_ms: test.duration.unwrap_or(0) as i32,
                retry_count: 0,
                error_message,
                sequence: seq,
                attachments: Vec::new(), // Detox doesn't embed attachments in JSON
                start_time: file_start_time, // From perfStats.start at file level
            };

            suite_tests.entry(suite_name).or_default().push(test_case);
        }

        // Create suites from grouped tests
        for (suite_name, test_cases) in suite_tests {
            let title = if suite_name.is_empty() {
                file_path.clone().unwrap_or_else(|| "Root".to_string())
            } else {
                suite_name
            };

            suites.push(ExtractedTestSuite {
                title,
                file_path: file_path.clone(),
                test_cases,
                start_time: file_start_time, // From perfStats.start
            });
        }
    }

    // Handle case where we didn't find any suites but screenshots exist
    let _ = screenshot_map; // Mark as used even though Detox doesn't embed in JSON

    suites
}

/// Extract test data from Playwright JSON format.
fn extract_playwright(
    json_content: &str,
    screenshot_map: &std::collections::HashMap<String, String>,
    global_sequence: &mut i32,
) -> Vec<ExtractedTestSuite> {
    let report: PlaywrightReport = match serde_json::from_str(json_content) {
        Ok(r) => r,
        Err(e) => {
            warn!("Failed to parse Playwright JSON: {}", e);
            return Vec::new();
        }
    };

    let mut suites = Vec::new();

    for suite in report.suites {
        let extracted = extract_playwright_suite(&suite, screenshot_map, global_sequence);
        suites.extend(extracted);
    }

    suites
}

fn extract_playwright_suite(
    suite: &PlaywrightSuite,
    screenshot_map: &std::collections::HashMap<String, String>,
    global_sequence: &mut i32,
) -> Vec<ExtractedTestSuite> {
    let mut result = Vec::new();

    let file_path = if suite.file.is_empty() {
        None
    } else {
        Some(suite.file.clone())
    };

    // Extract tests from specs
    let mut test_cases = Vec::new();
    for spec in &suite.specs {
        for test in &spec.tests {
            let extracted = extract_playwright_test(
                &spec.title,
                test,
                file_path.as_deref(),
                screenshot_map,
                global_sequence,
            );
            test_cases.extend(extracted);
        }
    }

    if !test_cases.is_empty() || !suite.title.is_empty() {
        // Compute earliest start_time from test cases
        let start_time = test_cases
            .iter()
            .filter_map(|tc| tc.start_time)
            .min();

        result.push(ExtractedTestSuite {
            title: if suite.title.is_empty() {
                file_path.clone().unwrap_or_else(|| "Root".to_string())
            } else {
                suite.title.clone()
            },
            file_path: file_path.clone(),
            test_cases,
            start_time,
        });
    }

    // Process nested suites
    for nested in &suite.suites {
        result.extend(extract_playwright_suite(nested, screenshot_map, global_sequence));
    }

    result
}

fn extract_playwright_test(
    spec_title: &str,
    test: &PlaywrightTest,
    file_path: Option<&str>,
    screenshot_map: &std::collections::HashMap<String, String>,
    global_sequence: &mut i32,
) -> Vec<ExtractedTestCase> {
    let mut cases = Vec::new();

    // Playwright can have multiple retry attempts
    for result in &test.results {
        let status = match result.status.as_str() {
            "passed" => "passed".to_string(),
            "failed" | "timedOut" => "failed".to_string(),
            "skipped" => "skipped".to_string(),
            "interrupted" => "failed".to_string(),
            other => other.to_string(),
        };

        // Check if this is a flaky test (passed after retries)
        let final_status = if result.retry > 0 && result.status == "passed" {
            "flaky".to_string()
        } else {
            status
        };

        let error_message = if result.errors.is_empty() {
            None
        } else {
            let msg = result.errors.iter()
                .filter_map(|e| e.message.clone().or_else(|| e.stack.clone()))
                .collect::<Vec<_>>()
                .join("\n");
            if msg.is_empty() { None } else { Some(truncate_message(&msg, 2000)) }
        };

        // Extract attachments
        let attachments = result.attachments.iter()
            .filter(|a| a.path.is_some())
            .enumerate()
            .map(|(idx, a)| {
                let path = a.path.clone().unwrap_or_default();
                let normalized = normalize_attachment_path(&path);
                let (s3_key, missing) = lookup_screenshot(&normalized, screenshot_map);

                ExtractedAttachment {
                    path,
                    content_type: Some(a.content_type.clone()),
                    retry: result.retry,
                    s3_key,
                    missing,
                    sequence: idx as i32,
                }
            })
            .collect();

        let seq = *global_sequence;
        *global_sequence += 1;

        // Build full title including project name if present
        let full_title = if test.project_name.is_empty() {
            spec_title.to_string()
        } else {
            format!("[{}] {}", test.project_name, spec_title)
        };

        // Parse start_time from ISO 8601 string
        let start_time = result.start_time.as_ref().and_then(|s| {
            DateTime::parse_from_rfc3339(s)
                .ok()
                .map(|dt| dt.with_timezone(&Utc))
        });

        cases.push(ExtractedTestCase {
            title: spec_title.to_string(),
            full_title,
            status: final_status,
            duration_ms: result.duration as i32,
            retry_count: result.retry,
            error_message,
            sequence: seq,
            attachments,
            start_time,
        });
    }

    // If no results, create a single case based on overall status
    if cases.is_empty() {
        let seq = *global_sequence;
        *global_sequence += 1;

        cases.push(ExtractedTestCase {
            title: spec_title.to_string(),
            full_title: spec_title.to_string(),
            status: test.status.clone(),
            duration_ms: 0,
            retry_count: 0,
            error_message: None,
            sequence: seq,
            attachments: Vec::new(),
            start_time: None,
        });
    }

    let _ = file_path; // Mark as used
    cases
}

/// Try to auto-detect the JSON format and extract accordingly.
fn try_auto_detect(
    json_content: &str,
    screenshot_map: &std::collections::HashMap<String, String>,
    global_sequence: &mut i32,
) -> Option<Vec<ExtractedTestSuite>> {
    let value: JsonValue = serde_json::from_str(json_content).ok()?;

    // Check for Playwright indicators
    if value.get("config").is_some() && value.get("suites").is_some() {
        return Some(extract_playwright(json_content, screenshot_map, global_sequence));
    }

    // Check for Detox/Jest indicators
    if value.get("numTotalTests").is_some() && value.get("testResults").is_some() {
        return Some(extract_detox(json_content, screenshot_map, global_sequence));
    }

    // Check for Cypress/mochawesome indicators
    if value.get("stats").is_some() && value.get("results").is_some() {
        return Some(extract_cypress(json_content, screenshot_map, global_sequence));
    }

    None
}

// ============================================================================
// Helper Functions
// ============================================================================

/// Normalize attachment path by removing common prefixes.
fn normalize_attachment_path(path: &str) -> String {
    let mut result = path;

    // Check for configurable prefix from environment
    if let Ok(prefix) = std::env::var("TSIO_ATTACHMENT_PATH_PREFIX")
        && result.starts_with(&prefix)
    {
        result = &result[prefix.len()..];
    }

    // Handle common prefixes
    let common_prefixes = [
        "../results/output/",
        "../output/",
        "./results/output/",
        "./output/",
    ];

    for prefix in common_prefixes {
        if result.starts_with(prefix) {
            result = &result[prefix.len()..];
            break;
        }
    }

    // Remove any remaining leading ../ or ./
    while result.starts_with("../") {
        result = &result[3..];
    }
    while result.starts_with("./") {
        result = &result[2..];
    }

    result.to_string()
}

/// Look up a screenshot path in the uploaded screenshots map.
fn lookup_screenshot(
    path: &str,
    screenshot_map: &std::collections::HashMap<String, String>,
) -> (Option<String>, bool) {
    // Direct lookup
    if let Some(key) = screenshot_map.get(path) {
        return (Some(key.clone()), false);
    }

    // Try with just the filename
    let filename = std::path::Path::new(path)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or(path);

    // Search for a matching screenshot by filename suffix
    if let Some((_, key)) = screenshot_map.iter().find(|(k, _)| k.ends_with(filename)) {
        return (Some(key.clone()), false);
    }

    (None, true)
}

/// Truncate a message to a maximum length.
fn truncate_message(msg: &str, max_len: usize) -> String {
    if msg.len() <= max_len {
        msg.to_string()
    } else {
        format!("{}...", &msg[..max_len])
    }
}

/// Count test case statuses by unique spec (full_title).
/// For specs with multiple attempts, use the status of the last attempt (highest retry_count).
/// Returns (passed, failed, skipped, flaky, unique_count).
fn count_statuses(test_cases: &[ExtractedTestCase]) -> (i32, i32, i32, i32, i32) {
    use std::collections::HashMap;

    // Group by full_title and find the status of the last attempt (highest retry_count)
    let mut final_statuses: HashMap<&str, &str> = HashMap::new();
    let mut max_retries: HashMap<&str, i32> = HashMap::new();

    for tc in test_cases {
        let current_max = *max_retries.get(tc.full_title.as_str()).unwrap_or(&-1);
        if tc.retry_count > current_max {
            max_retries.insert(&tc.full_title, tc.retry_count);
            final_statuses.insert(&tc.full_title, &tc.status);
        }
    }

    let mut passed = 0;
    let mut failed = 0;
    let mut skipped = 0;
    let mut flaky = 0;

    for status in final_statuses.values() {
        match *status {
            "passed" => passed += 1,
            "failed" | "timedOut" => failed += 1,
            "skipped" => skipped += 1,
            "flaky" => flaky += 1,
            _ => {}
        }
    }

    let unique_count = final_statuses.len() as i32;
    (passed, failed, skipped, flaky, unique_count)
}

/// Check if all jobs for a report are complete and update report status.
async fn check_report_completion(pool: &DbPool, report_id: Uuid) -> Result<(), String> {
    let report = pool
        .get_report_by_id(report_id)
        .await
        .map_err(|e| e.to_string())?
        .ok_or_else(|| format!("Report {} not found", report_id))?;

    let completed = pool
        .count_completed_jobs(report_id)
        .await
        .map_err(|e| e.to_string())?;

    let expected = report.expected_jobs as u64;

    if completed >= expected {
        pool.update_report_status(report_id, crate::models::ReportStatus::Complete)
            .await
            .map_err(|e| e.to_string())?;
        info!(
            "Report {} marked as complete ({}/{} jobs)",
            report_id, completed, expected
        );
    } else {
        info!(
            "Report {} progress: {}/{} jobs complete",
            report_id, completed, expected
        );
    }

    Ok(())
}
