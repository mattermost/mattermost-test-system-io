//! GitHub OIDC models for JWT claims and policy management.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use utoipa::ToSchema;

/// GitHub Actions OIDC JWT claims — safe subset only.
///
/// Contains only the 14 claims we persist plus fields required for
/// authentication/authorization.
/// Excluded claims (jti, iss, aud, exp, iat, nbf, repository_visibility,
/// repository_id, repository_owner_id, actor_id, runner_environment,
/// job_workflow_ref, workflow_ref, workflow_sha) are silently ignored
/// during deserialization and never stored.
///
/// See: <https://docs.github.com/en/actions/security-for-github-actions/security-hardening-your-deployments/about-security-hardening-with-openid-connect>
#[derive(Debug, Clone, Deserialize)]
pub struct GitHubOidcClaims {
    /// Subject (e.g., "repo:org/repo:ref:refs/heads/main").
    pub sub: String,
    /// Repository (e.g., "octo-org/octo-repo").
    pub repository: String,
    /// Repository owner (e.g., "octo-org").
    pub repository_owner: String,
    /// Actor (GitHub username who triggered the workflow).
    pub actor: String,
    /// Git ref (e.g., "refs/heads/main") — kept as-is, not stripped.
    #[serde(rename = "ref", default)]
    pub git_ref: Option<String>,
    /// Ref type ("branch" or "tag").
    #[serde(default)]
    pub ref_type: Option<String>,
    /// Commit SHA.
    #[serde(default)]
    pub sha: Option<String>,
    /// Workflow name.
    #[serde(default)]
    pub workflow: Option<String>,
    /// Event name (e.g., "push", "pull_request", "workflow_dispatch").
    #[serde(default)]
    pub event_name: Option<String>,
    /// Run ID.
    #[serde(default)]
    pub run_id: Option<String>,
    /// Run number (increments per workflow).
    #[serde(default)]
    pub run_number: Option<String>,
    /// Run attempt number.
    #[serde(default)]
    pub run_attempt: Option<String>,
    /// Head ref (source branch for PRs, empty otherwise).
    #[serde(default)]
    pub head_ref: Option<String>,
    /// Base ref (target branch for PRs, empty otherwise).
    #[serde(default)]
    pub base_ref: Option<String>,
}

impl GitHubOidcClaims {
    /// Extract only the 13 safe, non-sensitive claims for persistence.
    ///
    /// Excluded: jti, sub, iss, aud, exp, iat, nbf, repository_visibility,
    /// environment, runner_environment, repository_id, repository_owner_id,
    /// actor_id, workflow_ref, workflow_sha, job_workflow_ref, job_workflow_sha.
    pub fn to_safe_claims(
        &self,
        resolved_role: &str,
        api_path: &str,
        http_method: &str,
    ) -> SafeOidcClaims {
        SafeOidcClaims {
            sub: Some(self.sub.clone()),
            repository: Some(self.repository.clone()),
            repository_owner: Some(self.repository_owner.clone()),
            actor: Some(self.actor.clone()),
            sha: self.sha.clone(),
            git_ref: self.git_ref.clone(),
            ref_type: self.ref_type.clone(),
            workflow: self.workflow.clone(),
            event_name: self.event_name.clone(),
            run_id: self.run_id.clone(),
            run_number: self.run_number.clone(),
            run_attempt: self.run_attempt.clone(),
            head_ref: self.head_ref.clone(),
            base_ref: self.base_ref.clone(),
            resolved_role: resolved_role.to_string(),
            api_path: api_path.to_string(),
            http_method: http_method.to_string(),
        }
    }
}

/// OIDC policy stored in database.
#[derive(Debug, Clone, Serialize)]
pub struct OidcPolicy {
    pub id: String,
    pub repository_pattern: String,
    pub role: String,
    pub description: Option<String>,
    pub enabled: bool,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

/// Request to create an OIDC policy.
#[derive(Debug, Deserialize, ToSchema)]
pub struct CreateOidcPolicyRequest {
    /// Repository pattern (e.g., "org/repo" or "org/*")
    pub repository_pattern: String,
    /// Role to assign (admin, contributor, viewer). Default: contributor
    #[serde(default)]
    pub role: Option<String>,
    /// Description of the policy
    #[serde(default)]
    pub description: Option<String>,
}

/// Request to update an OIDC policy.
#[derive(Debug, Deserialize, ToSchema)]
pub struct UpdateOidcPolicyRequest {
    /// Repository pattern
    #[serde(default)]
    pub repository_pattern: Option<String>,
    /// Role
    #[serde(default)]
    pub role: Option<String>,
    /// Description
    #[serde(default)]
    pub description: Option<String>,
    /// Enabled
    #[serde(default)]
    pub enabled: Option<bool>,
}

/// OIDC policy response.
#[derive(Debug, Serialize, ToSchema)]
pub struct OidcPolicyResponse {
    pub id: String,
    pub repository_pattern: String,
    pub role: String,
    pub description: Option<String>,
    pub enabled: bool,
    pub created_at: String,
    pub updated_at: String,
}

impl From<OidcPolicy> for OidcPolicyResponse {
    fn from(p: OidcPolicy) -> Self {
        Self {
            id: p.id,
            repository_pattern: p.repository_pattern,
            role: p.role,
            description: p.description,
            enabled: p.enabled,
            created_at: p.created_at.to_rfc3339(),
            updated_at: p.updated_at.to_rfc3339(),
        }
    }
}

/// OIDC policy list response.
#[derive(Debug, Serialize, ToSchema)]
pub struct OidcPolicyListResponse {
    pub policies: Vec<OidcPolicyResponse>,
}

/// Safe subset of OIDC claims for persistence (13 claims + 3 audit fields).
#[derive(Debug, Clone)]
pub struct SafeOidcClaims {
    pub sub: Option<String>,
    pub repository: Option<String>,
    pub repository_owner: Option<String>,
    pub actor: Option<String>,
    pub sha: Option<String>,
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
}
