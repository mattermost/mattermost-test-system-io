//! OIDC claims entity -- stores non-sensitive token claims 1:1 with uploads.

use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, Eq, DeriveEntityModel)]
#[sea_orm(table_name = "oidc_claims")]
#[allow(dead_code)]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub id: Uuid,
    pub upload_id: Uuid,

    // --- 14 existing OIDC claims ---
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

    // --- 11 new OIDC claims ---
    pub repository_id: Option<String>,
    pub repository_owner_id: Option<String>,
    pub repository_visibility: Option<String>,
    pub actor_id: Option<String>,
    pub runner_environment: Option<String>,
    pub environment: Option<String>,
    pub check_run_id: Option<String>,
    pub job_workflow_ref: Option<String>,
    pub job_workflow_sha: Option<String>,
    pub workflow_ref: Option<String>,
    pub workflow_sha: Option<String>,

    // --- JWT string fields ---
    pub jti: Option<String>,
    pub iss: Option<String>,
    pub aud: Option<String>,

    // --- JWT numeric fields ---
    pub exp: Option<i64>,
    pub iat: Option<i64>,
    pub nbf: Option<i64>,

    // --- Audit fields (NOT optional) ---
    pub resolved_role: String,
    pub api_path: String,
    pub http_method: String,

    pub created_at: DateTimeUtc,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
#[allow(dead_code)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::report::Entity",
        from = "Column::UploadId",
        to = "super::report::Column::Id"
    )]
    Report,
}

impl Related<super::report::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Report.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
