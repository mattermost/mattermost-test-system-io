//! Migration: Create github_oidc_policies table.
//!
//! Manages GitHub Actions OIDC repository allow-list with role mapping.

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
                CREATE TABLE github_oidc_policies (
                    id UUID PRIMARY KEY,
                    repository_pattern VARCHAR(255) NOT NULL,
                    role VARCHAR(20) NOT NULL DEFAULT 'contributor'
                        CHECK (role IN ('admin', 'contributor', 'viewer')),
                    description VARCHAR(500),
                    enabled BOOLEAN NOT NULL DEFAULT TRUE,

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    deleted_at TIMESTAMPTZ
                );

                -- Unique constraint on pattern (active only)
                CREATE UNIQUE INDEX idx_oidc_policies_pattern_active
                    ON github_oidc_policies(repository_pattern)
                    WHERE deleted_at IS NULL;

                -- Index for enabled policies
                CREATE INDEX idx_oidc_policies_enabled
                    ON github_oidc_policies(enabled)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_github_oidc_policies_updated_at
                    BEFORE UPDATE ON github_oidc_policies
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
                DROP TRIGGER IF EXISTS update_github_oidc_policies_updated_at ON github_oidc_policies;
                DROP TABLE IF EXISTS github_oidc_policies CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
