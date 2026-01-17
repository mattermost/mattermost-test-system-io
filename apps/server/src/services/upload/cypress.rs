//! Cypress report upload handlers.

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
use crate::services::cypress_extraction;

use super::{Framework, UploadRequest, handle_upload_request};

/// Initialize Cypress report upload.
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
#[utoipa::path(
    post,
    path = "/api/v1/reports/upload/cypress/request",
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
#[post("/reports/upload/cypress/request")]
pub async fn request_cypress_upload(
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
        Framework::Cypress,
    )
    .await
}

/// Trigger Cypress results extraction.
///
/// Reads from database JSONB first (faster), falls back to S3 if not found.
pub(crate) async fn trigger_cypress_extraction(
    conn: &DatabaseConnection,
    report_id: Uuid,
    storage: &Storage,
    framework_version: Option<&str>,
) -> &'static str {
    // Try to get JSON from database first (populated during upload)
    // Try all.json first, then mochawesome.json
    let json_value = {
        // Try all.json from database
        match queries::get_report_json(conn, report_id, JsonFileType::AllJson).await {
            Ok(Some(report_json)) => {
                info!(
                    "Using cached all.json from database for report {}",
                    report_id
                );
                Some(report_json.data)
            }
            _ => {
                // Try mochawesome.json from database
                match queries::get_report_json(conn, report_id, JsonFileType::MochawesomeJson).await
                {
                    Ok(Some(report_json)) => {
                        info!(
                            "Using cached mochawesome.json from database for report {}",
                            report_id
                        );
                        Some(report_json.data)
                    }
                    _ => None,
                }
            }
        }
    };

    // If found in database, extract directly from JSON value
    if let Some(json) = json_value {
        return match cypress_extraction::extract_cypress_results_from_json(conn, report_id, json)
            .await
        {
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
                )
                .await;
                "failed"
            }
        };
    }

    // Fallback: Download from S3
    let report_id_str = report_id.to_string();
    let json_data = {
        let all_json_key = Storage::report_key(&report_id_str, "all.json");
        match storage.download(&all_json_key).await {
            Ok(Some(data)) => data,
            _ => {
                // Try mochawesome.json
                let mochawesome_key = Storage::report_key(&report_id_str, "mochawesome.json");
                match storage.download(&mochawesome_key).await {
                    Ok(Some(data)) => data,
                    Ok(None) => {
                        warn!("No Cypress JSON found in S3 for report {}", report_id);
                        return "pending";
                    }
                    Err(e) => {
                        warn!(
                            "Failed to download Cypress JSON for report {}: {}",
                            report_id, e
                        );
                        return "pending";
                    }
                }
            }
        }
    };

    match cypress_extraction::extract_cypress_results_from_bytes(conn, report_id, &json_data).await
    {
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
            )
            .await;
            "failed"
        }
    }
}
