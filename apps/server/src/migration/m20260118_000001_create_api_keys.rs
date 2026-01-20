//! Migration: Create api_keys table.
//!
//! API keys for authentication with role-based access control.

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
                CREATE TABLE api_keys (
                    id UUID PRIMARY KEY,
                    key_hash VARCHAR(64) NOT NULL,
                    key_prefix VARCHAR(12) NOT NULL,
                    name VARCHAR(100) NOT NULL,
                    role VARCHAR(20) NOT NULL DEFAULT 'contributor'
                        CHECK (role IN ('admin', 'contributor', 'viewer')),

                    expires_at TIMESTAMPTZ,
                    last_used_at TIMESTAMPTZ,

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    deleted_at TIMESTAMPTZ
                );

                -- Unique constraint on key_hash for active keys only
                CREATE UNIQUE INDEX idx_api_keys_key_hash_active
                    ON api_keys(key_hash)
                    WHERE deleted_at IS NULL;

                -- Index for prefix lookup (showing key prefix in UI)
                CREATE INDEX idx_api_keys_key_prefix ON api_keys(key_prefix);

                -- Index for soft-delete queries
                CREATE INDEX idx_api_keys_deleted_at ON api_keys(deleted_at)
                    WHERE deleted_at IS NULL;
                "#,
            )
            .await?;

        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .get_connection()
            .execute_unprepared("DROP TABLE IF EXISTS api_keys CASCADE;")
            .await?;

        Ok(())
    }
}
