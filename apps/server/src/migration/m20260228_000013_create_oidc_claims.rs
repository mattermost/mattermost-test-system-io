//! Migration: Create oidc_claims table.
//!
//! Stores OIDC token claims linked 1:1 to uploads.
//! Persists all ~29 GitHub OIDC claims, 6 JWT standard fields, and 3 audit fields.

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
                CREATE TABLE oidc_claims (
                    id              UUID PRIMARY KEY,
                    upload_id       UUID NOT NULL UNIQUE
                                    REFERENCES reports(id) ON DELETE CASCADE,

                    -- 14 existing GitHub OIDC claims (all nullable)
                    sub                     VARCHAR(500),
                    repository              VARCHAR(255),
                    repository_owner        VARCHAR(255),
                    actor                   VARCHAR(255),
                    sha                     VARCHAR(255),
                    ref                     VARCHAR(255),
                    ref_type                VARCHAR(50),
                    workflow                VARCHAR(255),
                    event_name              VARCHAR(100),
                    run_id                  VARCHAR(100),
                    run_number              VARCHAR(50),
                    run_attempt             VARCHAR(50),
                    head_ref                VARCHAR(255),
                    base_ref                VARCHAR(255),

                    -- 11 new GitHub OIDC claims (all nullable)
                    repository_id           VARCHAR(50),
                    repository_owner_id     VARCHAR(50),
                    repository_visibility   VARCHAR(20),
                    actor_id                VARCHAR(50),
                    runner_environment      VARCHAR(100),
                    environment             VARCHAR(255),
                    check_run_id            VARCHAR(100),
                    job_workflow_ref        VARCHAR(500),
                    job_workflow_sha        VARCHAR(255),
                    workflow_ref            VARCHAR(500),
                    workflow_sha            VARCHAR(255),

                    -- 6 JWT standard fields
                    jti                     VARCHAR(255),
                    iss                     VARCHAR(500),
                    aud                     VARCHAR(500),
                    exp                     BIGINT,
                    iat                     BIGINT,
                    nbf                     BIGINT,

                    -- 3 audit fields (system-generated, NOT NULL)
                    resolved_role           VARCHAR(20) NOT NULL,
                    api_path                VARCHAR(500) NOT NULL,
                    http_method             VARCHAR(10) NOT NULL,

                    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
                );

                -- Index for check_run_id lookups
                CREATE INDEX idx_oidc_claims_check_run_id
                    ON oidc_claims(check_run_id);

                -- Index for repository lookups
                CREATE INDEX idx_oidc_claims_repository
                    ON oidc_claims(repository);
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
                DROP TABLE IF EXISTS oidc_claims CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
