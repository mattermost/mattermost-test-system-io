//! OpenAPI documentation configuration.

use utoipa::OpenApi;

use crate::{api, error, models, services};

/// OpenAPI documentation.
#[derive(OpenApi)]
#[openapi(
    info(
        title = "Mattermost Test System IO",
        version = "0.2.0",
        description = "API server for uploading and viewing test reports (Playwright, Cypress, Detox)"
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
        api::reports::get_report,
        api::reports::begin,
        api::reports::complete,
        // Report upload endpoints
        api::register::register_report,
        api::register::upload_screenshots,
        api::register::upload_json,
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
            models::ReportSummary,
            models::ReportDetailResponse,
            // Uploads
            models::UploadStatus,
            models::EnvironmentMetadata,
            models::RegisterReportRequest,
            models::RegisterReportResponse,
            models::RejectedFile,
            models::UploadSummary,
            models::ScreenshotToUpload,
            models::AcceptedScreenshot,
            models::ScreenshotUploadResponse,
            models::JsonFileToUpload,
            models::AcceptedJsonFile,
            models::JsonUploadResponse,
            models::ReportGroupingRequest,
            models::BeginResponse,
            models::CompleteResponse,
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
        (name = "Reports", description = "Report management and uploads"),
        (name = "Auth", description = "API key management")
    ),
    modifiers(&SecurityAddon, &VersionFromCargo)
)]
pub struct ApiDoc;

/// Override the OpenAPI version with the value from Cargo.toml at runtime.
struct VersionFromCargo;

impl utoipa::Modify for VersionFromCargo {
    fn modify(&self, openapi: &mut utoipa::openapi::OpenApi) {
        openapi.info.version = env!("CARGO_PKG_VERSION").to_string();
    }
}

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
