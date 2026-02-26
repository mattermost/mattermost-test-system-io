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
mod m20260129_000009_create_github_oidc_policies;
mod m20260129_000010_create_users;
mod m20260129_000011_create_refresh_tokens;
mod m20260226_000012_create_report_oidc_claims;

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
            Box::new(m20260129_000009_create_github_oidc_policies::Migration),
            Box::new(m20260129_000010_create_users::Migration),
            Box::new(m20260129_000011_create_refresh_tokens::Migration),
            Box::new(m20260226_000012_create_report_oidc_claims::Migration),
        ]
    }
}
