//! Upload file entity for tracking batched file uploads.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, Eq, DeriveEntityModel)]
#[sea_orm(table_name = "upload_file")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub report_id: Uuid,
    pub filename: String,
    pub file_size: Option<i64>,
    pub uploaded_at: Option<DateTimeUtc>,
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
