//! Report OIDC claims entity — stores non-sensitive token claims 1:1 with reports.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, Eq, DeriveEntityModel)]
#[sea_orm(table_name = "report_oidc_claims")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub report_id: Uuid,
    pub sub: Option<String>,
    pub repository: Option<String>,
    pub repository_owner: Option<String>,
    pub actor: Option<String>,
    pub sha: Option<String>,
    #[sea_orm(column_name = "ref")]
    pub git_ref: Option<String>,
    pub ref_type: Option<String>,
    pub workflow: Option<String>,
    pub event_name: Option<String>,
    pub run_id: Option<String>,
    pub run_number: Option<String>,
    pub run_attempt: Option<String>,
    pub head_ref: Option<String>,
    pub base_ref: Option<String>,
    pub resolved_role: String,
    pub api_path: String,
    pub http_method: String,
    pub created_at: DateTimeUtc,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::test_report::Entity",
        from = "Column::ReportId",
        to = "super::test_report::Column::Id"
    )]
    Report,
}

impl Related<super::test_report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Report.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
