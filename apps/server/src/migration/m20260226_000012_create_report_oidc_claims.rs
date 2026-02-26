//! Migration: Create report_oidc_claims table.
//!
//! Stores non-sensitive OIDC token claims linked 1:1 to reports.
//! Only safe, public CI metadata is persisted (13 claims + 3 audit fields).

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
                CREATE TABLE report_oidc_claims (
                    id              UUID PRIMARY KEY,
                    report_id       UUID NOT NULL UNIQUE
                                    REFERENCES test_reports(id) ON DELETE CASCADE,

                    -- Safe claims (public CI metadata from OIDC token)
                    sub             VARCHAR(500),
                    repository      VARCHAR(255),
                    repository_owner VARCHAR(255),
                    actor           VARCHAR(255),
                    sha             VARCHAR(255),
                    ref             VARCHAR(255),
                    ref_type        VARCHAR(50),
                    workflow        VARCHAR(255),
                    event_name      VARCHAR(100),
                    run_id          VARCHAR(100),
                    run_number      VARCHAR(50),
                    run_attempt     VARCHAR(50),
                    head_ref        VARCHAR(255),
                    base_ref        VARCHAR(255),

                    -- Audit fields (system-generated, not from token)
                    resolved_role   VARCHAR(20) NOT NULL,
                    api_path        VARCHAR(500) NOT NULL,
                    http_method     VARCHAR(10) NOT NULL,

                    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
                );

                -- Fast lookup by report
                CREATE INDEX idx_report_oidc_claims_report_id
                    ON report_oidc_claims(report_id);

                -- Query reports by source repository
                CREATE INDEX idx_report_oidc_claims_repository
                    ON report_oidc_claims(repository);

                -- Query reports by workflow
                CREATE INDEX idx_report_oidc_claims_workflow
                    ON report_oidc_claims(workflow);
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
                DROP TABLE IF EXISTS report_oidc_claims CASCADE;
                "#,
            )
            .await?;

        Ok(())
    }
}
