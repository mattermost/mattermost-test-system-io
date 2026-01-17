//! Report entity for uploaded test reports.

use sea_orm::entity::prelude::*;
use serde_json::Value as JsonValue;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "report")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub created_at: DateTimeUtc,
    pub deleted_at: Option<DateTimeUtc>,
    pub extraction_status: String,
    pub file_path: String,
    pub error_message: Option<String>,
    pub has_files: bool,
    pub files_deleted_at: Option<DateTimeUtc>,
    pub framework: Option<String>,
    pub framework_version: Option<String>,
    pub platform: Option<String>,
    #[sea_orm(column_type = "JsonBinary", nullable)]
    pub github_context: Option<JsonValue>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(has_one = "super::report_stats::Entity")]
    ReportStats,
    #[sea_orm(has_many = "super::test_suite::Entity")]
    TestSuites,
    #[sea_orm(has_many = "super::detox_job::Entity")]
    DetoxJobs,
    #[sea_orm(has_many = "super::upload_file::Entity")]
    UploadFiles,
}

impl Related<super::report_stats::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::ReportStats.def()
    }
}

impl Related<super::test_suite::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::TestSuites.def()
    }
}

impl Related<super::detox_job::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::DetoxJobs.def()
    }
}

impl Related<super::upload_file::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::UploadFiles.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
