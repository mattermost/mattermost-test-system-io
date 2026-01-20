//! API endpoint modules.

pub mod files;
pub mod health;
pub mod openapi;
pub mod test_jobs;
pub mod test_reports;
pub mod test_results;

pub use files::configure_routes as configure_file_routes;
pub use health::configure_health_routes;
pub use openapi::ApiDoc;
pub use test_jobs::configure_routes as configure_job_routes;
pub use test_reports::configure_routes as configure_report_routes;
pub use test_results::configure_routes as configure_test_results_routes;
