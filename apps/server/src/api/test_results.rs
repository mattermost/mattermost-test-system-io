//! Test results API handlers for querying test suites and test cases.

use actix_web::{HttpResponse, web};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use utoipa::ToSchema;
use uuid::Uuid;

use crate::db::test_results::{QueryTestCasesParams, QueryTestSuitesParams};
use crate::db::DbPool;
use crate::error::AppResult;

/// Extract short ID from UUIDv7 (timestamp portion: first 13 chars).
/// Example: "019bcad1-9368-7abc-9def-123456789abc" -> "019bcad1-9368"
pub fn short_id(uuid: &Uuid) -> String {
    let s = uuid.to_string();
    s[..13].to_string()
}

/// Response for test suite.
#[derive(Debug, Serialize, ToSchema)]
pub struct TestSuiteResponse {
    pub id: Uuid,
    /// Short ID for display (timestamp portion of UUIDv7).
    pub short_id: String,
    pub job_id: Uuid,
    pub title: String,
    pub file_path: Option<String>,
    pub total_count: i32,
    pub passed_count: i32,
    pub failed_count: i32,
    pub skipped_count: i32,
    pub flaky_count: i32,
    pub duration_ms: i32,
    pub created_at: DateTime<Utc>,
}

/// Response for test case.
#[derive(Debug, Serialize, ToSchema)]
pub struct TestCaseResponse {
    pub id: Uuid,
    /// Short ID for display (timestamp portion of UUIDv7).
    pub short_id: String,
    pub suite_id: Uuid,
    pub job_id: Uuid,
    pub title: String,
    pub full_title: String,
    pub status: String,
    pub duration_ms: i32,
    pub retry_count: i32,
    pub error_message: Option<String>,
    pub sequence: i32,
    pub created_at: DateTime<Utc>,
}

/// Paginated test suites response.
#[derive(Debug, Serialize, ToSchema)]
pub struct TestSuitesListResponse {
    pub suites: Vec<TestSuiteResponse>,
    pub total: i64,
    pub limit: i64,
    pub offset: i64,
}

/// Paginated test cases response.
#[derive(Debug, Serialize, ToSchema)]
pub struct TestCasesListResponse {
    pub test_cases: Vec<TestCaseResponse>,
    pub total: i64,
    pub limit: i64,
    pub offset: i64,
}

/// Query parameters for test suites endpoint.
#[derive(Debug, Deserialize, ToSchema)]
pub struct QueryTestSuitesQuery {
    /// Filter by job ID.
    pub job_id: Option<Uuid>,
    /// Results per page (default 20, max 100).
    pub limit: Option<i64>,
    /// Pagination offset.
    pub offset: Option<i64>,
}

/// Query parameters for test cases endpoint.
#[derive(Debug, Deserialize, ToSchema)]
pub struct QueryTestCasesQuery {
    /// Filter by job ID.
    pub job_id: Option<Uuid>,
    /// Filter by suite ID.
    pub suite_id: Option<Uuid>,
    /// Filter by status (passed, failed, skipped, flaky, timedOut).
    pub status: Option<String>,
    /// Results per page (default 20, max 100).
    pub limit: Option<i64>,
    /// Pagination offset.
    pub offset: Option<i64>,
}

/// Query test suites with filtering and pagination.
#[utoipa::path(
    get,
    path = "/test-suites",
    tag = "Test Results",
    params(
        ("job_id" = Option<Uuid>, Query, description = "Filter by job ID"),
        ("limit" = Option<i64>, Query, description = "Results per page (default 20, max 100)"),
        ("offset" = Option<i64>, Query, description = "Pagination offset")
    ),
    responses(
        (status = 200, description = "List of test suites", body = TestSuitesListResponse),
    )
)]
pub async fn query_test_suites(
    pool: web::Data<DbPool>,
    query: web::Query<QueryTestSuitesQuery>,
) -> AppResult<HttpResponse> {
    let params = QueryTestSuitesParams {
        job_id: query.job_id,
        limit: query.limit.unwrap_or(20),
        offset: query.offset.unwrap_or(0),
    };

    let (suites, total) = pool.query_test_suites(&params).await?;

    let suites_response: Vec<TestSuiteResponse> = suites
        .into_iter()
        .map(|s| TestSuiteResponse {
            short_id: short_id(&s.id),
            id: s.id,
            job_id: s.test_job_id,
            title: s.title,
            file_path: s.file_path,
            total_count: s.total_count,
            passed_count: s.passed_count,
            failed_count: s.failed_count,
            skipped_count: s.skipped_count,
            flaky_count: s.flaky_count,
            duration_ms: s.duration_ms,
            created_at: s.created_at,
        })
        .collect();

    let response = TestSuitesListResponse {
        suites: suites_response,
        total: total as i64,
        limit: params.limit,
        offset: params.offset,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Query test cases with filtering and pagination.
#[utoipa::path(
    get,
    path = "/test-cases",
    tag = "Test Results",
    params(
        ("job_id" = Option<Uuid>, Query, description = "Filter by job ID"),
        ("suite_id" = Option<Uuid>, Query, description = "Filter by suite ID"),
        ("status" = Option<String>, Query, description = "Filter by status"),
        ("limit" = Option<i64>, Query, description = "Results per page (default 20, max 100)"),
        ("offset" = Option<i64>, Query, description = "Pagination offset")
    ),
    responses(
        (status = 200, description = "List of test cases", body = TestCasesListResponse),
    )
)]
pub async fn query_test_cases(
    pool: web::Data<DbPool>,
    query: web::Query<QueryTestCasesQuery>,
) -> AppResult<HttpResponse> {
    let params = QueryTestCasesParams {
        job_id: query.job_id,
        suite_id: query.suite_id,
        status: query.status.clone(),
        limit: query.limit.unwrap_or(20),
        offset: query.offset.unwrap_or(0),
    };

    let (cases, total) = pool.query_test_cases(&params).await?;

    let cases_response: Vec<TestCaseResponse> = cases
        .into_iter()
        .map(|c| TestCaseResponse {
            short_id: short_id(&c.id),
            id: c.id,
            suite_id: c.test_suite_id,
            job_id: c.test_job_id,
            title: c.title,
            full_title: c.full_title,
            status: c.status,
            duration_ms: c.duration_ms,
            retry_count: c.retry_count,
            error_message: c.error_message,
            sequence: c.sequence,
            created_at: c.created_at,
        })
        .collect();

    let response = TestCasesListResponse {
        test_cases: cases_response,
        total: total as i64,
        limit: params.limit,
        offset: params.offset,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get test suites for a specific job.
#[utoipa::path(
    get,
    path = "/jobs/{job_id}/test-suites",
    tag = "Test Results",
    params(
        ("job_id" = Uuid, Path, description = "Job UUID"),
        ("limit" = Option<i64>, Query, description = "Results per page (default 20, max 100)"),
        ("offset" = Option<i64>, Query, description = "Pagination offset")
    ),
    responses(
        (status = 200, description = "List of test suites for the job", body = TestSuitesListResponse),
    )
)]
pub async fn get_job_test_suites(
    pool: web::Data<DbPool>,
    path: web::Path<Uuid>,
    query: web::Query<QueryTestSuitesQuery>,
) -> AppResult<HttpResponse> {
    let job_id = path.into_inner();

    let params = QueryTestSuitesParams {
        job_id: Some(job_id),
        limit: query.limit.unwrap_or(20),
        offset: query.offset.unwrap_or(0),
    };

    let (suites, total) = pool.query_test_suites(&params).await?;

    let suites_response: Vec<TestSuiteResponse> = suites
        .into_iter()
        .map(|s| TestSuiteResponse {
            short_id: short_id(&s.id),
            id: s.id,
            job_id: s.test_job_id,
            title: s.title,
            file_path: s.file_path,
            total_count: s.total_count,
            passed_count: s.passed_count,
            failed_count: s.failed_count,
            skipped_count: s.skipped_count,
            flaky_count: s.flaky_count,
            duration_ms: s.duration_ms,
            created_at: s.created_at,
        })
        .collect();

    let response = TestSuitesListResponse {
        suites: suites_response,
        total: total as i64,
        limit: params.limit,
        offset: params.offset,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get test cases for a specific job.
#[utoipa::path(
    get,
    path = "/jobs/{job_id}/test-cases",
    tag = "Test Results",
    params(
        ("job_id" = Uuid, Path, description = "Job UUID"),
        ("status" = Option<String>, Query, description = "Filter by status"),
        ("limit" = Option<i64>, Query, description = "Results per page (default 20, max 100)"),
        ("offset" = Option<i64>, Query, description = "Pagination offset")
    ),
    responses(
        (status = 200, description = "List of test cases for the job", body = TestCasesListResponse),
    )
)]
pub async fn get_job_test_cases(
    pool: web::Data<DbPool>,
    path: web::Path<Uuid>,
    query: web::Query<QueryTestCasesQuery>,
) -> AppResult<HttpResponse> {
    let job_id = path.into_inner();

    let params = QueryTestCasesParams {
        job_id: Some(job_id),
        suite_id: query.suite_id,
        status: query.status.clone(),
        limit: query.limit.unwrap_or(20),
        offset: query.offset.unwrap_or(0),
    };

    let (cases, total) = pool.query_test_cases(&params).await?;

    let cases_response: Vec<TestCaseResponse> = cases
        .into_iter()
        .map(|c| TestCaseResponse {
            short_id: short_id(&c.id),
            id: c.id,
            suite_id: c.test_suite_id,
            job_id: c.test_job_id,
            title: c.title,
            full_title: c.full_title,
            status: c.status,
            duration_ms: c.duration_ms,
            retry_count: c.retry_count,
            error_message: c.error_message,
            sequence: c.sequence,
            created_at: c.created_at,
        })
        .collect();

    let response = TestCasesListResponse {
        test_cases: cases_response,
        total: total as i64,
        limit: params.limit,
        offset: params.offset,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Get test cases for a specific suite.
#[utoipa::path(
    get,
    path = "/test-suites/{suite_id}/test-cases",
    tag = "Test Results",
    params(
        ("suite_id" = Uuid, Path, description = "Suite UUID"),
        ("status" = Option<String>, Query, description = "Filter by status"),
        ("limit" = Option<i64>, Query, description = "Results per page (default 20, max 100)"),
        ("offset" = Option<i64>, Query, description = "Pagination offset")
    ),
    responses(
        (status = 200, description = "List of test cases for the suite", body = TestCasesListResponse),
    )
)]
pub async fn get_suite_test_cases(
    pool: web::Data<DbPool>,
    path: web::Path<Uuid>,
    query: web::Query<QueryTestCasesQuery>,
) -> AppResult<HttpResponse> {
    let suite_id = path.into_inner();

    let params = QueryTestCasesParams {
        job_id: None,
        suite_id: Some(suite_id),
        status: query.status.clone(),
        limit: query.limit.unwrap_or(20),
        offset: query.offset.unwrap_or(0),
    };

    let (cases, total) = pool.query_test_cases(&params).await?;

    let cases_response: Vec<TestCaseResponse> = cases
        .into_iter()
        .map(|c| TestCaseResponse {
            short_id: short_id(&c.id),
            id: c.id,
            suite_id: c.test_suite_id,
            job_id: c.test_job_id,
            title: c.title,
            full_title: c.full_title,
            status: c.status,
            duration_ms: c.duration_ms,
            retry_count: c.retry_count,
            error_message: c.error_message,
            sequence: c.sequence,
            created_at: c.created_at,
        })
        .collect();

    let response = TestCasesListResponse {
        test_cases: cases_response,
        total: total as i64,
        limit: params.limit,
        offset: params.offset,
    };

    Ok(HttpResponse::Ok().json(response))
}

/// Configure test results routes.
pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.service(web::resource("/test-suites").route(web::get().to(query_test_suites)))
        .service(web::resource("/test-cases").route(web::get().to(query_test_cases)))
        .service(
            web::resource("/jobs/{job_id}/test-suites").route(web::get().to(get_job_test_suites)),
        )
        .service(
            web::resource("/jobs/{job_id}/test-cases").route(web::get().to(get_job_test_cases)),
        )
        .service(
            web::resource("/test-suites/{suite_id}/test-cases")
                .route(web::get().to(get_suite_test_cases)),
        );
}
