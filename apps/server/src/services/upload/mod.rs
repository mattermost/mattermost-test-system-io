//! File upload service for test reports.
//!
//! ## Two-Step Upload API
//!
//! ### Step 1: Initialize Upload
//! Validate metadata and filenames, create report entry, register files for tracking.
//!
//! - `POST /api/v1/reports/upload/playwright/request`
//! - `POST /api/v1/reports/upload/cypress/request`
//! - `POST /api/v1/reports/upload/detox/request`
//!
//! ### Step 2: Transfer Files
//! Upload files in batches, track progress, trigger extraction when complete.
//!
//! - `POST /api/v1/reports/upload/{report_id}/files`

pub mod cypress;
pub mod detox;
pub mod playwright;

use actix_multipart::Multipart;
use actix_web::{post, web, HttpResponse};
use futures_util::StreamExt;
use serde::{Deserialize, Serialize};
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;
use tokio::io::AsyncWriteExt;
use tokio::sync::Semaphore;
use tracing::{debug, info, warn};
use utoipa::ToSchema;
use uuid::Uuid;

use crate::auth::ApiKeyAuth;
use crate::db::{queries, upload_files as db_upload_files, DbPool};
use crate::error::{AppError, AppResult};
use crate::models::{AuthenticatedCaller, DetoxPlatform, ExtractionStatus, GitHubContext, Report};

// Re-export extraction triggers for use in phase 2
pub(crate) use cypress::trigger_cypress_extraction;
pub(crate) use detox::trigger_detox_extraction;
pub(crate) use playwright::trigger_playwright_extraction;

// ============================================================================
// Constants
// ============================================================================

/// Video file extensions that are rejected during upload.
const VIDEO_EXTENSIONS: &[&str] = &[".mp4", ".webm", ".avi", ".mov", ".mkv"];

// ============================================================================
// Types
// ============================================================================

/// Target framework for upload.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Framework {
    Playwright,
    Cypress,
    Detox,
}

impl Framework {
    /// Get framework name as string.
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Playwright => "playwright",
            Self::Cypress => "cypress",
            Self::Detox => "detox",
        }
    }
}

/// Initialize upload request body.
#[derive(Debug, Deserialize, ToSchema)]
pub struct UploadRequest {
    /// Framework version (required).
    pub framework_version: String,

    /// GitHub context metadata (required).
    pub github_context: GitHubContext,

    /// List of filenames to be uploaded (required).
    pub filenames: Vec<String>,

    /// Platform for Detox (required for detox, ignored for others).
    #[serde(default)]
    pub platform: Option<String>,
}

/// Initialize upload response.
#[derive(Debug, Serialize, ToSchema)]
pub struct UploadRequestResponse {
    /// Report ID to use for file upload.
    pub report_id: Uuid,

    /// Report creation timestamp.
    pub created_at: String,

    /// Framework.
    pub framework: String,

    /// Framework version.
    pub framework_version: String,

    /// Maximum upload size in bytes.
    pub max_upload_size: usize,

    /// Maximum files per upload batch.
    pub max_files_per_request: usize,

    /// Files that will be accepted.
    pub files_accepted: Vec<String>,

    /// Files that will be rejected with reasons.
    pub files_rejected: Vec<FileError>,

    /// Status message.
    pub message: String,
}

/// File transfer response.
#[derive(Debug, Serialize, ToSchema)]
pub struct UploadFilesResponse {
    /// Report ID.
    pub report_id: Uuid,

    /// Number of files uploaded in this batch.
    pub files_uploaded: usize,

    /// Total files registered for this report.
    pub total_files: i64,

    /// Files still pending upload.
    pub files_pending: i64,

    /// Extraction status (pending, completed, failed).
    pub extraction_status: String,

    /// Files that were accepted in this batch.
    pub files_accepted: Vec<String>,

    /// Files rejected by policy (not registered, already uploaded) - don't retry.
    pub files_rejected: Vec<FileError>,

    /// Files that failed during processing (write error, etc.) - may retry.
    pub files_failed: Vec<FileError>,

    /// Status message.
    pub message: String,
}

/// A file that was rejected or failed during upload.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct FileError {
    pub file: String,
    pub reason: String,
}

// ============================================================================
// Route Configuration
// ============================================================================

/// Configure upload routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(playwright::request_playwright_upload)
        .service(cypress::request_cypress_upload)
        .service(detox::request_detox_upload)
        .service(upload_report_files);
}

// ============================================================================
// Step 1: Initialize Upload (Common Handler)
// ============================================================================

/// Handle initialize upload request.
///
/// Validates metadata and filenames, creates report entry, registers files, returns report_id.
pub(crate) async fn handle_upload_request(
    caller: &AuthenticatedCaller,
    body: web::Json<UploadRequest>,
    pool: web::Data<DbPool>,
    data_dir: web::Data<PathBuf>,
    max_upload_size: web::Data<usize>,
    max_files_per_request: web::Data<usize>,
    framework: Framework,
) -> AppResult<HttpResponse> {
    info!(
        "Processing {} upload request from {} ({})",
        framework.as_str(),
        caller.name,
        caller.key_prefix
    );

    // Validate required fields
    validate_request_body(&body, framework)?;

    // Parse and validate platform for Detox
    let platform = parse_platform(&body, framework)?;

    // Validate and categorize filenames
    let (files_accepted, files_rejected) = validate_filenames(&body.filenames);

    // Framework-specific file validation
    validate_framework_files(&files_accepted, framework, platform)?;

    // Create report entry
    let report = create_report(&body, framework, platform);
    let report_id = report.id;
    let report_dir = data_dir.join(report_id.to_string());

    // Create report directory
    tokio::fs::create_dir_all(&report_dir)
        .await
        .map_err(|e| AppError::FileSystem(format!("Failed to create report directory: {}", e)))?;

    // Insert report and register files in database
    let conn = pool.connection();
    queries::insert_report(&conn, &report)?;

    // Register accepted files for tracking
    db_upload_files::register_files(&conn, &report_id.to_string(), &files_accepted)?;

    info!(
        "Report {} created: {} files registered, {} rejected",
        report_id,
        files_accepted.len(),
        files_rejected.len()
    );

    Ok(HttpResponse::Created().json(UploadRequestResponse {
        report_id,
        created_at: report.created_at.to_rfc3339(),
        framework: framework.as_str().to_string(),
        framework_version: body.framework_version.clone(),
        max_upload_size: *max_upload_size.get_ref(),
        max_files_per_request: *max_files_per_request.get_ref(),
        files_accepted,
        files_rejected,
        message: "Report created. Upload files in batches to complete.".to_string(),
    }))
}

// ============================================================================
// Step 2: Transfer Files (Batched)
// ============================================================================

/// Upload a batch of files to an initialized report.
///
/// POST /reports/upload/{report_id}/files
/// Content-Type: multipart/form-data
/// X-API-Key: <api-key>
///
/// Files are streamed directly to their final location. Only files that were
/// registered during initialization are accepted. When all registered files have been
/// uploaded, extraction is triggered automatically.
///
/// Clients should upload files in batches of `max_files_per_request` (from initialize response).
#[utoipa::path(
    post,
    path = "/api/v1/reports/upload/{report_id}/files",
    tag = "Upload",
    params(
        ("report_id" = String, Path, description = "Report UUID from initialization")
    ),
    responses(
        (status = 200, description = "Files uploaded successfully", body = UploadFilesResponse),
        (status = 404, description = "Report not found"),
        (status = 503, description = "Service unavailable - too many concurrent uploads")
    ),
    security(
        ("api_key" = [])
    )
)]
#[post("/reports/upload/{report_id}/files")]
#[allow(clippy::too_many_arguments)]
pub async fn upload_report_files(
    auth: ApiKeyAuth,
    path: web::Path<String>,
    mut payload: Multipart,
    pool: web::Data<DbPool>,
    data_dir: web::Data<PathBuf>,
    upload_semaphore: web::Data<Arc<Semaphore>>,
    upload_queue_timeout_secs: web::Data<u64>,
) -> AppResult<HttpResponse> {
    info!(
        "Uploading files from {} ({})",
        auth.caller.name, auth.caller.key_prefix
    );

    // Parse report ID
    let report_id = Uuid::parse_str(&path.into_inner())
        .map_err(|_| AppError::InvalidInput("Invalid report ID".to_string()))?;

    // Acquire upload permit with timeout (queues requests instead of rejecting immediately)
    // This handles burst scenarios where multiple CI jobs finish simultaneously
    let queue_timeout = Duration::from_secs(*upload_queue_timeout_secs.get_ref());
    let _permit = match tokio::time::timeout(queue_timeout, upload_semaphore.acquire()).await {
        Ok(Ok(permit)) => {
            debug!("Upload permit acquired for report {}", report_id);
            permit
        }
        Ok(Err(_)) => {
            // Semaphore closed (shouldn't happen)
            return Err(AppError::ServiceUnavailable(
                "Upload service unavailable".to_string(),
            ));
        }
        Err(_) => {
            // Timeout waiting for permit
            warn!(
                "Upload timed out waiting for permit (report {}): queue full for {}s",
                report_id,
                queue_timeout.as_secs()
            );
            return Err(AppError::ServiceUnavailable(format!(
                "Upload queue full. {} concurrent uploads in progress. Please retry.",
                upload_semaphore.available_permits()
            )));
        }
    };

    // Get report and validate state
    let report = {
        let conn = pool.connection();
        queries::get_active_report_by_id(&conn, report_id)?
            .ok_or_else(|| AppError::NotFound(format!("Report {} not found", report_id)))?
    };

    if report.extraction_status != ExtractionStatus::Pending {
        return Err(AppError::InvalidInput(format!(
            "Report {} is not in pending state",
            report_id
        )));
    }

    let report_dir = data_dir.join(report_id.to_string());
    let report_id_str = report_id.to_string();

    info!("Processing file batch for report {}", report_id);

    // Pre-fetch all pending file statuses for this report (single DB query)
    // This reduces lock contention by avoiding per-file DB queries
    let mut file_status_cache: std::collections::HashMap<String, db_upload_files::FileStatus> = {
        let conn = pool.connection();
        // Get all files for this report - we'll filter as needed
        let all_filenames: Vec<String> = conn
            .prepare("SELECT filename FROM upload_files WHERE report_id = ?1")
            .map_err(|e| AppError::Database(format!("Failed to get filenames: {}", e)))?
            .query_map([&report_id_str], |row| row.get(0))
            .map_err(|e| AppError::Database(format!("Failed to query filenames: {}", e)))?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|e| AppError::Database(format!("Failed to read filenames: {}", e)))?;

        db_upload_files::get_files_status(&conn, &report_id_str, &all_filenames)?
    };

    // Process multipart upload - stream files directly to final location
    let mut files_accepted: Vec<String> = Vec::new();
    let mut files_rejected: Vec<FileError> = Vec::new(); // Policy rejections (don't retry)
    let mut files_failed: Vec<FileError> = Vec::new(); // Processing errors (may retry)

    while let Some(item) = payload.next().await {
        let mut field =
            item.map_err(|e| AppError::InvalidInput(format!("Multipart error: {}", e)))?;

        let content_disposition = field
            .content_disposition()
            .ok_or_else(|| AppError::InvalidInput("Missing content disposition".to_string()))?;

        let filename = match content_disposition.get_filename() {
            Some(name) => name.replace('\\', "/"),
            None => continue,
        };

        // Check file status from pre-fetched cache (no DB query needed)
        let file_status = file_status_cache.get(&filename);

        // Check if file was registered during initialization
        if file_status.is_none() {
            drain_field(&mut field).await;
            files_rejected.push(FileError {
                file: filename,
                reason: "File not registered during initialization".to_string(),
            });
            continue;
        }

        // Check if file was already uploaded
        if file_status.map(|s| s.is_uploaded).unwrap_or(false) {
            drain_field(&mut field).await;
            files_rejected.push(FileError {
                file: filename,
                reason: "File already uploaded".to_string(),
            });
            continue;
        }

        // Stream file directly to final location
        let final_path = report_dir.join(&filename);

        match stream_file_to_disk(&mut field, &final_path).await {
            Ok(file_size) => {
                // Mark file as uploaded in database (scope connection to avoid holding across await)
                let db_result = {
                    let conn = pool.connection();
                    db_upload_files::mark_uploaded(&conn, &report_id_str, &filename, file_size)
                };

                if let Err(e) = db_result {
                    warn!("Failed to mark {} as uploaded: {}", filename, e);
                    // File is on disk but not tracked - clean it up
                    let _ = tokio::fs::remove_file(&final_path).await;
                    files_failed.push(FileError {
                        file: filename,
                        reason: format!("Database error: {}", e),
                    });
                    continue;
                }

                // Update local cache to mark file as uploaded
                if let Some(status) = file_status_cache.get_mut(&filename) {
                    status.is_uploaded = true;
                }

                info!("Saved {} ({} bytes)", filename, file_size);
                files_accepted.push(filename);
            }
            Err(e) => {
                warn!("Failed to write {}: {}", filename, e);
                // Clean up partial file if it exists
                let _ = tokio::fs::remove_file(&final_path).await;
                files_failed.push(FileError {
                    file: filename,
                    reason: e,
                });
            }
        }
    }

    // Check upload progress (single query instead of 3 separate queries)
    let (total_files, uploaded_count, all_uploaded) = {
        let conn = pool.connection();
        db_upload_files::get_upload_progress(&conn, &report_id_str)?
    };
    let files_pending = total_files - uploaded_count;

    // Trigger extraction if all files are uploaded
    let extraction_status = if all_uploaded {
        let framework = report.framework.clone().unwrap_or_default();
        let conn = pool.connection();
        let framework_version = report.framework_version.as_deref();

        // Get all uploaded files for extraction (use already computed uploaded_count)
        let uploaded_files: Vec<String> = if framework == "detox" {
            // For Detox we need the file list - scan directory
            scan_uploaded_files(&report_dir)
        } else {
            Vec::with_capacity(uploaded_count as usize)
        };

        match framework.as_str() {
            "playwright" => {
                trigger_playwright_extraction(&conn, report_id, &report_dir, framework_version)
            }
            "cypress" => {
                trigger_cypress_extraction(&conn, report_id, &report_dir, framework_version)
            }
            "detox" => {
                trigger_detox_extraction(&conn, report_id, &report_dir, &uploaded_files, &report)
            }
            _ => {
                warn!("Unknown framework: {}", framework);
                "pending"
            }
        }
    } else {
        "pending"
    };

    info!(
        "Report {} batch complete: {} files uploaded, {} pending, extraction: {}",
        report_id,
        files_accepted.len(),
        files_pending,
        extraction_status
    );

    // Build message
    let message = if all_uploaded {
        match extraction_status {
            "completed" => "All files uploaded and extracted successfully".to_string(),
            "failed" => "All files uploaded but extraction failed".to_string(),
            _ => "All files uploaded, extraction pending".to_string(),
        }
    } else {
        format!("Batch uploaded. {} files remaining.", files_pending)
    };

    Ok(HttpResponse::Ok().json(UploadFilesResponse {
        report_id,
        files_uploaded: files_accepted.len(),
        total_files,
        files_pending,
        extraction_status: extraction_status.to_string(),
        files_accepted,
        files_rejected,
        files_failed,
        message,
    }))
}

/// Scan directory for uploaded files (used for Detox extraction).
fn scan_uploaded_files(dir: &std::path::Path) -> Vec<String> {
    let mut files = Vec::new();
    if let Ok(entries) = std::fs::read_dir(dir) {
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_file() {
                if let Some(name) = path.file_name() {
                    files.push(name.to_string_lossy().to_string());
                }
            } else if path.is_dir() {
                // Recurse into subdirectories
                let subdir_name = path.file_name().unwrap().to_string_lossy().to_string();
                for sub_file in scan_uploaded_files(&path) {
                    files.push(format!("{}/{}", subdir_name, sub_file));
                }
            }
        }
    }
    files
}

// ============================================================================
// Helper Functions
// ============================================================================

/// Validate request body fields.
fn validate_request_body(body: &UploadRequest, framework: Framework) -> AppResult<()> {
    if body.framework_version.trim().is_empty() {
        return Err(AppError::InvalidInput(
            "Missing required field: framework_version".to_string(),
        ));
    }

    if body.github_context.is_empty() {
        return Err(AppError::InvalidInput(
            "Missing required field: github_context".to_string(),
        ));
    }

    if body.filenames.is_empty() {
        return Err(AppError::InvalidInput(
            "Missing required field: filenames".to_string(),
        ));
    }

    // Detox requires platform
    if framework == Framework::Detox && body.platform.is_none() {
        return Err(AppError::InvalidInput(
            "Missing required field: platform (must be 'ios' or 'android')".to_string(),
        ));
    }

    Ok(())
}

/// Parse and validate platform for Detox.
fn parse_platform(body: &UploadRequest, framework: Framework) -> AppResult<Option<DetoxPlatform>> {
    if framework != Framework::Detox {
        return Ok(None);
    }

    match body.platform.as_deref() {
        Some("ios") => Ok(Some(DetoxPlatform::Ios)),
        Some("android") => Ok(Some(DetoxPlatform::Android)),
        Some(other) => Err(AppError::InvalidInput(format!(
            "Invalid platform '{}'. Must be 'ios' or 'android'",
            other
        ))),
        None => Err(AppError::InvalidInput(
            "Missing required field: platform".to_string(),
        )),
    }
}

/// Validate and categorize filenames.
fn validate_filenames(filenames: &[String]) -> (Vec<String>, Vec<FileError>) {
    let mut accepted = Vec::new();
    let mut rejected = Vec::new();

    for filename in filenames {
        let filename = filename.replace('\\', "/");

        // Check for path traversal
        if filename.contains("..") || filename.starts_with('/') {
            rejected.push(FileError {
                file: filename,
                reason: "Invalid path".to_string(),
            });
            continue;
        }

        // Check for video files
        if is_video_file(&filename) {
            rejected.push(FileError {
                file: filename,
                reason: "Video files not supported".to_string(),
            });
            continue;
        }

        accepted.push(filename);
    }

    (accepted, rejected)
}

/// Check if filename is a video file.
fn is_video_file(filename: &str) -> bool {
    let lower = filename.to_lowercase();
    VIDEO_EXTENSIONS.iter().any(|ext| lower.ends_with(ext))
}

/// Validate framework-specific required files.
fn validate_framework_files(
    files: &[String],
    framework: Framework,
    platform: Option<DetoxPlatform>,
) -> AppResult<()> {
    match framework {
        Framework::Playwright => {
            if !files.iter().any(|f| f == "index.html") {
                return Err(AppError::InvalidInput(
                    "Missing required file: index.html".to_string(),
                ));
            }
        }
        Framework::Cypress => {
            if !files
                .iter()
                .any(|f| f == "all.json" || f == "mochawesome.json")
            {
                return Err(AppError::InvalidInput(
                    "Missing required file: all.json or mochawesome.json".to_string(),
                ));
            }
        }
        Framework::Detox => {
            let platform_str = platform.map(|p| p.as_str()).unwrap_or("ios");
            let expected_data_suffix = format!("{}-data.json", platform_str);

            // Find all data files: {folder}/jest-stare/{platform}-data.json
            let data_files: Vec<_> = files
                .iter()
                .filter(|f| f.contains("/jest-stare/") && f.ends_with(&expected_data_suffix))
                .collect();

            if data_files.is_empty() {
                return Err(AppError::InvalidInput(format!(
                    "Missing required file: {{folder}}/jest-stare/{}-data.json",
                    platform_str
                )));
            }

            // Collect unique job folders and validate structure
            let mut job_folders: std::collections::HashSet<String> =
                std::collections::HashSet::new();

            for file in &data_files {
                let parts: Vec<&str> = file.split('/').collect();
                if parts.len() < 3 || parts[1] != "jest-stare" {
                    return Err(AppError::InvalidInput(format!(
                        "Invalid path: '{}'. Expected: {{folder}}/jest-stare/{}-data.json",
                        file, platform_str
                    )));
                }

                job_folders.insert(parts[0].to_string());
            }

            // Each job folder must have an HTML report
            for job_folder in &job_folders {
                let has_html = files.iter().any(|f| {
                    f.starts_with(&format!("{}/jest-stare/", job_folder)) && f.ends_with(".html")
                });

                if !has_html {
                    return Err(AppError::InvalidInput(format!(
                        "Missing HTML report for folder '{}'. Expected: {}/jest-stare/*.html",
                        job_folder, job_folder
                    )));
                }
            }
        }
    }

    Ok(())
}

/// Create report entry.
fn create_report(
    body: &UploadRequest,
    framework: Framework,
    platform: Option<DetoxPlatform>,
) -> Report {
    let mut report = Report::new_with_id();
    report.framework = Some(framework.as_str().to_string());
    report.framework_version = Some(body.framework_version.clone());
    report.github_context = Some(body.github_context.clone());
    report.extraction_status = ExtractionStatus::Pending;

    if let Some(p) = platform {
        report.platform = Some(p.as_str().to_string());
    }

    report
}

/// Drain a multipart field without saving.
async fn drain_field(field: &mut actix_multipart::Field) {
    while let Some(chunk) = field.next().await {
        let _ = chunk;
    }
}

/// Stream a multipart field directly to disk.
/// Returns the file size on success, or an error message on failure.
async fn stream_file_to_disk(
    field: &mut actix_multipart::Field,
    final_path: &std::path::Path,
) -> Result<i64, String> {
    // Create parent directories if needed
    if let Some(parent) = final_path.parent() {
        tokio::fs::create_dir_all(parent)
            .await
            .map_err(|e| format!("Failed to create directory: {}", e))?;
    }

    // Create the file
    let mut file = tokio::fs::File::create(final_path)
        .await
        .map_err(|e| format!("Failed to create file: {}", e))?;

    // Stream chunks to disk
    let mut file_size: i64 = 0;
    while let Some(chunk) = field.next().await {
        let chunk_data = chunk.map_err(|e| format!("Read error: {}", e))?;
        file_size += chunk_data.len() as i64;
        file.write_all(&chunk_data)
            .await
            .map_err(|e| format!("Write error: {}", e))?;
    }

    // Flush to ensure data is persisted
    file.flush()
        .await
        .map_err(|e| format!("Flush error: {}", e))?;

    Ok(file_size)
}
