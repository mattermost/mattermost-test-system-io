//! API endpoint modules.

pub mod detox;
pub mod health;
pub mod openapi;
pub mod reports;

pub use detox::configure_detox_routes;
pub use health::configure_health_routes;
pub use openapi::ApiDoc;
pub use reports::configure_report_routes;
