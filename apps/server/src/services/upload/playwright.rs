//! Playwright report upload handlers.

use actix_web::{post, web, HttpResponse};
use std::path::{Path, PathBuf};
use tracing::{info, warn};
use uuid::Uuid;

use crate::auth::ApiKeyAuth;
use crate::db::{queries, DbPool};
use crate::error::AppResult;
use crate::models::ExtractionStatus;
use crate::services::extraction;

use super::{handle_upload_request, Framework, UploadRequest};

/// Initialize Playwright report upload.
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
    auth: ApiKeyAuth,
    body: web::Json<UploadRequest>,
    pool: web::Data<DbPool>,
    data_dir: web::Data<PathBuf>,
    max_upload_size: web::Data<usize>,
    max_files_per_request: web::Data<usize>,
) -> AppResult<HttpResponse> {
    handle_upload_request(
        &auth.caller,
        body,
        pool,
        data_dir,
        max_upload_size,
        max_files_per_request,
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
