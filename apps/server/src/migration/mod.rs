//! SeaORM database migrations.

pub use sea_orm_migration::prelude::*;

mod m20250115_000001_create_reports;
mod m20250115_000002_create_report_stats;
mod m20250115_000003_create_test_suites;
mod m20250115_000004_create_test_specs;
mod m20250115_000005_create_test_results;
mod m20250115_000006_create_detox_jobs;
mod m20250115_000007_create_detox_screenshots;
mod m20250115_000008_create_api_keys;
mod m20250115_000009_create_upload_files;
mod m20250115_000010_create_server_metadata;
mod m20250115_000011_create_report_json;

pub struct Migrator;

#[async_trait::async_trait]
impl MigratorTrait for Migrator {
    fn migrations() -> Vec<Box<dyn MigrationTrait>> {
        vec![
            Box::new(m20250115_000001_create_reports::Migration),
            Box::new(m20250115_000002_create_report_stats::Migration),
            Box::new(m20250115_000003_create_test_suites::Migration),
            Box::new(m20250115_000004_create_test_specs::Migration),
            Box::new(m20250115_000005_create_test_results::Migration),
            Box::new(m20250115_000006_create_detox_jobs::Migration),
            Box::new(m20250115_000007_create_detox_screenshots::Migration),
            Box::new(m20250115_000008_create_api_keys::Migration),
            Box::new(m20250115_000009_create_upload_files::Migration),
            Box::new(m20250115_000010_create_server_metadata::Migration),
            Box::new(m20250115_000011_create_report_json::Migration),
        ]
    }
}
