//! Create upload_file table.

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
                    .table(UploadFile::Table)
                    .if_not_exists()
                    .col(
                        ColumnDef::new(UploadFile::Id)
                            .uuid()
                            .not_null()
                            .primary_key(),
                    )
                    .col(ColumnDef::new(UploadFile::ReportId).uuid().not_null())
                    .col(ColumnDef::new(UploadFile::Filename).string().not_null())
                    .col(ColumnDef::new(UploadFile::FileSize).big_integer())
                    .col(ColumnDef::new(UploadFile::UploadedAt).timestamp_with_time_zone())
                    .col(
                        ColumnDef::new(UploadFile::CreatedAt)
                            .timestamp_with_time_zone()
                            .not_null()
                            .default(Expr::current_timestamp()),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .from(UploadFile::Table, UploadFile::ReportId)
                            .to(Report::Table, Report::Id)
                            .on_delete(ForeignKeyAction::Cascade),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_upload_file_report")
                    .table(UploadFile::Table)
                    .col(UploadFile::ReportId)
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_upload_file_unique")
                    .table(UploadFile::Table)
                    .col(UploadFile::ReportId)
                    .col(UploadFile::Filename)
                    .unique()
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(UploadFile::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
enum UploadFile {
    Table,
    Id,
    ReportId,
    Filename,
    FileSize,
    UploadedAt,
    CreatedAt,
}
