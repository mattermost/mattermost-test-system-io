//! Test spec entity representing individual test specifications.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, Eq, DeriveEntityModel)]
#[sea_orm(table_name = "test_spec")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub suite_id: i64,
    pub title: String,
    pub ok: bool,
    pub full_title: String,
    pub file_path: String,
    pub line: i32,
    #[sea_orm(column_name = "col")]
    pub column: i32,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::test_suite::Entity",
        from = "Column::SuiteId",
        to = "super::test_suite::Column::Id",
        on_delete = "Cascade"
    )]
    TestSuite,
    #[sea_orm(has_many = "super::test_result::Entity")]
    TestResults,
}

impl Related<super::test_suite::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::TestSuite.def()
    }
}

impl Related<super::test_result::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::TestResults.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
