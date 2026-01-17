//! Create detox_screenshot table.

use sea_orm_migration::prelude::*;

use super::m20250115_000006_create_detox_jobs::DetoxJob;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .create_table(
                Table::create()
                    .table(DetoxScreenshot::Table)
                    .if_not_exists()
                    .col(
                        ColumnDef::new(DetoxScreenshot::Id)
                            .big_integer()
                            .not_null()
                            .auto_increment()
                            .primary_key(),
                    )
                    .col(ColumnDef::new(DetoxScreenshot::JobId).uuid().not_null())
                    .col(
                        ColumnDef::new(DetoxScreenshot::TestFullName)
                            .string()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(DetoxScreenshot::ScreenshotType)
                            .string()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(DetoxScreenshot::FilePath)
                            .string()
                            .not_null(),
                    )
                    .col(
                        ColumnDef::new(DetoxScreenshot::CreatedAt)
                            .timestamp_with_time_zone()
                            .not_null()
                            .default(Expr::current_timestamp()),
                    )
                    .col(ColumnDef::new(DetoxScreenshot::DeletedAt).timestamp_with_time_zone())
                    .foreign_key(
                        ForeignKey::create()
                            .from(DetoxScreenshot::Table, DetoxScreenshot::JobId)
                            .to(DetoxJob::Table, DetoxJob::Id)
                            .on_delete(ForeignKeyAction::Cascade),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_detox_screenshot_job_id")
                    .table(DetoxScreenshot::Table)
                    .col(DetoxScreenshot::JobId)
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_detox_screenshot_test_name")
                    .table(DetoxScreenshot::Table)
                    .col(DetoxScreenshot::TestFullName)
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(DetoxScreenshot::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
enum DetoxScreenshot {
    Table,
    Id,
    JobId,
    TestFullName,
    ScreenshotType,
    FilePath,
    CreatedAt,
    DeletedAt,
}
