//! Database operations for GitHub OIDC policies.

use chrono::Utc;
use sea_orm::*;
use uuid::Uuid;

use crate::error::AppResult;
use crate::models::github_oidc::OidcPolicy;

/// Insert a new OIDC policy.
pub async fn insert(db: &DatabaseConnection, policy: &OidcPolicy) -> AppResult<()> {
    let id = Uuid::parse_str(&policy.id).unwrap_or_else(|_| Uuid::new_v4());
    let now = Utc::now();

    let model = crate::entity::github_oidc_policy::ActiveModel {
        id: Set(id),
        repository_pattern: Set(policy.repository_pattern.clone()),
        role: Set(policy.role.clone()),
        description: Set(policy.description.clone()),
        enabled: Set(policy.enabled),
        created_at: Set(policy.created_at),
        updated_at: Set(now),
        deleted_at: Set(None),
    };

    crate::entity::github_oidc_policy::Entity::insert(model)
        .exec(db)
        .await?;

    Ok(())
}

/// List all active (non-deleted) OIDC policies.
pub async fn list_active(db: &DatabaseConnection) -> AppResult<Vec<OidcPolicy>> {
    let results = crate::entity::github_oidc_policy::Entity::find()
        .filter(crate::entity::github_oidc_policy::Column::DeletedAt.is_null())
        .order_by_asc(crate::entity::github_oidc_policy::Column::CreatedAt)
        .all(db)
        .await?;

    Ok(results.into_iter().map(model_to_policy).collect())
}

/// List all enabled OIDC policies.
pub async fn list_enabled(db: &DatabaseConnection) -> AppResult<Vec<OidcPolicy>> {
    let results = crate::entity::github_oidc_policy::Entity::find()
        .filter(crate::entity::github_oidc_policy::Column::DeletedAt.is_null())
        .filter(crate::entity::github_oidc_policy::Column::Enabled.eq(true))
        .all(db)
        .await?;

    Ok(results.into_iter().map(model_to_policy).collect())
}

/// Find a policy by ID.
pub async fn find_by_id(db: &DatabaseConnection, id: &str) -> AppResult<Option<OidcPolicy>> {
    let uuid = match Uuid::parse_str(id).ok() {
        Some(u) => u,
        None => return Ok(None),
    };

    let result = crate::entity::github_oidc_policy::Entity::find_by_id(uuid)
        .filter(crate::entity::github_oidc_policy::Column::DeletedAt.is_null())
        .one(db)
        .await?;

    Ok(result.map(model_to_policy))
}

/// Update a policy.
pub async fn update(
    db: &DatabaseConnection,
    id: &str,
    pattern: Option<&str>,
    role: Option<&str>,
    description: Option<&str>,
    enabled: Option<bool>,
) -> AppResult<bool> {
    let uuid = match Uuid::parse_str(id).ok() {
        Some(u) => u,
        None => return Ok(false),
    };

    let model = crate::entity::github_oidc_policy::Entity::find_by_id(uuid)
        .filter(crate::entity::github_oidc_policy::Column::DeletedAt.is_null())
        .one(db)
        .await?;

    if let Some(m) = model {
        let mut active: crate::entity::github_oidc_policy::ActiveModel = m.into();
        if let Some(p) = pattern {
            active.repository_pattern = Set(p.to_string());
        }
        if let Some(r) = role {
            active.role = Set(r.to_string());
        }
        if let Some(d) = description {
            active.description = Set(Some(d.to_string()));
        }
        if let Some(e) = enabled {
            active.enabled = Set(e);
        }
        active.update(db).await?;
        Ok(true)
    } else {
        Ok(false)
    }
}

/// Soft-delete a policy.
pub async fn delete(db: &DatabaseConnection, id: &str) -> AppResult<bool> {
    let uuid = match Uuid::parse_str(id).ok() {
        Some(u) => u,
        None => return Ok(false),
    };

    let model = crate::entity::github_oidc_policy::Entity::find_by_id(uuid)
        .filter(crate::entity::github_oidc_policy::Column::DeletedAt.is_null())
        .one(db)
        .await?;

    if let Some(m) = model {
        let mut active: crate::entity::github_oidc_policy::ActiveModel = m.into();
        active.deleted_at = Set(Some(Utc::now()));
        active.update(db).await?;
        Ok(true)
    } else {
        Ok(false)
    }
}

fn model_to_policy(m: crate::entity::github_oidc_policy::Model) -> OidcPolicy {
    OidcPolicy {
        id: m.id.to_string(),
        repository_pattern: m.repository_pattern,
        role: m.role,
        description: m.description,
        enabled: m.enabled,
        created_at: m.created_at,
        updated_at: m.updated_at,
    }
}
