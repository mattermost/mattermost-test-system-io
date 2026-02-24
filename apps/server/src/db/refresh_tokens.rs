//! Database operations for refresh tokens.

use chrono::Utc;
use sea_orm::*;
use sha2::{Digest, Sha256};
use uuid::Uuid;

use crate::error::AppResult;

/// Hash a refresh token using SHA-256 (same approach as API keys).
pub fn hash_token(token: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(token.as_bytes());
    hex::encode(hasher.finalize())
}

/// Generate a random refresh token string.
pub fn generate_token() -> String {
    let random_bytes: [u8; 32] = rand::random();
    format!("tsio_rt_{}", hex::encode(random_bytes))
}

/// Insert a new refresh token (stores the hash, not the raw token).
pub async fn insert(
    db: &DatabaseConnection,
    user_id: Uuid,
    token_hash: &str,
    ttl_secs: u64,
) -> AppResult<()> {
    let id = Uuid::new_v4();
    let now = Utc::now();
    let expires_at = now + chrono::Duration::seconds(ttl_secs as i64);

    let model = crate::entity::refresh_token::ActiveModel {
        id: Set(id),
        user_id: Set(user_id),
        token_hash: Set(token_hash.to_string()),
        expires_at: Set(expires_at),
        revoked_at: Set(None),
        created_at: Set(now),
        updated_at: Set(now),
        deleted_at: Set(None),
    };

    crate::entity::refresh_token::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(())
}

/// Find an active (non-revoked, non-expired) refresh token by its hash.
/// Returns the user_id if valid.
pub async fn find_valid_by_hash(
    db: &DatabaseConnection,
    token_hash: &str,
) -> AppResult<Option<Uuid>> {
    let result = crate::entity::refresh_token::Entity::find()
        .filter(crate::entity::refresh_token::Column::TokenHash.eq(token_hash))
        .filter(crate::entity::refresh_token::Column::RevokedAt.is_null())
        .filter(crate::entity::refresh_token::Column::DeletedAt.is_null())
        .filter(crate::entity::refresh_token::Column::ExpiresAt.gt(Utc::now()))
        .one(db)
        .await?;

    Ok(result.map(|m| m.user_id))
}

/// Revoke a refresh token by its hash.
pub async fn revoke_by_hash(db: &DatabaseConnection, token_hash: &str) -> AppResult<bool> {
    let result = crate::entity::refresh_token::Entity::find()
        .filter(crate::entity::refresh_token::Column::TokenHash.eq(token_hash))
        .filter(crate::entity::refresh_token::Column::RevokedAt.is_null())
        .filter(crate::entity::refresh_token::Column::DeletedAt.is_null())
        .one(db)
        .await?;

    if let Some(m) = result {
        let mut active: crate::entity::refresh_token::ActiveModel = m.into();
        active.revoked_at = Set(Some(Utc::now()));
        active.update(db).await?;
        Ok(true)
    } else {
        Ok(false)
    }
}

/// Soft-delete expired and revoked tokens older than the given age (cleanup job).
pub async fn cleanup_expired(db: &DatabaseConnection, older_than_secs: u64) -> AppResult<u64> {
    let cutoff = Utc::now() - chrono::Duration::seconds(older_than_secs as i64);
    let now = Utc::now();

    let result = crate::entity::refresh_token::Entity::update_many()
        .filter(crate::entity::refresh_token::Column::DeletedAt.is_null())
        .filter(
            Condition::any()
                .add(crate::entity::refresh_token::Column::ExpiresAt.lt(cutoff))
                .add(crate::entity::refresh_token::Column::RevokedAt.lt(cutoff)),
        )
        .col_expr(
            crate::entity::refresh_token::Column::DeletedAt,
            sea_orm::prelude::Expr::value(Some(now)),
        )
        .exec(db)
        .await?;

    Ok(result.rows_affected)
}
