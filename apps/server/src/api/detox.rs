//! Detox Report API endpoints.
//!
//! Provides endpoints for viewing Detox jobs and combined results for reports.

use actix_web::{get, web, HttpResponse};
use serde::{Deserialize, Serialize};
use tracing::{info, instrument};
use uuid::Uuid;

use crate::db::{queries, DbPool};
use crate::error::{AppError, AppResult};
use crate::models::{Pagination, PaginationParams};

/// Summary of a Detox job.
#[derive(Serialize)]
pub struct DetoxJobSummaryResponse {
    pub id: String,
    pub job_name: String,
    pub tests_count: i32,
    pub passed_count: i32,
    pub failed_count: i32,
    pub skipped_count: i32,
    pub duration_ms: i64,
    pub report_id: String,
    pub created_at: String,
}

/// Detox job list response.
#[derive(Serialize)]
pub struct DetoxJobListResponse {
    pub jobs: Vec<DetoxJobSummaryResponse>,
}

/// Combined test result for display.
#[derive(Serialize)]
pub struct DetoxCombinedTestResult {
    pub id: i64,
    pub title: String,
    pub full_title: String,
    pub status: String,
    pub duration_ms: i64,
    pub error_message: Option<String>,
    pub job_id: String,
    pub job_name: String,
    pub suite_title: Option<String>,
    pub has_screenshots: bool,
}

/// Combined tests list response.
#[derive(Serialize)]
pub struct DetoxCombinedTestsResponse {
    pub tests: Vec<DetoxCombinedTestResult>,
    pub pagination: Pagination,
}

/// Query parameters for combined tests.
#[derive(Debug, Deserialize)]
pub struct CombinedTestsQueryParams {
    #[serde(flatten)]
    pub pagination: PaginationParams,
    pub status: Option<String>,
    pub search: Option<String>,
}

/// Detox job detail response.
#[derive(Serialize)]
pub struct DetoxJobDetailResponse {
    pub id: String,
    pub job_name: String,
    pub tests_count: i32,
    pub passed_count: i32,
    pub failed_count: i32,
    pub skipped_count: i32,
    pub duration_ms: i64,
    pub report_id: String,
    pub created_at: String,
}

/// Screenshot response.
#[derive(Serialize)]
pub struct DetoxScreenshotResponse {
    pub id: i64,
    pub file_path: String,
    pub screenshot_type: String,
    pub test_full_name: String,
    pub available: bool,
}

/// Screenshots list response.
#[derive(Serialize)]
pub struct DetoxScreenshotsListResponse {
    pub screenshots: Vec<DetoxScreenshotResponse>,
}

/// Configure Detox routes.
pub fn configure_detox_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(get_report_detox_jobs)
        .service(get_report_detox_tests)
        .service(get_detox_job)
        .service(get_detox_job_html)
        .service(get_detox_test_screenshots);
}

/// Get Detox jobs for a report.
///
/// GET /reports/{id}/detox-jobs
#[get("/reports/{id}/detox-jobs")]
#[instrument(name = "detox.get_report_jobs", skip(pool, path))]
async fn get_report_detox_jobs(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
) -> AppResult<HttpResponse> {
    let report_id = Uuid::parse_str(&path.into_inner())?;
    info!(report_id = %report_id, "Getting Detox jobs for report");
    let conn = pool.connection();

    // Verify report exists
    let _ = queries::get_active_report_by_id(&conn, report_id)?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    let jobs = queries::get_detox_jobs_by_report_id(&conn, report_id)?;

    let job_summaries: Vec<DetoxJobSummaryResponse> = jobs
        .into_iter()
        .map(|j| DetoxJobSummaryResponse {
            id: j.id.to_string(),
            job_name: j.job_name,
            tests_count: j.tests_count,
            passed_count: j.passed_count,
            failed_count: j.failed_count,
            skipped_count: j.skipped_count,
            duration_ms: j.duration_ms,
            report_id: j.report_id.to_string(),
            created_at: j.created_at.to_rfc3339(),
        })
        .collect();

    Ok(HttpResponse::Ok().json(DetoxJobListResponse {
        jobs: job_summaries,
    }))
}

/// Get combined test results for a Detox report.
///
/// GET /reports/{id}/detox-tests?status=failed&search=Login
#[get("/reports/{id}/detox-tests")]
#[instrument(name = "detox.get_report_tests", skip(pool, path), fields(status = ?query.status, search = ?query.search))]
async fn get_report_detox_tests(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
    query: web::Query<CombinedTestsQueryParams>,
) -> AppResult<HttpResponse> {
    let report_id = Uuid::parse_str(&path.into_inner())?;
    info!(report_id = %report_id, "Getting combined test results for report");
    let conn = pool.connection();

    // Verify report exists
    let _ = queries::get_active_report_by_id(&conn, report_id)?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    let (tests, pagination) = queries::get_detox_combined_tests(
        &conn,
        report_id,
        &query.pagination,
        query.status.as_deref(),
        query.search.as_deref(),
    )?;

    Ok(HttpResponse::Ok().json(DetoxCombinedTestsResponse { tests, pagination }))
}

/// Get Detox job details by ID.
///
/// GET /detox-jobs/{id}
#[get("/detox-jobs/{id}")]
#[instrument(name = "detox.get_job", skip(pool, path))]
async fn get_detox_job(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
) -> AppResult<HttpResponse> {
    let id = Uuid::parse_str(&path.into_inner())?;
    info!(job_id = %id, "Getting Detox job details");
    let conn = pool.connection();

    let job = queries::get_detox_job_by_id(&conn, id)?
        .ok_or_else(|| AppError::DetoxJobNotFound(id.to_string()))?;

    Ok(HttpResponse::Ok().json(DetoxJobDetailResponse {
        id: job.id.to_string(),
        job_name: job.job_name,
        tests_count: job.tests_count,
        passed_count: job.passed_count,
        failed_count: job.failed_count,
        skipped_count: job.skipped_count,
        duration_ms: job.duration_ms,
        report_id: job.report_id.to_string(),
        created_at: job.created_at.to_rfc3339(),
    }))
}

/// Get HTML report for a Detox job.
///
/// GET /detox-jobs/{id}/html
#[get("/detox-jobs/{id}/html")]
#[instrument(name = "detox.get_job_html", skip(pool, path, data_dir))]
async fn get_detox_job_html(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
    data_dir: web::Data<std::path::PathBuf>,
) -> AppResult<HttpResponse> {
    let id = Uuid::parse_str(&path.into_inner())?;
    info!(job_id = %id, "Getting Detox job HTML report");

    // Get job and report path from database
    let report_file_path = {
        let conn = pool.connection();
        let job = queries::get_detox_job_by_id(&conn, id)?
            .ok_or_else(|| AppError::NotFound(format!("Detox job {}", id)))?;

        let report = queries::get_active_report_by_id(&conn, job.report_id)?
            .ok_or_else(|| AppError::NotFound(format!("Report {}", job.report_id)))?;

        report.file_path
    };

    // Look for jest-stare HTML file (e.g., jest-stare/ios-main.html or jest-stare/android-main.html)
    let report_dir = data_dir.join(&report_file_path);
    let jest_stare_dir = report_dir.join("jest-stare");

    // Find the HTML file in jest-stare directory
    let html_path = if jest_stare_dir.exists() {
        let entries = std::fs::read_dir(&jest_stare_dir)
            .map_err(|e| AppError::FileSystem(format!("Failed to read jest-stare dir: {}", e)))?;

        let html_file: Option<std::path::PathBuf> = entries
            .filter_map(|e: Result<std::fs::DirEntry, std::io::Error>| e.ok())
            .find(|e: &std::fs::DirEntry| {
                e.path()
                    .extension()
                    .map(|ext| ext == "html")
                    .unwrap_or(false)
            })
            .map(|e: std::fs::DirEntry| e.path());

        html_file.ok_or_else(|| AppError::NotFound("HTML report file".to_string()))?
    } else {
        return Err(AppError::NotFound("jest-stare directory".to_string()));
    };

    if !html_path.exists() {
        return Err(AppError::NotFound("HTML report file".to_string()));
    }

    let content: String = tokio::fs::read_to_string(&html_path).await?;

    Ok(HttpResponse::Ok()
        .content_type("text/html; charset=utf-8")
        .body(content))
}

/// Get screenshots for a Detox test by job ID and test name.
///
/// GET /detox-jobs/{job_id}/tests/{test_full_name}/screenshots
#[get("/detox-jobs/{job_id}/tests/{test_full_name}/screenshots")]
#[instrument(name = "detox.get_test_screenshots", skip(pool, path))]
async fn get_detox_test_screenshots(
    pool: web::Data<DbPool>,
    path: web::Path<(String, String)>,
) -> AppResult<HttpResponse> {
    let (job_id_str, test_full_name) = path.into_inner();
    let job_id = Uuid::parse_str(&job_id_str)?;
    info!(job_id = %job_id, test_name = %test_full_name, "Getting test screenshots");
    let conn = pool.connection();

    // URL decode the test name (it may contain spaces and special characters)
    let test_full_name = percent_encoding::percent_decode_str(&test_full_name)
        .decode_utf8()
        .map_err(|e| AppError::InvalidInput(format!("Invalid test name encoding: {}", e)))?
        .into_owned();

    // Normalize: replace / and " with _ since folder names can't contain these characters
    let normalized_test_name = test_full_name.replace(['/', '"'], "_");

    let screenshots =
        queries::get_detox_screenshots_by_test_name(&conn, job_id, &normalized_test_name)?;

    let screenshot_responses: Vec<DetoxScreenshotResponse> = screenshots
        .into_iter()
        .filter_map(|s| {
            s.id.map(|id| DetoxScreenshotResponse {
                id,
                file_path: s.file_path,
                screenshot_type: s.screenshot_type.as_str().to_string(),
                test_full_name: s.test_full_name,
                available: s.deleted_at.is_none(),
            })
        })
        .collect();

    Ok(HttpResponse::Ok().json(DetoxScreenshotsListResponse {
        screenshots: screenshot_responses,
    }))
}
