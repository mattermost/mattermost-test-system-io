//! Report API endpoints.

use actix_web::{HttpResponse, get, web};
use serde::Serialize;
use utoipa::ToSchema;
use uuid::Uuid;

use crate::db::{DbPool, queries};
use crate::error::{AppError, AppResult};
use crate::models::{
    Pagination, PaginationParams, ReportDetail, ReportSummary, TestSpecListResponse,
    TestSuiteListResponse,
};
use crate::services::Storage;

/// Report list response.
#[derive(Serialize, ToSchema)]
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
#[utoipa::path(
    get,
    path = "/api/v1/reports",
    tag = "Reports",
    params(
        ("page" = Option<u32>, Query, description = "Page number (default: 1)"),
        ("limit" = Option<u32>, Query, description = "Items per page (default: 100, max: 100)")
    ),
    responses(
        (status = 200, description = "List of reports", body = ReportListResponse)
    )
)]
#[get("/reports")]
pub async fn list_reports(
    pool: web::Data<DbPool>,
    query: web::Query<PaginationParams>,
) -> AppResult<HttpResponse> {
    let conn = pool.connection();
    let (reports, pagination) = queries::list_reports(conn, &query).await?;

    Ok(HttpResponse::Ok().json(ReportListResponse {
        reports,
        pagination,
    }))
}

/// Get report details by ID.
///
/// GET /reports/{id}
#[utoipa::path(
    get,
    path = "/api/v1/reports/{id}",
    tag = "Reports",
    params(
        ("id" = String, Path, description = "Report UUID")
    ),
    responses(
        (status = 200, description = "Report details", body = ReportDetail),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse)
    )
)]
#[get("/reports/{id}")]
pub async fn get_report(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
    storage: web::Data<Storage>,
) -> AppResult<HttpResponse> {
    let id = Uuid::parse_str(&path.into_inner())?;
    let conn = pool.connection();

    let report = queries::get_active_report_by_id(conn, id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;

    let stats = queries::get_report_stats(conn, id).await?;

    // Check if files exist in S3
    // Support both Playwright (index.html) and Cypress (mochawesome.html) reports
    let report_id_str = id.to_string();
    let index_key = Storage::report_key(&report_id_str, "index.html");
    let mochawesome_key = Storage::report_key(&report_id_str, "mochawesome.html");

    let has_files = match storage.download(&index_key).await {
        Ok(Some(_)) => true,
        _ => matches!(storage.download(&mochawesome_key).await, Ok(Some(_))),
    };

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
#[utoipa::path(
    get,
    path = "/api/v1/reports/{id}/html",
    tag = "Reports",
    params(
        ("id" = String, Path, description = "Report UUID")
    ),
    responses(
        (status = 200, description = "HTML report content", content_type = "text/html"),
        (status = 404, description = "Report or HTML file not found")
    )
)]
#[get("/reports/{id}/html")]
pub async fn get_report_html(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
    storage: web::Data<Storage>,
) -> AppResult<HttpResponse> {
    let id = Uuid::parse_str(&path.into_inner())?;

    // Get framework from database
    let conn = pool.connection();
    let report = queries::get_active_report_by_id(conn, id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;
    let framework = report.framework.clone();

    // Determine HTML file based on framework
    let report_id_str = id.to_string();
    let content = if framework.as_deref() == Some("cypress") {
        // For Cypress, prefer mochawesome.html, fallback to index.html
        let mochawesome_key = Storage::report_key(&report_id_str, "mochawesome.html");
        match storage.download(&mochawesome_key).await? {
            Some(data) => data,
            None => {
                let index_key = Storage::report_key(&report_id_str, "index.html");
                storage
                    .download(&index_key)
                    .await?
                    .ok_or_else(|| AppError::NotFound("HTML report file".to_string()))?
            }
        }
    } else {
        // For Playwright and unknown frameworks, use index.html
        let index_key = Storage::report_key(&report_id_str, "index.html");
        storage
            .download(&index_key)
            .await?
            .ok_or_else(|| AppError::NotFound("HTML report file".to_string()))?
    };

    let content_str = String::from_utf8(content)
        .map_err(|e| AppError::InvalidInput(format!("Invalid UTF-8 in HTML file: {}", e)))?;

    Ok(HttpResponse::Ok()
        .content_type("text/html; charset=utf-8")
        .body(content_str))
}

/// Get asset files for HTML report (CSS, JS, fonts).
///
/// GET /reports/{id}/assets/{filename}
///
/// Serves files from the assets/ directory for mochawesome HTML reports.
#[utoipa::path(
    get,
    path = "/api/v1/reports/{id}/assets/{filename}",
    tag = "Reports",
    params(
        ("id" = String, Path, description = "Report UUID"),
        ("filename" = String, Path, description = "Asset filename")
    ),
    responses(
        (status = 200, description = "Asset file content"),
        (status = 404, description = "Asset file not found")
    )
)]
#[get("/reports/{id}/assets/{filename:.*}")]
pub async fn get_report_assets(
    pool: web::Data<DbPool>,
    path: web::Path<(String, String)>,
    storage: web::Data<Storage>,
) -> AppResult<HttpResponse> {
    let (id_str, filename) = path.into_inner();
    let id = Uuid::parse_str(&id_str)?;

    // Validate filename to prevent path traversal
    if filename.contains("..") || filename.starts_with('/') {
        return Err(AppError::InvalidInput("Invalid filename".to_string()));
    }

    // Verify report exists
    let conn = pool.connection();
    let _ = queries::get_active_report_by_id(conn, id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;

    // Download asset from S3
    let report_id_str = id.to_string();
    let key = Storage::report_key(&report_id_str, &format!("assets/{}", filename));
    let content = storage
        .download(&key)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Asset file {}", filename)))?;

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
#[utoipa::path(
    get,
    path = "/api/v1/reports/{id}/screenshots/{filepath}",
    tag = "Reports",
    params(
        ("id" = String, Path, description = "Report UUID"),
        ("filepath" = String, Path, description = "Screenshot file path")
    ),
    responses(
        (status = 200, description = "Screenshot file content"),
        (status = 404, description = "Screenshot file not found")
    )
)]
#[get("/reports/{id}/screenshots/{filepath:.*}")]
pub async fn get_report_screenshots(
    pool: web::Data<DbPool>,
    path: web::Path<(String, String)>,
    storage: web::Data<Storage>,
) -> AppResult<HttpResponse> {
    let (id_str, filepath) = path.into_inner();
    let id = Uuid::parse_str(&id_str)?;

    // Validate filepath to prevent path traversal
    if filepath.contains("..") || filepath.starts_with('/') {
        return Err(AppError::InvalidInput("Invalid filepath".to_string()));
    }

    // Verify report exists
    let conn = pool.connection();
    let _ = queries::get_active_report_by_id(conn, id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;

    // Download screenshot from S3
    let report_id_str = id.to_string();
    let key = Storage::report_key(&report_id_str, &format!("screenshots/{}", filepath));
    let content = storage
        .download(&key)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Screenshot file {}", filepath)))?;

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
#[utoipa::path(
    get,
    path = "/api/v1/reports/{id}/data/{filename}",
    tag = "Reports",
    params(
        ("id" = String, Path, description = "Report UUID"),
        ("filename" = String, Path, description = "Data filename")
    ),
    responses(
        (status = 200, description = "Data file content"),
        (status = 404, description = "Data file not found")
    )
)]
#[get("/reports/{id}/data/{filename:.*}")]
pub async fn get_report_data_file(
    pool: web::Data<DbPool>,
    path: web::Path<(String, String)>,
    storage: web::Data<Storage>,
) -> AppResult<HttpResponse> {
    let (id_str, filename) = path.into_inner();
    let id = Uuid::parse_str(&id_str)?;

    // Validate filename to prevent path traversal
    if filename.contains("..") || filename.starts_with('/') {
        return Err(AppError::InvalidInput("Invalid filename".to_string()));
    }

    // Verify report exists
    let conn = pool.connection();
    let _ = queries::get_active_report_by_id(conn, id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;

    // Search multiple S3 paths for the file:
    // 1. data/ (Playwright screenshots, traces)
    // 2. screenshots/ (Cypress screenshots)
    // 3. root report directory (fallback)
    let report_id_str = id.to_string();
    let search_keys = [
        Storage::report_key(&report_id_str, &format!("data/{}", filename)),
        Storage::report_key(&report_id_str, &format!("screenshots/{}", filename)),
        Storage::report_key(&report_id_str, &filename),
    ];

    let mut content: Option<Vec<u8>> = None;
    for key in &search_keys {
        if let Ok(Some(data)) = storage.download(key).await {
            content = Some(data);
            break;
        }
    }

    let content = content.ok_or_else(|| AppError::NotFound(format!("Data file {}", filename)))?;
    let content_type = mime_guess::from_path(&filename)
        .first_or_octet_stream()
        .to_string();

    Ok(HttpResponse::Ok().content_type(content_type).body(content))
}

/// Get test suites for a report.
///
/// GET /reports/{id}/suites
#[utoipa::path(
    get,
    path = "/api/v1/reports/{id}/suites",
    tag = "Reports",
    params(
        ("id" = String, Path, description = "Report UUID")
    ),
    responses(
        (status = 200, description = "List of test suites", body = TestSuiteListResponse),
        (status = 404, description = "Report not found")
    )
)]
#[get("/reports/{id}/suites")]
pub async fn get_report_suites(
    pool: web::Data<DbPool>,
    path: web::Path<String>,
) -> AppResult<HttpResponse> {
    let id = Uuid::parse_str(&path.into_inner())?;
    let conn = pool.connection();

    // Verify report exists
    let _ = queries::get_active_report_by_id(conn, id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", id)))?;

    let suites = queries::get_test_suites(conn, id).await?;

    Ok(HttpResponse::Ok().json(TestSuiteListResponse { suites }))
}

/// Get test specs with results for a suite.
///
/// GET /reports/{id}/suites/{suite_id}/specs
#[utoipa::path(
    get,
    path = "/api/v1/reports/{id}/suites/{suite_id}/specs",
    tag = "Reports",
    params(
        ("id" = String, Path, description = "Report UUID"),
        ("suite_id" = i64, Path, description = "Suite ID")
    ),
    responses(
        (status = 200, description = "List of test specs with results", body = TestSpecListResponse),
        (status = 404, description = "Report not found")
    )
)]
#[get("/reports/{id}/suites/{suite_id}/specs")]
pub async fn get_suite_specs(
    pool: web::Data<DbPool>,
    path: web::Path<(String, i64)>,
) -> AppResult<HttpResponse> {
    use crate::models::ScreenshotInfo;

    let (report_id_str, suite_id) = path.into_inner();
    let report_id = Uuid::parse_str(&report_id_str)?;
    let conn = pool.connection();

    // Verify report exists and get framework info
    let report = queries::get_active_report_by_id(conn, report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    let mut specs = queries::get_suite_specs_with_results(conn, suite_id).await?;

    // For Detox reports, enrich specs with screenshot info
    if report.framework.as_deref() == Some("detox") {
        // Get all jobs for this report (screenshots can be in any job)
        let jobs = queries::get_detox_jobs_by_report_id(conn, report_id).await?;
        for spec in &mut specs {
            // full_title contains the full_name used for screenshot lookup
            // Normalize: replace / and " with _ since folder names can't contain these characters
            let normalized_full_title = spec.full_title.replace(['/', '"'], "_");

            // Search across all jobs for screenshots matching this spec
            for job in &jobs {
                let screenshots = queries::get_detox_screenshots_by_test_name(
                    conn,
                    job.id,
                    &normalized_full_title,
                )
                .await?;

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
