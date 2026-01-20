//! Migration: Create reports table and shared trigger function.
//!
//! Reports represent a test run with one or more jobs.
//! Also creates the shared updated_at trigger function.

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
                -- Shared trigger function for updated_at
                CREATE OR REPLACE FUNCTION update_updated_at_column()
                RETURNS TRIGGER AS $$
                BEGIN
                    NEW.updated_at = NOW();
                    RETURN NEW;
                END;
                $$ LANGUAGE plpgsql;

                -- Test reports table
                CREATE TABLE test_reports (
                    id UUID PRIMARY KEY,
                    expected_jobs INTEGER NOT NULL
                        CHECK (expected_jobs >= 1 AND expected_jobs <= 100),
                    framework VARCHAR(20) NOT NULL
                        CHECK (framework IN ('playwright', 'cypress', 'detox')),
                    status VARCHAR(20) NOT NULL DEFAULT 'initializing'
                        CHECK (status IN ('initializing', 'uploading', 'processing', 'complete', 'failed')),

                    -- Metadata as JSONB (flexible schema)
                    -- {repo, branch, commit, pr_number, pr_author, workflow, run_id, run_attempt}
                    github_metadata JSONB,

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    deleted_at TIMESTAMPTZ
                );

                -- Index for listing reports by status (active only)
                CREATE INDEX idx_test_reports_status ON test_reports(status)
                    WHERE deleted_at IS NULL;

                -- Index for filtering by framework (active only)
                CREATE INDEX idx_test_reports_framework ON test_reports(framework)
                    WHERE deleted_at IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_test_reports_deleted_at ON test_reports(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Index for listing by creation date (active only)
                CREATE INDEX idx_test_reports_created_at ON test_reports(created_at DESC)
                    WHERE deleted_at IS NULL;

                -- GIN index for JSONB queries (repo, branch searches)
                CREATE INDEX idx_test_reports_github_metadata ON test_reports USING GIN (github_metadata);

                -- Trigger to update updated_at
                CREATE TRIGGER update_test_reports_updated_at
                    BEFORE UPDATE ON test_reports
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
                DROP TRIGGER IF EXISTS update_test_reports_updated_at ON test_reports;
                DROP TABLE IF EXISTS test_reports CASCADE;
                DROP FUNCTION IF EXISTS update_updated_at_column();
                "#,
            )
            .await?;

        Ok(())
    }
}
