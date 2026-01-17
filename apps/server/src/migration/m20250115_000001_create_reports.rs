//! Create report table.

use sea_orm_migration::prelude::*;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .create_table(
                Table::create()
                    .table(Report::Table)
                    .if_not_exists()
                    .col(ColumnDef::new(Report::Id).uuid().not_null().primary_key())
                    .col(
                        ColumnDef::new(Report::CreatedAt)
                            .timestamp_with_time_zone()
                            .not_null()
                            .default(Expr::current_timestamp()),
                    )
                    .col(ColumnDef::new(Report::DeletedAt).timestamp_with_time_zone())
                    .col(
                        ColumnDef::new(Report::ExtractionStatus)
                            .string()
                            .not_null()
                            .default("pending"),
                    )
                    .col(ColumnDef::new(Report::FilePath).string().not_null())
                    .col(ColumnDef::new(Report::ErrorMessage).text())
                    .col(
                        ColumnDef::new(Report::HasFiles)
                            .boolean()
                            .not_null()
                            .default(true),
                    )
                    .col(ColumnDef::new(Report::FilesDeletedAt).timestamp_with_time_zone())
                    .col(ColumnDef::new(Report::Framework).string())
                    .col(ColumnDef::new(Report::FrameworkVersion).string())
                    .col(ColumnDef::new(Report::Platform).string())
                    .col(ColumnDef::new(Report::GithubContext).json_binary())
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_report_created_at")
                    .table(Report::Table)
                    .col(Report::CreatedAt)
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_report_deleted_at")
                    .table(Report::Table)
                    .col(Report::DeletedAt)
                    .to_owned(),
            )
            .await?;

        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(Report::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
pub enum Report {
    Table,
    Id,
    CreatedAt,
    DeletedAt,
    ExtractionStatus,
    FilePath,
    ErrorMessage,
    HasFiles,
    FilesDeletedAt,
    Framework,
    FrameworkVersion,
    Platform,
    GithubContext,
}
