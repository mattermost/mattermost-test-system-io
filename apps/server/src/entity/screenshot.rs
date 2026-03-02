//! Screenshot entity for SeaORM.
//!
//! Tracks screenshots with upload status for request-then-transfer pattern.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "screenshots")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub upload_id: Uuid,
    pub case_id: Option<Uuid>, // linked after extraction

    // File info
    pub filename: String,
    pub s3_key: String,
    pub size_bytes: i64,
    pub content_type: Option<String>,

    // Metadata extracted from path
    pub test_name: String,
    pub sequence: i32,

    // Upload tracking
    pub status: String,
    pub created_at: DateTimeUtc,
    pub updated_at: DateTimeUtc,
    pub uploaded_at: Option<DateTimeUtc>,
    pub deleted_at: Option<DateTimeUtc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::report::Entity",
        from = "Column::UploadId",
        to = "super::report::Column::Id",
        on_delete = "Cascade"
    )]
    Report,
    #[sea_orm(
        belongs_to = "super::case::Entity",
        from = "Column::CaseId",
        to = "super::case::Column::Id",
        on_delete = "SetNull"
    )]
    Case,
}

impl Related<super::report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Report.def()
    }
}

impl Related<super::case::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Case.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
