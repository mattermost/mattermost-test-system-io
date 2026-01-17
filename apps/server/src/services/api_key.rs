//! API Key service for generation, verification, and management.

use chrono::{Duration, Utc};
use rand::Rng;
use sha2::{Digest, Sha256};

use crate::db::{DbPool, api_keys as db};
use crate::error::{AppError, AppResult};
use crate::models::{ApiKey, ApiKeyRole, AuthenticatedCaller};

/// API key prefix.
const KEY_PREFIX: &str = "rrv_";
/// Length of random part of the key.
const KEY_RANDOM_LENGTH: usize = 32;
/// Length of the key prefix stored for identification.
const KEY_PREFIX_LENGTH: usize = 8;

/// Generate a new random API key.
///
/// Returns the full key (to be shown to user once) and the key data for storage.
pub fn generate_key(
    name: &str,
    role: ApiKeyRole,
    expires_in: Option<&str>,
) -> AppResult<(String, ApiKey)> {
    // Generate random key
    let random_part: String = rand::thread_rng()
        .sample_iter(&rand::distributions::Alphanumeric)
        .take(KEY_RANDOM_LENGTH)
        .map(char::from)
        .collect();

    let full_key = format!("{}{}", KEY_PREFIX, random_part);

    // Hash the key for storage
    let key_hash = hash_key(&full_key);

    // Extract prefix for identification (first 8 chars of full key)
    let key_prefix = full_key.chars().take(KEY_PREFIX_LENGTH).collect::<String>();

    // Parse expiration
    let expires_at = expires_in.and_then(parse_duration).map(|d| Utc::now() + d);

    let api_key = ApiKey {
        id: uuid::Uuid::new_v4().to_string(),
        key_hash,
        key_prefix,
        name: name.to_string(),
        role: role.as_str().to_string(),
        expires_at,
        last_used_at: None,
        created_at: Utc::now(),
        deleted_at: None,
    };

    Ok((full_key, api_key))
}

/// Hash an API key using SHA-256.
pub fn hash_key(key: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(key.as_bytes());
    hex::encode(hasher.finalize())
}

/// Parse a duration string like "365d", "30d", "1y", "6m".
fn parse_duration(s: &str) -> Option<Duration> {
    let s = s.trim().to_lowercase();

    if let Some(days) = s.strip_suffix('d') {
        days.parse::<i64>().ok().and_then(Duration::try_days)
    } else if let Some(years) = s.strip_suffix('y') {
        years
            .parse::<i64>()
            .ok()
            .and_then(|y| Duration::try_days(y * 365))
    } else if let Some(months) = s.strip_suffix('m') {
        months
            .parse::<i64>()
            .ok()
            .and_then(|m| Duration::try_days(m * 30))
    } else if let Some(weeks) = s.strip_suffix('w') {
        weeks.parse::<i64>().ok().and_then(Duration::try_weeks)
    } else {
        // Try parsing as days by default
        s.parse::<i64>().ok().and_then(Duration::try_days)
    }
}

/// Verify an API key and return the authenticated caller.
pub async fn verify_key(pool: &DbPool, key: &str) -> AppResult<AuthenticatedCaller> {
    let key_hash = hash_key(key);
    let conn = pool.connection();

    // Look up by hash
    let api_key = db::find_by_hash(conn, &key_hash)
        .await?
        .ok_or_else(|| AppError::Unauthorized("Invalid API key".to_string()))?;

    // Check if revoked
    if api_key.is_revoked() {
        return Err(AppError::Unauthorized(
            "API key has been revoked".to_string(),
        ));
    }

    // Check if expired
    if api_key.is_expired() {
        return Err(AppError::Unauthorized("API key has expired".to_string()));
    }

    // Update last used timestamp (fire and forget)
    let _ = db::update_last_used(conn, &api_key.id).await;

    // Extract role before moving other fields
    let role = api_key.role_enum();

    Ok(AuthenticatedCaller {
        key_id: api_key.id,
        name: api_key.name,
        key_prefix: api_key.key_prefix,
        role,
    })
}

/// Create a new API key and store it in the database.
pub async fn create_key(
    pool: &DbPool,
    name: &str,
    role: ApiKeyRole,
    expires_in: Option<&str>,
) -> AppResult<(String, ApiKey)> {
    let (full_key, api_key) = generate_key(name, role, expires_in)?;

    let conn = pool.connection();
    db::insert_api_key(conn, &api_key).await?;

    Ok((full_key, api_key))
}

/// List all API keys.
pub async fn list_keys(pool: &DbPool) -> AppResult<Vec<ApiKey>> {
    let conn = pool.connection();
    db::list_all(conn).await
}

/// Revoke an API key by ID.
pub async fn revoke_key(pool: &DbPool, id: &str) -> AppResult<bool> {
    let conn = pool.connection();
    db::revoke(conn, id).await
}

/// Restore a revoked API key by ID.
pub async fn restore_key(pool: &DbPool, id: &str) -> AppResult<bool> {
    let conn = pool.connection();
    db::restore(conn, id).await
}

/// Get an API key by ID.
pub async fn get_key(pool: &DbPool, id: &str) -> AppResult<Option<ApiKey>> {
    let conn = pool.connection();
    db::find_by_id(conn, id).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_generate_key() {
        let (full_key, api_key) = generate_key("Test Key", ApiKeyRole::Contributor, None).unwrap();

        assert!(full_key.starts_with(KEY_PREFIX));
        assert_eq!(full_key.len(), KEY_PREFIX.len() + KEY_RANDOM_LENGTH);
        assert_eq!(api_key.key_prefix.len(), KEY_PREFIX_LENGTH);
        assert_eq!(api_key.role, "contributor");
        assert!(api_key.expires_at.is_none());
    }

    #[test]
    fn test_generate_key_with_expiration() {
        let (_, api_key) = generate_key("Test Key", ApiKeyRole::Admin, Some("365d")).unwrap();

        assert!(api_key.expires_at.is_some());
        let expires = api_key.expires_at.unwrap();
        let diff = expires - Utc::now();
        // Should be approximately 365 days (allowing for test execution time)
        assert!(diff.num_days() >= 364 && diff.num_days() <= 366);
    }

    #[test]
    fn test_hash_key() {
        let key = "rrv_test123";
        let hash1 = hash_key(key);
        let hash2 = hash_key(key);

        assert_eq!(hash1, hash2);
        assert_eq!(hash1.len(), 64); // SHA-256 produces 64 hex chars
    }

    #[test]
    fn test_parse_duration() {
        assert_eq!(parse_duration("30d").map(|d| d.num_days()), Some(30));
        assert_eq!(parse_duration("1y").map(|d| d.num_days()), Some(365));
        assert_eq!(parse_duration("6m").map(|d| d.num_days()), Some(180));
        assert_eq!(parse_duration("2w").map(|d| d.num_days()), Some(14));
        assert_eq!(parse_duration("invalid"), None);
    }
}
