//! API Key model for authentication.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

/// API Key roles for future RBAC.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum ApiKeyRole {
    Admin,
    #[default]
    Contributor,
    Viewer,
}

impl ApiKeyRole {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Admin => "admin",
            Self::Contributor => "contributor",
            Self::Viewer => "viewer",
        }
    }

    pub fn parse(s: &str) -> Option<Self> {
        match s.to_lowercase().as_str() {
            "admin" => Some(Self::Admin),
            "contributor" => Some(Self::Contributor),
            "viewer" => Some(Self::Viewer),
            _ => None,
        }
    }
}

impl std::fmt::Display for ApiKeyRole {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// API Key stored in database.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ApiKey {
    /// Unique identifier (UUID)
    pub id: String,
    /// SHA-256 hash of the full key
    pub key_hash: String,
    /// First 8 characters of the key for identification
    pub key_prefix: String,
    /// Human-readable name (e.g., "CI - GitHub Actions")
    pub name: String,
    /// Role for future RBAC
    pub role: String,
    /// Expiration timestamp (optional)
    pub expires_at: Option<DateTime<Utc>>,
    /// Last used timestamp
    pub last_used_at: Option<DateTime<Utc>>,
    /// Creation timestamp
    pub created_at: DateTime<Utc>,
    /// Soft delete timestamp (revoked)
    pub deleted_at: Option<DateTime<Utc>>,
}

impl ApiKey {
    /// Check if the key is revoked.
    pub fn is_revoked(&self) -> bool {
        self.deleted_at.is_some()
    }

    /// Check if the key is expired.
    pub fn is_expired(&self) -> bool {
        if let Some(expires_at) = self.expires_at {
            Utc::now() > expires_at
        } else {
            false
        }
    }

    /// Get the role as enum.
    pub fn role_enum(&self) -> ApiKeyRole {
        ApiKeyRole::parse(&self.role).unwrap_or_default()
    }
}

/// Response when creating a new API key (includes the full key).
#[derive(Debug, Serialize)]
pub struct ApiKeyCreateResponse {
    pub id: String,
    pub key: String, // Full key - only shown once
    pub name: String,
    pub role: String,
    pub expires_at: Option<String>,
    pub created_at: String,
}

/// Response for listing API keys (key masked).
#[derive(Debug, Serialize)]
pub struct ApiKeyListItem {
    pub id: String,
    pub key_prefix: String,
    pub name: String,
    pub role: String,
    pub expires_at: Option<String>,
    pub last_used_at: Option<String>,
    pub created_at: String,
    pub is_revoked: bool,
}

impl From<ApiKey> for ApiKeyListItem {
    fn from(key: ApiKey) -> Self {
        Self {
            id: key.id,
            key_prefix: key.key_prefix,
            name: key.name,
            role: key.role,
            expires_at: key.expires_at.map(|d| d.to_rfc3339()),
            last_used_at: key.last_used_at.map(|d| d.to_rfc3339()),
            created_at: key.created_at.to_rfc3339(),
            is_revoked: key.deleted_at.is_some(),
        }
    }
}

/// Request to create a new API key.
#[derive(Debug, Deserialize)]
pub struct CreateApiKeyRequest {
    pub name: String,
    #[serde(default)]
    pub role: Option<String>,
    /// Expiration duration (e.g., "365d", "30d", "1y")
    #[serde(default)]
    pub expires_in: Option<String>,
}

/// Authenticated caller information extracted from API key.
#[derive(Debug, Clone)]
pub struct AuthenticatedCaller {
    pub key_id: String,
    pub name: String,
    pub key_prefix: String,
    pub role: ApiKeyRole,
}

impl AuthenticatedCaller {
    /// Check if the caller has admin role.
    pub fn is_admin(&self) -> bool {
        matches!(self.role, ApiKeyRole::Admin)
    }
}
