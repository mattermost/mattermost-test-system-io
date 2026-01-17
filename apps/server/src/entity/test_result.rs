//! Test result entity representing individual test execution results.

use sea_orm::entity::prelude::*;
use serde_json::Value as JsonValue;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "test_result")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub spec_id: i64,
    pub status: String,
    pub duration_ms: i64,
    pub retry: i32,
    pub start_time: DateTimeUtc,
    pub project_id: String,
    pub project_name: String,
    #[sea_orm(column_type = "JsonBinary", nullable)]
    pub errors_json: Option<JsonValue>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::test_spec::Entity",
        from = "Column::SpecId",
        to = "super::test_spec::Column::Id",
        on_delete = "Cascade"
    )]
    TestSpec,
}

impl Related<super::test_spec::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::TestSpec.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
