//! API endpoint modules.

pub mod file_validation;
pub mod files;
pub mod health;
pub mod openapi;
pub mod register;
pub mod reports;
pub mod websocket;

pub use files::configure_routes as configure_file_routes;
pub use health::configure_health_routes;
pub use openapi::ApiDoc;
pub use reports::configure_routes as configure_report_routes;
pub use websocket::configure_routes as configure_websocket_routes;
