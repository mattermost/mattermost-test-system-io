//! Job entity for SeaORM.

use sea_orm::entity::prelude::*;
use serde_json::Value as JsonValue;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "test_jobs")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub test_report_id: Uuid,
    pub status: String,
    /// HTML upload status: NULL, started, completed, failed, timedout
    pub html_upload_status: Option<String>,
    /// Screenshots upload status: NULL, started, completed, failed, timedout
    pub screenshots_upload_status: Option<String>,
    /// JSON upload status: NULL, started, completed, failed, timedout
    pub json_upload_status: Option<String>,
    pub html_path: Option<String>,
    #[sea_orm(column_type = "JsonBinary", nullable)]
    pub github_metadata: Option<JsonValue>,
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
        belongs_to = "super::test_report::Entity",
        from = "Column::TestReportId",
        to = "super::test_report::Column::Id",
        on_delete = "Cascade"
    )]
    Report,
    #[sea_orm(has_many = "super::html_file::Entity")]
    HtmlFiles,
    #[sea_orm(has_many = "super::screenshot::Entity")]
    Screenshots,
    #[sea_orm(has_many = "super::json_file::Entity")]
    JsonFiles,
    #[sea_orm(has_many = "super::test_suite::Entity")]
    TestSuites,
}

impl Related<super::test_report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Report.def()
    }
}

impl Related<super::html_file::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::HtmlFiles.def()
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

impl Related<super::test_suite::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::TestSuites.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
