//! Domain models for Test System IO.

pub mod api_key;
pub mod github_oidc;
pub mod oidc_claim;
pub mod report;
pub mod user;
pub mod ws_event;

// Re-export commonly used types
pub use api_key::{
    ApiKey, ApiKeyCreateResponse, ApiKeyListItem, ApiKeyRole, AuthenticatedCaller,
    CreateApiKeyRequest, OIDC_ADMIN_DENIED_MSG,
};
#[allow(unused_imports)]
pub use oidc_claim::OidcClaimsResponse;
pub use report::{
    AcceptedJsonFile, AcceptedScreenshot, BeginResponse, CompleteResponse, EnvironmentMetadata,
    Framework, JsonFileToUpload, JsonUploadResponse, ListReportsQuery, RegisterReportRequest,
    RegisterReportResponse, RejectedFile, ReportDetailResponse, ReportGroupingRequest,
    ReportListResponse, ReportStatus, ReportSummary, ScreenshotToUpload, ScreenshotUploadResponse,
    TestStats, UploadStatus, UploadSummary,
};
pub use ws_event::{WsEvent, WsEventMessage};
