//! Case entity for SeaORM.

use sea_orm::entity::prelude::*;
use serde_json::Value as JsonValue;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "cases")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub suite_id: Uuid,
    pub upload_id: Uuid,
    pub title: String,
    pub full_title: String,
    pub status: String,
    pub duration_ms: i32,
    pub retry_count: i32,
    pub error_message: Option<String>,
    pub sequence: i32,
    #[sea_orm(column_type = "JsonBinary", nullable)]
    pub attachments: Option<JsonValue>,
    pub created_at: DateTimeUtc,
    pub updated_at: DateTimeUtc,
    pub deleted_at: Option<DateTimeUtc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::suite::Entity",
        from = "Column::SuiteId",
        to = "super::suite::Column::Id",
        on_delete = "Cascade"
    )]
    Suite,
    #[sea_orm(
        belongs_to = "super::report::Entity",
        from = "Column::UploadId",
        to = "super::report::Column::Id",
        on_delete = "Cascade"
    )]
    Report,
}

impl Related<super::suite::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Suite.def()
    }
}

impl Related<super::report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Report.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
