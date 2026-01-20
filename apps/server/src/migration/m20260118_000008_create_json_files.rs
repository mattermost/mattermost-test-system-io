//! Migration: Create json_files table.
//!
//! Tracks JSON file uploads for test data extraction.
//! JSON files contain rich test data per framework (Cypress, Detox, Playwright).

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
                CREATE TABLE json_files (
                    id UUID PRIMARY KEY, -- UUIDv7 for time-ordered sorting
                    test_job_id UUID NOT NULL REFERENCES test_jobs(id) ON DELETE CASCADE,

                    -- File info
                    filename VARCHAR(500) NOT NULL,        -- relative path (e.g., "results.json")
                    s3_key VARCHAR(500) NOT NULL,          -- full S3 object key
                    size_bytes BIGINT NOT NULL DEFAULT 0,  -- expected file size
                    content_type VARCHAR(100),             -- MIME type (application/json)

                    -- Upload tracking
                    status VARCHAR(20) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'uploaded', 'failed')),

                    -- Extraction tracking
                    extracted_at TIMESTAMPTZ,              -- when data was extracted from this file
                    extraction_error TEXT,                 -- error message if extraction failed

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    uploaded_at TIMESTAMPTZ,
                    deleted_at TIMESTAMPTZ
                );

                -- Unique constraint: one file per job+filename (active only)
                CREATE UNIQUE INDEX idx_json_files_test_job_filename ON json_files(test_job_id, filename)
                    WHERE deleted_at IS NULL;

                -- Index for job lookup (active only)
                CREATE INDEX idx_json_files_test_job_id ON json_files(test_job_id)
                    WHERE deleted_at IS NULL;

                -- Index for pending files (for upload progress)
                CREATE INDEX idx_json_files_status ON json_files(test_job_id, status)
                    WHERE deleted_at IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_json_files_deleted_at ON json_files(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_json_files_updated_at
                    BEFORE UPDATE ON json_files
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
                DROP TRIGGER IF EXISTS update_json_files_updated_at ON json_files;
                DROP TABLE IF EXISTS json_files CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
