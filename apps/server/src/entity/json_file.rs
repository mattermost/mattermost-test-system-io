//! JsonFile entity for SeaORM.
//!
//! Tracks JSON file uploads for test data extraction.
//! JSON files contain rich test data per framework (Cypress, Detox, Playwright).

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "json_files")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub test_job_id: Uuid,
    pub filename: String,
    pub s3_key: String,
    pub size_bytes: i64,
    pub content_type: Option<String>,
    pub status: String,
    /// When data was extracted from this file
    pub extracted_at: Option<DateTimeUtc>,
    /// Error message if extraction failed
    pub extraction_error: Option<String>,
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
