//! OpenAPI documentation configuration.

use utoipa::OpenApi;

use crate::{api, error, models, services};

/// OpenAPI documentation.
#[derive(OpenApi)]
#[openapi(
    info(
        title = "Test System IO Server",
        version = "0.1.0",
        description = "API server for uploading and viewing test reports (Playwright, Cypress, Detox) with job-based artifact upload"
    ),
    servers(
        (url = "/", description = "Local server")
    ),
    paths(
        // Health endpoints
        api::health::health,
        api::health::ready,
        api::health::info,
        // Report endpoints
        api::test_reports::register_report,
        api::test_reports::list_reports,
        api::test_reports::get_report,
        // Job endpoints
        api::test_jobs::init_job,
        api::test_jobs::init_html,
        api::test_jobs::upload_html,
        api::test_jobs::get_html_progress,
        api::test_jobs::init_screenshots,
        api::test_jobs::upload_screenshots,
        api::test_jobs::init_json,
        api::test_jobs::upload_json,
        api::test_jobs::get_json_progress,
        api::test_jobs::query_jobs,
        api::test_jobs::get_job,
        // Test results endpoints
        api::test_results::query_test_suites,
        api::test_results::query_test_cases,
        api::test_results::get_job_test_suites,
        api::test_results::get_job_test_cases,
        api::test_results::get_suite_test_cases,
        // Auth endpoints
        services::auth_admin::create_api_key,
        services::auth_admin::list_api_keys,
        services::auth_admin::get_api_key,
        services::auth_admin::revoke_api_key,
        services::auth_admin::restore_api_key,
    ),
    components(
        schemas(
            // Common
            error::ErrorResponse,
            // Health
            api::health::HealthResponse,
            api::health::ReadyResponse,
            api::health::ServerInfoResponse,
            // Reports
            models::Framework,
            models::ReportStatus,
            models::GitHubMetadata,
            models::RegisterReportRequest,
            models::RegisterReportResponse,
            models::ReportSummary,
            models::ReportListResponse,
            models::ReportDetailResponse,
            models::ListReportsQuery,
            // Jobs
            models::JobStatus,
            models::UploadStatus,
            models::JobGitHubMetadata,
            models::EnvironmentMetadata,
            models::InitJobRequest,
            models::InitJobResponse,
            models::HtmlFileToUpload,
            models::InitHtmlRequest,
            models::InitHtmlResponse,
            models::AcceptedHtmlFile,
            models::HtmlUploadResponse,
            models::HtmlUploadProgress,
            models::RejectedFile,
            models::JobStatusResponse,
            models::JobSummary,
            models::JobDetailResponse,
            models::JobListResponse,
            models::QueryJobsParams,
            models::ScreenshotToUpload,
            models::InitScreenshotsRequest,
            models::InitScreenshotsResponse,
            models::AcceptedScreenshot,
            models::ScreenshotUploadResponse,
            models::JsonFileToUpload,
            models::InitJsonRequest,
            models::InitJsonResponse,
            models::AcceptedJsonFile,
            models::JsonUploadResponse,
            models::JsonUploadProgress,
            // Auth
            models::ApiKeyRole,
            models::ApiKeyCreateResponse,
            models::ApiKeyListItem,
            models::CreateApiKeyRequest,
            services::auth_admin::ListApiKeysResponse,
            services::auth_admin::RevokeApiKeyResponse,
            services::auth_admin::RestoreApiKeyResponse,
            // Test Results
            api::test_results::TestSuiteResponse,
            api::test_results::TestCaseResponse,
            api::test_results::TestSuitesListResponse,
            api::test_results::TestCasesListResponse,
        )
    ),
    tags(
        (name = "Health", description = "Health check endpoints"),
        (name = "Reports", description = "Report registration and management"),
        (name = "Jobs", description = "Job initialization and uploads"),
        (name = "Test Results", description = "Query test suites and test cases"),
        (name = "Auth", description = "API key management")
    ),
    modifiers(&SecurityAddon)
)]
pub struct ApiDoc;

/// Add API key security scheme.
struct SecurityAddon;

impl utoipa::Modify for SecurityAddon {
    fn modify(&self, openapi: &mut utoipa::openapi::OpenApi) {
        if let Some(components) = openapi.components.as_mut() {
            components.add_security_scheme(
                "api_key",
                utoipa::openapi::security::SecurityScheme::ApiKey(
                    utoipa::openapi::security::ApiKey::Header(
                        utoipa::openapi::security::ApiKeyValue::new("X-API-Key"),
                    ),
                ),
            );
        }
    }
}
