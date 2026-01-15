//! GitHub Actions context model for CI/CD metadata.

use serde::{Deserialize, Serialize};
use utoipa::ToSchema;

/// GitHub Actions context metadata.
/// All fields are optional to support various CI scenarios.
#[derive(Debug, Clone, Default, Serialize, Deserialize, ToSchema)]
pub struct GitHubContext {
    /// Repository in "owner/repo" format (from GITHUB_REPOSITORY)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub repository: Option<String>,

    /// Branch name (from GITHUB_HEAD_REF for PRs, or extracted from GITHUB_REF)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub branch: Option<String>,

    /// Full commit SHA (from GITHUB_SHA)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub commit_sha: Option<String>,

    /// PR number (extracted from GITHUB_REF for pull_request events)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr_number: Option<u64>,

    /// PR author username (from github.event.pull_request.user.login)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pr_author: Option<String>,

    /// Workflow run ID (from GITHUB_RUN_ID)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_id: Option<u64>,

    /// Run attempt number (from GITHUB_RUN_ATTEMPT)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub run_attempt: Option<u32>,
}

impl GitHubContext {
    /// Check if the context has any data.
    pub fn is_empty(&self) -> bool {
        self.repository.is_none()
            && self.branch.is_none()
            && self.commit_sha.is_none()
            && self.pr_number.is_none()
            && self.pr_author.is_none()
            && self.run_id.is_none()
            && self.run_attempt.is_none()
    }

    /// Serialize to JSON string for database storage.
    pub fn to_json_string(&self) -> Option<String> {
        if self.is_empty() {
            None
        } else {
            serde_json::to_string(self).ok()
        }
    }

    /// Deserialize from JSON string from database.
    pub fn from_json_string(s: &str) -> Option<Self> {
        serde_json::from_str(s).ok()
    }
}
