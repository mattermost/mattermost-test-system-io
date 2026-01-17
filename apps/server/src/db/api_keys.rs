//! Database operations for API keys using SeaORM.

use chrono::Utc;
use sea_orm::*;
use uuid::Uuid;

use crate::error::AppResult;
use crate::models::ApiKey;

/// Insert a new API key.
pub async fn insert_api_key(db: &DatabaseConnection, key: &ApiKey) -> AppResult<()> {
    let id = Uuid::parse_str(&key.id).unwrap_or_else(|_| Uuid::new_v4());

    let model = crate::entity::api_key::ActiveModel {
        id: Set(id),
        key_hash: Set(key.key_hash.clone()),
        key_prefix: Set(key.key_prefix.clone()),
        name: Set(key.name.clone()),
        role: Set(key.role.clone()),
        expires_at: Set(key.expires_at),
        last_used_at: Set(key.last_used_at),
        created_at: Set(key.created_at),
        deleted_at: Set(key.deleted_at),
    };

    crate::entity::api_key::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(())
}

/// Find an API key by its hash.
pub async fn find_by_hash(db: &DatabaseConnection, key_hash: &str) -> AppResult<Option<ApiKey>> {
    let result = crate::entity::api_key::Entity::find()
        .filter(crate::entity::api_key::Column::KeyHash.eq(key_hash))
        .one(db)
        .await?;

    Ok(result.map(model_to_api_key))
}

/// Find an API key by ID.
pub async fn find_by_id(db: &DatabaseConnection, id: &str) -> AppResult<Option<ApiKey>> {
    let uuid = Uuid::parse_str(id).ok();
    if uuid.is_none() {
        return Ok(None);
    }

    let result = crate::entity::api_key::Entity::find_by_id(uuid.unwrap())
        .one(db)
        .await?;

    Ok(result.map(model_to_api_key))
}

/// Update last used timestamp.
pub async fn update_last_used(db: &DatabaseConnection, id: &str) -> AppResult<()> {
    let uuid = Uuid::parse_str(id).ok();
    if uuid.is_none() {
        return Ok(());
    }

    let model = crate::entity::api_key::Entity::find_by_id(uuid.unwrap())
        .one(db)
        .await?;

    if let Some(m) = model {
        let mut active: crate::entity::api_key::ActiveModel = m.into();
        active.last_used_at = Set(Some(Utc::now()));
        active.update(db).await?;
    }

    Ok(())
}

/// List all API keys (including revoked).
pub async fn list_all(db: &DatabaseConnection) -> AppResult<Vec<ApiKey>> {
    let results = crate::entity::api_key::Entity::find()
        .order_by_desc(crate::entity::api_key::Column::CreatedAt)
        .all(db)
        .await?;

    Ok(results.into_iter().map(model_to_api_key).collect())
}

/// Revoke an API key (soft delete).
pub async fn revoke(db: &DatabaseConnection, id: &str) -> AppResult<bool> {
    let uuid = Uuid::parse_str(id).ok();
    if uuid.is_none() {
        return Ok(false);
    }

    let model = crate::entity::api_key::Entity::find_by_id(uuid.unwrap())
        .one(db)
        .await?;

    if let Some(m) = model {
        if m.deleted_at.is_some() {
            return Ok(false); // Already revoked
        }
        let mut active: crate::entity::api_key::ActiveModel = m.into();
        active.deleted_at = Set(Some(Utc::now()));
        active.update(db).await?;
        Ok(true)
    } else {
        Ok(false)
    }
}

/// Restore a revoked API key.
pub async fn restore(db: &DatabaseConnection, id: &str) -> AppResult<bool> {
    let uuid = Uuid::parse_str(id).ok();
    if uuid.is_none() {
        return Ok(false);
    }

    let model = crate::entity::api_key::Entity::find_by_id(uuid.unwrap())
        .one(db)
        .await?;

    if let Some(m) = model {
        if m.deleted_at.is_none() {
            return Ok(false); // Not revoked
        }
        let mut active: crate::entity::api_key::ActiveModel = m.into();
        active.deleted_at = Set(None);
        active.update(db).await?;
        Ok(true)
    } else {
        Ok(false)
    }
}

fn model_to_api_key(m: crate::entity::api_key::Model) -> ApiKey {
    ApiKey {
        id: m.id.to_string(),
        key_hash: m.key_hash,
        key_prefix: m.key_prefix,
        name: m.name,
        role: m.role,
        expires_at: m.expires_at,
        last_used_at: m.last_used_at,
        created_at: m.created_at,
        deleted_at: m.deleted_at,
    }
}
