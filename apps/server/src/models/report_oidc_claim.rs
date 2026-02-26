//! DTO for report OIDC claims in API responses.

use serde::Serialize;
use utoipa::ToSchema;

/// OIDC claims associated with a report (API response DTO).
///
/// Contains only the 13 safe claims (public CI metadata) plus 3 audit fields.
/// Excluded claims (jti, sub, iss, aud, exp, iat, nbf, etc.) are never persisted.
#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ReportOidcClaimsResponse {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sub: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_owner: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub actor: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sha: Option<String>,
    #[serde(rename = "ref", skip_serializing_if = "Option::is_none")]
    pub git_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ref_type: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub workflow: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub event_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_number: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_attempt: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub head_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub base_ref: Option<String>,
    pub resolved_role: String,
    pub api_path: String,
    pub http_method: String,
    pub created_at: String,
}

impl ReportOidcClaimsResponse {
    /// Convert from SeaORM entity model to API response DTO.
    pub fn from_entity(m: crate::entity::report_oidc_claim::Model) -> Self {
        Self {
            sub: m.sub,
            repository: m.repository,
            repository_owner: m.repository_owner,
            actor: m.actor,
            sha: m.sha,
            git_ref: m.git_ref,
            ref_type: m.ref_type,
            workflow: m.workflow,
            event_name: m.event_name,
            run_id: m.run_id,
            run_number: m.run_number,
            run_attempt: m.run_attempt,
            head_ref: m.head_ref,
            base_ref: m.base_ref,
            resolved_role: m.resolved_role,
            api_path: m.api_path,
            http_method: m.http_method,
            created_at: m.created_at.to_rfc3339(),
        }
    }
}
