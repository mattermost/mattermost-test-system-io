//! GitHub OIDC models for JWT claims and policy management.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use utoipa::ToSchema;

/// GitHub Actions OIDC JWT claims — all ~29 fields.
///
/// Includes every claim GitHub Actions places in the OIDC token so we can
/// persist the full set in `oidc_claims` for audit and correlation.
///
/// See: <https://docs.github.com/en/actions/security-for-github-actions/security-hardening-your-deployments/about-security-hardening-with-openid-connect>
#[derive(Debug, Clone, Deserialize)]
#[allow(dead_code)]
pub struct GitHubOidcClaims {
    // --- Standard JWT fields ---
    /// JWT token ID.
    #[serde(default)]
    pub jti: Option<String>,
    /// Issuer.
    #[serde(default)]
    pub iss: Option<String>,
    /// Audience.
    #[serde(default)]
    pub aud: Option<String>,
    /// Expiration (unix timestamp).
    #[serde(default)]
    pub exp: Option<i64>,
    /// Issued-at (unix timestamp).
    #[serde(default)]
    pub iat: Option<i64>,
    /// Not-before (unix timestamp).
    #[serde(default)]
    pub nbf: Option<i64>,

    // --- GitHub identity claims ---
    /// Subject (e.g., "repo:org/repo:ref:refs/heads/main").
    pub sub: String,
    /// Repository (e.g., "octo-org/octo-repo").
    pub repository: String,
    /// Repository owner (e.g., "octo-org").
    pub repository_owner: String,
    /// Actor (GitHub username who triggered the workflow).
    pub actor: String,
    /// Repository numeric ID.
    #[serde(default)]
    pub repository_id: Option<String>,
    /// Repository owner numeric ID.
    #[serde(default)]
    pub repository_owner_id: Option<String>,
    /// Repository visibility ("public", "private", "internal").
    #[serde(default)]
    pub repository_visibility: Option<String>,
    /// Actor numeric ID.
    #[serde(default)]
    pub actor_id: Option<String>,

    // --- Git ref claims ---
    /// Git ref (e.g., "refs/heads/main") -- kept as-is, not stripped.
    #[serde(rename = "ref", default)]
    pub git_ref: Option<String>,
    /// Ref type ("branch" or "tag").
    #[serde(default)]
    pub ref_type: Option<String>,
    /// Commit SHA.
    #[serde(default)]
    pub sha: Option<String>,
    /// Head ref (source branch for PRs, empty otherwise).
    #[serde(default)]
    pub head_ref: Option<String>,
    /// Base ref (target branch for PRs, empty otherwise).
    #[serde(default)]
    pub base_ref: Option<String>,

    // --- Workflow / run claims ---
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

    // --- Environment / runner claims ---
    /// Runner environment (e.g., "github-hosted").
    #[serde(default)]
    pub runner_environment: Option<String>,
    /// Deployment environment name (if the job references an environment).
    #[serde(default)]
    pub environment: Option<String>,

    // --- Check / workflow ref claims ---
    /// Check run ID.
    #[serde(default)]
    pub check_run_id: Option<String>,
    /// Job workflow ref (reusable workflow ref).
    #[serde(default)]
    pub job_workflow_ref: Option<String>,
    /// Job workflow SHA.
    #[serde(default)]
    pub job_workflow_sha: Option<String>,
    /// Workflow ref.
    #[serde(default)]
    pub workflow_ref: Option<String>,
    /// Workflow SHA.
    #[serde(default)]
    pub workflow_sha: Option<String>,
}

impl GitHubOidcClaims {
    /// Convert all claims to the safe persistence struct that maps 1:1 to
    /// the `oidc_claims` table columns.
    #[allow(dead_code)]
    pub fn to_safe_claims(
        &self,
        resolved_role: &str,
        api_path: &str,
        http_method: &str,
    ) -> SafeOidcClaims {
        SafeOidcClaims {
            // Standard JWT
            jti: self.jti.clone(),
            iss: self.iss.clone(),
            aud: self.aud.clone(),
            exp: self.exp,
            iat: self.iat,
            nbf: self.nbf,
            // Identity
            sub: Some(self.sub.clone()),
            repository: Some(self.repository.clone()),
            repository_owner: Some(self.repository_owner.clone()),
            actor: Some(self.actor.clone()),
            repository_id: self.repository_id.clone(),
            repository_owner_id: self.repository_owner_id.clone(),
            repository_visibility: self.repository_visibility.clone(),
            actor_id: self.actor_id.clone(),
            // Git ref
            git_ref: self.git_ref.clone(),
            ref_type: self.ref_type.clone(),
            sha: self.sha.clone(),
            head_ref: self.head_ref.clone(),
            base_ref: self.base_ref.clone(),
            // Workflow / run
            workflow: self.workflow.clone(),
            event_name: self.event_name.clone(),
            run_id: self.run_id.clone(),
            run_number: self.run_number.clone(),
            run_attempt: self.run_attempt.clone(),
            // Environment / runner
            runner_environment: self.runner_environment.clone(),
            environment: self.environment.clone(),
            // Check / workflow ref
            check_run_id: self.check_run_id.clone(),
            job_workflow_ref: self.job_workflow_ref.clone(),
            job_workflow_sha: self.job_workflow_sha.clone(),
            workflow_ref: self.workflow_ref.clone(),
            workflow_sha: self.workflow_sha.clone(),
            // Audit
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

/// All OIDC claims for persistence in `oidc_claims` table.
///
/// Maps 1:1 to the `oidc_claims` table columns: all ~29 token claims
/// plus 3 audit fields (resolved_role, api_path, http_method).
#[derive(Debug, Clone)]
#[allow(dead_code)]
pub struct SafeOidcClaims {
    // Standard JWT fields
    pub jti: Option<String>,
    pub iss: Option<String>,
    pub aud: Option<String>,
    pub exp: Option<i64>,
    pub iat: Option<i64>,
    pub nbf: Option<i64>,
    // Identity
    pub sub: Option<String>,
    pub repository: Option<String>,
    pub repository_owner: Option<String>,
    pub actor: Option<String>,
    pub repository_id: Option<String>,
    pub repository_owner_id: Option<String>,
    pub repository_visibility: Option<String>,
    pub actor_id: Option<String>,
    // Git ref
    pub git_ref: Option<String>,
    pub ref_type: Option<String>,
    pub sha: Option<String>,
    pub head_ref: Option<String>,
    pub base_ref: Option<String>,
    // Workflow / run
    pub workflow: Option<String>,
    pub event_name: Option<String>,
    pub run_id: Option<String>,
    pub run_number: Option<String>,
    pub run_attempt: Option<String>,
    // Environment / runner
    pub runner_environment: Option<String>,
    pub environment: Option<String>,
    // Check / workflow ref
    pub check_run_id: Option<String>,
    pub job_workflow_ref: Option<String>,
    pub job_workflow_sha: Option<String>,
    pub workflow_ref: Option<String>,
    pub workflow_sha: Option<String>,
    // Audit fields (NOT optional)
    pub resolved_role: String,
    pub api_path: String,
    pub http_method: String,
}
