//! Report JSON entity for storing extraction JSON data as JSONB.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, Eq, DeriveEntityModel)]
#[sea_orm(table_name = "report_json")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub report_id: Uuid,
    pub file_type: String,
    #[sea_orm(column_type = "JsonBinary")]
    pub data: serde_json::Value,
    pub created_at: DateTimeUtc,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::report::Entity",
        from = "Column::ReportId",
        to = "super::report::Column::Id",
        on_delete = "Cascade"
    )]
    Report,
}

impl Related<super::report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Report.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
