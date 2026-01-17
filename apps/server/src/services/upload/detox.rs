//! Detox report upload handlers.

use actix_web::{HttpResponse, post, web};
use sea_orm::DatabaseConnection;
use tracing::{info, warn};
use utoipa;
use uuid::Uuid;

use crate::auth::ApiKeyAuth;
use crate::db::{DbPool, queries};
use crate::error::AppResult;
use crate::models::{DetoxJob, DetoxScreenshot, ExtractionStatus, Report, ReportStats};
use crate::services::Storage;
use crate::services::extraction;

use super::{Framework, UploadRequest, handle_upload_request};

/// Initialize Detox report upload.
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
#[utoipa::path(
    post,
    path = "/api/v1/reports/upload/detox/request",
    tag = "Upload",
    request_body = super::UploadRequest,
    responses(
        (status = 200, description = "Upload request initialized", body = super::UploadRequestResponse),
        (status = 400, description = "Invalid request")
    ),
    security(
        ("api_key" = [])
    )
)]
#[post("/reports/upload/detox/request")]
pub async fn request_detox_upload(
    auth: ApiKeyAuth,
    body: web::Json<UploadRequest>,
    pool: web::Data<DbPool>,
    max_upload_size: web::Data<usize>,
    max_files_per_request: web::Data<usize>,
) -> AppResult<HttpResponse> {
    handle_upload_request(
        &auth.caller,
        body,
        pool,
        max_upload_size,
        max_files_per_request,
        Framework::Detox,
    )
    .await
}

/// Trigger Detox results extraction.
pub(crate) async fn trigger_detox_extraction(
    conn: &DatabaseConnection,
    report_id: Uuid,
    storage: &Storage,
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
        )
        .await;
        return "failed";
    }

    let result = process_jobs(conn, report_id, storage, &data_files, files).await;

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
            )
            .await;
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
            )
            .await;
            "failed"
        }
    }
}

/// Process Detox jobs (one or more).
async fn process_jobs(
    conn: &DatabaseConnection,
    report_id: Uuid,
    storage: &Storage,
    data_files: &[&String],
    all_files: &[String],
) -> AppResult<()> {
    info!(
        "Processing {} Detox jobs for report {}",
        data_files.len(),
        report_id
    );

    let report_id_str = report_id.to_string();
    let mut total_tests = 0;
    let mut total_passed = 0;
    let mut total_failed = 0;
    let mut total_skipped = 0;
    let mut total_duration: i64 = 0;

    for data_file in data_files {
        // Extract job folder (job name): "ios-results-xxx-1/jest-stare/ios-data.json" -> "ios-results-xxx-1"
        let job_name = data_file.split('/').next().unwrap_or("default");

        // Download JSON from S3
        let s3_key = Storage::report_key(&report_id_str, data_file);
        let json_data = match storage.download(&s3_key).await? {
            Some(data) => data,
            None => {
                warn!("Data file {} not found in S3", data_file);
                continue;
            }
        };

        let stats =
            extraction::extract_jest_stare_results_from_bytes(conn, report_id, &json_data).await?;

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
        queries::insert_detox_job(conn, &job).await?;

        // Store screenshots for this job (scan file list instead of directory)
        store_screenshots_from_file_list(conn, &job, all_files, job_name).await?;

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
    )
    .await?;

    info!(
        "Detox extraction complete: report={}, {} jobs, {} tests",
        report_id,
        data_files.len(),
        total_tests
    );
    Ok(())
}

/// Store screenshots from file list (for S3 storage).
async fn store_screenshots_from_file_list(
    conn: &DatabaseConnection,
    job: &DetoxJob,
    all_files: &[String],
    job_name: &str,
) -> AppResult<()> {
    let mut count = 0;
    let job_prefix = format!("{}/", job_name);

    // Find screenshot files for this job
    for file_path in all_files {
        if !file_path.starts_with(&job_prefix) {
            continue;
        }

        // Check if it's a screenshot (PNG file not in jest-stare)
        if !file_path.ends_with(".png") || file_path.contains("/jest-stare/") {
            continue;
        }

        // Parse screenshot info from filename
        if let Some(discovered) = extraction::parse_detox_screenshot_path(file_path) {
            let screenshot = DetoxScreenshot::new(
                job.id,
                discovered.test_full_name,
                discovered.screenshot_type,
                file_path.to_string(),
            );
            queries::insert_detox_screenshot(conn, &screenshot).await?;
            count += 1;
        }
    }

    if count > 0 {
        info!("Stored {} screenshots for job {}", count, job.id);
    }

    Ok(())
}
