//! Job API handlers.

use actix_multipart::Multipart;
use actix_web::{HttpResponse, web};
use futures_util::StreamExt;
use tracing::info;
use uuid::Uuid;

use crate::db::DbPool;
use crate::db::html_files::HtmlFileEntry;
use crate::db::json_files::JsonFileEntry;
use crate::db::screenshots::ScreenshotEntry;
use crate::error::{AppError, AppResult};
use crate::models::{
    AcceptedHtmlFile, AcceptedJsonFile, AcceptedScreenshot, EnvironmentMetadata,
    HtmlUploadProgress, HtmlUploadResponse, InitHtmlRequest, InitHtmlResponse, InitJobRequest,
    InitJobResponse, InitJsonRequest, InitJsonResponse, InitScreenshotsRequest,
    InitScreenshotsResponse, JobDetailResponse, JobGitHubMetadata, JobListResponse, JobStatus,
    JsonUploadProgress, JsonUploadResponse, QueryJobsParams, RejectedFile, ReportStatus,
    ScreenshotUploadResponse, WsEvent, WsEventMessage,
};
use crate::services::extraction;
use crate::services::{EventBroadcaster, Storage};

/// Allowed file extensions for HTML report uploads.
const ALLOWED_EXTENSIONS: &[&str] = &[
    "html", "htm", "css", "js", "json", "map", // Web assets
    "png", "jpg", "jpeg", "gif", "svg", "webp", "ico", // Images
    "woff", "woff2", "ttf", "eot", "otf", // Fonts
    "txt", "md", // Text
];

/// Allowed image extensions for screenshot uploads.
/// Can be configured to accept more formats if needed.
const ALLOWED_IMAGE_EXTENSIONS: &[&str] = &["png", "jpg", "jpeg", "gif", "webp"];

/// Maximum file size (50 MB).
const MAX_FILE_SIZE: i64 = 50 * 1024 * 1024;

/// Maximum screenshot file size (10 MB).
const MAX_SCREENSHOT_SIZE: i64 = 10 * 1024 * 1024;

/// Validate a file path and return rejection reason if invalid.
fn validate_file(path: &str, size: Option<i64>) -> Option<String> {
    // Check for path traversal
    if path.contains("..") {
        return Some("Path traversal not allowed".to_string());
    }

    // Check empty path
    if path.is_empty() {
        return Some("Empty file path".to_string());
    }

    // Check extension
    let extension = path.rsplit('.').next().unwrap_or("").to_lowercase();
    if !ALLOWED_EXTENSIONS.contains(&extension.as_str()) {
        return Some(format!("File extension '{}' not allowed", extension));
    }

    // Check file size
    if let Some(size) = size {
        if size > MAX_FILE_SIZE {
            return Some(format!(
                "File size {} exceeds maximum {} bytes",
                size, MAX_FILE_SIZE
            ));
        }
        if size < 0 {
            return Some("Invalid file size".to_string());
        }
    }

    None
}

/// Infer content type from file extension.
fn infer_content_type(path: &str) -> &'static str {
    let ext = path.rsplit('.').next().unwrap_or("").to_lowercase();
    match ext.as_str() {
        "html" | "htm" => "text/html",
        "css" => "text/css",
        "js" => "application/javascript",
        "json" => "application/json",
        "map" => "application/json",
        "png" => "image/png",
        "jpg" | "jpeg" => "image/jpeg",
        "gif" => "image/gif",
        "svg" => "image/svg+xml",
        "webp" => "image/webp",
        "ico" => "image/x-icon",
        "woff" => "font/woff",
        "woff2" => "font/woff2",
        "ttf" => "font/ttf",
        "eot" => "application/vnd.ms-fontobject",
        "otf" => "font/otf",
        "txt" => "text/plain",
        "md" => "text/markdown",
        _ => "application/octet-stream",
    }
}

/// Initialize a job for the report.
///
/// Creates a job record. If github_job_id is provided and exists, returns existing job (idempotent).
/// Use init_html to register HTML files for upload after job creation.
#[utoipa::path(
    post,
    path = "/reports/{report_id}/jobs/init",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID")
    ),
    request_body = InitJobRequest,
    responses(
        (status = 200, description = "Job initialized", body = InitJobResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn init_job(
    auth: crate::auth::ApiKeyAuth,
    pool: web::Data<DbPool>,
    broadcaster: web::Data<EventBroadcaster>,
    path: web::Path<Uuid>,
    body: web::Json<InitJobRequest>,
) -> AppResult<HttpResponse> {
    if auth.caller.role == crate::models::ApiKeyRole::Viewer {
        return Err(AppError::Unauthorized(
            "Viewer role cannot initialize jobs".to_string(),
        ));
    }

    let report_id = path.into_inner();
    let req = body.into_inner();

    // Verify report exists
    let report = pool
        .get_report_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    // Check for existing job with same github_job_id (idempotency)
    let github_job_id = req.github_metadata.as_ref().and_then(|m| m.job_id.as_ref());
    if let Some(github_job_id) = github_job_id
        && let Some(existing_job) = pool.find_job_by_github_id(report_id, github_job_id).await?
    {
        info!(
            "Returning existing job: job_id={}, github_job_id={}",
            existing_job.id, github_job_id
        );

        let response = InitJobResponse {
            job_id: existing_job.id,
            is_existing: true,
        };

        return Ok(HttpResponse::Ok().json(response));
    }

    // Generate UUIDv7 for time-ordered job ID
    let job_id = Uuid::now_v7();

    // Insert job with Pending status
    let github_job_name = req
        .github_metadata
        .as_ref()
        .and_then(|m| m.job_name.clone());
    let _job = pool
        .insert_job(
            job_id,
            report_id,
            req.github_metadata.clone(),
            req.environment.clone(),
            JobStatus::Pending,
        )
        .await?;

    // Update report status to "uploading" if this is the first job
    let current_status = ReportStatus::parse(&report.status);
    if current_status == Some(ReportStatus::Initializing) {
        pool.update_report_status(report_id, ReportStatus::Uploading)
            .await?;
        // Broadcast report_updated event for status change
        let event = WsEventMessage::new(WsEvent::report_updated(report_id));
        broadcaster.send(event);
    }

    // Broadcast job_created event
    let event = WsEventMessage::new(WsEvent::job_created(report_id, job_id));
    broadcaster.send(event);

    info!(
        "Job initialized: report_id={}, job_id={}, github_job_name={:?}",
        report_id, job_id, github_job_name
    );

    let response = InitJobResponse {
        job_id,
        is_existing: false,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Initialize HTML file uploads for a job.
///
/// Validates files and records them for tracking. Files must be uploaded using upload_html endpoint.
#[utoipa::path(
    post,
    path = "/reports/{report_id}/jobs/{job_id}/html/init",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("job_id" = Uuid, Path, description = "Job UUID")
    ),
    request_body = InitHtmlRequest,
    responses(
        (status = 200, description = "HTML files initialized", body = InitHtmlResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "Job not found", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn init_html(
    auth: crate::auth::ApiKeyAuth,
    pool: web::Data<DbPool>,
    path: web::Path<(Uuid, Uuid)>,
    body: web::Json<InitHtmlRequest>,
) -> AppResult<HttpResponse> {
    if auth.caller.role == crate::models::ApiKeyRole::Viewer {
        return Err(AppError::Unauthorized(
            "Viewer role cannot initialize HTML uploads".to_string(),
        ));
    }

    let (report_id, job_id) = path.into_inner();
    let req = body.into_inner();

    // Validate request has files
    if req.files.is_empty() {
        return Err(AppError::InvalidInput(
            "At least one file must be specified".to_string(),
        ));
    }

    // Verify job exists and belongs to report
    let job = pool
        .get_job_by_id(job_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Job {}", job_id)))?;

    if job.test_report_id != report_id {
        return Err(AppError::NotFound(format!(
            "Job {} in report {}",
            job_id, report_id
        )));
    }

    // Validate files and separate accepted/rejected
    let mut accepted_files: Vec<AcceptedHtmlFile> = Vec::new();
    let mut rejected_files: Vec<RejectedFile> = Vec::new();
    let mut file_entries: Vec<HtmlFileEntry> = Vec::new();

    for file in &req.files {
        if let Some(reason) = validate_file(&file.path, file.size) {
            rejected_files.push(RejectedFile {
                path: file.path.clone(),
                reason,
            });
        } else {
            let s3_key = Storage::job_key(&report_id.to_string(), &job_id.to_string(), &file.path);
            let content_type = file
                .content_type
                .clone()
                .unwrap_or_else(|| infer_content_type(&file.path).to_string());

            accepted_files.push(AcceptedHtmlFile {
                path: file.path.clone(),
                s3_key: s3_key.clone(),
            });

            file_entries.push(HtmlFileEntry {
                filename: file.path.clone(),
                s3_key,
                size_bytes: file.size.unwrap_or(0),
                content_type: Some(content_type),
            });
        }
    }

    // Require at least one accepted file
    if accepted_files.is_empty() {
        return Err(AppError::InvalidInput(
            "No valid files to upload. All files were rejected.".to_string(),
        ));
    }

    // Insert HTML file records to track uploads (pending status)
    pool.insert_html_files(job_id, file_entries).await?;

    // Set HTML upload status to "started"
    pool.set_html_upload_status(job_id, Some("started")).await?;

    info!(
        "HTML files initialized: report_id={}, job_id={}, files_accepted={}, files_rejected={}",
        report_id,
        job_id,
        accepted_files.len(),
        rejected_files.len()
    );

    let response = InitHtmlResponse {
        job_id,
        accepted_files,
        rejected_files,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get HTML upload progress for a job.
///
/// Returns the number of HTML files uploaded and total expected.
#[utoipa::path(
    get,
    path = "/reports/{report_id}/jobs/{job_id}/html/progress",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("job_id" = Uuid, Path, description = "Job UUID")
    ),
    responses(
        (status = 200, description = "HTML upload progress", body = HtmlUploadProgress),
        (status = 404, description = "Job not found", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn get_html_progress(
    pool: web::Data<DbPool>,
    path: web::Path<(Uuid, Uuid)>,
) -> AppResult<HttpResponse> {
    let (report_id, job_id) = path.into_inner();

    // Verify job exists and belongs to report
    let job = pool
        .get_job_by_id(job_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Job {}", job_id)))?;

    if job.test_report_id != report_id {
        return Err(AppError::NotFound(format!(
            "Job {} in report {}",
            job_id, report_id
        )));
    }

    // Get upload progress
    let (uploaded, total) = pool.get_html_upload_progress(job_id).await?;
    let all_uploaded = pool.all_html_files_uploaded(job_id).await?;

    let response = HtmlUploadProgress {
        job_id,
        uploaded,
        total,
        all_uploaded,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Query jobs with filtering and pagination.
#[utoipa::path(
    get,
    path = "/jobs",
    tag = "Jobs",
    params(
        ("report_id" = Option<Uuid>, Query, description = "Filter by report ID"),
        ("status" = Option<String>, Query, description = "Filter by status"),
        ("github_job_name" = Option<String>, Query, description = "Filter by GitHub job name (partial match)"),
        ("from_date" = Option<String>, Query, description = "Filter from date (ISO 8601)"),
        ("to_date" = Option<String>, Query, description = "Filter to date (ISO 8601)"),
        ("limit" = Option<i32>, Query, description = "Results per page (default 20, max 100)"),
        ("offset" = Option<i32>, Query, description = "Pagination offset")
    ),
    responses(
        (status = 200, description = "List of jobs", body = JobListResponse),
    )
)]
pub async fn query_jobs(
    pool: web::Data<DbPool>,
    query: web::Query<QueryJobsParams>,
) -> AppResult<HttpResponse> {
    let params = query.into_inner();
    let (jobs, total) = pool.query_jobs(&params).await?;

    let jobs_response: Vec<JobDetailResponse> = jobs
        .into_iter()
        .map(|j| {
            let github_metadata = JobGitHubMetadata::from_json(j.github_metadata.as_ref());
            let github_metadata = if github_metadata.is_empty() {
                None
            } else {
                Some(github_metadata)
            };

            let env = EnvironmentMetadata::from_json(j.environment.as_ref());
            let env = if env.is_empty() { None } else { Some(env) };

            JobDetailResponse {
                id: j.id,
                report_id: j.test_report_id,
                github_metadata,
                status: JobStatus::parse(&j.status).unwrap_or(JobStatus::Pending),
                html_url: j.html_path.map(|p| format!("/files/{}/index.html", p)),
                environment: env,
                error_message: j.error_message,
                created_at: j.created_at,
                updated_at: j.updated_at,
            }
        })
        .collect();

    let response = JobListResponse {
        jobs: jobs_response,
        total: total as i64,
        limit: params.limit,
        offset: params.offset,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get a single job by ID.
#[utoipa::path(
    get,
    path = "/reports/{report_id}/jobs/{job_id}",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("job_id" = Uuid, Path, description = "Job UUID")
    ),
    responses(
        (status = 200, description = "Job details", body = JobDetailResponse),
        (status = 404, description = "Job not found", body = crate::error::ErrorResponse),
    )
)]
pub async fn get_job(
    pool: web::Data<DbPool>,
    path: web::Path<(Uuid, Uuid)>,
) -> AppResult<HttpResponse> {
    let (report_id, job_id) = path.into_inner();

    let job = pool
        .get_job_by_id(job_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Job {}", job_id)))?;

    if job.test_report_id != report_id {
        return Err(AppError::NotFound(format!(
            "Job {} in report {}",
            job_id, report_id
        )));
    }

    let github_metadata = JobGitHubMetadata::from_json(job.github_metadata.as_ref());
    let github_metadata = if github_metadata.is_empty() {
        None
    } else {
        Some(github_metadata)
    };

    let env = EnvironmentMetadata::from_json(job.environment.as_ref());
    let env = if env.is_empty() { None } else { Some(env) };

    let response = JobDetailResponse {
        id: job.id,
        report_id: job.test_report_id,
        github_metadata,
        status: JobStatus::parse(&job.status).unwrap_or(JobStatus::Pending),
        html_url: job.html_path.map(|p| format!("/files/{}/index.html", p)),
        environment: env,
        error_message: job.error_message,
        created_at: job.created_at,
        updated_at: job.updated_at,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Upload HTML files for a job.
///
/// Accepts multipart form data with files. Each file must have been registered
/// during init_html. Files are uploaded to storage and tracked in the database.
/// When all files are uploaded, job status is automatically updated to html_uploaded.
#[utoipa::path(
    post,
    path = "/reports/{report_id}/jobs/{job_id}/html",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("job_id" = Uuid, Path, description = "Job UUID")
    ),
    responses(
        (status = 200, description = "HTML files uploaded", body = HtmlUploadResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "Job not found", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn upload_html(
    auth: crate::auth::ApiKeyAuth,
    pool: web::Data<DbPool>,
    storage: web::Data<Storage>,
    path: web::Path<(Uuid, Uuid)>,
    mut payload: Multipart,
) -> AppResult<HttpResponse> {
    if auth.caller.role == crate::models::ApiKeyRole::Viewer {
        return Err(AppError::Unauthorized(
            "Viewer role cannot upload HTML files".to_string(),
        ));
    }

    let (report_id, job_id) = path.into_inner();

    // Verify job exists and belongs to report
    let job = pool
        .get_job_by_id(job_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Job {}", job_id)))?;

    if job.test_report_id != report_id {
        return Err(AppError::NotFound(format!(
            "Job {} in report {}",
            job_id, report_id
        )));
    }

    // Get pending HTML files for this job
    let pending_files = pool.get_pending_html_files(job_id).await?;
    let pending_filenames: std::collections::HashSet<_> =
        pending_files.iter().map(|f| f.filename.as_str()).collect();

    let mut files_uploaded_this_request = 0u64;
    let mut uploaded_filenames: Vec<String> = Vec::new();

    // Process each file in the multipart form
    while let Some(item) = payload.next().await {
        let mut field =
            item.map_err(|e| AppError::InvalidInput(format!("Multipart error: {}", e)))?;

        // Get the filename from Content-Disposition header
        let filename = field
            .content_disposition()
            .and_then(|cd| cd.get_filename())
            .ok_or_else(|| AppError::InvalidInput("Missing filename in multipart".to_string()))?
            .to_string();

        // Check if this file is expected
        if !pending_filenames.contains(filename.as_str()) {
            // Skip files not in the pending list (already uploaded or not registered)
            continue;
        }

        // Get the HTML file record to get s3_key
        let file_record = pool
            .get_html_file(job_id, &filename)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("HTML file {} not found", filename)))?;

        // Collect file data
        let mut data = Vec::new();
        while let Some(chunk) = field.next().await {
            let chunk = chunk.map_err(|e| AppError::InvalidInput(format!("Read error: {}", e)))?;
            data.extend_from_slice(&chunk);
        }

        // Upload to storage
        let content_type = file_record
            .content_type
            .as_deref()
            .unwrap_or(infer_content_type(&filename));

        storage
            .put(&file_record.s3_key, data, Some(content_type))
            .await?;

        uploaded_filenames.push(filename);
        files_uploaded_this_request += 1;
    }

    // Mark HTML files as uploaded in database
    if !uploaded_filenames.is_empty() {
        pool.mark_html_files_uploaded(job_id, &uploaded_filenames)
            .await?;
    }

    // Get updated progress
    let (total_uploaded, total_expected) = pool.get_html_upload_progress(job_id).await?;
    let all_uploaded = pool.all_html_files_uploaded(job_id).await?;

    // Set html_upload_status to "completed" when all HTML files are uploaded
    if all_uploaded {
        let html_path = Storage::job_key_prefix(&report_id.to_string(), &job_id.to_string());
        pool.update_job_html_path(job_id, html_path).await?;
        pool.set_html_upload_status(job_id, Some("completed"))
            .await?;

        info!(
            "All HTML files uploaded: report_id={}, job_id={}",
            report_id, job_id
        );
    }

    info!(
        "HTML files uploaded: report_id={}, job_id={}, this_request={}, total={}/{}",
        report_id, job_id, files_uploaded_this_request, total_uploaded, total_expected
    );

    let response = HtmlUploadResponse {
        job_id,
        files_uploaded: files_uploaded_this_request,
        total_uploaded,
        total_expected,
        all_uploaded,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Validate a screenshot file and return rejection reason if invalid.
fn validate_screenshot(path: &str, size: Option<i64>) -> Option<String> {
    // Check for path traversal
    if path.contains("..") {
        return Some("Path traversal not allowed".to_string());
    }

    // Check empty path
    if path.is_empty() {
        return Some("Empty file path".to_string());
    }

    // Check extension
    let extension = path.rsplit('.').next().unwrap_or("").to_lowercase();
    if !ALLOWED_IMAGE_EXTENSIONS.contains(&extension.as_str()) {
        return Some(format!(
            "File extension '{}' not allowed. Allowed: {:?}",
            extension, ALLOWED_IMAGE_EXTENSIONS
        ));
    }

    // Check file size
    if let Some(size) = size {
        if size > MAX_SCREENSHOT_SIZE {
            return Some(format!(
                "File size {} exceeds maximum {} bytes",
                size, MAX_SCREENSHOT_SIZE
            ));
        }
        if size < 0 {
            return Some("Invalid file size".to_string());
        }
    }

    None
}

/// Extract test name from screenshot path.
/// Path format: "test-name/screenshot.png" -> "test-name"
/// Path format: "test-name/subdir/screenshot.png" -> "test-name" (first directory only)
/// Path format: "screenshot.png" -> "" (root level)
///
/// For Detox: screenshots are organized as "full-test-name/screenshot.png"
/// where full-test-name is the entire directory path up to the filename.
fn extract_test_name(path: &str) -> String {
    // Find the last slash to get directory path (everything except filename)
    if let Some(pos) = path.rfind('/') {
        path[..pos].to_string()
    } else {
        // No directory, use empty string for root-level screenshots
        String::new()
    }
}

/// Initialize screenshot uploads for a job.
///
/// Validates files, creates records, and returns accepted/rejected files.
/// Files must be uploaded using the upload_screenshots endpoint after initialization.
#[utoipa::path(
    post,
    path = "/reports/{report_id}/jobs/{job_id}/screenshots/init",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("job_id" = Uuid, Path, description = "Job UUID")
    ),
    request_body = InitScreenshotsRequest,
    responses(
        (status = 200, description = "Screenshots initialized", body = InitScreenshotsResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "Job not found", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn init_screenshots(
    auth: crate::auth::ApiKeyAuth,
    pool: web::Data<DbPool>,
    _storage: web::Data<Storage>,
    path: web::Path<(Uuid, Uuid)>,
    body: web::Json<InitScreenshotsRequest>,
) -> AppResult<HttpResponse> {
    if auth.caller.role == crate::models::ApiKeyRole::Viewer {
        return Err(AppError::Unauthorized(
            "Viewer role cannot initialize screenshot uploads".to_string(),
        ));
    }

    let (report_id, job_id) = path.into_inner();
    let req = body.into_inner();

    // Validate request has files
    if req.files.is_empty() {
        return Err(AppError::InvalidInput(
            "At least one file must be specified".to_string(),
        ));
    }

    // Verify job exists and belongs to report
    let job = pool
        .get_job_by_id(job_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Job {}", job_id)))?;

    if job.test_report_id != report_id {
        return Err(AppError::NotFound(format!(
            "Job {} in report {}",
            job_id, report_id
        )));
    }

    // Validate files and separate accepted/rejected
    let mut accepted_files: Vec<AcceptedScreenshot> = Vec::new();
    let mut rejected_files: Vec<RejectedFile> = Vec::new();
    let mut screenshot_entries: Vec<ScreenshotEntry> = Vec::new();
    let mut sequence: i32 = 0;

    for file in &req.files {
        if let Some(reason) = validate_screenshot(&file.path, file.size) {
            rejected_files.push(RejectedFile {
                path: file.path.clone(),
                reason,
            });
        } else {
            let s3_key = format!(
                "{}/screenshots/{}",
                Storage::job_key_prefix(&report_id.to_string(), &job_id.to_string()),
                &file.path
            );
            let content_type = file
                .content_type
                .clone()
                .unwrap_or_else(|| infer_content_type(&file.path).to_string());
            let test_name = extract_test_name(&file.path);

            accepted_files.push(AcceptedScreenshot {
                path: file.path.clone(),
                s3_key: s3_key.clone(),
                test_name: test_name.clone(),
            });

            screenshot_entries.push(ScreenshotEntry {
                filename: file.path.clone(),
                s3_key,
                size_bytes: file.size.unwrap_or(0),
                content_type: Some(content_type),
                test_name,
                sequence,
            });

            sequence += 1;
        }
    }

    // Require at least one accepted file
    if accepted_files.is_empty() {
        return Err(AppError::InvalidInput(
            "No valid files to upload. All files were rejected.".to_string(),
        ));
    }

    // Insert screenshot records to track uploads (pending status)
    pool.insert_screenshots(job_id, screenshot_entries).await?;

    // Set screenshots upload status to "started"
    pool.set_screenshots_upload_status(job_id, Some("started"))
        .await?;

    info!(
        "Screenshots initialized: report_id={}, job_id={}, files_accepted={}, files_rejected={}",
        report_id,
        job_id,
        accepted_files.len(),
        rejected_files.len()
    );

    let response = InitScreenshotsResponse {
        job_id,
        accepted_files,
        rejected_files,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Upload screenshots for a job.
///
/// Accepts multipart form data with image files. Only files that were registered
/// during init_screenshots will be accepted. Files are uploaded to S3 and their
/// status is updated in the database.
#[utoipa::path(
    post,
    path = "/reports/{report_id}/jobs/{job_id}/screenshots",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("job_id" = Uuid, Path, description = "Job UUID")
    ),
    responses(
        (status = 200, description = "Screenshots uploaded", body = ScreenshotUploadResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "Job not found", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn upload_screenshots(
    auth: crate::auth::ApiKeyAuth,
    pool: web::Data<DbPool>,
    storage: web::Data<Storage>,
    path: web::Path<(Uuid, Uuid)>,
    mut payload: Multipart,
) -> AppResult<HttpResponse> {
    if auth.caller.role == crate::models::ApiKeyRole::Viewer {
        return Err(AppError::Unauthorized(
            "Viewer role cannot upload screenshots".to_string(),
        ));
    }

    let (report_id, job_id) = path.into_inner();

    // Verify job exists and belongs to report
    let job = pool
        .get_job_by_id(job_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Job {}", job_id)))?;

    if job.test_report_id != report_id {
        return Err(AppError::NotFound(format!(
            "Job {} in report {}",
            job_id, report_id
        )));
    }

    // Get pending screenshots for this job
    let pending_screenshots = pool.get_pending_screenshots(job_id).await?;
    let pending_filenames: std::collections::HashSet<_> = pending_screenshots
        .iter()
        .map(|s| s.filename.as_str())
        .collect();

    let mut files_uploaded_this_request = 0u64;
    let mut uploaded_filenames: Vec<String> = Vec::new();

    // Process each file in the multipart form
    while let Some(item) = payload.next().await {
        let mut field =
            item.map_err(|e| AppError::InvalidInput(format!("Multipart error: {}", e)))?;

        // Get the filename from Content-Disposition header
        let filename = field
            .content_disposition()
            .and_then(|cd| cd.get_filename())
            .ok_or_else(|| AppError::InvalidInput("Missing filename in multipart".to_string()))?
            .to_string();

        // Check if this file is expected
        if !pending_filenames.contains(filename.as_str()) {
            // Skip files not in the pending list (already uploaded or not registered)
            continue;
        }

        // Get the screenshot record to get s3_key
        let screenshot_record = pool
            .get_screenshot(job_id, &filename)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("Screenshot {} not found", filename)))?;

        // Collect file data
        let mut data = Vec::new();
        while let Some(chunk) = field.next().await {
            let chunk = chunk.map_err(|e| AppError::InvalidInput(format!("Read error: {}", e)))?;
            data.extend_from_slice(&chunk);
        }

        // Upload to storage
        let content_type = screenshot_record
            .content_type
            .as_deref()
            .unwrap_or(infer_content_type(&filename));

        storage
            .put(&screenshot_record.s3_key, data, Some(content_type))
            .await?;

        uploaded_filenames.push(filename);
        files_uploaded_this_request += 1;
    }

    // Mark screenshots as uploaded in database
    if !uploaded_filenames.is_empty() {
        pool.mark_screenshots_uploaded(job_id, &uploaded_filenames)
            .await?;

        // Try to link uploaded screenshots to existing test cases
        // (test cases may already exist if JSON was uploaded first)
        if let Err(e) = extraction::link_screenshots_to_test_cases(&pool, job_id).await {
            // Don't fail the upload - linking is not critical
            tracing::warn!("Failed to link screenshots for job {}: {}", job_id, e);
        }
    }

    // Get updated progress
    let (total_uploaded, total_expected) = pool.get_screenshot_upload_progress(job_id).await?;
    let all_uploaded = pool.all_screenshots_uploaded(job_id).await?;

    // Set screenshots_upload_status to "completed" when all screenshots are uploaded
    if all_uploaded {
        pool.set_screenshots_upload_status(job_id, Some("completed"))
            .await?;

        info!(
            "All screenshots uploaded: report_id={}, job_id={}",
            report_id, job_id
        );
    }

    info!(
        "Screenshots uploaded: report_id={}, job_id={}, this_request={}, total={}/{}",
        report_id, job_id, files_uploaded_this_request, total_uploaded, total_expected
    );

    let response = ScreenshotUploadResponse {
        job_id,
        files_uploaded: files_uploaded_this_request,
        total_uploaded,
        total_expected,
        all_uploaded,
    };

    Ok(HttpResponse::Ok().json(response))
}

// ============================================================================
// JSON File Upload Handlers
// ============================================================================

/// Allowed file extensions for JSON test result uploads.
const ALLOWED_JSON_EXTENSIONS: &[&str] = &["json"];

/// Maximum JSON file size (50 MB).
const MAX_JSON_FILE_SIZE: i64 = 50 * 1024 * 1024;

/// Validate a JSON file and return rejection reason if invalid.
fn validate_json_file(path: &str, size: Option<i64>) -> Option<String> {
    // Check for path traversal
    if path.contains("..") {
        return Some("Path traversal not allowed".to_string());
    }

    // Check empty path
    if path.is_empty() {
        return Some("Empty file path".to_string());
    }

    // Check extension
    let extension = path.rsplit('.').next().unwrap_or("").to_lowercase();
    if !ALLOWED_JSON_EXTENSIONS.contains(&extension.as_str()) {
        return Some(format!(
            "File extension '{}' not allowed. Allowed: {:?}",
            extension, ALLOWED_JSON_EXTENSIONS
        ));
    }

    // Check file size
    if let Some(size) = size {
        if size > MAX_JSON_FILE_SIZE {
            return Some(format!(
                "File size {} exceeds maximum {} bytes",
                size, MAX_JSON_FILE_SIZE
            ));
        }
        if size < 0 {
            return Some("Invalid file size".to_string());
        }
    }

    None
}

/// Initialize JSON file uploads for a job.
///
/// Validates files, creates records, and returns accepted/rejected files.
/// Files must be uploaded using the upload_json endpoint after initialization.
/// JSON files are required and will trigger extraction when all are uploaded.
#[utoipa::path(
    post,
    path = "/reports/{report_id}/jobs/{job_id}/json/init",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("job_id" = Uuid, Path, description = "Job UUID")
    ),
    request_body = InitJsonRequest,
    responses(
        (status = 200, description = "JSON files initialized", body = InitJsonResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "Job not found", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn init_json(
    auth: crate::auth::ApiKeyAuth,
    pool: web::Data<DbPool>,
    path: web::Path<(Uuid, Uuid)>,
    body: web::Json<InitJsonRequest>,
) -> AppResult<HttpResponse> {
    if auth.caller.role == crate::models::ApiKeyRole::Viewer {
        return Err(AppError::Unauthorized(
            "Viewer role cannot initialize JSON uploads".to_string(),
        ));
    }

    let (report_id, job_id) = path.into_inner();
    let req = body.into_inner();

    // Validate request has files
    if req.files.is_empty() {
        return Err(AppError::InvalidInput(
            "At least one JSON file must be specified".to_string(),
        ));
    }

    // Verify job exists and belongs to report
    let job = pool
        .get_job_by_id(job_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Job {}", job_id)))?;

    if job.test_report_id != report_id {
        return Err(AppError::NotFound(format!(
            "Job {} in report {}",
            job_id, report_id
        )));
    }

    // Validate files and separate accepted/rejected
    let mut accepted_files: Vec<AcceptedJsonFile> = Vec::new();
    let mut rejected_files: Vec<RejectedFile> = Vec::new();
    let mut file_entries: Vec<JsonFileEntry> = Vec::new();

    for file in &req.files {
        if let Some(reason) = validate_json_file(&file.path, file.size) {
            rejected_files.push(RejectedFile {
                path: file.path.clone(),
                reason,
            });
        } else {
            let s3_key = format!(
                "{}/json/{}",
                Storage::job_key_prefix(&report_id.to_string(), &job_id.to_string()),
                &file.path
            );
            let content_type = file
                .content_type
                .clone()
                .unwrap_or_else(|| "application/json".to_string());

            accepted_files.push(AcceptedJsonFile {
                path: file.path.clone(),
                s3_key: s3_key.clone(),
            });

            file_entries.push(JsonFileEntry {
                filename: file.path.clone(),
                s3_key,
                size_bytes: file.size.unwrap_or(0),
                content_type: Some(content_type),
            });
        }
    }

    // Require at least one accepted file
    if accepted_files.is_empty() {
        return Err(AppError::InvalidInput(
            "No valid JSON files to upload. All files were rejected.".to_string(),
        ));
    }

    // Insert JSON file records to track uploads (pending status)
    pool.insert_json_files(job_id, file_entries).await?;

    // Set JSON upload status to "started"
    pool.set_json_upload_status(job_id, Some("started")).await?;

    info!(
        "JSON files initialized: report_id={}, job_id={}, files_accepted={}, files_rejected={}",
        report_id,
        job_id,
        accepted_files.len(),
        rejected_files.len()
    );

    let response = InitJsonResponse {
        job_id,
        accepted_files,
        rejected_files,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get JSON upload progress for a job.
///
/// Returns the number of JSON files uploaded and total expected.
#[utoipa::path(
    get,
    path = "/reports/{report_id}/jobs/{job_id}/json/progress",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("job_id" = Uuid, Path, description = "Job UUID")
    ),
    responses(
        (status = 200, description = "JSON upload progress", body = JsonUploadProgress),
        (status = 404, description = "Job not found", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn get_json_progress(
    pool: web::Data<DbPool>,
    path: web::Path<(Uuid, Uuid)>,
) -> AppResult<HttpResponse> {
    let (report_id, job_id) = path.into_inner();

    // Verify job exists and belongs to report
    let job = pool
        .get_job_by_id(job_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Job {}", job_id)))?;

    if job.test_report_id != report_id {
        return Err(AppError::NotFound(format!(
            "Job {} in report {}",
            job_id, report_id
        )));
    }

    // Get upload progress
    let (uploaded, total) = pool.get_json_upload_progress(job_id).await?;
    let all_uploaded = pool.all_json_files_uploaded(job_id).await?;

    let response = JsonUploadProgress {
        job_id,
        uploaded,
        total,
        all_uploaded,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Upload JSON files for a job.
///
/// Accepts multipart form data with JSON files. Only files that were registered
/// during init_json will be accepted. Files are uploaded to S3 and their
/// status is updated in the database.
/// When all JSON files are uploaded, extraction is automatically triggered.
#[utoipa::path(
    post,
    path = "/reports/{report_id}/jobs/{job_id}/json",
    tag = "Jobs",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("job_id" = Uuid, Path, description = "Job UUID")
    ),
    responses(
        (status = 200, description = "JSON files uploaded", body = JsonUploadResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "Job not found", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn upload_json(
    auth: crate::auth::ApiKeyAuth,
    pool: web::Data<DbPool>,
    storage: web::Data<Storage>,
    broadcaster: web::Data<EventBroadcaster>,
    path: web::Path<(Uuid, Uuid)>,
    mut payload: Multipart,
) -> AppResult<HttpResponse> {
    if auth.caller.role == crate::models::ApiKeyRole::Viewer {
        return Err(AppError::Unauthorized(
            "Viewer role cannot upload JSON files".to_string(),
        ));
    }

    let (report_id, job_id) = path.into_inner();

    // Verify job exists and belongs to report
    let job = pool
        .get_job_by_id(job_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Job {}", job_id)))?;

    if job.test_report_id != report_id {
        return Err(AppError::NotFound(format!(
            "Job {} in report {}",
            job_id, report_id
        )));
    }

    // Get pending JSON files for this job
    let pending_files = pool.get_pending_json_files(job_id).await?;
    let pending_filenames: std::collections::HashSet<_> =
        pending_files.iter().map(|f| f.filename.as_str()).collect();

    let mut files_uploaded_this_request = 0u64;
    let mut uploaded_filenames: Vec<String> = Vec::new();

    // Process each file in the multipart form
    while let Some(item) = payload.next().await {
        let mut field =
            item.map_err(|e| AppError::InvalidInput(format!("Multipart error: {}", e)))?;

        // Get the filename from Content-Disposition header
        let filename = field
            .content_disposition()
            .and_then(|cd| cd.get_filename())
            .ok_or_else(|| AppError::InvalidInput("Missing filename in multipart".to_string()))?
            .to_string();

        // Check if this file is expected
        if !pending_filenames.contains(filename.as_str()) {
            // Skip files not in the pending list (already uploaded or not registered)
            continue;
        }

        // Get the JSON file record to get s3_key
        let file_record = pool
            .get_json_file(job_id, &filename)
            .await?
            .ok_or_else(|| AppError::NotFound(format!("JSON file {} not found", filename)))?;

        // Collect file data
        let mut data = Vec::new();
        while let Some(chunk) = field.next().await {
            let chunk = chunk.map_err(|e| AppError::InvalidInput(format!("Read error: {}", e)))?;
            data.extend_from_slice(&chunk);
        }

        // Upload to storage
        let content_type = file_record
            .content_type
            .as_deref()
            .unwrap_or("application/json");

        storage
            .put(&file_record.s3_key, data, Some(content_type))
            .await?;

        uploaded_filenames.push(filename);
        files_uploaded_this_request += 1;
    }

    // Mark JSON files as uploaded in database
    if !uploaded_filenames.is_empty() {
        pool.mark_json_files_uploaded(job_id, &uploaded_filenames)
            .await?;
    }

    // Get updated progress
    let (total_uploaded, total_expected) = pool.get_json_upload_progress(job_id).await?;
    let all_uploaded = pool.all_json_files_uploaded(job_id).await?;

    let mut extraction_triggered = false;

    // When all JSON files are uploaded, trigger extraction
    if all_uploaded {
        pool.set_json_upload_status(job_id, Some("completed"))
            .await?;

        // Update job status to processing
        pool.update_job_status(job_id, JobStatus::Processing, None)
            .await?;

        // Broadcast job_updated event for status change to processing
        let event = WsEventMessage::new(WsEvent::job_updated(
            report_id,
            job_id,
            "processing".to_string(),
        ));
        broadcaster.send(event);

        // Get report info for framework
        let report = pool.get_report_by_id(report_id).await?.unwrap();

        // Spawn extraction task (per-job, non-blocking)
        let pool_clone = pool.get_ref().clone();
        let storage_clone = storage.get_ref().clone();
        let broadcaster_clone = broadcaster.get_ref().clone();
        let framework = report.framework.clone();
        tokio::spawn(async move {
            extraction::extract_job(
                &pool_clone,
                &storage_clone,
                &broadcaster_clone,
                job_id,
                &framework,
            )
            .await;
        });

        extraction_triggered = true;

        info!(
            "All JSON files uploaded, extraction started: report_id={}, job_id={}",
            report_id, job_id
        );
    }

    info!(
        "JSON files uploaded: report_id={}, job_id={}, this_request={}, total={}/{}",
        report_id, job_id, files_uploaded_this_request, total_uploaded, total_expected
    );

    let response = JsonUploadResponse {
        job_id,
        files_uploaded: files_uploaded_this_request,
        total_uploaded,
        total_expected,
        all_uploaded,
        extraction_triggered,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Configure job routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(web::resource("/jobs").route(web::get().to(query_jobs)))
        .service(web::resource("/reports/{report_id}/jobs/init").route(web::post().to(init_job)))
        .service(web::resource("/reports/{report_id}/jobs/{job_id}").route(web::get().to(get_job)))
        // HTML upload routes (request-then-transfer pattern)
        .service(
            web::resource("/reports/{report_id}/jobs/{job_id}/html/init")
                .route(web::post().to(init_html)),
        )
        .service(
            web::resource("/reports/{report_id}/jobs/{job_id}/html")
                .route(web::post().to(upload_html)),
        )
        .service(
            web::resource("/reports/{report_id}/jobs/{job_id}/html/progress")
                .route(web::get().to(get_html_progress)),
        )
        // Screenshot upload routes (request-then-transfer pattern)
        .service(
            web::resource("/reports/{report_id}/jobs/{job_id}/screenshots/init")
                .route(web::post().to(init_screenshots)),
        )
        .service(
            web::resource("/reports/{report_id}/jobs/{job_id}/screenshots")
                .route(web::post().to(upload_screenshots)),
        )
        // JSON upload routes (request-then-transfer pattern)
        .service(
            web::resource("/reports/{report_id}/jobs/{job_id}/json/init")
                .route(web::post().to(init_json)),
        )
        .service(
            web::resource("/reports/{report_id}/jobs/{job_id}/json")
                .route(web::post().to(upload_json)),
        )
        .service(
            web::resource("/reports/{report_id}/jobs/{job_id}/json/progress")
                .route(web::get().to(get_json_progress)),
        );
}
