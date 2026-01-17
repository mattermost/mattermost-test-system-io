//! Create test_result table.

use sea_orm_migration::prelude::*;

use super::m20250115_000004_create_test_specs::TestSpec;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .create_table(
                Table::create()
                    .table(TestResult::Table)
                    .if_not_exists()
                    .col(
                        ColumnDef::new(TestResult::Id)
                            .big_integer()
                            .not_null()
                            .auto_increment()
                            .primary_key(),
                    )
                    .col(ColumnDef::new(TestResult::SpecId).big_integer().not_null())
                    .col(ColumnDef::new(TestResult::Status).string().not_null())
                    .col(
                        ColumnDef::new(TestResult::DurationMs)
                            .big_integer()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(TestResult::Retry)
                            .integer()
                            .not_null()
                            .default(0),
                    )
                    .col(
                        ColumnDef::new(TestResult::StartTime)
                            .timestamp_with_time_zone()
                            .not_null(),
                    )
                    .col(ColumnDef::new(TestResult::ProjectId).string().not_null())
                    .col(ColumnDef::new(TestResult::ProjectName).string().not_null())
                    .col(ColumnDef::new(TestResult::ErrorsJson).json_binary())
                    .foreign_key(
                        ForeignKey::create()
                            .from(TestResult::Table, TestResult::SpecId)
                            .to(TestSpec::Table, TestSpec::Id)
                            .on_delete(ForeignKeyAction::Cascade),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_test_result_spec_id")
                    .table(TestResult::Table)
                    .col(TestResult::SpecId)
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(TestResult::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
enum TestResult {
    Table,
    Id,
    SpecId,
    Status,
    DurationMs,
    Retry,
    StartTime,
    ProjectId,
    ProjectName,
    ErrorsJson,
}
