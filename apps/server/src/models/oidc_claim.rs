//! DTO for OIDC claims in API responses.

use serde::Serialize;
use utoipa::ToSchema;

/// OIDC claims associated with a report (API response DTO).
///
/// Contains all ~29 token claims plus 3 audit fields.
/// JWT numeric fields (exp, iat, nbf) are serialized as strings for
/// uniform JSON handling in admin API responses.
#[derive(Debug, Clone, Serialize, ToSchema)]
#[allow(dead_code)]
pub struct OidcClaimsResponse {
    // --- Standard JWT fields (numeric -> string) ---
    #[serde(skip_serializing_if = "Option::is_none")]
    pub jti: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub iss: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub aud: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub exp: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub iat: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub nbf: Option<String>,

    // --- Identity claims ---
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sub: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_owner: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub actor: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_owner_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository_visibility: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub actor_id: Option<String>,

    // --- Git ref claims ---
    #[serde(rename = "ref", skip_serializing_if = "Option::is_none")]
    pub git_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ref_type: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sha: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub head_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub base_ref: Option<String>,

    // --- Workflow / run claims ---
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

    // --- Environment / runner claims ---
    #[serde(skip_serializing_if = "Option::is_none")]
    pub runner_environment: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment: Option<String>,

    // --- Check / workflow ref claims ---
    #[serde(skip_serializing_if = "Option::is_none")]
    pub check_run_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub job_workflow_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub job_workflow_sha: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub workflow_ref: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub workflow_sha: Option<String>,

    // --- Audit fields (always present) ---
    pub resolved_role: String,
    pub api_path: String,
    pub http_method: String,

    pub created_at: String,
}

impl OidcClaimsResponse {
    /// Convert from SeaORM entity model to API response DTO.
    #[allow(dead_code)]
    pub fn from_entity(m: crate::entity::oidc_claim::Model) -> Self {
        Self {
            // JWT string fields
            jti: m.jti,
            iss: m.iss,
            aud: m.aud,
            // JWT numeric fields -> string
            exp: m.exp.map(|v| v.to_string()),
            iat: m.iat.map(|v| v.to_string()),
            nbf: m.nbf.map(|v| v.to_string()),
            // Identity
            sub: m.sub,
            repository: m.repository,
            repository_owner: m.repository_owner,
            actor: m.actor,
            repository_id: m.repository_id,
            repository_owner_id: m.repository_owner_id,
            repository_visibility: m.repository_visibility,
            actor_id: m.actor_id,
            // Git ref
            git_ref: m.git_ref,
            ref_type: m.ref_type,
            sha: m.sha,
            head_ref: m.head_ref,
            base_ref: m.base_ref,
            // Workflow / run
            workflow: m.workflow,
            event_name: m.event_name,
            run_id: m.run_id,
            run_number: m.run_number,
            run_attempt: m.run_attempt,
            // Environment / runner
            runner_environment: m.runner_environment,
            environment: m.environment,
            // Check / workflow ref
            check_run_id: m.check_run_id,
            job_workflow_ref: m.job_workflow_ref,
            job_workflow_sha: m.job_workflow_sha,
            workflow_ref: m.workflow_ref,
            workflow_sha: m.workflow_sha,
            // Audit
            resolved_role: m.resolved_role,
            api_path: m.api_path,
            http_method: m.http_method,
            created_at: m.created_at.to_rfc3339(),
        }
    }
}
