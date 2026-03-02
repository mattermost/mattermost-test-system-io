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
                    upload_id UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
                    case_id UUID REFERENCES cases(id) ON DELETE SET NULL, -- linked after extraction

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

                -- Unique constraint: one file per upload+filename (active only)
                CREATE UNIQUE INDEX idx_screenshots_upload_filename ON screenshots(upload_id, filename)
                    WHERE deleted_at IS NULL;

                -- Index for upload lookup (active only)
                CREATE INDEX idx_screenshots_upload_id ON screenshots(upload_id)
                    WHERE deleted_at IS NULL;

                -- Index for pending files (for upload progress)
                CREATE INDEX idx_screenshots_status ON screenshots(upload_id, status)
                    WHERE deleted_at IS NULL;

                -- Index for test name lookup within an upload
                CREATE INDEX idx_screenshots_test_name ON screenshots(upload_id, test_name)
                    WHERE deleted_at IS NULL;

                -- Index for case_id lookup
                CREATE INDEX idx_screenshots_case_id ON screenshots(case_id)
                    WHERE deleted_at IS NULL AND case_id IS NOT NULL;

                -- Index for unmapped screenshots (case_id IS NULL)
                CREATE INDEX idx_screenshots_unmapped ON screenshots(upload_id)
                    WHERE deleted_at IS NULL AND case_id IS NULL;

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
