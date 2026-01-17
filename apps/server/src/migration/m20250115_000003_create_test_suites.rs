//! Create test_suite table.

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
                    .table(TestSuite::Table)
                    .if_not_exists()
                    .col(
                        ColumnDef::new(TestSuite::Id)
                            .big_integer()
                            .not_null()
                            .auto_increment()
                            .primary_key(),
                    )
                    .col(ColumnDef::new(TestSuite::ReportId).uuid().not_null())
                    .col(ColumnDef::new(TestSuite::Title).string().not_null())
                    .col(ColumnDef::new(TestSuite::FilePath).string().not_null())
                    .col(ColumnDef::new(TestSuite::SpecsCount).integer())
                    .col(ColumnDef::new(TestSuite::PassedCount).integer())
                    .col(ColumnDef::new(TestSuite::FailedCount).integer())
                    .col(ColumnDef::new(TestSuite::FlakyCount).integer())
                    .col(ColumnDef::new(TestSuite::SkippedCount).integer())
                    .col(ColumnDef::new(TestSuite::DurationMs).big_integer())
                    .foreign_key(
                        ForeignKey::create()
                            .from(TestSuite::Table, TestSuite::ReportId)
                            .to(Report::Table, Report::Id)
                            .on_delete(ForeignKeyAction::Cascade),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_test_suite_report_id")
                    .table(TestSuite::Table)
                    .col(TestSuite::ReportId)
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(TestSuite::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
pub enum TestSuite {
    Table,
    Id,
    ReportId,
    Title,
    FilePath,
    SpecsCount,
    PassedCount,
    FailedCount,
    FlakyCount,
    SkippedCount,
    DurationMs,
}
