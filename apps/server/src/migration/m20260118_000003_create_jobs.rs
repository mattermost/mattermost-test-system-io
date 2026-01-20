//! Migration: Create jobs table.
//!
//! Jobs represent individual test jobs within a report (e.g., parallel shards).

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
                CREATE TABLE test_jobs (
                    id UUID PRIMARY KEY, -- UUIDv7 for time-ordered sorting
                    test_report_id UUID NOT NULL REFERENCES test_reports(id) ON DELETE CASCADE,

                    -- Job processing status (independent of upload status)
                    status VARCHAR(20) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'processing', 'complete', 'failed')),

                    -- Upload status tracking (NULL = not started)
                    -- HTML and screenshots are optional, JSON is required
                    html_upload_status VARCHAR(20)
                        CHECK (html_upload_status IS NULL OR html_upload_status IN ('started', 'completed', 'failed', 'timedout')),
                    screenshots_upload_status VARCHAR(20)
                        CHECK (screenshots_upload_status IS NULL OR screenshots_upload_status IN ('started', 'completed', 'failed', 'timedout')),
                    json_upload_status VARCHAR(20)
                        CHECK (json_upload_status IS NULL OR json_upload_status IN ('started', 'completed', 'failed', 'timedout')),

                    -- S3 path prefix for HTML report files
                    html_path VARCHAR(500),

                    -- Metadata as JSONB (flexible schema)
                    -- github_metadata: {job_id, job_name}
                    -- environment: {os, browser, device, tags}
                    github_metadata JSONB,
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

                -- Index for report lookup (active jobs only)
                CREATE INDEX idx_test_jobs_test_report_id ON test_jobs(test_report_id)
                    WHERE deleted_at IS NULL;

                -- Index for status queries (active only)
                CREATE INDEX idx_test_jobs_status ON test_jobs(status)
                    WHERE deleted_at IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_test_jobs_deleted_at ON test_jobs(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Unique index for GitHub job idempotency (active jobs only)
                -- Prevents duplicate uploads for the same github job_id within a report
                CREATE UNIQUE INDEX idx_test_jobs_github_job ON test_jobs(test_report_id, (github_metadata->>'job_id'))
                    WHERE github_metadata->>'job_id' IS NOT NULL AND deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_test_jobs_updated_at
                    BEFORE UPDATE ON test_jobs
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
                DROP TRIGGER IF EXISTS update_test_jobs_updated_at ON test_jobs;
                DROP TABLE IF EXISTS test_jobs CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
