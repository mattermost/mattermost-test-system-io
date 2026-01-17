//! Playwright report upload handlers.

use actix_web::{HttpResponse, post, web};
use sea_orm::DatabaseConnection;
use tracing::{info, warn};
use utoipa;
use uuid::Uuid;

use crate::auth::ApiKeyAuth;
use crate::db::{DbPool, queries};
use crate::error::AppResult;
use crate::models::{ExtractionStatus, JsonFileType};
use crate::services::Storage;
use crate::services::extraction;

use super::{Framework, UploadRequest, handle_upload_request};

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
#[utoipa::path(
    post,
    path = "/api/v1/reports/upload/playwright/request",
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
#[post("/reports/upload/playwright/request")]
pub async fn request_playwright_upload(
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
        Framework::Playwright,
    )
    .await
}

/// Trigger Playwright results extraction.
///
/// Reads from database JSONB first (faster), falls back to S3 if not found.
pub(crate) async fn trigger_playwright_extraction(
    conn: &DatabaseConnection,
    report_id: Uuid,
    storage: &Storage,
    framework_version: Option<&str>,
) -> &'static str {
    // Try to get JSON from database first (populated during upload)
    let json_value =
        match queries::get_report_json(conn, report_id, JsonFileType::ResultsJson).await {
            Ok(Some(report_json)) => {
                info!(
                    "Using cached results.json from database for report {}",
                    report_id
                );
                Some(report_json.data)
            }
            Ok(None) => None,
            Err(e) => {
                warn!(
                    "Failed to query database for results.json (report {}): {}",
                    report_id, e
                );
                None
            }
        };

    // If found in database, extract directly from JSON value
    if let Some(json) = json_value {
        return match extraction::extract_results_from_json(conn, report_id, json).await {
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
                )
                .await;
                "failed"
            }
        };
    }

    // Fallback: Download results.json from S3
    let s3_key = Storage::report_key(&report_id.to_string(), "results.json");
    let results_data = match storage.download(&s3_key).await {
        Ok(Some(data)) => data,
        Ok(None) => {
            warn!("No results.json found in S3 for report {}", report_id);
            return "pending";
        }
        Err(e) => {
            warn!(
                "Failed to download results.json for report {}: {}",
                report_id, e
            );
            return "pending";
        }
    };

    match extraction::extract_results_from_bytes(conn, report_id, &results_data).await {
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
            )
            .await;
            "failed"
        }
    }
}
