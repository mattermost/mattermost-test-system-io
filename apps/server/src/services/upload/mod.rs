//! File upload service for test reports.
//!
//! ## Two-Phase Upload API
//!
//! ### Phase 1: Request Upload
//! Validate metadata and filenames, create report entry, return report_id.
//!
//! - `POST /api/v1/reports/upload/playwright/request`
//! - `POST /api/v1/reports/upload/cypress/request`
//! - `POST /api/v1/reports/upload/detox/request`
//!
//! ### Phase 2: Upload Files
//! Stream files to report directory, trigger extraction.
//!
//! - `POST /api/v1/reports/upload/{report_id}/files`

mod cypress;
mod detox;
mod playwright;

use actix_multipart::Multipart;
use actix_web::{post, web, HttpRequest, HttpResponse};
use futures_util::StreamExt;
use serde::{Deserialize, Serialize};
use std::path::PathBuf;
use std::sync::Arc;
use tokio::io::AsyncWriteExt;
use tokio::sync::Semaphore;
use tracing::{info, warn};
use uuid::Uuid;

use crate::config::API_KEY_HEADER;
use crate::db::{queries, DbPool};
use crate::error::{AppError, AppResult};
use crate::models::{DetoxPlatform, ExtractionStatus, GitHubContext, Report};

// Re-export extraction triggers for use in phase 2
pub(crate) use cypress::trigger_cypress_extraction;
pub(crate) use detox::trigger_detox_extraction;
pub(crate) use playwright::trigger_playwright_extraction;

// ============================================================================
// Constants
// ============================================================================

/// Video file extensions that are rejected during upload.
const VIDEO_EXTENSIONS: &[&str] = &[".mp4", ".webm", ".avi", ".mov", ".mkv"];

/// Screenshot/image file extensions (optional files, deprioritized during upload).
const SCREENSHOT_EXTENSIONS: &[&str] = &[".png", ".jpg", ".jpeg", ".gif", ".webp"];

/// Log file extensions (lowest priority, skipped first when size limit reached).
const LOG_EXTENSIONS: &[&str] = &[".log"];

/// Trace/debug file patterns (lowest priority, skipped first when size limit reached).
const TRACE_FILE_PATTERNS: &[&str] = &["detox.trace.json"];

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

/// Phase 1 request body.
#[derive(Debug, Deserialize)]
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

/// Phase 1 response.
#[derive(Debug, Serialize)]
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

    /// Files that will be accepted.
    pub files_accepted: Vec<String>,

    /// Files that will be rejected with reasons.
    pub files_rejected: Vec<RejectedFile>,

    /// Status message.
    pub message: String,
}

/// Phase 2 response.
#[derive(Debug, Serialize)]
pub struct UploadFilesResponse {
    /// Report ID.
    pub report_id: Uuid,

    /// Number of files uploaded.
    pub files_count: usize,

    /// Extraction status (pending, completed, failed).
    pub extraction_status: String,

    /// Files that were accepted.
    pub files_accepted: Vec<String>,

    /// Files that were rejected.
    pub files_rejected: Vec<RejectedFile>,

    /// Files skipped due to size limit (screenshots only).
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub skipped_due_to_size: Vec<String>,

    /// Status message.
    pub message: String,
}

/// A file rejected during upload.
#[derive(Debug, Clone, Serialize)]
pub struct RejectedFile {
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
        .service(upload_files);
}

// ============================================================================
// Phase 1: Request Upload (Common Handler)
// ============================================================================

/// Handle Phase 1 upload request.
///
/// Validates metadata and filenames, creates report entry, returns report_id.
pub(crate) async fn handle_upload_request(
    req: HttpRequest,
    body: web::Json<UploadRequest>,
    pool: web::Data<DbPool>,
    data_dir: web::Data<PathBuf>,
    api_key: web::Data<String>,
    max_upload_size: web::Data<usize>,
    framework: Framework,
) -> AppResult<HttpResponse> {
    // Validate API key
    validate_api_key(&req, &api_key)?;

    info!("Processing {} upload request", framework.as_str());

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

    // Insert report into database
    let conn = pool.connection();
    queries::insert_report(&conn, &report)?;

    info!(
        "Report {} created: {} files accepted, {} rejected",
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
        files_accepted,
        files_rejected,
        message: "Report created. Upload files to complete.".to_string(),
    }))
}

// ============================================================================
// Phase 2: Upload Files
// ============================================================================

/// Upload files to an initialized report.
///
/// POST /reports/upload/{report_id}/files
/// Content-Type: multipart/form-data
/// X-API-Key: <api-key>
#[post("/reports/upload/{report_id}/files")]
#[allow(clippy::too_many_arguments)]
async fn upload_files(
    req: HttpRequest,
    path: web::Path<String>,
    mut payload: Multipart,
    pool: web::Data<DbPool>,
    data_dir: web::Data<PathBuf>,
    api_key: web::Data<String>,
    max_upload_size: web::Data<usize>,
    upload_semaphore: web::Data<Arc<Semaphore>>,
) -> AppResult<HttpResponse> {
    // Validate API key
    validate_api_key(&req, &api_key)?;

    // Parse report ID
    let report_id = Uuid::parse_str(&path.into_inner())
        .map_err(|_| AppError::InvalidInput("Invalid report ID".to_string()))?;

    // Acquire upload permit (limits concurrent uploads to bound memory usage)
    let _permit = upload_semaphore.try_acquire().map_err(|_| {
        warn!(
            "Upload rejected for report {}: too many concurrent uploads",
            report_id
        );
        AppError::ServiceUnavailable(
            "Too many concurrent uploads. Please try again later.".to_string(),
        )
    })?;

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

    let framework = report.framework.clone().unwrap_or_default();
    let report_dir = data_dir.join(report_id.to_string());

    info!("Uploading files for report {}", report_id);

    // Process multipart upload with size limit handling
    let multipart_result =
        process_multipart(&mut payload, &report_dir, *max_upload_size.get_ref()).await?;

    let files_accepted = multipart_result.accepted;
    let files_rejected = multipart_result.rejected;
    let skipped_due_to_size = multipart_result.skipped_due_to_size;

    // Trigger extraction based on framework
    let extraction_status = {
        let conn = pool.connection();
        let framework_version = report.framework_version.as_deref();

        match framework.as_str() {
            "playwright" => {
                trigger_playwright_extraction(&conn, report_id, &report_dir, framework_version)
            }
            "cypress" => {
                trigger_cypress_extraction(&conn, report_id, &report_dir, framework_version)
            }
            "detox" => {
                trigger_detox_extraction(&conn, report_id, &report_dir, &files_accepted, &report)
            }
            _ => {
                warn!("Unknown framework: {}", framework);
                "pending"
            }
        }
    };

    info!(
        "Report {} complete: {} files uploaded, {} skipped, extraction: {}",
        report_id,
        files_accepted.len(),
        skipped_due_to_size.len(),
        extraction_status
    );

    // Build message based on extraction status and skipped files
    let base_message = match extraction_status {
        "completed" => "Files uploaded and extracted successfully",
        "failed" => "Files uploaded but extraction failed",
        _ => "Files uploaded, extraction pending",
    };

    let message = if skipped_due_to_size.is_empty() {
        base_message.to_string()
    } else {
        format!(
            "{} ({} optional files skipped due to size limit)",
            base_message,
            skipped_due_to_size.len()
        )
    };

    Ok(HttpResponse::Ok().json(UploadFilesResponse {
        report_id,
        files_count: files_accepted.len(),
        extraction_status: extraction_status.to_string(),
        files_accepted,
        files_rejected,
        skipped_due_to_size,
        message,
    }))
}

// ============================================================================
// Helper Functions
// ============================================================================

/// Validate API key from request headers.
fn validate_api_key(req: &HttpRequest, api_key: &str) -> AppResult<()> {
    let provided_key = req
        .headers()
        .get(API_KEY_HEADER)
        .and_then(|v| v.to_str().ok());

    match provided_key {
        Some(key) if key == api_key => Ok(()),
        Some(_) => Err(AppError::Unauthorized("Invalid API key".to_string())),
        None => Err(AppError::Unauthorized(
            "Missing API key. Provide X-API-Key header.".to_string(),
        )),
    }
}

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
fn validate_filenames(filenames: &[String]) -> (Vec<String>, Vec<RejectedFile>) {
    let mut accepted = Vec::new();
    let mut rejected = Vec::new();

    for filename in filenames {
        let filename = filename.replace('\\', "/");

        // Check for path traversal
        if filename.contains("..") || filename.starts_with('/') {
            rejected.push(RejectedFile {
                file: filename,
                reason: "Invalid path".to_string(),
            });
            continue;
        }

        // Check for video files
        if is_video_file(&filename) {
            rejected.push(RejectedFile {
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

/// Check if filename is a screenshot/image file (optional, deprioritized).
fn is_screenshot_file(filename: &str) -> bool {
    let lower = filename.to_lowercase();
    SCREENSHOT_EXTENSIONS.iter().any(|ext| lower.ends_with(ext))
}

/// Check if filename is a log/trace file (lowest priority).
fn is_log_file(filename: &str) -> bool {
    let lower = filename.to_lowercase();
    LOG_EXTENSIONS.iter().any(|ext| lower.ends_with(ext))
        || TRACE_FILE_PATTERNS.iter().any(|pat| lower.ends_with(pat))
}

/// Check if this is a Detox screenshot that should be skipped.
/// Skip: testDone, afterAllFailure, beforeAllFailure
/// Keep: testFnFailure, testStart
fn is_skipped_detox_screenshot(filename: &str) -> bool {
    if !is_screenshot_file(filename) {
        return false;
    }
    // Check for Detox screenshot types to skip
    filename.contains("/testDone.")
        || filename.contains("/afterAllFailure.")
        || filename.contains("/beforeAllFailure.")
}

/// File priority for upload ordering.
/// Lower number = higher priority = processed first.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
enum FilePriority {
    /// Essential files (html, json, xml, etc.) - always kept
    Essential = 0,
    /// Failure screenshots (testFnFailure) - high priority, skip if size limit reached
    FailureScreenshot = 1,
    /// Start screenshots (testStart) - medium priority, skip if size limit reached
    StartScreenshot = 2,
    /// Other screenshots - skip if size limit reached
    Screenshot = 3,
    /// Log files - lowest priority, skip first
    Log = 4,
}

impl FilePriority {
    fn from_filename(filename: &str) -> Self {
        if is_log_file(filename) {
            Self::Log
        } else if is_screenshot_file(filename) {
            // Check for Detox screenshot types in the filename
            // Pattern: {device}/{testName}/testFnFailure.png
            if filename.contains("/testFnFailure.") {
                Self::FailureScreenshot
            } else if filename.contains("/testStart.") {
                Self::StartScreenshot
            } else {
                Self::Screenshot
            }
        } else {
            Self::Essential
        }
    }

    /// Returns true if this file can be skipped when size limit is reached.
    fn is_optional(&self) -> bool {
        matches!(
            self,
            Self::FailureScreenshot | Self::StartScreenshot | Self::Screenshot | Self::Log
        )
    }
}

/// Metadata for a file streamed to disk during upload.
/// Only metadata is kept in memory, not file contents.
struct StreamedFile {
    /// Original filename from upload
    filename: String,
    /// Temporary file path on disk
    temp_path: std::path::PathBuf,
    /// File size in bytes
    size: usize,
    /// File priority (essential, screenshot, log)
    priority: FilePriority,
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

/// Result from processing multipart upload.
struct MultipartResult {
    accepted: Vec<String>,
    rejected: Vec<RejectedFile>,
    skipped_due_to_size: Vec<String>,
}

/// Detailed error for payload too large.
#[derive(Debug, serde::Serialize)]
struct PayloadTooLargeDetails {
    max_upload_size: usize,
    total_accepted_size: usize,
    files_accepted: usize,
    files_skipped: usize,
    skipped_files: Vec<String>,
    failed_file: String,
    failed_file_size: usize,
}

/// Process multipart upload using streaming to temp files.
///
/// Files are streamed directly to disk (temp files), then sorted by priority
/// (non-screenshots first). Files are moved to final location until size limit
/// is reached. Screenshots that don't fit are deleted and reported.
///
/// This approach uses O(num_files) memory instead of O(total_size).
async fn process_multipart(
    payload: &mut Multipart,
    report_dir: &std::path::Path,
    max_upload_size: usize,
) -> AppResult<MultipartResult> {
    let mut streamed_files: Vec<StreamedFile> = Vec::new();
    let mut rejected: Vec<RejectedFile> = Vec::new();

    // Create temp directory for streaming uploads
    let temp_dir = report_dir.join(".upload_temp");
    tokio::fs::create_dir_all(&temp_dir)
        .await
        .map_err(|e| AppError::FileSystem(format!("Failed to create temp directory: {}", e)))?;

    // Phase 1: Stream each file directly to a temp file
    let mut file_counter: u32 = 0;
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

        // Validate filename
        if filename.contains("..") || filename.starts_with('/') {
            drain_field(&mut field).await;
            rejected.push(RejectedFile {
                file: filename,
                reason: "Invalid path".to_string(),
            });
            continue;
        }

        // Reject video files
        if is_video_file(&filename) {
            drain_field(&mut field).await;
            rejected.push(RejectedFile {
                file: filename,
                reason: "Video files not supported".to_string(),
            });
            continue;
        }

        // Skip low-priority Detox screenshots (testDone, afterAllFailure, beforeAllFailure)
        if is_skipped_detox_screenshot(&filename) {
            drain_field(&mut field).await;
            rejected.push(RejectedFile {
                file: filename,
                reason: "Low-priority screenshot skipped".to_string(),
            });
            continue;
        }

        // Stream to temp file
        let temp_path = temp_dir.join(format!("upload_{}", file_counter));
        file_counter += 1;

        let mut temp_file = tokio::fs::File::create(&temp_path)
            .await
            .map_err(|e| AppError::FileSystem(format!("Failed to create temp file: {}", e)))?;

        let mut size: usize = 0;
        while let Some(chunk) = field.next().await {
            let chunk_data =
                chunk.map_err(|e| AppError::InvalidInput(format!("Read error: {}", e)))?;
            size += chunk_data.len();
            temp_file
                .write_all(&chunk_data)
                .await
                .map_err(|e| AppError::FileSystem(format!("Failed to write temp file: {}", e)))?;
        }
        temp_file.flush().await.ok();

        let priority = FilePriority::from_filename(&filename);

        streamed_files.push(StreamedFile {
            filename,
            temp_path,
            size,
            priority,
        });
    }

    // Phase 2: Sort files by priority (essential first, then screenshots, then logs)
    streamed_files.sort_by_key(|f| f.priority);

    // Phase 3: Move files to final location until size limit reached
    let mut accepted: Vec<String> = Vec::new();
    let mut skipped_due_to_size: Vec<String> = Vec::new();
    let mut total_size: usize = 0;

    for file in streamed_files {
        let would_exceed = total_size + file.size > max_upload_size;

        if would_exceed {
            if file.priority.is_optional() {
                // Skip optional files (screenshots, logs) that would exceed the limit
                info!(
                    "Skipping {:?} {} ({} bytes) - would exceed size limit",
                    file.priority, file.filename, file.size
                );
                let _ = tokio::fs::remove_file(&file.temp_path).await;
                skipped_due_to_size.push(file.filename);
                continue;
            } else {
                // Essential files are required - fail if they don't fit
                // Cleanup temp files before returning error
                cleanup_temp_dir(&temp_dir).await;

                // Build detailed error
                let details = PayloadTooLargeDetails {
                    max_upload_size,
                    total_accepted_size: total_size,
                    files_accepted: accepted.len(),
                    files_skipped: skipped_due_to_size.len(),
                    skipped_files: skipped_due_to_size.clone(),
                    failed_file: file.filename.clone(),
                    failed_file_size: file.size,
                };

                return Err(AppError::PayloadTooLarge {
                    message: format!(
                        "Essential file '{}' ({} bytes) would exceed upload limit of {} bytes",
                        file.filename, file.size, max_upload_size
                    ),
                    details: serde_json::to_value(details).unwrap_or_default(),
                });
            }
        }

        // Move temp file to final location
        let final_path = report_dir.join(&file.filename);

        if let Some(parent) = final_path.parent() {
            tokio::fs::create_dir_all(parent)
                .await
                .map_err(|e| AppError::FileSystem(format!("Failed to create directory: {}", e)))?;
        }

        tokio::fs::rename(&file.temp_path, &final_path)
            .await
            .map_err(|e| AppError::FileSystem(format!("Failed to move file: {}", e)))?;

        info!("Saved {} ({} bytes)", file.filename, file.size);
        total_size += file.size;
        accepted.push(file.filename);
    }

    // Cleanup temp directory
    cleanup_temp_dir(&temp_dir).await;

    if !skipped_due_to_size.is_empty() {
        info!(
            "Skipped {} screenshots due to size limit ({} bytes written, {} bytes limit)",
            skipped_due_to_size.len(),
            total_size,
            max_upload_size
        );
    }

    Ok(MultipartResult {
        accepted,
        rejected,
        skipped_due_to_size,
    })
}

/// Cleanup temporary upload directory.
async fn cleanup_temp_dir(temp_dir: &std::path::Path) {
    if let Err(e) = tokio::fs::remove_dir_all(temp_dir).await {
        warn!("Failed to cleanup temp directory: {}", e);
    }
}

/// Drain a multipart field without saving.
async fn drain_field(field: &mut actix_multipart::Field) {
    while let Some(chunk) = field.next().await {
        let _ = chunk;
    }
}
