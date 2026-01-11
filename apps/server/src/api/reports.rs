//! Report API endpoints.

use actix_web::{get, web, HttpResponse};
use serde::Serialize;
use uuid::Uuid;

use crate::db::{queries, DbPool};
use crate::error::{AppError, AppResult};
use crate::models::{
    Pagination, PaginationParams, ReportDetail, ReportSummary, TestSpecListResponse,
    TestSuiteListResponse,
};

/// Report list response.
#[derive(Serialize)]
pub struct ReportListResponse {
    pub reports: Vec<ReportSummary>,
    pub pagination: Pagination,
}

/// Configure report routes.
/// Note: More specific routes must be registered before generic ones.
pub fn configure_report_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(list_reports)
        // Specific paths first
        .service(get_report_html)
        .service(get_report_assets)
        .service(get_report_screenshots)
        .service(get_report_data_file)
        .service(get_suite_specs)
        .service(get_report_suites)
        // Generic paths last
        .service(get_report);
}

/// List all reports with pagination.
///
/// GET /reports?page=1&limit=20
#[get("/reports")]
async fn list_reports(
    pool: web::Data<DbPool>,
    query: web::Query<PaginationParams>,
) -> AppResult<HttpResponse> {
    let conn = pool.connection();
    let (reports, pagination) = queries::list_reports(&conn, &query)?;

    Ok(HttpResponse::Ok().json(ReportListResponse {
        reports,
        pagination,
    }))
}

/// Get report details by ID.
///
/// GET /reports/{id}
#[get("/reports/{id}")]
async fn get_report(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
    data_dir: web::Data<std::path::PathBuf>,
) -> AppResult<HttpResponse> {
    let id = Uuid::parse_str(&path.into_inner())?;
    let conn = pool.connection();

    let report = queries::get_active_report_by_id(&conn, id)?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;

    let stats = queries::get_report_stats(&conn, id)?;

    // Check if files exist on disk
    // Support both Playwright (index.html) and Cypress (mochawesome.html) reports
    let report_dir = data_dir.join(&report.file_path);
    let has_files = report_dir.exists()
        && (report_dir.join("index.html").exists() || report_dir.join("mochawesome.html").exists());

    let mut detail: ReportDetail = report.into();
    detail.stats = stats;
    detail.has_files = has_files;

    Ok(HttpResponse::Ok().json(detail))
}

/// Get HTML report for viewing.
///
/// GET /reports/{id}/html
///
/// Serves different HTML files based on framework:
/// - Playwright: index.html
/// - Cypress: mochawesome.html (fallback to index.html if not found)
#[get("/reports/{id}/html")]
async fn get_report_html(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
    data_dir: web::Data<std::path::PathBuf>,
) -> AppResult<HttpResponse> {
    let id = Uuid::parse_str(&path.into_inner())?;

    // Get file path and framework from database, then drop connection before async file read
    let (report_dir, framework) = {
        let conn = pool.connection();
        let report = queries::get_active_report_by_id(&conn, id)?
            .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;
        (data_dir.join(&report.file_path), report.framework.clone())
    };

    // Determine HTML file based on framework
    let html_path = if framework.as_deref() == Some("cypress") {
        // For Cypress, prefer mochawesome.html, fallback to index.html
        let mochawesome_path = report_dir.join("mochawesome.html");
        if mochawesome_path.exists() {
            mochawesome_path
        } else {
            report_dir.join("index.html")
        }
    } else {
        // For Playwright and unknown frameworks, use index.html
        report_dir.join("index.html")
    };

    if !html_path.exists() {
        return Err(AppError::NotFound("HTML report file".to_string()));
    }

    let content = tokio::fs::read_to_string(&html_path).await?;

    Ok(HttpResponse::Ok()
        .content_type("text/html; charset=utf-8")
        .body(content))
}

/// Get asset files for HTML report (CSS, JS, fonts).
///
/// GET /reports/{id}/assets/{filename}
///
/// Serves files from the assets/ directory for mochawesome HTML reports.
#[get("/reports/{id}/assets/{filename:.*}")]
async fn get_report_assets(
    pool: web::Data<DbPool>,
    path: web::Path<(String, String)>,
    data_dir: web::Data<std::path::PathBuf>,
) -> AppResult<HttpResponse> {
    let (id_str, filename) = path.into_inner();
    let id = Uuid::parse_str(&id_str)?;

    // Validate filename to prevent path traversal
    if filename.contains("..") || filename.starts_with('/') {
        return Err(AppError::InvalidInput("Invalid filename".to_string()));
    }

    // Get report path from database, then drop connection before async file read
    let report_path = {
        let conn = pool.connection();
        let report = queries::get_active_report_by_id(&conn, id)?
            .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;
        data_dir.join(&report.file_path)
    };

    let file_path = report_path.join("assets").join(&filename);

    if !file_path.exists() {
        return Err(AppError::NotFound(format!("Asset file {}", filename)));
    }

    let content = tokio::fs::read(&file_path).await?;
    let content_type = mime_guess::from_path(&filename)
        .first_or_octet_stream()
        .to_string();

    Ok(HttpResponse::Ok().content_type(content_type).body(content))
}

/// Get screenshot files for Cypress reports.
///
/// GET /reports/{id}/screenshots/{filepath}
///
/// Serves files from the screenshots/ directory for Cypress HTML reports.
/// Supports nested paths like screenshots/spec_file/screenshot.png
#[get("/reports/{id}/screenshots/{filepath:.*}")]
async fn get_report_screenshots(
    pool: web::Data<DbPool>,
    path: web::Path<(String, String)>,
    data_dir: web::Data<std::path::PathBuf>,
) -> AppResult<HttpResponse> {
    let (id_str, filepath) = path.into_inner();
    let id = Uuid::parse_str(&id_str)?;

    // Validate filepath to prevent path traversal
    if filepath.contains("..") || filepath.starts_with('/') {
        return Err(AppError::InvalidInput("Invalid filepath".to_string()));
    }

    // Get report path from database, then drop connection before async file read
    let report_path = {
        let conn = pool.connection();
        let report = queries::get_active_report_by_id(&conn, id)?
            .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;
        data_dir.join(&report.file_path)
    };

    let file_path = report_path.join("screenshots").join(&filepath);

    if !file_path.exists() {
        return Err(AppError::NotFound(format!("Screenshot file {}", filepath)));
    }

    let content = tokio::fs::read(&file_path).await?;
    let content_type = mime_guess::from_path(&filepath)
        .first_or_octet_stream()
        .to_string();

    Ok(HttpResponse::Ok().content_type(content_type).body(content))
}

/// Get a file from the report's data directory (screenshots, traces, etc.).
///
/// GET /reports/{id}/data/{filename}
///
/// Searches multiple directories for the file:
/// - Playwright: data/ subdirectory
/// - Cypress: screenshots/ subdirectory
/// - Fallback: root report directory
#[get("/reports/{id}/data/{filename:.*}")]
async fn get_report_data_file(
    pool: web::Data<DbPool>,
    path: web::Path<(String, String)>,
    data_dir: web::Data<std::path::PathBuf>,
) -> AppResult<HttpResponse> {
    let (id_str, filename) = path.into_inner();
    let id = Uuid::parse_str(&id_str)?;

    // Validate filename to prevent path traversal
    if filename.contains("..") || filename.starts_with('/') {
        return Err(AppError::InvalidInput("Invalid filename".to_string()));
    }

    // Get report path from database, then drop connection before async file read
    let report_path = {
        let conn = pool.connection();
        let report = queries::get_active_report_by_id(&conn, id)?
            .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;
        data_dir.join(&report.file_path)
    };

    // Search multiple directories for the file:
    // 1. data/ (Playwright screenshots, traces)
    // 2. screenshots/ (Cypress screenshots)
    // 3. root report directory (fallback)
    let search_paths = [
        report_path.join("data").join(&filename),
        report_path.join("screenshots").join(&filename),
        report_path.join(&filename),
    ];

    let file_path = search_paths
        .iter()
        .find(|p| p.exists())
        .ok_or_else(|| AppError::NotFound(format!("Data file {}", filename)))?;

    let content = tokio::fs::read(file_path).await?;
    let content_type = mime_guess::from_path(&filename)
        .first_or_octet_stream()
        .to_string();

    Ok(HttpResponse::Ok().content_type(content_type).body(content))
}

/// Get test suites for a report.
///
/// GET /reports/{id}/suites
#[get("/reports/{id}/suites")]
async fn get_report_suites(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
) -> AppResult<HttpResponse> {
    let id = Uuid::parse_str(&path.into_inner())?;
    let conn = pool.connection();

    // Verify report exists
    let _ = queries::get_active_report_by_id(&conn, id)?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;

    let suites = queries::get_test_suites(&conn, id)?;

    Ok(HttpResponse::Ok().json(TestSuiteListResponse { suites }))
}

/// Get test specs with results for a suite.
///
/// GET /reports/{id}/suites/{suite_id}/specs
#[get("/reports/{id}/suites/{suite_id}/specs")]
async fn get_suite_specs(
    pool: web::Data<DbPool>,
    path: web::Path<(String, i64)>,
) -> AppResult<HttpResponse> {
    use crate::models::ScreenshotInfo;

    let (report_id_str, suite_id) = path.into_inner();
    let report_id = Uuid::parse_str(&report_id_str)?;
    let conn = pool.connection();

    // Verify report exists and get framework info
    let report = queries::get_active_report_by_id(&conn, report_id)?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    let mut specs = queries::get_suite_specs_with_results(&conn, suite_id)?;

    // For Detox reports, enrich specs with screenshot info
    if report.framework.as_deref() == Some("detox") {
        // Get all jobs for this report (screenshots can be in any job)
        let jobs = queries::get_detox_jobs_by_report_id(&conn, report_id)?;
        for spec in &mut specs {
            // full_title contains the full_name used for screenshot lookup
            // Normalize: replace / and " with _ since folder names can't contain these characters
            let normalized_full_title = spec.full_title.replace(['/', '"'], "_");

            // Search across all jobs for screenshots matching this spec
            for job in &jobs {
                let screenshots = queries::get_detox_screenshots_by_test_name(
                    &conn,
                    job.id,
                    &normalized_full_title,
                )?;

                if !screenshots.is_empty() {
                    spec.screenshots = Some(
                        screenshots
                            .into_iter()
                            .map(|s| ScreenshotInfo {
                                file_path: s.file_path,
                                screenshot_type: s.screenshot_type.as_str().to_string(),
                            })
                            .collect(),
                    );
                    break; // Found screenshots, no need to check other jobs
                }
            }
        }
    }

    Ok(HttpResponse::Ok().json(TestSpecListResponse { specs }))
}
