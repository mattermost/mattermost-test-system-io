//! OpenAPI documentation configuration.

use utoipa::OpenApi;

use crate::{api, error, models, services};

/// OpenAPI documentation.
#[derive(OpenApi)]
#[openapi(
    info(
        title = "Rust Report Server",
        version = "0.1.0",
        description = "API server for uploading and viewing test reports (Playwright, Cypress, Detox)"
    ),
    servers(
        (url = "/", description = "Local server")
    ),
    paths(
        // Health endpoints
        api::health::health,
        api::health::ready,
        // Report endpoints
        api::reports::list_reports,
        api::reports::get_report,
        api::reports::get_report_html,
        api::reports::get_report_assets,
        api::reports::get_report_screenshots,
        api::reports::get_report_data_file,
        api::reports::get_report_suites,
        api::reports::get_suite_specs,
        // Detox endpoints
        api::detox::get_report_detox_jobs,
        api::detox::get_report_detox_tests,
        api::detox::get_detox_job,
        api::detox::get_detox_job_html,
        api::detox::get_detox_test_screenshots,
        // Upload endpoints
        services::upload::playwright::request_playwright_upload,
        services::upload::cypress::request_cypress_upload,
        services::upload::detox::request_detox_upload,
        services::upload::upload_report_files,
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
            models::Pagination,
            models::PaginationParams,
            models::GitHubContext,
            // Health
            api::health::HealthResponse,
            api::health::ReadyResponse,
            // Reports
            models::ReportSummary,
            models::ReportDetail,
            models::ReportStats,
            models::ExtractionStatus,
            models::DetoxPlatform,
            api::reports::ReportListResponse,
            // Test suites/specs
            models::TestSuite,
            models::TestSuiteListResponse,
            models::TestSpecWithResults,
            models::TestSpecListResponse,
            models::ScreenshotInfo,
            models::TestResult,
            models::TestStatus,
            // Detox
            api::detox::DetoxJobSummaryResponse,
            api::detox::DetoxJobListResponse,
            api::detox::DetoxJobDetailResponse,
            api::detox::DetoxCombinedTestResult,
            api::detox::DetoxCombinedTestsResponse,
            api::detox::CombinedTestsQueryParams,
            api::detox::DetoxScreenshotResponse,
            api::detox::DetoxScreenshotsListResponse,
            // Upload
            services::upload::UploadRequest,
            services::upload::UploadRequestResponse,
            services::upload::UploadFilesResponse,
            services::upload::FileError,
            // Auth
            models::ApiKeyRole,
            models::ApiKeyCreateResponse,
            models::ApiKeyListItem,
            models::CreateApiKeyRequest,
            services::auth_admin::ListApiKeysResponse,
            services::auth_admin::RevokeApiKeyResponse,
            services::auth_admin::RestoreApiKeyResponse,
        )
    ),
    tags(
        (name = "Health", description = "Health check endpoints"),
        (name = "Reports", description = "Test report management"),
        (name = "Detox", description = "Detox-specific report endpoints"),
        (name = "Upload", description = "Report upload endpoints"),
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
