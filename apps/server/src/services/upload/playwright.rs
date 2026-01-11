//! Playwright report upload handlers.

use actix_web::{post, web, HttpRequest, HttpResponse};
use std::path::{Path, PathBuf};
use tracing::{info, warn};
use uuid::Uuid;

use crate::db::{queries, DbPool};
use crate::error::AppResult;
use crate::models::ExtractionStatus;
use crate::services::extraction;

use super::{handle_upload_request, Framework, UploadRequest};

/// Request Playwright report upload (Phase 1).
///
/// POST /reports/upload/playwright/request
/// Content-Type: application/json
/// X-API-Key: <api-key>
///
/// ## Request Body
/// ```json
/// {
///   "framework_version": "1.40.0",
///   "github_context": { "repository": "owner/repo", ... },
///   "filenames": ["index.html", "results.json", "data/..."]
/// }
/// ```
///
/// ## Response
/// Returns report_id and list of accepted/rejected files.
#[post("/reports/upload/playwright/request")]
pub(crate) async fn request_playwright_upload(
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
        Framework::Playwright,
    )
    .await
}

/// Trigger Playwright results extraction.
pub(crate) fn trigger_playwright_extraction(
    conn: &rusqlite::Connection,
    report_id: Uuid,
    report_dir: &Path,
    framework_version: Option<&str>,
) -> &'static str {
    let results_path = report_dir.join("results.json");

    if !results_path.exists() {
        warn!("No results.json found for report {}", report_id);
        return "pending";
    }

    match extraction::extract_results(conn, report_id, &results_path) {
        Ok(_) => {
            info!("Playwright extraction completed for report {}", report_id);
            "completed"
        }
        Err(e) => {
            warn!(
                "Playwright extraction failed for report {}: {}",
                report_id, e
            );
            let _ = queries::update_report_status(
                conn,
                report_id,
                ExtractionStatus::Failed,
                Some("playwright"),
                framework_version,
                Some(&e.to_string()),
            );
            "failed"
        }
    }
}
