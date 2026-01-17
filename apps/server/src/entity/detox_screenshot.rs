//! Detox screenshot entity representing screenshots captured during test execution.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, Eq, DeriveEntityModel)]
#[sea_orm(table_name = "detox_screenshot")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub job_id: Uuid,
    pub test_full_name: String,
    pub screenshot_type: String,
    pub file_path: String,
    pub created_at: DateTimeUtc,
    pub deleted_at: Option<DateTimeUtc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::detox_job::Entity",
        from = "Column::JobId",
        to = "super::detox_job::Column::Id",
        on_delete = "Cascade"
    )]
    DetoxJob,
}

impl Related<super::detox_job::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::DetoxJob.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
