//! Migration: Create suites table.
//!
//! Stores normalized test suite data extracted from framework JSON.

use sea_orm_migration::prelude::*;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .get_connection()
            .execute_unprepared(
                r#"
                CREATE TABLE suites (
                    id UUID PRIMARY KEY, -- UUIDv7 for time-ordered sorting
                    upload_id UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,

                    -- Suite identification
                    title VARCHAR(500) NOT NULL,           -- Suite name/description
                    file_path VARCHAR(500),                -- Test file path (optional)

                    -- Aggregated counts (denormalized for performance)
                    total_count INTEGER NOT NULL DEFAULT 0,
                    passed_count INTEGER NOT NULL DEFAULT 0,
                    failed_count INTEGER NOT NULL DEFAULT 0,
                    skipped_count INTEGER NOT NULL DEFAULT 0,
                    flaky_count INTEGER NOT NULL DEFAULT 0,

                    -- Timing
                    duration_ms INTEGER NOT NULL DEFAULT 0,
                    start_time TIMESTAMPTZ,                -- Actual test execution start time from framework JSON

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    deleted_at TIMESTAMPTZ
                );

                -- Index for upload lookup (active only)
                CREATE INDEX idx_suites_upload_id ON suites(upload_id)
                    WHERE deleted_at IS NULL;

                -- Index for ordering by start time (actual test execution)
                CREATE INDEX idx_suites_start_time ON suites(start_time ASC NULLS LAST)
                    WHERE deleted_at IS NULL;

                -- Index for ordering by creation (fallback)
                CREATE INDEX idx_suites_created_at ON suites(created_at DESC)
                    WHERE deleted_at IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_suites_deleted_at ON suites(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_suites_updated_at
                    BEFORE UPDATE ON suites
                    FOR EACH ROW
                    EXECUTE FUNCTION update_updated_at_column();
                "#,
            )
            .await?;

        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .get_connection()
            .execute_unprepared(
                r#"
                DROP TRIGGER IF EXISTS update_suites_updated_at ON suites;
                DROP TABLE IF EXISTS suites CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
