//! Report statistics entity.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, Eq, DeriveEntityModel)]
#[sea_orm(table_name = "report_stat")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    #[sea_orm(unique)]
    pub report_id: Uuid,
    pub start_time: DateTimeUtc,
    pub duration_ms: i64,
    pub expected: i32,
    pub skipped: i32,
    pub unexpected: i32,
    pub flaky: i32,
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
}

impl Related<super::report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Report.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
