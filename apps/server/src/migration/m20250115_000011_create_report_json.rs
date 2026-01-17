//! Create report_json table for storing extraction JSON data as JSONB.

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
                    .table(ReportJson::Table)
                    .if_not_exists()
                    .col(
                        ColumnDef::new(ReportJson::Id)
                            .big_integer()
                            .not_null()
                            .auto_increment()
                            .primary_key(),
                    )
                    .col(ColumnDef::new(ReportJson::ReportId).uuid().not_null())
                    .col(
                        ColumnDef::new(ReportJson::FileType)
                            .string()
                            .not_null()
                            .comment("Type of JSON file: results.json, all.json, mochawesome.json, ios-data.json, android-data.json"),
                    )
                    .col(
                        ColumnDef::new(ReportJson::Data)
                            .json_binary()
                            .not_null()
                            .comment("JSON content stored as JSONB for efficient querying"),
                    )
                    .col(
                        ColumnDef::new(ReportJson::CreatedAt)
                            .timestamp_with_time_zone()
                            .not_null()
                            .default(Expr::current_timestamp()),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .name("fk_report_json_report_id")
                            .from(ReportJson::Table, ReportJson::ReportId)
                            .to(Report::Table, Report::Id)
                            .on_delete(ForeignKeyAction::Cascade),
                    )
                    .to_owned(),
            )
            .await?;

        // Index on report_id for fast lookups
        manager
            .create_index(
                Index::create()
                    .name("idx_report_json_report_id")
                    .table(ReportJson::Table)
                    .col(ReportJson::ReportId)
                    .to_owned(),
            )
            .await?;

        // Composite index on report_id and file_type for quick file lookups
        manager
            .create_index(
                Index::create()
                    .name("idx_report_json_report_id_file_type")
                    .table(ReportJson::Table)
                    .col(ReportJson::ReportId)
                    .col(ReportJson::FileType)
                    .to_owned(),
            )
            .await?;

        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(ReportJson::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
pub enum ReportJson {
    Table,
    Id,
    ReportId,
    FileType,
    Data,
    CreatedAt,
}
