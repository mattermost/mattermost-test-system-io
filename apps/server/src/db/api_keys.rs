//! Database operations for API keys.

use chrono::{DateTime, Utc};
use rusqlite::{params, Connection, OptionalExtension};

use crate::error::AppResult;
use crate::models::ApiKey;

/// Insert a new API key.
pub fn insert_api_key(conn: &Connection, key: &ApiKey) -> AppResult<()> {
    conn.execute(
        r#"
        INSERT INTO api_keys (id, key_hash, key_prefix, name, role, expires_at, created_at)
        VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)
        "#,
        params![
            key.id,
            key.key_hash,
            key.key_prefix,
            key.name,
            key.role,
            key.expires_at.map(|d| d.to_rfc3339()),
            key.created_at.to_rfc3339(),
        ],
    )?;
    Ok(())
}

/// Find an API key by its hash.
pub fn find_by_hash(conn: &Connection, key_hash: &str) -> AppResult<Option<ApiKey>> {
    let result = conn
        .query_row(
            r#"
            SELECT id, key_hash, key_prefix, name, role, expires_at, last_used_at, created_at, deleted_at
            FROM api_keys
            WHERE key_hash = ?1
            "#,
            params![key_hash],
            |row| {
                Ok(ApiKey {
                    id: row.get(0)?,
                    key_hash: row.get(1)?,
                    key_prefix: row.get(2)?,
                    name: row.get(3)?,
                    role: row.get(4)?,
                    expires_at: row
                        .get::<_, Option<String>>(5)?
                        .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                        .map(|d| d.with_timezone(&Utc)),
                    last_used_at: row
                        .get::<_, Option<String>>(6)?
                        .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                        .map(|d| d.with_timezone(&Utc)),
                    created_at: row
                        .get::<_, String>(7)
                        .ok()
                        .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                        .map(|d| d.with_timezone(&Utc))
                        .unwrap_or_else(Utc::now),
                    deleted_at: row
                        .get::<_, Option<String>>(8)?
                        .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                        .map(|d| d.with_timezone(&Utc)),
                })
            },
        )
        .optional()?;

    Ok(result)
}

/// Find an API key by ID.
pub fn find_by_id(conn: &Connection, id: &str) -> AppResult<Option<ApiKey>> {
    let result = conn
        .query_row(
            r#"
            SELECT id, key_hash, key_prefix, name, role, expires_at, last_used_at, created_at, deleted_at
            FROM api_keys
            WHERE id = ?1
            "#,
            params![id],
            |row| {
                Ok(ApiKey {
                    id: row.get(0)?,
                    key_hash: row.get(1)?,
                    key_prefix: row.get(2)?,
                    name: row.get(3)?,
                    role: row.get(4)?,
                    expires_at: row
                        .get::<_, Option<String>>(5)?
                        .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                        .map(|d| d.with_timezone(&Utc)),
                    last_used_at: row
                        .get::<_, Option<String>>(6)?
                        .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                        .map(|d| d.with_timezone(&Utc)),
                    created_at: row
                        .get::<_, String>(7)
                        .ok()
                        .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                        .map(|d| d.with_timezone(&Utc))
                        .unwrap_or_else(Utc::now),
                    deleted_at: row
                        .get::<_, Option<String>>(8)?
                        .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                        .map(|d| d.with_timezone(&Utc)),
                })
            },
        )
        .optional()?;

    Ok(result)
}

/// List all API keys.
pub fn list_all(conn: &Connection) -> AppResult<Vec<ApiKey>> {
    let mut stmt = conn.prepare(
        r#"
        SELECT id, key_hash, key_prefix, name, role, expires_at, last_used_at, created_at, deleted_at
        FROM api_keys
        ORDER BY created_at DESC
        "#,
    )?;

    let keys = stmt
        .query_map([], |row| {
            Ok(ApiKey {
                id: row.get(0)?,
                key_hash: row.get(1)?,
                key_prefix: row.get(2)?,
                name: row.get(3)?,
                role: row.get(4)?,
                expires_at: row
                    .get::<_, Option<String>>(5)?
                    .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                    .map(|d| d.with_timezone(&Utc)),
                last_used_at: row
                    .get::<_, Option<String>>(6)?
                    .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                    .map(|d| d.with_timezone(&Utc)),
                created_at: row
                    .get::<_, String>(7)
                    .ok()
                    .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                    .map(|d| d.with_timezone(&Utc))
                    .unwrap_or_else(Utc::now),
                deleted_at: row
                    .get::<_, Option<String>>(8)?
                    .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
                    .map(|d| d.with_timezone(&Utc)),
            })
        })?
        .collect::<Result<Vec<_>, _>>()?;

    Ok(keys)
}

/// Update last_used_at timestamp for an API key.
pub fn update_last_used(conn: &Connection, id: &str) -> AppResult<()> {
    let now = Utc::now().to_rfc3339();
    conn.execute(
        "UPDATE api_keys SET last_used_at = ?1 WHERE id = ?2",
        params![now, id],
    )?;
    Ok(())
}

/// Soft delete (revoke) an API key.
pub fn revoke(conn: &Connection, id: &str) -> AppResult<bool> {
    let now = Utc::now().to_rfc3339();
    let rows = conn.execute(
        "UPDATE api_keys SET deleted_at = ?1 WHERE id = ?2 AND deleted_at IS NULL",
        params![now, id],
    )?;
    Ok(rows > 0)
}

/// Restore a revoked API key.
pub fn restore(conn: &Connection, id: &str) -> AppResult<bool> {
    let rows = conn.execute(
        "UPDATE api_keys SET deleted_at = NULL WHERE id = ?1 AND deleted_at IS NOT NULL",
        params![id],
    )?;
    Ok(rows > 0)
}
