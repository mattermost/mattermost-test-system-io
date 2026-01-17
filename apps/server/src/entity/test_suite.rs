//! Test suite entity representing test files.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, Eq, DeriveEntityModel)]
#[sea_orm(table_name = "test_suite")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub report_id: Uuid,
    pub title: String,
    pub file_path: String,
    pub specs_count: Option<i32>,
    pub passed_count: Option<i32>,
    pub failed_count: Option<i32>,
    pub flaky_count: Option<i32>,
    pub skipped_count: Option<i32>,
    pub duration_ms: Option<i64>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::report::Entity",
        from = "Column::ReportId",
        to = "super::report::Column::Id",
        on_delete = "Cascade"
    )]
    Report,
    #[sea_orm(has_many = "super::test_spec::Entity")]
    TestSpecs,
}

impl Related<super::report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Report.def()
    }
}

impl Related<super::test_spec::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::TestSpecs.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
