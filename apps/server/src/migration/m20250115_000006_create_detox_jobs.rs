//! Create detox_job table.

use sea_orm_migration::prelude::*;

use super::m20250115_000001_create_reports::Report;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .create_table(
                Table::create()
                    .table(DetoxJob::Table)
                    .if_not_exists()
                    .col(ColumnDef::new(DetoxJob::Id).uuid().not_null().primary_key())
                    .col(ColumnDef::new(DetoxJob::ReportId).uuid().not_null())
                    .col(ColumnDef::new(DetoxJob::JobName).string().not_null())
                    .col(
                        ColumnDef::new(DetoxJob::CreatedAt)
                            .timestamp_with_time_zone()
                            .not_null()
                            .default(Expr::current_timestamp()),
                    )
                    .col(
                        ColumnDef::new(DetoxJob::TestsCount)
                            .integer()
                            .not_null()
                            .default(0),
                    )
                    .col(
                        ColumnDef::new(DetoxJob::PassedCount)
                            .integer()
                            .not_null()
                            .default(0),
                    )
                    .col(
                        ColumnDef::new(DetoxJob::FailedCount)
                            .integer()
                            .not_null()
                            .default(0),
                    )
                    .col(
                        ColumnDef::new(DetoxJob::SkippedCount)
                            .integer()
                            .not_null()
                            .default(0),
                    )
                    .col(
                        ColumnDef::new(DetoxJob::DurationMs)
                            .big_integer()
                            .not_null()
                            .default(0),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .from(DetoxJob::Table, DetoxJob::ReportId)
                            .to(Report::Table, Report::Id)
                            .on_delete(ForeignKeyAction::Cascade),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_detox_job_report_id")
                    .table(DetoxJob::Table)
                    .col(DetoxJob::ReportId)
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_detox_job_unique")
                    .table(DetoxJob::Table)
                    .col(DetoxJob::ReportId)
                    .col(DetoxJob::JobName)
                    .unique()
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(DetoxJob::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
pub enum DetoxJob {
    Table,
    Id,
    ReportId,
    JobName,
    CreatedAt,
    TestsCount,
    PassedCount,
    FailedCount,
    SkippedCount,
    DurationMs,
}
