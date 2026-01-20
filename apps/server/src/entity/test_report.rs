//! Report entity for SeaORM.

use sea_orm::entity::prelude::*;
use serde_json::Value as JsonValue;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "test_reports")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub expected_jobs: i32,
    pub framework: String,
    pub status: String,
    #[sea_orm(column_type = "JsonBinary", nullable)]
    pub github_metadata: Option<JsonValue>,
    pub created_at: DateTimeUtc,
    pub updated_at: DateTimeUtc,
    pub deleted_at: Option<DateTimeUtc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(has_many = "super::test_job::Entity")]
    Jobs,
}

impl Related<super::test_job::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Jobs.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
