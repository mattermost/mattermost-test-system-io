//! Domain models for Test System IO.

pub mod api_key;
pub mod github_oidc;
pub mod job;
pub mod report;
pub mod report_oidc_claim;
pub mod user;
pub mod ws_event;

// Re-export commonly used types
pub use api_key::{
    ApiKey, ApiKeyCreateResponse, ApiKeyListItem, ApiKeyRole, AuthenticatedCaller,
    CreateApiKeyRequest, OIDC_ADMIN_DENIED_MSG,
};
pub use job::{
    AcceptedHtmlFile, AcceptedJsonFile, AcceptedScreenshot, EnvironmentMetadata, HtmlFileToUpload,
    HtmlUploadProgress, HtmlUploadResponse, InitHtmlRequest, InitHtmlResponse, InitJobRequest,
    InitJobResponse, InitJsonRequest, InitJsonResponse, InitScreenshotsRequest,
    InitScreenshotsResponse, JobDetailResponse, JobGitHubMetadata, JobListResponse, JobStatus,
    JobStatusResponse, JobSummary, JsonFileToUpload, JsonUploadProgress, JsonUploadResponse,
    QueryJobsParams, RejectedFile, ScreenshotToUpload, ScreenshotUploadResponse, UploadStatus,
};
pub use report::{
    Framework, GitHubMetadata, ListReportsQuery, RegisterReportRequest, RegisterReportResponse,
    ReportDetailResponse, ReportListResponse, ReportStatus, ReportSummary, TestStats,
};
pub use ws_event::{WsEvent, WsEventMessage};
