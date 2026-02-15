//! Migration: Create users table.
//!
//! Stores GitHub OAuth users for web UI authentication.

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
                CREATE TABLE users (
                    id UUID PRIMARY KEY,
                    github_id BIGINT NOT NULL,
                    username VARCHAR(100) NOT NULL,
                    display_name VARCHAR(255),
                    avatar_url VARCHAR(500),
                    email VARCHAR(255),
                    role VARCHAR(20) NOT NULL DEFAULT 'viewer'
                        CHECK (role IN ('admin', 'contributor', 'viewer')),
                    last_login_at TIMESTAMPTZ,

                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    deleted_at TIMESTAMPTZ
                );

                -- Unique constraint on github_id (active only)
                CREATE UNIQUE INDEX idx_users_github_id_active
                    ON users(github_id)
                    WHERE deleted_at IS NULL;

                -- Index for username lookup
                CREATE INDEX idx_users_username
                    ON users(username)
                    WHERE deleted_at IS NULL;

                -- Trigger to update updated_at
                CREATE TRIGGER update_users_updated_at
                    BEFORE UPDATE ON users
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
                DROP TRIGGER IF EXISTS update_users_updated_at ON users;
                DROP TABLE IF EXISTS users CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
