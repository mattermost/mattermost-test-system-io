//! Detox job entity representing individual jobs within a test report.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, Eq, DeriveEntityModel)]
#[sea_orm(table_name = "detox_job")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub report_id: Uuid,
    pub job_name: String,
    pub created_at: DateTimeUtc,
    pub tests_count: i32,
    pub passed_count: i32,
    pub failed_count: i32,
    pub skipped_count: i32,
    pub duration_ms: i64,
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
    #[sea_orm(has_many = "super::detox_screenshot::Entity")]
    DetoxScreenshots,
}

impl Related<super::report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Report.def()
    }
}

impl Related<super::detox_screenshot::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::DetoxScreenshots.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
