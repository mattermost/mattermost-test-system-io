//! HtmlFile entity for SeaORM.
//!
//! Tracks HTML report file uploads with request-then-transfer pattern.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "html_files")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub test_job_id: Uuid,
    pub filename: String,
    pub s3_key: String,
    pub size_bytes: i64,
    pub content_type: Option<String>,
    pub status: String,
    pub created_at: DateTimeUtc,
    pub updated_at: DateTimeUtc,
    pub uploaded_at: Option<DateTimeUtc>,
    pub deleted_at: Option<DateTimeUtc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::test_job::Entity",
        from = "Column::TestJobId",
        to = "super::test_job::Column::Id",
        on_delete = "Cascade"
    )]
    Job,
}

impl Related<super::test_job::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Job.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
