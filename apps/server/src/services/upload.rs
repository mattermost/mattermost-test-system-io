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

/// Optional but expected files
const EXPECTED_FILES: &[&str] = &["results.json", "results.xml"];

/// Video file extensions that are rejected during upload.
const VIDEO_EXTENSIONS: &[&str] = &[".mp4", ".webm", ".avi", ".mov", ".mkv"];

/// Check if a filename is a video file that should be rejected.
fn is_video_file(filename: &str) -> bool {
    let filename_lower = filename.to_lowercase();
    VIDEO_EXTENSIONS
        .iter()
        .any(|ext| filename_lower.ends_with(ext))
}

/// Detect the test framework based on uploaded files.
///
/// Returns (framework, framework_version) tuple.
/// - If explicit parameters are provided, uses those.
/// - Otherwise auto-detects: all.json/mochawesome.json = Cypress, results.json = Playwright.
/// - Falls back to "unknown" if no pattern matches.
fn detect_framework(
    files: &[String],
    explicit_framework: Option<&str>,
    explicit_version: Option<&str>,
) -> (String, Option<String>) {
    // Use explicit parameters if provided
    if let Some(fw) = explicit_framework {
        return (fw.to_string(), explicit_version.map(|v| v.to_string()));
    }

    // Auto-detect by file presence
    let has_cypress_json = files
        .iter()
        .any(|f| f == "all.json" || f == "mochawesome.json");
    let has_playwright_json = files.iter().any(|f| f == "results.json");

    if has_cypress_json {
        return ("cypress".to_string(), None);
    }
    if has_playwright_json {
        return ("playwright".to_string(), None);
    }

    ("unknown".to_string(), None)
}

/// A file that was rejected during upload.
#[derive(Debug, Clone, Serialize)]
pub struct RejectedFile {
    pub file: String,
    pub reason: String,
}

/// Upload response.
#[derive(Serialize)]
pub struct UploadResponse {
    pub id: Uuid,
    pub created_at: String,
    pub files_count: usize,
    pub extraction_status: String,
    pub message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub framework: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub framework_version: Option<String>,
    pub files_accepted: Vec<String>,
    pub files_rejected: Vec<RejectedFile>,
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
///
/// Supports both Playwright and Cypress reports:
/// - Playwright: requires index.html, optionally results.json
/// - Cypress: requires all.json or mochawesome.json, optionally mochawesome.html
///
/// Optional form fields:
/// - framework: explicit framework type ("playwright" or "cypress")
/// - framework_version: framework version string
/// - github_context: JSON-encoded GitHub context metadata
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

    // Track uploaded files and rejected files
    let mut files_accepted: Vec<String> = Vec::new();
    let mut files_rejected: Vec<RejectedFile> = Vec::new();
    let mut total_size: usize = 0;

    // Track optional form fields for framework detection
    let mut explicit_framework: Option<String> = None;
    let mut explicit_framework_version: Option<String> = None;

    while let Some(item) = payload.next().await {
        let mut field =
            item.map_err(|e| AppError::InvalidInput(format!("Multipart error: {}", e)))?;

        let content_disposition = field
            .content_disposition()
            .ok_or_else(|| AppError::InvalidInput("Missing content disposition".to_string()))?;

        // Check for form fields (non-file fields)
        let field_name = content_disposition.get_name();

        // Handle github_context field
        if field_name == Some("github_context") {
            let mut json_data = Vec::new();
            while let Some(chunk) = field.next().await {
                let data =
                    chunk.map_err(|e| AppError::InvalidInput(format!("Read error: {}", e)))?;
                json_data.extend_from_slice(&data);
            }

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
                    }
                }
            }
            continue;
        }

        // Handle framework field
        if field_name == Some("framework") {
            let mut data = Vec::new();
            while let Some(chunk) = field.next().await {
                let chunk_data =
                    chunk.map_err(|e| AppError::InvalidInput(format!("Read error: {}", e)))?;
                data.extend_from_slice(&chunk_data);
            }
            if let Ok(value) = String::from_utf8(data) {
                let value = value.trim().to_string();
                if !value.is_empty() {
                    info!(
                        "Received explicit framework: {} for report {}",
                        value, report_id
                    );
                    explicit_framework = Some(value);
                }
            }
            continue;
        }

        // Handle framework_version field
        if field_name == Some("framework_version") {
            let mut data = Vec::new();
            while let Some(chunk) = field.next().await {
                let chunk_data =
                    chunk.map_err(|e| AppError::InvalidInput(format!("Read error: {}", e)))?;
                data.extend_from_slice(&chunk_data);
            }
            if let Ok(value) = String::from_utf8(data) {
                let value = value.trim().to_string();
                if !value.is_empty() {
                    info!(
                        "Received explicit framework_version: {} for report {}",
                        value, report_id
                    );
                    explicit_framework_version = Some(value);
                }
            }
            continue;
        }

        // Process file uploads
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

        // Check if this is a video file - reject it
        if is_video_file(&filename) {
            // Drain the field content but don't save
            while let Some(chunk) = field.next().await {
                let _ = chunk;
            }
            warn!(
                "Rejecting video file: {} for report {}",
                filename, report_id
            );
            files_rejected.push(RejectedFile {
                file: filename,
                reason: "video files not supported".to_string(),
            });
            continue;
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
        files_accepted.push(filename.to_string());
    }

    // Detect framework based on uploaded files
    let (framework, mut framework_version) = detect_framework(
        &files_accepted,
        explicit_framework.as_deref(),
        explicit_framework_version.as_deref(),
    );

    info!(
        "Detected framework: {} (version: {:?}) for report {}",
        framework, framework_version, report_id
    );

    // Update report with framework info
    report.framework = Some(framework.clone());
    report.framework_version = framework_version.clone();

    // Validate required files based on framework
    let is_cypress = framework == "cypress";
    let has_playwright_html = files_accepted.iter().any(|f| f == "index.html");
    let has_cypress_json = files_accepted
        .iter()
        .any(|f| f == "all.json" || f == "mochawesome.json");

    // For Playwright, require index.html. For Cypress, require all.json or mochawesome.json.
    // For unknown framework, require at least one recognized pattern.
    if !is_cypress && !has_playwright_html && !has_cypress_json {
        cleanup_on_error(&report_dir).await;
        return Err(AppError::InvalidInput(
            "Missing required file: index.html (Playwright) or all.json/mochawesome.json (Cypress)"
                .to_string(),
        ));
    }

    // Warn about missing expected files for Playwright
    if !is_cypress {
        for expected in EXPECTED_FILES {
            if !files_accepted.iter().any(|f| f == *expected) {
                warn!(
                    "Report {} is missing expected file: {}",
                    report_id, expected
                );
            }
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
        "Report {} created with {} files ({} rejected)",
        report_id,
        files_accepted.len(),
        files_rejected.len()
    );

    // Trigger extraction based on framework
    let extraction_status = if is_cypress {
        // Cypress extraction
        let json_path = if files_accepted.iter().any(|f| f == "all.json") {
            report_dir.join("all.json")
        } else {
            report_dir.join("mochawesome.json")
        };

        match crate::services::cypress_extraction::extract_cypress_results(
            &conn, report_id, &json_path,
        ) {
            Ok(version) => {
                // Update framework version if extracted from JSON
                if framework_version.is_none() && version.is_some() {
                    framework_version = version;
                    // Update in database
                    if let Err(e) = queries::update_report_framework(
                        &conn,
                        report_id,
                        &framework,
                        &framework_version,
                    ) {
                        warn!("Failed to update framework version: {}", e);
                    }
                }
                info!("Cypress extraction completed for report {}", report_id);
                "completed"
            }
            Err(e) => {
                warn!("Cypress extraction failed for report {}: {}", report_id, e);
                "failed"
            }
        }
    } else {
        // Playwright extraction
        let has_results_json = files_accepted.iter().any(|f| f == "results.json");
        if has_results_json {
            let results_path = report_dir.join("results.json");
            match extraction::extract_results(&conn, report_id, &results_path) {
                Ok(_) => {
                    info!("Playwright extraction completed for report {}", report_id);
                    "completed"
                }
                Err(e) => {
                    warn!(
                        "Playwright extraction failed for report {}: {}",
                        report_id, e
                    );
                    "failed"
                }
            }
        } else {
            warn!(
                "No results.json found, skipping extraction for report {}",
                report_id
            );
            "pending"
        }
    };

    // Drop connection to release the lock
    drop(conn);

    Ok(HttpResponse::Created().json(UploadResponse {
        id: report_id,
        created_at: report.created_at.to_rfc3339(),
        files_count: files_accepted.len(),
        extraction_status: extraction_status.to_string(),
        message: if extraction_status == "completed" {
            "Report uploaded and extracted successfully".to_string()
        } else if extraction_status == "failed" {
            "Report uploaded but extraction failed".to_string()
        } else {
            "Report uploaded, extraction pending".to_string()
        },
        framework: Some(framework),
        framework_version,
        files_accepted,
        files_rejected,
    }))
}

/// Cleanup files on error.
async fn cleanup_on_error(dir: &PathBuf) {
    if let Err(e) = tokio::fs::remove_dir_all(dir).await {
        warn!("Failed to cleanup directory {:?}: {}", dir, e);
    }
}
