//! Report entity for SeaORM (individual uploaded report, formerly "upload").

use sea_orm::entity::prelude::*;
use serde_json::Value as JsonValue;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "reports")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub report_group_id: Uuid,
    pub name: String,
    pub status: String,
    /// Screenshots upload status: NULL, started, completed, failed, timedout
    pub screenshots_upload_status: Option<String>,
    /// JSON upload status: NULL, started, completed, failed, timedout
    pub json_upload_status: Option<String>,
    pub gh_job_id: Option<String>,
    pub gh_job_name: Option<String>,
    #[sea_orm(column_type = "JsonBinary", nullable)]
    pub environment: Option<JsonValue>,
    pub error_message: Option<String>,
    /// Duration in milliseconds extracted from JSON files
    pub duration_ms: Option<i64>,
    /// Start time extracted from JSON files
    pub start_time: Option<DateTimeUtc>,
    pub created_at: DateTimeUtc,
    pub updated_at: DateTimeUtc,
    pub deleted_at: Option<DateTimeUtc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::report_group::Entity",
        from = "Column::ReportGroupId",
        to = "super::report_group::Column::Id",
        on_delete = "Cascade"
    )]
    ReportGroup,
    #[sea_orm(has_many = "super::screenshot::Entity")]
    Screenshots,
    #[sea_orm(has_many = "super::json_file::Entity")]
    JsonFiles,
    #[sea_orm(has_many = "super::suite::Entity")]
    Suites,
}

impl Related<super::report_group::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::ReportGroup.def()
    }
}

impl Related<super::screenshot::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Screenshots.def()
    }
}

impl Related<super::json_file::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::JsonFiles.def()
    }
}

impl Related<super::suite::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Suites.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
