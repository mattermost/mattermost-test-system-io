//! TestCase entity for SeaORM.

use sea_orm::entity::prelude::*;
use serde_json::Value as JsonValue;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "test_cases")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub test_suite_id: Uuid,
    pub test_job_id: Uuid,
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
        belongs_to = "super::test_suite::Entity",
        from = "Column::TestSuiteId",
        to = "super::test_suite::Column::Id",
        on_delete = "Cascade"
    )]
    TestSuite,
    #[sea_orm(
        belongs_to = "super::test_job::Entity",
        from = "Column::TestJobId",
        to = "super::test_job::Column::Id",
        on_delete = "Cascade"
    )]
    Job,
}

impl Related<super::test_suite::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::TestSuite.def()
    }
}

impl Related<super::test_job::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Job.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
