//! Migration: Create reports table.
//!
//! Reports represent individual test uploads within a report group (e.g., parallel shards).

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
                CREATE TABLE reports (
                    id UUID PRIMARY KEY, -- UUIDv7 for time-ordered sorting
                    report_group_id UUID NOT NULL REFERENCES report_groups(id) ON DELETE CASCADE,

                    -- Denormalized from report_groups for direct access
                    name TEXT NOT NULL,

                    -- Upload processing status (independent of upload status)
                    status VARCHAR(20) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'processing', 'complete', 'failed')),

                    -- Upload status tracking (NULL = not started)
                    screenshots_upload_status VARCHAR(20)
                        CHECK (screenshots_upload_status IS NULL OR screenshots_upload_status IN ('started', 'completed', 'failed', 'timedout')),
                    json_upload_status VARCHAR(20)
                        CHECK (json_upload_status IS NULL OR json_upload_status IN ('started', 'completed', 'failed', 'timedout')),

                    -- GitHub Actions job metadata (for idempotency and display)
                    gh_job_id VARCHAR(100),
                    gh_job_name VARCHAR(255),

                    -- Environment metadata as JSONB
                    environment JSONB,

                    -- Extracted stats from JSON files
                    duration_ms BIGINT,
                    start_time TIMESTAMPTZ,

                    -- Error message if status is 'failed'
                    error_message TEXT,

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    deleted_at TIMESTAMPTZ
                );

                -- Index for report group lookup (active reports only)
                CREATE INDEX idx_reports_report_group_id ON reports(report_group_id)
                    WHERE deleted_at IS NULL;

                -- Index for status queries (active only)
                CREATE INDEX idx_reports_status ON reports(status)
                    WHERE deleted_at IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_reports_deleted_at ON reports(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Unique index for GitHub Actions job idempotency (active reports only)
                -- Prevents duplicate reports for the same gh_job_id within a report group
                CREATE UNIQUE INDEX idx_reports_gh_job ON reports(report_group_id, gh_job_id)
                    WHERE gh_job_id IS NOT NULL AND deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_reports_updated_at
                    BEFORE UPDATE ON reports
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
                DROP TRIGGER IF EXISTS update_reports_updated_at ON reports;
                DROP TABLE IF EXISTS reports CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
