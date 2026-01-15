//! Cypress report upload handlers.

use actix_web::{post, web, HttpResponse};
use std::path::{Path, PathBuf};
use tracing::{info, warn};
use uuid::Uuid;

use crate::auth::ApiKeyAuth;
use crate::db::{queries, DbPool};
use crate::error::AppResult;
use crate::models::ExtractionStatus;
use crate::services::cypress_extraction;

use super::{handle_upload_request, Framework, UploadRequest};

/// Request Cypress report upload (Phase 1).
///
/// POST /reports/upload/cypress/request
/// Content-Type: application/json
/// X-API-Key: <api-key>
///
/// ## Request Body
/// ```json
/// {
///   "framework_version": "13.6.0",
///   "github_context": { "repository": "owner/repo", ... },
///   "filenames": ["all.json", "mochawesome.html", ...]
/// }
/// ```
///
/// ## Response
/// Returns report_id and list of accepted/rejected files.
#[post("/reports/upload/cypress/request")]
pub(crate) async fn request_cypress_upload(
    auth: ApiKeyAuth,
    body: web::Json<UploadRequest>,
    pool: web::Data<DbPool>,
    data_dir: web::Data<PathBuf>,
    max_upload_size: web::Data<usize>,
) -> AppResult<HttpResponse> {
    handle_upload_request(
        &auth.caller,
        body,
        pool,
        data_dir,
        max_upload_size,
        Framework::Cypress,
    )
    .await
}

/// Trigger Cypress results extraction.
pub(crate) fn trigger_cypress_extraction(
    conn: &rusqlite::Connection,
    report_id: Uuid,
    report_dir: &Path,
    framework_version: Option<&str>,
) -> &'static str {
    // Find the JSON file (all.json or mochawesome.json)
    let json_path = if report_dir.join("all.json").exists() {
        report_dir.join("all.json")
    } else {
        report_dir.join("mochawesome.json")
    };

    if !json_path.exists() {
        warn!("No Cypress JSON found for report {}", report_id);
        return "pending";
    }

    match cypress_extraction::extract_cypress_results(conn, report_id, &json_path) {
        Ok(_) => {
            info!("Cypress extraction completed for report {}", report_id);
            "completed"
        }
        Err(e) => {
            warn!("Cypress extraction failed for report {}: {}", report_id, e);
            let _ = queries::update_report_status(
                conn,
                report_id,
                ExtractionStatus::Failed,
                Some("cypress"),
                framework_version,
                Some(&e.to_string()),
            );
            "failed"
        }
    }
}
