//! Migration: Create test_cases table.
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
                CREATE TABLE test_cases (
                    id UUID PRIMARY KEY, -- UUIDv7 for time-ordered sorting
                    test_suite_id UUID NOT NULL REFERENCES test_suites(id) ON DELETE CASCADE,
                    test_job_id UUID NOT NULL REFERENCES test_jobs(id) ON DELETE CASCADE,

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
                CREATE INDEX idx_test_cases_test_suite_id ON test_cases(test_suite_id)
                    WHERE deleted_at IS NULL;

                -- Index for job lookup - flat queries (active only)
                CREATE INDEX idx_test_cases_test_job_id ON test_cases(test_job_id)
                    WHERE deleted_at IS NULL;

                -- Index for status filtering
                CREATE INDEX idx_test_cases_status ON test_cases(test_job_id, status)
                    WHERE deleted_at IS NULL;

                -- Index for ordering within suite
                CREATE INDEX idx_test_cases_sequence ON test_cases(test_suite_id, sequence)
                    WHERE deleted_at IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_test_cases_deleted_at ON test_cases(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Index for attachments (GIN index for JSONB queries)
                CREATE INDEX idx_test_cases_attachments ON test_cases USING GIN (attachments)
                    WHERE deleted_at IS NULL AND attachments IS NOT NULL AND attachments != '[]'::jsonb;

                -- Enable pg_trgm extension for text search (if not exists)
                CREATE EXTENSION IF NOT EXISTS pg_trgm;

                -- Trigram indexes for text search on title and full_title
                -- These enable efficient LIKE '%pattern%' queries
                CREATE INDEX idx_test_cases_title_trgm ON test_cases USING GIN (title gin_trgm_ops)
                    WHERE deleted_at IS NULL;
                CREATE INDEX idx_test_cases_full_title_trgm ON test_cases USING GIN (full_title gin_trgm_ops)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_test_cases_updated_at
                    BEFORE UPDATE ON test_cases
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
                DROP TRIGGER IF EXISTS update_test_cases_updated_at ON test_cases;
                DROP INDEX IF EXISTS idx_test_cases_title_trgm;
                DROP INDEX IF EXISTS idx_test_cases_full_title_trgm;
                DROP TABLE IF EXISTS test_cases CASCADE;
                -- Note: pg_trgm extension is left installed as other tables may use it
                "#,
            )
            .await?;

        Ok(())
    }
}
