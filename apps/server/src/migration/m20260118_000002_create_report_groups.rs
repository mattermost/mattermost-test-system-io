//! Migration: Create report_groups table and shared trigger function.
//!
//! Report groups represent a test run with one or more reports (individual uploads).
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

                -- Report groups table
                CREATE TABLE report_groups (
                    id UUID PRIMARY KEY,
                    framework VARCHAR(20) NOT NULL
                        CHECK (framework IN ('playwright', 'cypress', 'detox')),
                    name TEXT NOT NULL,
                    status VARCHAR(20) NOT NULL DEFAULT 'in_progress'
                        CHECK (status IN ('in_progress', 'completed')),

                    repository VARCHAR(255) NOT NULL,
                    branch VARCHAR(255) NOT NULL,
                    commit VARCHAR(255) NOT NULL,
                    gh_run_id VARCHAR(100) NOT NULL DEFAULT '',
                    gh_run_attempt VARCHAR(50) NOT NULL DEFAULT '1',
                    gh_pr_number INTEGER,

                    -- Environment metadata as JSONB (tool + server info)
                    environment_metadata JSONB,

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    deleted_at TIMESTAMPTZ
                );

                -- Index for listing reports by status (active only)
                CREATE INDEX idx_report_groups_status ON report_groups(status)
                    WHERE deleted_at IS NULL;

                -- Index for filtering by framework (active only)
                CREATE INDEX idx_report_groups_framework ON report_groups(framework)
                    WHERE deleted_at IS NULL;

                -- Index for soft-delete queries
                CREATE INDEX idx_report_groups_deleted_at ON report_groups(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Index for listing by creation date (active only)
                CREATE INDEX idx_report_groups_created_at ON report_groups(created_at DESC)
                    WHERE deleted_at IS NULL;

                -- Index for repository queries (active only)
                CREATE INDEX idx_report_groups_repository ON report_groups(repository)
                    WHERE deleted_at IS NULL;

                -- Index for commit lookups (active only)
                CREATE INDEX idx_report_groups_commit ON report_groups(commit)
                    WHERE deleted_at IS NULL;

                -- Index for gh_run_id lookups (active only)
                CREATE INDEX idx_report_groups_gh_run_id ON report_groups(gh_run_id)
                    WHERE deleted_at IS NULL;

                -- Unique partial index for grouping key (prevents duplicate reports)
                CREATE UNIQUE INDEX idx_report_groups_grouping_key
                    ON report_groups(repository, commit, gh_run_id, name, gh_run_attempt)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_report_groups_updated_at
                    BEFORE UPDATE ON report_groups
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
                DROP TRIGGER IF EXISTS update_report_groups_updated_at ON report_groups;
                DROP TABLE IF EXISTS report_groups CASCADE;
                DROP FUNCTION IF EXISTS update_updated_at_column();
                "#,
            )
            .await?;

        Ok(())
    }
}
