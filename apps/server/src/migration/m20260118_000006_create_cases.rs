//! Migration: Create cases table.
//!
//! Stores individual test case results extracted from framework JSON.

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
                CREATE TABLE cases (
                    id UUID PRIMARY KEY, -- UUIDv7 for time-ordered sorting
                    suite_id UUID NOT NULL REFERENCES suites(id) ON DELETE CASCADE,
                    upload_id UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,

                    -- Test identification
                    title VARCHAR(500) NOT NULL,           -- Test name
                    full_title VARCHAR(1000) NOT NULL,     -- Full path including suite

                    -- Status
                    status VARCHAR(20) NOT NULL
                        CHECK (status IN ('passed', 'failed', 'skipped', 'flaky', 'timedOut')),

                    -- Timing and retries
                    duration_ms INTEGER NOT NULL DEFAULT 0,
                    retry_count INTEGER NOT NULL DEFAULT 0,

                    -- Error info (nullable)
                    error_message TEXT,

                    -- Attachments (screenshots, videos) from JUnit XML
                    attachments JSONB DEFAULT '[]'::jsonb,

                    -- Ordering within suite
                    sequence INTEGER NOT NULL DEFAULT 0,

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    deleted_at TIMESTAMPTZ
                );

                -- Index for suite lookup (active only)
                CREATE INDEX idx_cases_suite_id ON cases(suite_id)
                    WHERE deleted_at IS NULL;

                -- Index for upload lookup - flat queries (active only)
                CREATE INDEX idx_cases_upload_id ON cases(upload_id)
                    WHERE deleted_at IS NULL;

                -- Index for status filtering
                CREATE INDEX idx_cases_status ON cases(upload_id, status)
                    WHERE deleted_at IS NULL;

                -- Index for ordering within suite
                CREATE INDEX idx_cases_sequence ON cases(suite_id, sequence)
                    WHERE deleted_at IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_cases_deleted_at ON cases(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Index for attachments (GIN index for JSONB queries)
                CREATE INDEX idx_cases_attachments ON cases USING GIN (attachments)
                    WHERE deleted_at IS NULL AND attachments IS NOT NULL AND attachments != '[]'::jsonb;

                -- Enable pg_trgm extension for text search (if not exists)
                CREATE EXTENSION IF NOT EXISTS pg_trgm;

                -- Trigram indexes for text search on title and full_title
                -- These enable efficient LIKE '%pattern%' queries
                CREATE INDEX idx_cases_title_trgm ON cases USING GIN (title gin_trgm_ops)
                    WHERE deleted_at IS NULL;
                CREATE INDEX idx_cases_full_title_trgm ON cases USING GIN (full_title gin_trgm_ops)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_cases_updated_at
                    BEFORE UPDATE ON cases
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
                DROP TRIGGER IF EXISTS update_cases_updated_at ON cases;
                DROP INDEX IF EXISTS idx_cases_title_trgm;
                DROP INDEX IF EXISTS idx_cases_full_title_trgm;
                DROP TABLE IF EXISTS cases CASCADE;
                -- Note: pg_trgm extension is left installed as other tables may use it
                "#,
            )
            .await?;

        Ok(())
    }
}
