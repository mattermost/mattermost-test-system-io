//! TestSuite entity for SeaORM.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "test_suites")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub test_job_id: Uuid,
    pub title: String,
    pub file_path: Option<String>,
    pub total_count: i32,
    pub passed_count: i32,
    pub failed_count: i32,
    pub skipped_count: i32,
    pub flaky_count: i32,
    pub duration_ms: i32,
    /// Actual test execution start time from framework JSON.
    pub start_time: Option<DateTimeUtc>,
    pub created_at: DateTimeUtc,
    pub updated_at: DateTimeUtc,
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
    #[sea_orm(has_many = "super::test_case::Entity")]
    TestCases,
}

impl Related<super::test_job::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Job.def()
    }
}

impl Related<super::test_case::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::TestCases.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
