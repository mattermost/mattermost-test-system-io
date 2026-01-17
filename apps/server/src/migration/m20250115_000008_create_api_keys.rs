//! Create api_key table.

use sea_orm_migration::prelude::*;

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .create_table(
                Table::create()
                    .table(ApiKey::Table)
                    .if_not_exists()
                    .col(ColumnDef::new(ApiKey::Id).uuid().not_null().primary_key())
                    .col(
                        ColumnDef::new(ApiKey::KeyHash)
                            .string()
                            .not_null()
                            .unique_key(),
                    )
                    .col(ColumnDef::new(ApiKey::KeyPrefix).string().not_null())
                    .col(ColumnDef::new(ApiKey::Name).string().not_null())
                    .col(
                        ColumnDef::new(ApiKey::Role)
                            .string()
                            .not_null()
                            .default("contributor"),
                    )
                    .col(ColumnDef::new(ApiKey::ExpiresAt).timestamp_with_time_zone())
                    .col(ColumnDef::new(ApiKey::LastUsedAt).timestamp_with_time_zone())
                    .col(
                        ColumnDef::new(ApiKey::CreatedAt)
                            .timestamp_with_time_zone()
                            .not_null()
                            .default(Expr::current_timestamp()),
                    )
                    .col(ColumnDef::new(ApiKey::DeletedAt).timestamp_with_time_zone())
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_api_key_hash")
                    .table(ApiKey::Table)
                    .col(ApiKey::KeyHash)
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(
                Index::create()
                    .name("idx_api_key_prefix")
                    .table(ApiKey::Table)
                    .col(ApiKey::KeyPrefix)
                    .to_owned(),
            )
            .await
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(ApiKey::Table).to_owned())
            .await
    }
}

#[derive(DeriveIden)]
enum ApiKey {
    Table,
    Id,
    KeyHash,
    KeyPrefix,
    Name,
    Role,
    ExpiresAt,
    LastUsedAt,
    CreatedAt,
    DeletedAt,
}
