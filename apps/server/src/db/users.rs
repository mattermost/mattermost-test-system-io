//! Database operations for users.

use chrono::Utc;
use sea_orm::*;
use uuid::Uuid;

use crate::error::AppResult;
use crate::models::user::User;

/// Find or create a user by GitHub ID. Updates profile on each login.
pub async fn upsert_from_github(
    db: &DatabaseConnection,
    github_id: i64,
    username: &str,
    display_name: Option<&str>,
    avatar_url: Option<&str>,
    email: Option<&str>,
) -> AppResult<User> {
    // Try to find existing user
    let existing = crate::entity::user::Entity::find()
        .filter(crate::entity::user::Column::GithubId.eq(github_id))
        .filter(crate::entity::user::Column::DeletedAt.is_null())
        .one(db)
        .await?;

    if let Some(m) = existing {
        // Update user info and last_login_at
        let mut active: crate::entity::user::ActiveModel = m.into();
        active.username = Set(username.to_string());
        active.display_name = Set(display_name.map(|s| s.to_string()));
        active.avatar_url = Set(avatar_url.map(|s| s.to_string()));
        active.email = Set(email.map(|s| s.to_string()));
        active.last_login_at = Set(Some(Utc::now()));
        let updated = active.update(db).await?;
        return Ok(model_to_user(updated));
    }

    // Create new user
    let id = Uuid::new_v4();
    let now = Utc::now();

    let model = crate::entity::user::ActiveModel {
        id: Set(id),
        github_id: Set(github_id),
        username: Set(username.to_string()),
        display_name: Set(display_name.map(|s| s.to_string())),
        avatar_url: Set(avatar_url.map(|s| s.to_string())),
        email: Set(email.map(|s| s.to_string())),
        role: Set("viewer".to_string()),
        last_login_at: Set(Some(now)),
        created_at: Set(now),
        updated_at: Set(now),
        deleted_at: Set(None),
    };

    crate::entity::user::Entity::insert(model).exec(db).await?;

    // Fetch back the inserted user
    let inserted = crate::entity::user::Entity::find_by_id(id)
        .one(db)
        .await?
        .ok_or_else(|| {
            crate::error::AppError::Database("Failed to fetch newly inserted user".to_string())
        })?;

    Ok(model_to_user(inserted))
}

/// Find a user by ID.
pub async fn find_by_id(db: &DatabaseConnection, id: &str) -> AppResult<Option<User>> {
    let uuid = match Uuid::parse_str(id).ok() {
        Some(u) => u,
        None => return Ok(None),
    };

    let result = crate::entity::user::Entity::find_by_id(uuid)
        .filter(crate::entity::user::Column::DeletedAt.is_null())
        .one(db)
        .await?;

    Ok(result.map(model_to_user))
}

fn model_to_user(m: crate::entity::user::Model) -> User {
    User {
        id: m.id.to_string(),
        github_id: m.github_id,
        username: m.username,
        display_name: m.display_name,
        avatar_url: m.avatar_url,
        email: m.email,
        role: m.role,
        last_login_at: m.last_login_at,
        created_at: m.created_at,
    }
}
