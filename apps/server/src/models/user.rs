//! User models for GitHub OAuth authentication.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use utoipa::ToSchema;

/// User stored in database.
#[derive(Debug, Clone, Serialize)]
pub struct User {
    pub id: String,
    pub github_id: i64,
    pub username: String,
    pub display_name: Option<String>,
    pub avatar_url: Option<String>,
    pub email: Option<String>,
    pub role: String,
    pub last_login_at: Option<DateTime<Utc>>,
    pub created_at: DateTime<Utc>,
}

/// User info response (returned by /auth/me).
#[derive(Debug, Serialize, ToSchema)]
pub struct UserResponse {
    pub id: String,
    pub username: String,
    pub display_name: Option<String>,
    pub avatar_url: Option<String>,
    pub role: String,
}

impl From<User> for UserResponse {
    fn from(u: User) -> Self {
        Self {
            id: u.id,
            username: u.username,
            display_name: u.display_name,
            avatar_url: u.avatar_url,
            role: u.role,
        }
    }
}

/// GitHub user info from API.
#[derive(Debug, Deserialize)]
pub struct GitHubUserInfo {
    pub id: i64,
    pub login: String,
    pub name: Option<String>,
    pub avatar_url: Option<String>,
    pub email: Option<String>,
}

/// GitHub org info from API.
#[derive(Debug, Deserialize)]
pub struct GitHubOrg {
    pub login: String,
}

/// Session JWT claims.
#[derive(Debug, Serialize, Deserialize)]
pub struct SessionClaims {
    pub sub: String,
    pub iss: String,
    pub exp: usize,
    pub iat: usize,
    pub user_id: String,
    pub username: String,
    pub role: String,
}
