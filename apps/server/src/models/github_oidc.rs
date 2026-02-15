//! GitHub OIDC models for JWT claims and policy management.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use utoipa::ToSchema;

/// GitHub Actions OIDC JWT claims.
///
/// All fields match the claim names from GitHub's OIDC token:
/// <https://docs.github.com/en/actions/security-for-github-actions/security-hardening-your-deployments/about-security-hardening-with-openid-connect#understanding-the-oidc-token>
#[derive(Debug, Clone, Deserialize)]
pub struct GitHubOidcClaims {
    /// Subject (e.g., "repo:org/repo:ref:refs/heads/main").
    pub sub: String,
    /// Repository (e.g., "octo-org/octo-repo").
    pub repository: String,
    /// Repository owner (e.g., "octo-org").
    pub repository_owner: String,
    /// Repository owner numeric ID.
    #[serde(default)]
    pub repository_owner_id: Option<String>,
    /// Repository visibility ("public", "private", "internal").
    #[serde(default)]
    pub repository_visibility: Option<String>,
    /// Repository numeric ID.
    #[serde(default)]
    pub repository_id: Option<String>,
    /// Actor (GitHub username who triggered the workflow).
    pub actor: String,
    /// Actor numeric ID.
    #[serde(default)]
    pub actor_id: Option<String>,
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
    /// Runner environment (e.g., "github-hosted").
    #[serde(default)]
    pub runner_environment: Option<String>,
    /// Head ref (source branch for PRs, empty otherwise).
    #[serde(default)]
    pub head_ref: Option<String>,
    /// Base ref (target branch for PRs, empty otherwise).
    #[serde(default)]
    pub base_ref: Option<String>,
    /// Full workflow ref (e.g., "org/repo/.github/workflows/ci.yml@refs/heads/main").
    #[serde(default)]
    pub job_workflow_ref: Option<String>,
}

impl GitHubOidcClaims {
    /// Convert OIDC claims to report-level `GitHubMetadata`.
    ///
    /// All claim values are transferred as-is (no stripping or parsing).
    pub fn to_github_metadata(&self) -> super::report::GitHubMetadata {
        super::report::GitHubMetadata {
            sub: Some(self.sub.clone()),
            repository: Some(self.repository.clone()),
            repository_owner: Some(self.repository_owner.clone()),
            repository_owner_id: self.repository_owner_id.clone(),
            repository_visibility: self.repository_visibility.clone(),
            repository_id: self.repository_id.clone(),
            actor: Some(self.actor.clone()),
            actor_id: self.actor_id.clone(),
            git_ref: self.git_ref.clone(),
            ref_type: self.ref_type.clone(),
            sha: self.sha.clone(),
            workflow: self.workflow.clone(),
            event_name: self.event_name.clone(),
            run_id: self.run_id.clone(),
            run_number: self.run_number.clone(),
            run_attempt: self.run_attempt.clone(),
            runner_environment: self.runner_environment.clone(),
            head_ref: self.head_ref.clone(),
            base_ref: self.base_ref.clone(),
            job_workflow_ref: self.job_workflow_ref.clone(),
            pr_number: None, // not available in OIDC claims
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
