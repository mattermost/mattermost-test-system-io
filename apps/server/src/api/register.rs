//! Report upload API handlers.

use actix_multipart::Multipart;
use actix_web::{HttpResponse, web};
use futures_util::StreamExt;
use sea_orm::Set;
use tracing::info;
use uuid::Uuid;

use crate::auth::ApiKeyAuth;
use crate::db::DbPool;
use crate::db::json_files::JsonFileEntry;
use crate::db::screenshots::ScreenshotEntry;
use crate::error::{AppError, AppResult};
use crate::models::{
    AcceptedJsonFile, AcceptedScreenshot, ApiKeyRole, JsonUploadResponse, RegisterReportRequest,
    RegisterReportResponse, RejectedFile, ReportStatus, ScreenshotUploadResponse, UploadStatus,
    WsEvent, WsEventMessage,
};
use crate::services::extraction;
use crate::services::{EventBroadcaster, Storage};

use super::file_validation::{
    extract_test_name, infer_content_type, validate_json_file, validate_screenshot,
};

/// Register a report (auto-creates report group by grouping key).
///
/// Creates a report group if none exists for the grouping key, creates a report,
/// validates declared files, and returns accepted/rejected lists in a single
/// API call.
#[utoipa::path(
    post,
    path = "/reports/register",
    tag = "Reports",
    request_body = RegisterReportRequest,
    responses(
        (status = 200, description = "Report registered", body = RegisterReportResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
    ),
    security(
        ("api_key" = [])
    )
)]
pub async fn register_report(
    auth: ApiKeyAuth,
    pool: web::Data<DbPool>,
    broadcaster: web::Data<EventBroadcaster>,
    body: web::Json<RegisterReportRequest>,
) -> AppResult<HttpResponse> {
    // Require at least contributor role
    if auth.caller.role == ApiKeyRole::Viewer {
        return Err(AppError::Unauthorized(
            "Viewer role cannot register reports".to_string(),
        ));
    }

    let req = body.into_inner();

    // ── Validate required fields ─────────────────────────────────────────
    if req.repository.trim().is_empty() {
        return Err(AppError::InvalidInput("repository is required".to_string()));
    }
    if !req.repository.contains('/') {
        return Err(AppError::InvalidInput(
            "repository must be in org/repo format".to_string(),
        ));
    }
    if req.commit.trim().is_empty() {
        return Err(AppError::InvalidInput("commit is required".to_string()));
    }
    if req.commit.len() < 7
        || req.commit.len() > 40
        || !req.commit.chars().all(|c| c.is_ascii_hexdigit())
    {
        return Err(AppError::InvalidInput(
            "commit must be a 7-40 character hex SHA".to_string(),
        ));
    }
    if req.name.trim().is_empty() {
        return Err(AppError::InvalidInput("name is required".to_string()));
    }
    if !req
        .name
        .chars()
        .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
    {
        return Err(AppError::InvalidInput(
            "name must contain only alphanumeric characters, hyphens, and underscores".to_string(),
        ));
    }
    if req.gh_run_id.is_empty() || !req.gh_run_id.chars().all(|c| c.is_ascii_digit()) {
        return Err(AppError::InvalidInput(
            "gh_run_id is required and must be numeric".to_string(),
        ));
    }
    if req.gh_job_id.is_empty() || !req.gh_job_id.chars().all(|c| c.is_ascii_digit()) {
        return Err(AppError::InvalidInput(
            "gh_job_id is required and must be numeric".to_string(),
        ));
    }
    if req.gh_job_name.trim().is_empty() {
        return Err(AppError::InvalidInput(
            "gh_job_name is required".to_string(),
        ));
    }
    if let Some(ref branch) = req.branch
        && branch.trim().is_empty()
    {
        return Err(AppError::InvalidInput(
            "branch must not be empty when provided".to_string(),
        ));
    }

    let gh_run_id = &req.gh_run_id;

    // ── Derive gh_run_attempt from OIDC claims (or default "1") ──────────
    let gh_run_attempt = auth
        .caller
        .oidc_claims
        .as_ref()
        .and_then(|c| c.run_attempt.clone())
        .unwrap_or_else(|| "1".to_string());

    if !gh_run_attempt.chars().all(|c| c.is_ascii_digit()) {
        return Err(AppError::InvalidInput(
            "run_attempt must be numeric".to_string(),
        ));
    }

    // Branch: use request body if provided, otherwise derive from OIDC claims
    let branch = req.branch.clone().unwrap_or_else(|| {
        auth.caller
            .oidc_claims
            .as_ref()
            .and_then(|c| c.head_ref.clone().or_else(|| c.git_ref.clone()))
            .unwrap_or_default()
    });

    // ── Validate gh_pr_number for pull_request events ────────────────────
    let is_pr_event = auth
        .caller
        .oidc_claims
        .as_ref()
        .and_then(|c| c.event_name.as_deref())
        .map(|e| e == "pull_request")
        .unwrap_or(false);

    if is_pr_event && req.gh_pr_number.is_none() {
        return Err(AppError::InvalidInput(
            "gh_pr_number is required for pull_request events".to_string(),
        ));
    }

    // ── Upsert or find report group ─────────────────────────────────────
    use crate::db::report_groups::InsertReportGroupParams;

    let report_group_id = Uuid::now_v7();
    let (report, _is_new_report) = pool
        .upsert_or_find_report_group(InsertReportGroupParams {
            id: report_group_id,
            framework: req.framework,
            name: &req.name,
            repository: &req.repository,
            branch: &branch,
            commit: &req.commit,
            gh_run_id,
            gh_run_attempt: &gh_run_attempt,
            gh_pr_number: req.gh_pr_number,
            environment_metadata: req.environment_metadata.clone(),
        })
        .await?;

    let report_group_id = report.id; // use the actual ID (may differ if existing)

    // ── Idempotency: check for existing report by gh_job_id ──────────────
    if let Some(existing_report) = pool
        .find_report_by_gh_job_id(report_group_id, &req.gh_job_id)
        .await?
    {
        info!(
            "Returning existing report: report_id={}, gh_job_id={}",
            existing_report.id, req.gh_job_id
        );

        let reports_count = pool.count_reports_in_group(report_group_id).await?;

        return Ok(HttpResponse::Ok().json(RegisterReportResponse {
            report_id: report_group_id,
            upload_id: existing_report.id,
            is_existing: true,
            report_status: ReportStatus::parse(&report.status).unwrap_or(ReportStatus::InProgress),
            reports_in_group: reports_count,
            accepted_json_files: vec![],
            rejected_json_files: vec![],
            accepted_screenshots: vec![],
            rejected_screenshots: vec![],
        }));
    }

    // ── Create new individual report ─────────────────────────────────────
    let report_id = Uuid::now_v7();
    let _individual_report = pool
        .insert_report(
            report_id,
            report_group_id,
            &req.name,
            Some(req.gh_job_id.clone()),
            Some(req.gh_job_name.clone()),
            req.environment.clone(),
            UploadStatus::Pending,
        )
        .await?;

    // Broadcast report_registered event
    let event = WsEventMessage::new(WsEvent::report_registered(report_group_id, report_id));
    broadcaster.send(event);

    info!(
        "Report registered: report_group_id={}, report_id={}, gh_job_name={:?}",
        report_group_id, report_id, req.gh_job_name
    );

    // ── Validate and insert declared files ───────────────────────────────

    // -- JSON files --
    let mut accepted_json_files: Vec<AcceptedJsonFile> = Vec::new();
    let mut rejected_json_files: Vec<RejectedFile> = Vec::new();

    if let Some(ref json_files) = req.json_files {
        let mut json_entries: Vec<JsonFileEntry> = Vec::new();

        for file in json_files {
            if let Some(reason) = validate_json_file(&file.path, file.size) {
                rejected_json_files.push(RejectedFile {
                    path: file.path.clone(),
                    reason,
                });
            } else {
                let s3_key = format!(
                    "{}/json/{}",
                    Storage::report_key_prefix(
                        &report_group_id.to_string(),
                        &report_id.to_string()
                    ),
                    &file.path
                );
                let content_type = file
                    .content_type
                    .clone()
                    .unwrap_or_else(|| "application/json".to_string());

                accepted_json_files.push(AcceptedJsonFile {
                    path: file.path.clone(),
                    s3_key: s3_key.clone(),
                });

                json_entries.push(JsonFileEntry {
                    filename: file.path.clone(),
                    s3_key,
                    size_bytes: file.size.unwrap_or(0),
                    content_type: Some(content_type),
                });
            }
        }

        if !json_entries.is_empty() {
            pool.insert_json_files(report_id, json_entries).await?;
            pool.set_json_upload_status(report_id, Some("started"))
                .await?;
        }
    }

    // -- Screenshots --
    let mut accepted_screenshots: Vec<AcceptedScreenshot> = Vec::new();
    let mut rejected_screenshots: Vec<RejectedFile> = Vec::new();

    if let Some(ref screenshots) = req.screenshots {
        let mut screenshot_entries: Vec<ScreenshotEntry> = Vec::new();
        let mut sequence: i32 = 0;

        for file in screenshots {
            if let Some(reason) = validate_screenshot(&file.path, file.size) {
                rejected_screenshots.push(RejectedFile {
                    path: file.path.clone(),
                    reason,
                });
            } else {
                let s3_key = format!(
                    "{}/screenshots/{}",
                    Storage::report_key_prefix(
                        &report_group_id.to_string(),
                        &report_id.to_string()
                    ),
                    &file.path
                );
                let content_type = file
                    .content_type
                    .clone()
                    .unwrap_or_else(|| infer_content_type(&file.path).to_string());
                let test_name = extract_test_name(&file.path);

                accepted_screenshots.push(AcceptedScreenshot {
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

        if !screenshot_entries.is_empty() {
            pool.insert_screenshots(report_id, screenshot_entries)
                .await?;
            pool.set_screenshots_upload_status(report_id, Some("started"))
                .await?;
        }
    }

    // ── Store per-report OIDC claims if present ────────────────────────────
    if let Some(ref claims) = auth.caller.oidc_claims {
        let safe = claims.to_safe_claims(auth.caller.role.as_str(), "/reports/register", "POST");

        let claim_model = crate::entity::oidc_claim::ActiveModel {
            id: Set(Uuid::now_v7()),
            upload_id: Set(report_id),
            // Standard JWT
            jti: Set(safe.jti),
            iss: Set(safe.iss),
            aud: Set(safe.aud),
            exp: Set(safe.exp),
            iat: Set(safe.iat),
            nbf: Set(safe.nbf),
            // Identity
            sub: Set(safe.sub),
            repository: Set(safe.repository),
            repository_owner: Set(safe.repository_owner),
            actor: Set(safe.actor),
            repository_id: Set(safe.repository_id),
            repository_owner_id: Set(safe.repository_owner_id),
            repository_visibility: Set(safe.repository_visibility),
            actor_id: Set(safe.actor_id),
            // Git ref
            git_ref: Set(safe.git_ref),
            ref_type: Set(safe.ref_type),
            sha: Set(safe.sha),
            head_ref: Set(safe.head_ref),
            base_ref: Set(safe.base_ref),
            // Workflow / run
            workflow: Set(safe.workflow),
            event_name: Set(safe.event_name),
            run_id: Set(safe.run_id),
            run_number: Set(safe.run_number),
            run_attempt: Set(safe.run_attempt),
            // Environment / runner
            runner_environment: Set(safe.runner_environment),
            environment: Set(safe.environment),
            // Check / workflow ref
            check_run_id: Set(safe.check_run_id),
            job_workflow_ref: Set(safe.job_workflow_ref),
            job_workflow_sha: Set(safe.job_workflow_sha),
            workflow_ref: Set(safe.workflow_ref),
            workflow_sha: Set(safe.workflow_sha),
            // Audit
            resolved_role: Set(safe.resolved_role),
            api_path: Set(safe.api_path),
            http_method: Set(safe.http_method),
            created_at: Set(chrono::Utc::now()),
        };

        crate::db::oidc_claims::insert_oidc_claims(pool.connection(), claim_model).await?;
    }

    // ── Count reports in group ─────────────────────────────────────────
    let reports_count = pool.count_reports_in_group(report_group_id).await?;

    // ── Return response ──────────────────────────────────────────────────
    let response = RegisterReportResponse {
        report_id: report_group_id,
        upload_id: report_id,
        is_existing: false,
        report_status: ReportStatus::parse(&report.status).unwrap_or(ReportStatus::InProgress),
        reports_in_group: reports_count,
        accepted_json_files,
        rejected_json_files,
        accepted_screenshots,
        rejected_screenshots,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Upload screenshots for a report.
///
/// Accepts multipart form data with image files. Only files that were declared
/// during register will be accepted. Files are uploaded to S3 and their
/// status is updated in the database.
#[utoipa::path(
    post,
    path = "/reports/upload/{report_id}/{upload_id}/screenshots",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("upload_id" = Uuid, Path, description = "Upload ID (from register response)")
    ),
    responses(
        (status = 200, description = "Screenshots uploaded", body = ScreenshotUploadResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse),
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

    let (report_group_id, report_id) = path.into_inner();

    // Verify report exists and belongs to report group
    let report = pool
        .get_report_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    if report.report_group_id != report_group_id {
        return Err(AppError::NotFound(format!(
            "Report {} in group {}",
            report_id, report_group_id
        )));
    }

    // Get pending screenshots for this report
    let pending_screenshots = pool.get_pending_screenshots(report_id).await?;
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
            .get_screenshot(report_id, &filename)
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
        pool.mark_screenshots_uploaded(report_id, &uploaded_filenames)
            .await?;

        // Try to link uploaded screenshots to existing test cases
        // (test cases may already exist if JSON was uploaded first)
        if let Err(e) = extraction::link_screenshots_to_test_cases(&pool, report_id).await {
            // Don't fail the upload - linking is not critical
            tracing::warn!("Failed to link screenshots for report {}: {}", report_id, e);
        }
    }

    // Get updated progress
    let (total_uploaded, total_expected) = pool.get_screenshot_upload_progress(report_id).await?;
    let all_uploaded = pool.all_screenshots_uploaded(report_id).await?;

    // Set screenshots_upload_status to "completed" when all screenshots are uploaded
    if all_uploaded {
        pool.set_screenshots_upload_status(report_id, Some("completed"))
            .await?;

        info!(
            "All screenshots uploaded: report_group_id={}, report_id={}",
            report_group_id, report_id
        );
    }

    info!(
        "Screenshots uploaded: report_group_id={}, report_id={}, this_request={}, total={}/{}",
        report_group_id, report_id, files_uploaded_this_request, total_uploaded, total_expected
    );

    let response = ScreenshotUploadResponse {
        report_id,
        files_uploaded: files_uploaded_this_request,
        total_uploaded,
        total_expected,
        all_uploaded,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Upload JSON files for a report.
///
/// Accepts multipart form data with JSON files. Only files that were declared
/// during register will be accepted. Files are uploaded to S3 and their
/// status is updated in the database.
/// When all JSON files are uploaded, extraction is automatically triggered.
#[utoipa::path(
    post,
    path = "/reports/upload/{report_id}/{upload_id}/json",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("upload_id" = Uuid, Path, description = "Upload ID (from register response)")
    ),
    responses(
        (status = 200, description = "JSON files uploaded", body = JsonUploadResponse),
        (status = 400, description = "Invalid request", body = crate::error::ErrorResponse),
        (status = 401, description = "Unauthorized", body = crate::error::ErrorResponse),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse),
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

    let (report_group_id, report_id) = path.into_inner();

    // Verify report exists and belongs to report group
    let report = pool
        .get_report_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    if report.report_group_id != report_group_id {
        return Err(AppError::NotFound(format!(
            "Report {} in group {}",
            report_id, report_group_id
        )));
    }

    // Get pending JSON files for this report
    let pending_files = pool.get_pending_json_files(report_id).await?;
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
            .get_json_file(report_id, &filename)
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
        pool.mark_json_files_uploaded(report_id, &uploaded_filenames)
            .await?;
    }

    // Get updated progress
    let (total_uploaded, total_expected) = pool.get_json_upload_progress(report_id).await?;
    let all_uploaded = pool.all_json_files_uploaded(report_id).await?;

    let mut extraction_triggered = false;

    // When all JSON files are uploaded, trigger extraction
    if all_uploaded {
        pool.set_json_upload_status(report_id, Some("completed"))
            .await?;

        // Update report status to processing
        pool.update_report_status(report_id, UploadStatus::Processing, None)
            .await?;

        // Broadcast report_entry_updated event for status change to processing
        let event = WsEventMessage::new(WsEvent::report_entry_updated(
            report_group_id,
            report_id,
            "processing".to_string(),
        ));
        broadcaster.send(event);

        // Get report group info for framework
        let report_group = pool.get_report_group_by_id(report_group_id).await?.unwrap();

        // Spawn extraction task (per-report, non-blocking)
        let pool_clone = pool.get_ref().clone();
        let storage_clone = storage.get_ref().clone();
        let broadcaster_clone = broadcaster.get_ref().clone();
        let framework = report_group.framework.clone();
        tokio::spawn(async move {
            extraction::extract_report(
                &pool_clone,
                &storage_clone,
                &broadcaster_clone,
                report_id,
                &framework,
            )
            .await;
        });

        extraction_triggered = true;

        info!(
            "All JSON files uploaded, extraction started: report_group_id={}, report_id={}",
            report_group_id, report_id
        );
    }

    info!(
        "JSON files uploaded: report_group_id={}, report_id={}, this_request={}, total={}/{}",
        report_group_id, report_id, files_uploaded_this_request, total_uploaded, total_expected
    );

    let response = JsonUploadResponse {
        report_id,
        files_uploaded: files_uploaded_this_request,
        total_uploaded,
        total_expected,
        all_uploaded,
        extraction_triggered,
    };

    Ok(HttpResponse::Ok().json(response))
}

// Routes are registered in reports.rs configure_routes
