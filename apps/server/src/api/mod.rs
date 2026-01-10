//! API endpoint modules.

pub mod health;
pub mod reports;

pub use health::configure_health_routes;
pub use reports::configure_report_routes;
