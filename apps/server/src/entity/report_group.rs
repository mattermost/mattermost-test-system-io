//! Report group entity for SeaORM.

use sea_orm::entity::prelude::*;
use serde_json::Value as JsonValue;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "report_groups")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub framework: String,
    pub name: String,
    pub status: String,
    pub repository: String,
    pub branch: String,
    pub commit: String,
    pub gh_run_id: String,
    pub gh_run_attempt: String,
    pub gh_pr_number: Option<i32>,
    #[sea_orm(column_type = "JsonBinary", nullable)]
    pub environment_metadata: Option<JsonValue>,
    pub created_at: DateTimeUtc,
    pub updated_at: DateTimeUtc,
    pub deleted_at: Option<DateTimeUtc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(has_many = "super::report::Entity")]
    Reports,
}

impl Related<super::report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Reports.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
