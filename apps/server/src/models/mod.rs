//! Domain models for the Rust Report Viewer.

pub mod api_key;
pub mod job;
pub mod report;

// Re-export commonly used types
pub use api_key::{
    ApiKey, ApiKeyCreateResponse, ApiKeyListItem, ApiKeyRole, AuthenticatedCaller,
    CreateApiKeyRequest,
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
