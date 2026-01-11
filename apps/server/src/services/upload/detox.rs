//! Detox report upload handlers.

use actix_web::{post, web, HttpRequest, HttpResponse};
use std::path::{Path, PathBuf};
use tracing::{info, warn};
use uuid::Uuid;

use crate::db::{queries, DbPool};
use crate::error::AppResult;
use crate::models::{DetoxJob, DetoxScreenshot, ExtractionStatus, Report, ReportStats};
use crate::services::extraction;

use super::{handle_upload_request, Framework, UploadRequest};

/// Request Detox report upload (Phase 1).
///
/// POST /reports/upload/detox/request
/// Content-Type: application/json
/// X-API-Key: <api-key>
///
/// ## Request Body
/// ```json
/// {
///   "framework_version": "20.x",
///   "platform": "ios",
///   "github_context": { "repository": "owner/repo", ... },
///   "filenames": [
///     "ios-results-abc123-1/jest-stare/ios-data.json",
///     "ios-results-abc123-1/jest-stare/ios-main.html",
///     "ios-results-abc123-1/artifacts/screenshot1.png",
///     "ios-results-abc123-2/jest-stare/ios-data.json",
///     "ios-results-abc123-2/jest-stare/ios-main.html"
///   ]
/// }
/// ```
///
/// ## File Structure
/// - Job folder: `{platform}-results-{id}-{number}/` (e.g., `ios-results-abc123-1/`)
/// - Data file: `{job-folder}/jest-stare/{platform}-data.json`
/// - HTML report: `{job-folder}/jest-stare/{platform}-main.html`
///
/// ## Response
/// Returns report_id and list of accepted/rejected files.
#[post("/reports/upload/detox/request")]
pub(crate) async fn request_detox_upload(
    req: HttpRequest,
    body: web::Json<UploadRequest>,
    pool: web::Data<DbPool>,
    data_dir: web::Data<PathBuf>,
    api_key: web::Data<String>,
    max_upload_size: web::Data<usize>,
) -> AppResult<HttpResponse> {
    handle_upload_request(
        req,
        body,
        pool,
        data_dir,
        api_key,
        max_upload_size,
        Framework::Detox,
    )
    .await
}

/// Trigger Detox results extraction.
pub(crate) fn trigger_detox_extraction(
    conn: &rusqlite::Connection,
    report_id: Uuid,
    report_dir: &Path,
    files: &[String],
    report: &Report,
) -> &'static str {
    // Find all data files: {job-folder}/jest-stare/{platform}-data.json
    let data_files: Vec<_> = files
        .iter()
        .filter(|f| f.contains("/jest-stare/") && f.ends_with("-data.json"))
        .collect();

    if data_files.is_empty() {
        let msg = "No jest-stare data files found";
        warn!("Detox extraction failed for report {}: {}", report_id, msg);
        let _ = queries::update_report_status(
            conn,
            report_id,
            ExtractionStatus::Failed,
            Some("detox"),
            report.framework_version.as_deref(),
            Some(msg),
        );
        return "failed";
    }

    let result = process_jobs(conn, report_id, report_dir, &data_files);

    match result {
        Ok(_) => {
            // Update report status
            let _ = queries::update_report_status(
                conn,
                report_id,
                ExtractionStatus::Completed,
                Some("detox"),
                report.framework_version.as_deref(),
                None,
            );
            "completed"
        }
        Err(e) => {
            warn!("Detox extraction failed for report {}: {}", report_id, e);
            let _ = queries::update_report_status(
                conn,
                report_id,
                ExtractionStatus::Failed,
                Some("detox"),
                report.framework_version.as_deref(),
                Some(&e.to_string()),
            );
            "failed"
        }
    }
}

/// Process Detox jobs (one or more).
fn process_jobs(
    conn: &rusqlite::Connection,
    report_id: Uuid,
    report_dir: &Path,
    data_files: &[&String],
) -> AppResult<()> {
    info!(
        "Processing {} Detox jobs for report {}",
        data_files.len(),
        report_id
    );

    let mut total_tests = 0;
    let mut total_passed = 0;
    let mut total_failed = 0;
    let mut total_skipped = 0;
    let mut total_duration: i64 = 0;

    for data_file in data_files {
        // Extract job folder (job name): "ios-results-xxx-1/jest-stare/ios-data.json" -> "ios-results-xxx-1"
        let job_name = data_file.split('/').next().unwrap_or("default");

        let json_path = report_dir.join(data_file);
        let stats = extraction::extract_jest_stare_results(conn, report_id, &json_path)?;

        info!(
            "Job '{}': {} tests ({} passed, {} failed)",
            job_name, stats.total_tests, stats.passed_tests, stats.failed_tests
        );

        // Create job
        let mut job = DetoxJob::new(report_id, job_name.to_string());
        job.tests_count = stats.total_tests;
        job.passed_count = stats.passed_tests;
        job.failed_count = stats.failed_tests;
        job.skipped_count = stats.skipped_tests;
        job.duration_ms = stats.duration_ms;
        queries::insert_detox_job(conn, &job)?;

        // Scan screenshots for this job
        let job_dir = report_dir.join(job_name);
        if job_dir.exists() {
            store_screenshots(conn, &job, &job_dir, Some(job_name))?;
        }

        // Accumulate totals
        total_tests += stats.total_tests;
        total_passed += stats.passed_tests;
        total_failed += stats.failed_tests;
        total_skipped += stats.skipped_tests;
        total_duration += stats.duration_ms;
    }

    // Insert report stats
    queries::insert_report_stats(
        conn,
        &ReportStats {
            report_id,
            start_time: chrono::Utc::now(),
            duration_ms: total_duration,
            expected: total_passed,
            skipped: total_skipped,
            unexpected: total_failed,
            flaky: 0,
        },
    )?;

    info!(
        "Detox extraction complete: report={}, {} jobs, {} tests",
        report_id,
        data_files.len(),
        total_tests
    );
    Ok(())
}

/// Scan and store screenshots for a job.
fn store_screenshots(
    conn: &rusqlite::Connection,
    job: &DetoxJob,
    scan_dir: &Path,
    path_prefix: Option<&str>,
) -> AppResult<()> {
    let screenshots = extraction::scan_detox_screenshots(scan_dir);
    let mut count = 0;

    for discovered in &screenshots {
        let file_path = match path_prefix {
            Some(prefix) => format!("{}/{}", prefix, discovered.file_path),
            None => discovered.file_path.clone(),
        };

        let screenshot = DetoxScreenshot::new(
            job.id,
            discovered.test_full_name.clone(),
            discovered.screenshot_type,
            file_path,
        );
        queries::insert_detox_screenshot(conn, &screenshot)?;
        count += 1;
    }

    if count > 0 {
        info!("Stored {} screenshots for job {}", count, job.id);
    }

    Ok(())
}
