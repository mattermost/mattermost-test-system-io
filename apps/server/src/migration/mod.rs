//! SeaORM database migrations.
//!
//! All tables follow these conventions:
//! - UUIDv7 primary keys for time-ordered sorting
//! - created_at, updated_at, deleted_at for auditing and soft-delete
//! - Partial indexes with WHERE deleted_at IS NULL for query performance
//! - Cascading deletes for child tables

pub use sea_orm_migration::prelude::*;

mod m20260118_000001_create_api_keys;
mod m20260118_000002_create_reports;
mod m20260118_000003_create_jobs;
mod m20260118_000004_create_html_files;
mod m20260118_000005_create_test_suites;
mod m20260118_000006_create_test_cases;
mod m20260118_000007_create_screenshots;
mod m20260118_000008_create_json_files;

pub struct Migrator;

#[async_trait::async_trait]
impl MigratorTrait for Migrator {
    fn migrations() -> Vec<Box<dyn MigrationTrait>> {
        vec![
            Box::new(m20260118_000001_create_api_keys::Migration),
            Box::new(m20260118_000002_create_reports::Migration),
            Box::new(m20260118_000003_create_jobs::Migration),
            Box::new(m20260118_000004_create_html_files::Migration),
            Box::new(m20260118_000005_create_test_suites::Migration),
            Box::new(m20260118_000006_create_test_cases::Migration),
            Box::new(m20260118_000007_create_screenshots::Migration),
            Box::new(m20260118_000008_create_json_files::Migration),
        ]
    }
}
