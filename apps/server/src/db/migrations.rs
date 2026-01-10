//! Database migration runner.

use rusqlite::Connection;
use tracing::info;

use crate::error::{AppError, AppResult};

use super::DbPool;

/// Embedded migration files.
const MIGRATIONS: &[(&str, &str)] = &[(
    "001_initial_schema",
    include_str!("migrations/001_initial_schema.sql"),
)];

/// Run all pending migrations.
///
/// Migrations are run in order and tracked in the `schema_migrations` table.
/// Backup should be handled separately before calling this function.
pub fn run_migrations(pool: &DbPool) -> AppResult<()> {
    let conn = pool.connection();
    let current_version = get_current_version(&conn)?;
    info!("Current schema version: {}", current_version);

    // Find pending migrations
    let pending: Vec<usize> = MIGRATIONS
        .iter()
        .enumerate()
        .filter(|(i, _)| (*i as i64 + 1) > current_version)
        .map(|(i, _)| i)
        .collect();

    if pending.is_empty() {
        info!("No pending migrations");
        return Ok(());
    }

    info!("{} migration(s) pending", pending.len());

    // Run pending migrations
    for i in pending {
        let (name, sql) = MIGRATIONS[i];
        let version = i as i64 + 1;
        info!("Applying migration {}: {}", version, name);

        // Execute migration SQL
        conn.execute_batch(sql)
            .map_err(|e| AppError::Database(format!("Migration {} failed: {}", name, e)))?;

        info!("Migration {} applied successfully", name);
    }

    Ok(())
}

/// Get the current schema version from the database.
fn get_current_version(conn: &Connection) -> AppResult<i64> {
    // Try to get max version from schema_migrations
    // If table doesn't exist, return 0
    let result = conn.query_row("SELECT MAX(version) FROM schema_migrations", [], |row| {
        row.get::<_, Option<i64>>(0)
    });

    match result {
        Ok(version) => Ok(version.unwrap_or(0)),
        Err(rusqlite::Error::SqliteFailure(_, _)) => {
            // Table doesn't exist yet
            Ok(0)
        }
        Err(rusqlite::Error::QueryReturnedNoRows) => Ok(0),
        Err(e) => Err(AppError::Database(e.to_string())),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_migrations_ordered() {
        // Verify migrations are in order
        for (i, (name, _)) in MIGRATIONS.iter().enumerate() {
            let expected_prefix = format!("{:03}_", i + 1);
            assert!(
                name.starts_with(&expected_prefix),
                "Migration {} should start with {}",
                name,
                expected_prefix
            );
        }
    }
}
