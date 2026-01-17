//! Create test_spec table.

use sea_orm_migration::prelude::*;

use super::m20250115_000003_create_test_suites::TestSuite;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .create_table(
                Table::create()
                    .table(TestSpec::Table)
                    .if_not_exists()
                    .col(
                        ColumnDef::new(TestSpec::Id)
                            .big_integer()
                            .not_null()
                            .auto_increment()
                            .primary_key(),
                    )
                    .col(ColumnDef::new(TestSpec::SuiteId).big_integer().not_null())
                    .col(ColumnDef::new(TestSpec::Title).string().not_null())
                    .col(ColumnDef::new(TestSpec::Ok).boolean().not_null())
                    .col(ColumnDef::new(TestSpec::FullTitle).string().not_null())
                    .col(ColumnDef::new(TestSpec::FilePath).string().not_null())
                    .col(ColumnDef::new(TestSpec::Line).integer().not_null())
                    .col(ColumnDef::new(TestSpec::Col).integer().not_null())
                    .foreign_key(
                        ForeignKey::create()
                            .from(TestSpec::Table, TestSpec::SuiteId)
                            .to(TestSuite::Table, TestSuite::Id)
                            .on_delete(ForeignKeyAction::Cascade),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_test_spec_suite_id")
                    .table(TestSpec::Table)
                    .col(TestSpec::SuiteId)
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(TestSpec::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
pub enum TestSpec {
    Table,
    Id,
    SuiteId,
    Title,
    Ok,
    FullTitle,
    FilePath,
    Line,
    Col,
}
