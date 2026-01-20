//! Migration: Create screenshots table.
//!
//! Tracks screenshot uploads with request-then-transfer pattern.

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
                CREATE TABLE screenshots (
                    id UUID PRIMARY KEY, -- UUIDv7 for time-ordered sorting
                    test_job_id UUID NOT NULL REFERENCES test_jobs(id) ON DELETE CASCADE,
                    test_case_id UUID REFERENCES test_cases(id) ON DELETE SET NULL, -- linked after extraction

                    -- File info
                    filename VARCHAR(500) NOT NULL,           -- relative path from screenshots root
                    s3_key VARCHAR(500) NOT NULL,             -- full S3 object key
                    size_bytes BIGINT NOT NULL DEFAULT 0,     -- expected file size
                    content_type VARCHAR(100),                -- MIME type

                    -- Metadata extracted from path
                    test_name VARCHAR(500) NOT NULL DEFAULT '',
                    sequence INTEGER NOT NULL DEFAULT 0,

                    -- Upload tracking
                    status VARCHAR(20) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'uploaded', 'failed')),

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    uploaded_at TIMESTAMPTZ,
                    deleted_at TIMESTAMPTZ
                );

                -- Unique constraint: one file per job+filename (active only)
                CREATE UNIQUE INDEX idx_screenshots_test_job_filename ON screenshots(test_job_id, filename)
                    WHERE deleted_at IS NULL;

                -- Index for job lookup (active only)
                CREATE INDEX idx_screenshots_test_job_id ON screenshots(test_job_id)
                    WHERE deleted_at IS NULL;

                -- Index for pending files (for upload progress)
                CREATE INDEX idx_screenshots_status ON screenshots(test_job_id, status)
                    WHERE deleted_at IS NULL;

                -- Index for test name lookup within a job
                CREATE INDEX idx_screenshots_test_name ON screenshots(test_job_id, test_name)
                    WHERE deleted_at IS NULL;

                -- Index for test_case_id lookup
                CREATE INDEX idx_screenshots_test_case_id ON screenshots(test_case_id)
                    WHERE deleted_at IS NULL AND test_case_id IS NOT NULL;

                -- Index for unmapped screenshots (test_case_id IS NULL)
                CREATE INDEX idx_screenshots_unmapped ON screenshots(test_job_id)
                    WHERE deleted_at IS NULL AND test_case_id IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_screenshots_deleted_at ON screenshots(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_screenshots_updated_at
                    BEFORE UPDATE ON screenshots
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
                DROP TRIGGER IF EXISTS update_screenshots_updated_at ON screenshots;
                DROP TABLE IF EXISTS screenshots CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
