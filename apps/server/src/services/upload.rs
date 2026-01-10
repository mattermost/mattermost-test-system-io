//! File upload service.

use actix_multipart::Multipart;
use actix_web::{post, web, HttpRequest, HttpResponse};
use futures_util::StreamExt;
use serde::Serialize;
use std::path::PathBuf;
use tokio::io::AsyncWriteExt;
use tracing::{info, warn};
use uuid::Uuid;

use crate::config::API_KEY_HEADER;
use crate::db::{queries, DbPool};
use crate::error::{AppError, AppResult};
use crate::models::{GitHubContext, Report};
use crate::services::extraction;

/// Maximum file size (500MB)
const MAX_FILE_SIZE: usize = 500 * 1024 * 1024;

/// Required file for a valid report
const REQUIRED_FILES: &[&str] = &["index.html"];

/// Optional but expected files
const EXPECTED_FILES: &[&str] = &["results.json", "results.xml"];

/// Upload response.
#[derive(Serialize)]
pub struct UploadResponse {
    pub id: Uuid,
    pub created_at: String,
    pub files_count: usize,
    pub extraction_status: String,
    pub message: String,
}

/// Configure upload routes.
pub fn configure_upload_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(upload_report);
}

/// Upload a new report.
///
/// POST /reports
/// Content-Type: multipart/form-data
/// X-API-Key: <api-key>
#[post("/reports")]
async fn upload_report(
    req: HttpRequest,
    mut payload: Multipart,
    pool: web::Data<DbPool>,
    data_dir: web::Data<PathBuf>,
    api_key: web::Data<String>,
) -> AppResult<HttpResponse> {
    // Validate API key
    let provided_key = req
        .headers()
        .get(API_KEY_HEADER)
        .and_then(|v| v.to_str().ok());

    match provided_key {
        Some(key) if key == api_key.as_str() => {}
        Some(_) => {
            return Err(AppError::Unauthorized("Invalid API key".to_string()));
        }
        None => {
            return Err(AppError::Unauthorized(
                "Missing API key. Provide X-API-Key header.".to_string(),
            ));
        }
    }

    // Create report first to get the ID
    let mut report = Report::new_with_id();
    let report_id = report.id;
    let report_dir = data_dir.join(report_id.to_string());

    tokio::fs::create_dir_all(&report_dir)
        .await
        .map_err(|e| AppError::FileSystem(format!("Failed to create report directory: {}", e)))?;

    info!("Processing upload for report {}", report_id);

    // Process uploaded files
    let mut uploaded_files: Vec<String> = Vec::new();
    let mut total_size: usize = 0;

    while let Some(item) = payload.next().await {
        let mut field =
            item.map_err(|e| AppError::InvalidInput(format!("Multipart error: {}", e)))?;

        let content_disposition = field
            .content_disposition()
            .ok_or_else(|| AppError::InvalidInput("Missing content disposition".to_string()))?;

        // Check for github_context form field (non-file field)
        let field_name = content_disposition.get_name();
        if field_name == Some("github_context") {
            // Read the JSON content
            let mut json_data = Vec::new();
            while let Some(chunk) = field.next().await {
                let data =
                    chunk.map_err(|e| AppError::InvalidInput(format!("Read error: {}", e)))?;
                json_data.extend_from_slice(&data);
            }

            // Parse as GitHubContext
            if !json_data.is_empty() {
                match serde_json::from_slice::<GitHubContext>(&json_data) {
                    Ok(ctx) if !ctx.is_empty() => {
                        info!("Received github_context for report {}", report_id);
                        report.github_context = Some(ctx);
                    }
                    Ok(_) => {
                        info!("Empty github_context received, ignoring");
                    }
                    Err(e) => {
                        warn!("Invalid github_context JSON: {}", e);
                        // Don't fail the upload, just log and continue
                    }
                }
            }
            continue;
        }

        let filename = content_disposition
            .get_filename()
            .ok_or_else(|| AppError::InvalidInput("Missing filename".to_string()))?
            .to_string();

        // Normalize backslashes to forward slashes and validate filename
        let filename = filename.replace('\\', "/");

        // Prevent path traversal attacks
        if filename.contains("..") || filename.starts_with('/') {
            cleanup_on_error(&report_dir).await;
            return Err(AppError::InvalidInput(format!(
                "Invalid filename: {}",
                filename
            )));
        }

        let filepath = report_dir.join(&filename);

        // Create parent directories if needed (preserves folder structure)
        if let Some(parent) = filepath.parent() {
            tokio::fs::create_dir_all(parent).await.map_err(|e| {
                AppError::FileSystem(format!(
                    "Failed to create directory for {}: {}",
                    filename, e
                ))
            })?;
        }

        let mut file = tokio::fs::File::create(&filepath).await.map_err(|e| {
            AppError::FileSystem(format!("Failed to create file {}: {}", filename, e))
        })?;

        let mut file_size: usize = 0;

        // Stream file content to disk
        while let Some(chunk) = field.next().await {
            let data = chunk.map_err(|e| AppError::InvalidInput(format!("Read error: {}", e)))?;
            file_size += data.len();
            total_size += data.len();

            if total_size > MAX_FILE_SIZE {
                cleanup_on_error(&report_dir).await;
                return Err(AppError::PayloadTooLarge(format!(
                    "Total upload size exceeds {} bytes",
                    MAX_FILE_SIZE
                )));
            }

            file.write_all(&data)
                .await
                .map_err(|e| AppError::FileSystem(format!("Failed to write file: {}", e)))?;
        }

        file.flush()
            .await
            .map_err(|e| AppError::FileSystem(format!("Failed to flush file: {}", e)))?;

        info!(
            "Saved file {} ({} bytes) for report {}",
            filename, file_size, report_id
        );
        uploaded_files.push(filename.to_string());
    }

    // Validate required files
    for required in REQUIRED_FILES {
        if !uploaded_files.iter().any(|f| f == *required) {
            cleanup_on_error(&report_dir).await;
            return Err(AppError::InvalidInput(format!(
                "Missing required file: {}",
                required
            )));
        }
    }

    // Warn about missing expected files
    for expected in EXPECTED_FILES {
        if !uploaded_files.iter().any(|f| f == *expected) {
            warn!(
                "Report {} is missing expected file: {}",
                report_id, expected
            );
        }
    }

    // Insert report record (synchronous database operations)
    let conn = pool.connection();

    queries::insert_report(&conn, &report).inspect_err(|_| {
        // Cleanup on database error
        let dir = report_dir.clone();
        tokio::spawn(async move {
            cleanup_on_error(&dir).await;
        });
    })?;

    info!(
        "Report {} created with {} files",
        report_id,
        uploaded_files.len()
    );

    // Trigger extraction if results.json exists
    let has_results_json = uploaded_files.iter().any(|f| f == "results.json");
    let extraction_status = if has_results_json {
        let results_path = report_dir.join("results.json");
        match extraction::extract_results(&conn, report_id, &results_path) {
            Ok(_) => {
                info!("Extraction completed for report {}", report_id);
                "completed"
            }
            Err(e) => {
                warn!("Extraction failed for report {}: {}", report_id, e);
                "failed"
            }
        }
    } else {
        warn!(
            "No results.json found, skipping extraction for report {}",
            report_id
        );
        "pending"
    };

    // Drop connection to release the lock
    drop(conn);

    Ok(HttpResponse::Created().json(UploadResponse {
        id: report_id,
        created_at: report.created_at.to_rfc3339(),
        files_count: uploaded_files.len(),
        extraction_status: extraction_status.to_string(),
        message: if extraction_status == "completed" {
            "Report uploaded and extracted successfully".to_string()
        } else if extraction_status == "failed" {
            "Report uploaded but extraction failed".to_string()
        } else {
            "Report uploaded, extraction skipped (no results.json)".to_string()
        },
    }))
}

/// Cleanup files on error.
async fn cleanup_on_error(dir: &PathBuf) {
    if let Err(e) = tokio::fs::remove_dir_all(dir).await {
        warn!("Failed to cleanup directory {:?}: {}", dir, e);
    }
}
