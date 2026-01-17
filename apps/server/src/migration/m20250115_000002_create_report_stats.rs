//! Create report_stat table.

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
                    .table(ReportStat::Table)
                    .if_not_exists()
                    .col(
                        ColumnDef::new(ReportStat::Id)
                            .big_integer()
                            .not_null()
                            .auto_increment()
                            .primary_key(),
                    )
                    .col(
                        ColumnDef::new(ReportStat::ReportId)
                            .uuid()
                            .not_null()
                            .unique_key(),
                    )
                    .col(
                        ColumnDef::new(ReportStat::StartTime)
                            .timestamp_with_time_zone()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(ReportStat::DurationMs)
                            .big_integer()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(ReportStat::Expected)
                            .integer()
                            .not_null()
                            .default(0),
                    )
                    .col(
                        ColumnDef::new(ReportStat::Skipped)
                            .integer()
                            .not_null()
                            .default(0),
                    )
                    .col(
                        ColumnDef::new(ReportStat::Unexpected)
                            .integer()
                            .not_null()
                            .default(0),
                    )
                    .col(
                        ColumnDef::new(ReportStat::Flaky)
                            .integer()
                            .not_null()
                            .default(0),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .from(ReportStat::Table, ReportStat::ReportId)
                            .to(Report::Table, Report::Id)
                            .on_delete(ForeignKeyAction::Cascade),
                    )
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(ReportStat::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
enum ReportStat {
    Table,
    Id,
    ReportId,
    StartTime,
    DurationMs,
    Expected,
    Skipped,
    Unexpected,
    Flaky,
}
