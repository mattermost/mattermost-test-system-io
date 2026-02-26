//! Report API handlers.

use actix_web::{HttpResponse, web};
use serde::Serialize;
use tracing::info;
use utoipa::ToSchema;
use uuid::Uuid;

use crate::auth::ApiKeyAuth;
use crate::db::DbPool;
use crate::error::{AppError, AppResult};
use crate::models::{
    Framework, GitHubMetadata, JobGitHubMetadata, JobStatus, JobSummary, ListReportsQuery,
    RegisterReportRequest, RegisterReportResponse, ReportDetailResponse, ReportListResponse,
    ReportStatus, ReportSummary, WsEvent, WsEventMessage,
};
use crate::services::EventBroadcaster;

/// Response for test suite (simplified for report-level aggregation).
#[derive(Debug, Serialize, ToSchema)]
pub struct TestSuiteResponse {
    pub id: Uuid,
    /// Short ID for display (timestamp portion of UUIDv7).
    pub short_id: String,
    pub job_id: Uuid,
    /// Job display name (github_job_name or "Job N").
    pub job_name: Option<String>,
    /// Job number for display (parsed from suffix or sequential).
    pub job_number: i32,
    pub title: String,
    pub file_path: Option<String>,
    pub specs_count: i32,
    pub total_count: i32,
    pub passed_count: i32,
    pub failed_count: i32,
    pub skipped_count: i32,
    pub flaky_count: i32,
    pub duration_ms: i32,
    /// Actual test execution start time from framework JSON.
    pub start_time: Option<chrono::DateTime<chrono::Utc>>,
    /// When the suite was created in the database.
    pub created_at: chrono::DateTime<chrono::Utc>,
}

/// Job info for filtering.
#[derive(Debug, Serialize, ToSchema)]
pub struct JobInfo {
    pub job_id: Uuid,
    pub job_name: String,
    pub job_number: i32,
}

/// Response for report suites endpoint.
#[derive(Debug, Serialize, ToSchema)]
pub struct ReportSuitesResponse {
    pub suites: Vec<TestSuiteResponse>,
    /// List of jobs for filtering (only included when multiple jobs exist).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub jobs: Option<Vec<JobInfo>>,
}

/// Response for a single test result (one execution/retry).
#[derive(Debug, Serialize, ToSchema)]
pub struct TestResultResponse {
    pub id: Uuid,
    pub status: String,
    pub duration_ms: i32,
    pub retry: i32,
    pub start_time: String,
    pub project_id: String,
    pub project_name: String,
    pub errors_json: Option<String>,
    /// Attachments (screenshots, videos) for this test result.
    pub attachments: Option<serde_json::Value>,
}

/// Response for a test spec (a logical test with potentially multiple results/retries).
#[derive(Debug, Serialize, ToSchema)]
pub struct TestSpecResponse {
    pub id: Uuid,
    pub title: String,
    pub ok: bool,
    pub spec_id: String,
    pub file_path: String,
    pub line: i32,
    pub column: i32,
    pub results: Vec<TestResultResponse>,
    pub screenshots: Vec<ScreenshotInfo>,
}

/// Screenshot info for a test spec.
#[derive(Debug, Serialize, ToSchema)]
pub struct ScreenshotInfo {
    pub file_path: String,
    pub screenshot_type: String,
}

/// Response for suite specs endpoint.
#[derive(Debug, Serialize, ToSchema)]
pub struct SuiteSpecsResponse {
    pub specs: Vec<TestSpecResponse>,
}

/// Search query parameters.
#[derive(Debug, serde::Deserialize, ToSchema)]
pub struct SearchQuery {
    /// Search query string (matches test case title).
    pub q: String,
    /// Maximum number of results per suite (default: 100, max: 500).
    #[serde(default = "default_search_limit")]
    pub limit: u64,
}

fn default_search_limit() -> u64 {
    100
}

/// A matched test case within a suite.
#[derive(Debug, Serialize, ToSchema)]
pub struct SearchMatchedTestCase {
    pub test_case_id: Uuid,
    pub title: String,
    pub full_title: String,
    pub status: String,
    /// Matching tokens from the search query found in title/full_title.
    pub match_tokens: Vec<String>,
}

/// Search results grouped by suite.
#[derive(Debug, Serialize, ToSchema)]
pub struct SearchSuiteResult {
    pub suite_id: Uuid,
    pub suite_title: String,
    pub suite_file_path: Option<String>,
    pub job_id: Uuid,
    /// Test cases in this suite that match the search query.
    pub matches: Vec<SearchMatchedTestCase>,
}

/// Response for search endpoint.
#[derive(Debug, Serialize, ToSchema)]
pub struct SearchResponse {
    /// The search query.
    pub query: String,
    /// Minimum search length configured on server.
    pub search_min_length: usize,
    /// Total number of matching test cases across all suites.
    pub total_matches: usize,
    /// Results grouped by suite.
    pub results: Vec<SearchSuiteResult>,
}

/// Register a new test report.
///
/// Creates a report with expected job count and optional GitHub metadata.
#[utoipa::path(
    post,
    path = "/reports",
    tag = "Reports",
    request_body = RegisterReportRequest,
    responses(
        (status = 201, description = "Report registered successfully", body = RegisterReportResponse),
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
    // Viewers cannot upload reports
    if auth.caller.role == crate::models::ApiKeyRole::Viewer {
        return Err(AppError::Unauthorized(
            "Viewer role cannot upload reports. Contributor or admin role required.".to_string(),
        ));
    }

    let req = body.into_inner();

    // Validate expected_jobs range
    if req.expected_jobs < 1 || req.expected_jobs > 100 {
        return Err(AppError::InvalidInput(
            "expected_jobs must be between 1 and 100".to_string(),
        ));
    }

    // Keep github_metadata from request body only — OIDC claims stored separately
    let github_metadata = req.github_metadata;

    // Generate report ID (UUIDv7 for time-ordered sorting)
    let report_id = Uuid::now_v7();

    // Insert report with JSONB github_metadata (caller-supplied only)
    let report = pool
        .insert_report(report_id, req.expected_jobs, req.framework, github_metadata)
        .await?;

    // If authenticated via OIDC, store safe claims in separate table
    if let Some(ref claims) = auth.caller.oidc_claims {
        let safe = claims.to_safe_claims(auth.caller.role.as_str(), "/api/v1/reports", "POST");
        let claim_model = crate::entity::report_oidc_claim::ActiveModel {
            id: sea_orm::Set(Uuid::now_v7()),
            report_id: sea_orm::Set(report_id),
            sub: sea_orm::Set(safe.sub),
            repository: sea_orm::Set(safe.repository),
            repository_owner: sea_orm::Set(safe.repository_owner),
            actor: sea_orm::Set(safe.actor),
            sha: sea_orm::Set(safe.sha),
            git_ref: sea_orm::Set(safe.git_ref),
            ref_type: sea_orm::Set(safe.ref_type),
            workflow: sea_orm::Set(safe.workflow),
            event_name: sea_orm::Set(safe.event_name),
            run_id: sea_orm::Set(safe.run_id),
            run_number: sea_orm::Set(safe.run_number),
            run_attempt: sea_orm::Set(safe.run_attempt),
            head_ref: sea_orm::Set(safe.head_ref),
            base_ref: sea_orm::Set(safe.base_ref),
            resolved_role: sea_orm::Set(safe.resolved_role),
            api_path: sea_orm::Set(safe.api_path),
            http_method: sea_orm::Set(safe.http_method),
            created_at: sea_orm::Set(chrono::Utc::now()),
        };
        crate::db::report_oidc_claims::insert(pool.connection(), claim_model).await?;
    }

    info!(
        "Report registered: id={}, framework={}, expected_jobs={}",
        report_id,
        req.framework.as_str(),
        req.expected_jobs
    );

    // Broadcast report_created event
    let event = WsEventMessage::new(WsEvent::report_created(report_id));
    broadcaster.send(event);

    let response = RegisterReportResponse {
        report_id: report.id,
        status: ReportStatus::parse(&report.status).unwrap_or(ReportStatus::Initializing),
        expected_jobs: report.expected_jobs,
        framework: Framework::parse(&report.framework).unwrap_or(Framework::Playwright),
        created_at: report.created_at,
    };

    Ok(HttpResponse::Created().json(response))
}

/// List reports with pagination and filtering.
#[utoipa::path(
    get,
    path = "/reports",
    tag = "Reports",
    params(
        ("limit" = Option<i32>, Query, description = "Results per page (default 20, max 100)"),
        ("offset" = Option<i32>, Query, description = "Pagination offset"),
        ("framework" = Option<String>, Query, description = "Filter by framework"),
        ("status" = Option<String>, Query, description = "Filter by status")
    ),
    responses(
        (status = 200, description = "List of reports", body = ReportListResponse),
    )
)]
pub async fn list_reports(
    pool: web::Data<DbPool>,
    query: web::Query<ListReportsQuery>,
) -> AppResult<HttpResponse> {
    let query = query.into_inner();
    let (reports, total) = pool.list_reports(&query).await?;

    // Batch fetch completed job counts, test stats, and OIDC claims for all reports
    let report_ids: Vec<uuid::Uuid> = reports.iter().map(|r| r.id).collect();
    let jobs_complete_map = pool.count_completed_jobs_batch(&report_ids).await?;
    let test_stats_map = pool.get_test_stats_by_report_ids(&report_ids).await?;
    let oidc_claims_list =
        crate::db::report_oidc_claims::find_by_report_ids(pool.connection(), &report_ids).await?;
    let oidc_claims_map: std::collections::HashMap<uuid::Uuid, _> =
        oidc_claims_list.into_iter().collect();

    let reports: Vec<ReportSummary> = reports
        .into_iter()
        .map(|r| {
            let github_metadata = GitHubMetadata::from_json(r.github_metadata.as_ref());
            let github_metadata = if github_metadata.is_empty() {
                None
            } else {
                Some(github_metadata)
            };

            // Extract short ID (first 13 chars of UUID - timestamp portion)
            let short_id = r.id.to_string()[..13].to_string();

            // Get test stats, only include if there are any tests
            let test_stats = test_stats_map
                .get(&r.id)
                .cloned()
                .and_then(|stats| if stats.total > 0 { Some(stats) } else { None });

            let oidc_claims = oidc_claims_map.get(&r.id).cloned();

            ReportSummary {
                id: r.id,
                short_id,
                framework: Framework::parse(&r.framework).unwrap_or(Framework::Playwright),
                status: ReportStatus::parse(&r.status).unwrap_or(ReportStatus::Initializing),
                expected_jobs: r.expected_jobs,
                jobs_complete: *jobs_complete_map.get(&r.id).unwrap_or(&0),
                test_stats,
                github_metadata,
                oidc_claims,
                created_at: r.created_at,
            }
        })
        .collect();

    let response = ReportListResponse {
        reports,
        total: total as i64,
        limit: query.limit,
        offset: query.offset,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get test suites for a report.
#[utoipa::path(
    get,
    path = "/reports/{report_id}/suites",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID")
    ),
    responses(
        (status = 200, description = "List of test suites for the report", body = ReportSuitesResponse),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse),
    )
)]
pub async fn get_report_suites(
    pool: web::Data<DbPool>,
    path: web::Path<Uuid>,
) -> AppResult<HttpResponse> {
    let report_id = path.into_inner();

    // Verify report exists
    let _report = pool
        .get_report_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    // Get all jobs for this report and parse job numbers
    let jobs = pool.get_jobs_by_report_id(report_id).await?;

    // Helper to parse suffix number from job name (e.g., "ubuntu-chrome-5" -> Some(5))
    fn parse_suffix_number(name: &str) -> Option<i32> {
        // Try to find a trailing number after a separator (-, _, or space)
        let trimmed = name.trim();
        for sep in ['-', '_', ' '] {
            if let Some(pos) = trimmed.rfind(sep)
                && let Ok(num) = trimmed[pos + 1..].parse::<i32>()
            {
                return Some(num);
            }
        }
        // Also try parsing just the last characters as a number
        let chars: Vec<char> = trimmed.chars().collect();
        for i in (0..chars.len()).rev() {
            let suffix: String = chars[i..].iter().collect();
            if let Ok(num) = suffix.parse::<i32>() {
                return Some(num);
            }
            if !chars[i].is_ascii_digit() {
                break;
            }
        }
        None
    }

    // Build job info with parsed or sequential numbers
    let job_infos: Vec<(Uuid, String, i32)> = jobs
        .into_iter()
        .enumerate()
        .map(|(i, j)| {
            let github_metadata = JobGitHubMetadata::from_json(j.github_metadata.as_ref());
            let display_name = github_metadata
                .job_name
                .clone()
                .unwrap_or_else(|| format!("Job {}", i + 1));

            // Try to parse suffix number, otherwise use sequential (1-based)
            let job_number = parse_suffix_number(&display_name).unwrap_or((i + 1) as i32);

            (j.id, display_name, job_number)
        })
        .collect();

    // Build lookup map: job_id -> (name, number)
    let job_map: std::collections::HashMap<Uuid, (String, i32)> = job_infos
        .iter()
        .map(|(id, name, num)| (*id, (name.clone(), *num)))
        .collect();

    // Build jobs list for filtering (only if multiple jobs)
    let jobs_for_response = if job_infos.len() > 1 {
        Some(
            job_infos
                .iter()
                .map(|(id, name, num)| JobInfo {
                    job_id: *id,
                    job_name: name.clone(),
                    job_number: *num,
                })
                .collect(),
        )
    } else {
        None
    };

    // Get all test suites for this report (through jobs)
    let suites = pool.get_test_suites_by_report_id(report_id).await?;

    let suite_responses: Vec<TestSuiteResponse> = suites
        .into_iter()
        .map(|s| {
            let (job_name, job_number) = job_map
                .get(&s.test_job_id)
                .map(|(n, num)| (Some(n.clone()), *num))
                .unwrap_or((None, 1));

            TestSuiteResponse {
                id: s.id,
                short_id: s.id.to_string()[..13].to_string(),
                job_name,
                job_number,
                job_id: s.test_job_id,
                title: s.title,
                file_path: s.file_path,
                specs_count: s.total_count,
                total_count: s.total_count,
                passed_count: s.passed_count,
                failed_count: s.failed_count,
                skipped_count: s.skipped_count,
                flaky_count: s.flaky_count,
                duration_ms: s.duration_ms,
                start_time: s.start_time,
                created_at: s.created_at,
            }
        })
        .collect();

    let response = ReportSuitesResponse {
        suites: suite_responses,
        jobs: jobs_for_response,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get report details with jobs.
#[utoipa::path(
    get,
    path = "/reports/{report_id}",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID")
    ),
    responses(
        (status = 200, description = "Report details with jobs", body = ReportDetailResponse),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse),
    )
)]
pub async fn get_report(pool: web::Data<DbPool>, path: web::Path<Uuid>) -> AppResult<HttpResponse> {
    let report_id = path.into_inner();

    let report = pool
        .get_report_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    // Get jobs for this report
    let jobs = pool.get_jobs_by_report_id(report_id).await?;

    // Get root HTML filenames for all jobs
    let job_ids: Vec<_> = jobs.iter().map(|j| j.id).collect();
    let html_filenames = pool.get_root_html_filenames_for_jobs(&job_ids).await?;

    let job_summaries: Vec<JobSummary> = jobs
        .into_iter()
        .enumerate()
        .map(|(i, j)| {
            let github_metadata = JobGitHubMetadata::from_json(j.github_metadata.as_ref());
            let display_name = github_metadata
                .job_name
                .clone()
                .unwrap_or_else(|| format!("Job {}", i + 1));
            let github_metadata = if github_metadata.is_empty() {
                None
            } else {
                Some(github_metadata)
            };

            // Get the actual HTML filename from uploaded files
            let html_url = j.html_path.and_then(|p| {
                html_filenames
                    .get(&j.id)
                    .map(|filename| format!("/files/{}/{}", p, filename))
            });

            JobSummary {
                id: j.id,
                short_id: j.id.to_string()[..13].to_string(),
                github_metadata,
                display_name,
                status: JobStatus::parse(&j.status).unwrap_or(JobStatus::Pending),
                html_url,
            }
        })
        .collect();

    // Parse GitHub metadata from JSONB
    let github_metadata = GitHubMetadata::from_json(report.github_metadata.as_ref());
    let github_metadata = if github_metadata.is_empty() {
        None
    } else {
        Some(github_metadata)
    };

    // Fetch OIDC claims for this report (if uploaded via OIDC)
    let oidc_claims =
        crate::db::report_oidc_claims::find_by_report_id(pool.connection(), report_id).await?;

    let response = ReportDetailResponse {
        id: report.id,
        framework: Framework::parse(&report.framework).unwrap_or(Framework::Playwright),
        status: ReportStatus::parse(&report.status).unwrap_or(ReportStatus::Initializing),
        expected_jobs: report.expected_jobs,
        github_metadata,
        oidc_claims,
        created_at: report.created_at,
        updated_at: report.updated_at,
        jobs: job_summaries,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Path parameters for suite specs endpoint.
#[derive(serde::Deserialize)]
pub struct SuiteSpecsPath {
    pub report_id: Uuid,
    pub suite_id: Uuid,
}

/// Get test specs for a specific suite.
#[utoipa::path(
    get,
    path = "/reports/{report_id}/suites/{suite_id}/specs",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("suite_id" = Uuid, Path, description = "Suite UUID")
    ),
    responses(
        (status = 200, description = "List of test specs for the suite", body = SuiteSpecsResponse),
        (status = 404, description = "Report or suite not found", body = crate::error::ErrorResponse),
    )
)]
pub async fn get_suite_specs(
    pool: web::Data<DbPool>,
    path: web::Path<SuiteSpecsPath>,
) -> AppResult<HttpResponse> {
    let SuiteSpecsPath {
        report_id,
        suite_id,
    } = path.into_inner();

    // Verify report exists
    let _report = pool
        .get_report_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    // Get the suite to get the job_id
    let suite = pool
        .get_test_suite_by_id(suite_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Suite {}", suite_id)))?;

    // Get test cases for this suite
    let test_cases = pool.get_test_cases_by_suite_id(suite_id).await?;

    // Get screenshots for this job
    let screenshots = pool.get_screenshots_by_job_id(suite.test_job_id).await?;

    // Group test cases by full_title to combine retries into specs
    use std::collections::HashMap;
    let mut specs_map: HashMap<String, Vec<_>> = HashMap::new();
    for tc in test_cases {
        specs_map.entry(tc.full_title.clone()).or_default().push(tc);
    }

    // Convert to spec responses
    let mut specs: Vec<TestSpecResponse> = specs_map
        .into_iter()
        .map(|(full_title, cases)| {
            // Sort by retry_count to get correct order
            let mut cases = cases;
            cases.sort_by_key(|c| c.retry_count);

            // Use first case for spec-level info
            let first = &cases[0];

            // Determine if spec passed (last result is passed/flaky or all passed)
            // "flaky" status means test passed after retries, so it's also considered passed
            let ok = cases
                .iter()
                .any(|c| c.status == "passed" || c.status == "flaky");

            // Create results from all cases (retries)
            let results: Vec<TestResultResponse> = cases
                .iter()
                .map(|c| TestResultResponse {
                    id: c.id,
                    status: c.status.clone(),
                    duration_ms: c.duration_ms,
                    retry: c.retry_count,
                    start_time: c.created_at.to_rfc3339(),
                    project_id: "default".to_string(),
                    project_name: "default".to_string(),
                    errors_json: c.error_message.as_ref().map(|msg| {
                        // Wrap error message in JSON array format expected by frontend
                        serde_json::to_string(&vec![msg]).unwrap_or_else(|_| "[]".to_string())
                    }),
                    attachments: c.attachments.clone(),
                })
                .collect();

            // Get screenshots linked to test cases in this spec
            // First try to get by test_case_id (preferred), then fallback to name matching
            let case_ids: Vec<_> = cases.iter().map(|c| c.id).collect();
            let mut spec_screenshots: Vec<ScreenshotInfo> = screenshots
                .iter()
                .filter(|s| {
                    // Primary: match by test_case_id (linked during extraction)
                    if let Some(tc_id) = s.test_case_id
                        && case_ids.contains(&tc_id)
                    {
                        return true;
                    }
                    // Fallback: match by test_name for old/unlinked data
                    let normalized_test_name = s.test_name.replace('/', " > ");
                    full_title == s.test_name
                        || full_title == normalized_test_name
                        || full_title.starts_with(&format!("{} [", s.test_name))
                        || full_title.starts_with(&format!("{} [", normalized_test_name))
                })
                .map(|s| {
                    // Extract screenshot type from filename (e.g., "testStart.png" -> "testStart")
                    let screenshot_type = s
                        .filename
                        .strip_suffix(".png")
                        .or_else(|| s.filename.strip_suffix(".jpg"))
                        .or_else(|| s.filename.strip_suffix(".jpeg"))
                        .unwrap_or(&s.filename)
                        .to_string();
                    ScreenshotInfo {
                        file_path: s.s3_key.clone(),
                        screenshot_type,
                    }
                })
                .collect();

            // Sort screenshots by Detox order: testStart, testFnFailure, testDone
            spec_screenshots.sort_by_key(|s| {
                match s.screenshot_type.as_str() {
                    "testStart" => 0,
                    "testFnFailure" => 1,
                    "testDone" => 2,
                    _ => 3, // Other screenshot types come last
                }
            });

            TestSpecResponse {
                id: first.id,
                title: first.title.clone(),
                ok,
                spec_id: first.id.to_string(),
                file_path: full_title.clone(),
                line: 0,
                column: 0,
                results,
                screenshots: spec_screenshots,
            }
        })
        .collect();

    // Sort specs by their first case's sequence
    specs.sort_by_key(|s| s.results.first().map(|r| r.retry).unwrap_or(0));

    let response = SuiteSpecsResponse { specs };

    Ok(HttpResponse::Ok().json(response))
}

/// Search test cases within a report.
///
/// Searches for test cases matching the query string in their title/full_title.
/// Results are grouped by suite for easier UI rendering.
#[utoipa::path(
    get,
    path = "/reports/{report_id}/search",
    tag = "Reports",
    params(
        ("report_id" = Uuid, Path, description = "Report UUID"),
        ("q" = String, Query, description = "Search query string"),
        ("limit" = Option<u64>, Query, description = "Maximum results (default: 100, max: 500)")
    ),
    responses(
        (status = 200, description = "Search results grouped by suite", body = SearchResponse),
        (status = 400, description = "Query too short", body = crate::error::ErrorResponse),
        (status = 404, description = "Report not found", body = crate::error::ErrorResponse),
    )
)]
pub async fn search_test_cases(
    pool: web::Data<DbPool>,
    config: web::Data<crate::config::Config>,
    path: web::Path<Uuid>,
    query: web::Query<SearchQuery>,
) -> AppResult<HttpResponse> {
    let report_id = path.into_inner();
    let search_query = query.into_inner();
    let search_min_length = config.features.search_min_length;

    // Validate query length
    let q = search_query.q.trim();
    if q.is_empty() || q.len() < search_min_length {
        return Ok(HttpResponse::Ok().json(SearchResponse {
            query: search_query.q,
            search_min_length,
            total_matches: 0,
            results: vec![],
        }));
    }

    // Verify report exists
    let _report = pool
        .get_report_by_id(report_id)
        .await?
        .ok_or_else(|| AppError::NotFound(format!("Report {}", report_id)))?;

    // Limit to max 500 results
    let limit = search_query.limit.min(500);

    // Search for test cases
    let results = pool
        .search_test_cases_by_report(report_id, q, limit)
        .await?;

    // Group results by suite and extract match tokens
    let mut suite_map: std::collections::HashMap<Uuid, SearchSuiteResult> =
        std::collections::HashMap::new();

    let query_lower = q.to_lowercase();

    for (tc, suite) in results {
        // Extract match tokens from title and full_title
        let match_tokens = extract_match_tokens(&tc.title, &tc.full_title, &query_lower);

        let matched_case = SearchMatchedTestCase {
            test_case_id: tc.id,
            title: tc.title,
            full_title: tc.full_title,
            status: tc.status,
            match_tokens,
        };

        suite_map
            .entry(suite.id)
            .or_insert_with(|| SearchSuiteResult {
                suite_id: suite.id,
                suite_title: suite.title.clone(),
                suite_file_path: suite.file_path.clone(),
                job_id: tc.test_job_id,
                matches: vec![],
            })
            .matches
            .push(matched_case);
    }

    // Convert to vec and count total matches
    let results: Vec<SearchSuiteResult> = suite_map.into_values().collect();
    let total_matches: usize = results.iter().map(|s| s.matches.len()).sum();

    let response = SearchResponse {
        query: search_query.q,
        search_min_length,
        total_matches,
        results,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Extract matching tokens from title and full_title.
/// Returns tokens (words/substrings) that contain the query.
fn extract_match_tokens(title: &str, full_title: &str, query_lower: &str) -> Vec<String> {
    let mut tokens = Vec::new();

    // Check title for matches
    let title_lower = title.to_lowercase();
    if let Some(start) = title_lower.find(query_lower) {
        // Extract the matching portion with some context (the word containing it)
        let token = extract_word_containing_match(title, start, query_lower.len());
        if !tokens.contains(&token) {
            tokens.push(token);
        }
    }

    // Check full_title for additional matches not in title
    let full_title_lower = full_title.to_lowercase();
    if let Some(start) = full_title_lower.find(query_lower) {
        let token = extract_word_containing_match(full_title, start, query_lower.len());
        if !tokens.contains(&token) {
            tokens.push(token);
        }
    }

    tokens
}

/// Extract the word/token containing the match at the given position.
fn extract_word_containing_match(text: &str, match_start: usize, match_len: usize) -> String {
    let chars: Vec<char> = text.chars().collect();
    let char_start = text[..match_start].chars().count();
    let char_end = char_start + text[match_start..match_start + match_len].chars().count();

    // Find word boundaries (spaces, punctuation)
    let word_chars: &[char] = &[' ', '\t', '\n', '>', '<', '|', '[', ']', '(', ')', '{', '}'];

    let mut word_start = char_start;
    while word_start > 0 && !word_chars.contains(&chars[word_start - 1]) {
        word_start -= 1;
    }

    let mut word_end = char_end;
    while word_end < chars.len() && !word_chars.contains(&chars[word_end]) {
        word_end += 1;
    }

    chars[word_start..word_end].iter().collect()
}

/// Configure report routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(
        web::resource("/reports")
            .route(web::get().to(list_reports))
            .route(web::post().to(register_report)),
    )
    .service(web::resource("/reports/{report_id}").route(web::get().to(get_report)))
    .service(web::resource("/reports/{report_id}/suites").route(web::get().to(get_report_suites)))
    .service(
        web::resource("/reports/{report_id}/suites/{suite_id}/specs")
            .route(web::get().to(get_suite_specs)),
    )
    .service(web::resource("/reports/{report_id}/search").route(web::get().to(search_test_cases)));
}
