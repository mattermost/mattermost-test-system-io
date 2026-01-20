//! Migration: Create html_files table.
//!
//! Tracks HTML report file uploads with request-then-transfer pattern.

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
                CREATE TABLE html_files (
                    id UUID PRIMARY KEY, -- UUIDv7 for time-ordered sorting
                    test_job_id UUID NOT NULL REFERENCES test_jobs(id) ON DELETE CASCADE,

                    -- File info
                    filename VARCHAR(500) NOT NULL,        -- relative path from HTML root
                    s3_key VARCHAR(500) NOT NULL,          -- full S3 object key
                    size_bytes BIGINT NOT NULL DEFAULT 0,  -- expected file size
                    content_type VARCHAR(100),             -- MIME type

                    -- Upload tracking
                    status VARCHAR(20) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'uploaded', 'failed')),

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    uploaded_at TIMESTAMPTZ,
                    deleted_at TIMESTAMPTZ
                );

                -- Unique constraint: one file per job+filename (active only)
                CREATE UNIQUE INDEX idx_html_files_test_job_filename ON html_files(test_job_id, filename)
                    WHERE deleted_at IS NULL;

                -- Index for job lookup (active only)
                CREATE INDEX idx_html_files_test_job_id ON html_files(test_job_id)
                    WHERE deleted_at IS NULL;

                -- Index for pending files (for upload progress)
                CREATE INDEX idx_html_files_status ON html_files(test_job_id, status)
                    WHERE deleted_at IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_html_files_deleted_at ON html_files(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_html_files_updated_at
                    BEFORE UPDATE ON html_files
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
                DROP TRIGGER IF EXISTS update_html_files_updated_at ON html_files;
                DROP TABLE IF EXISTS html_files CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
