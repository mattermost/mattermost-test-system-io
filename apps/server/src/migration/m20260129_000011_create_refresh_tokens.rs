//! Migration: Create refresh_tokens table.
//!
//! Stores hashed refresh tokens for session management.
//! Enables server-side revocation and token rotation.

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
                CREATE TABLE refresh_tokens (
                    id UUID PRIMARY KEY,
                    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                    token_hash VARCHAR(64) NOT NULL,
                    expires_at TIMESTAMPTZ NOT NULL,
                    revoked_at TIMESTAMPTZ,

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    deleted_at TIMESTAMPTZ
                );

                -- Lookup by hash (active tokens only)
                CREATE UNIQUE INDEX idx_refresh_tokens_hash_active
                    ON refresh_tokens(token_hash)
                    WHERE revoked_at IS NULL AND deleted_at IS NULL;

                -- Cleanup: find expired tokens
                CREATE INDEX idx_refresh_tokens_expires_at
                    ON refresh_tokens(expires_at)
                    WHERE revoked_at IS NULL AND deleted_at IS NULL;

                -- User's active tokens (for revoke-all)
                CREATE INDEX idx_refresh_tokens_user_id
                    ON refresh_tokens(user_id)
                    WHERE revoked_at IS NULL AND deleted_at IS NULL;

                -- Soft-delete index
                CREATE INDEX idx_refresh_tokens_deleted_at
                    ON refresh_tokens(deleted_at)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_refresh_tokens_updated_at
                    BEFORE UPDATE ON refresh_tokens
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
                DROP TRIGGER IF EXISTS update_refresh_tokens_updated_at ON refresh_tokens;
                DROP TABLE IF EXISTS refresh_tokens CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
