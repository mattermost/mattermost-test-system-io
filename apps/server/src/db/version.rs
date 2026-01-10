//! Server version tracking and comparison.
//!
//! Tracks server version in the database and determines when backups are needed.

use std::fmt;

use rusqlite::Connection;
use tracing::info;

use crate::error::{AppError, AppResult};

/// Server version from Cargo.toml.
pub const SERVER_VERSION: &str = env!("CARGO_PKG_VERSION");

/// Parsed semantic version.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Version {
    pub major: u32,
    pub minor: u32,
    pub patch: u32,
}

impl Version {
    /// Parse a version string (e.g., "0.1.0").
    pub fn parse(version: &str) -> AppResult<Self> {
        let parts: Vec<&str> = version.split('.').collect();
        if parts.len() != 3 {
            return Err(AppError::InvalidInput(format!(
                "Invalid version format: {}",
                version
            )));
        }

        let major = parts[0]
            .parse()
            .map_err(|_| AppError::InvalidInput(format!("Invalid major version: {}", parts[0])))?;
        let minor = parts[1]
            .parse()
            .map_err(|_| AppError::InvalidInput(format!("Invalid minor version: {}", parts[1])))?;
        let patch = parts[2]
            .parse()
            .map_err(|_| AppError::InvalidInput(format!("Invalid patch version: {}", parts[2])))?;

        Ok(Version {
            major,
            minor,
            patch,
        })
    }

    /// Check if this version has a minor bump compared to another.
    /// Returns true if major or minor version is different (not just patch).
    pub fn has_minor_bump(&self, other: &Version) -> bool {
        self.major != other.major || self.minor != other.minor
    }
}

impl fmt::Display for Version {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}.{}.{}", self.major, self.minor, self.patch)
    }
}

/// Metadata keys stored in server_metadata table.
const KEY_SERVER_VERSION: &str = "server_version";

/// Get the stored server version from the database.
pub fn get_stored_version(conn: &Connection) -> AppResult<Option<String>> {
    let result = conn.query_row(
        "SELECT value FROM server_metadata WHERE key = ?",
        [KEY_SERVER_VERSION],
        |row| row.get::<_, String>(0),
    );

    match result {
        Ok(version) => Ok(Some(version)),
        Err(rusqlite::Error::QueryReturnedNoRows) => Ok(None),
        Err(rusqlite::Error::SqliteFailure(_, _)) => {
            // Table might not exist yet
            Ok(None)
        }
        Err(e) => Err(AppError::Database(e.to_string())),
    }
}

/// Update the stored server version in the database.
pub fn update_stored_version(conn: &Connection, version: &str) -> AppResult<()> {
    conn.execute(
        "INSERT OR REPLACE INTO server_metadata (key, value, updated_at) VALUES (?, ?, datetime('now'))",
        [KEY_SERVER_VERSION, version],
    )
    .map_err(|e| AppError::Database(format!("Failed to update server version: {}", e)))?;

    info!("Server version updated to {}", version);
    Ok(())
}

/// Check version and determine if backup is needed.
///
/// Returns (needs_backup, version_changed) tuple.
pub fn check_version(conn: &Connection) -> AppResult<(bool, bool)> {
    let current = Version::parse(SERVER_VERSION)?;
    let stored = get_stored_version(conn)?;

    match stored {
        None => {
            // First run - no backup needed, but version changed
            info!("First run, no previous version stored");
            Ok((false, true))
        }
        Some(stored_str) => {
            if stored_str == SERVER_VERSION {
                // Same version - no changes
                info!("Server version unchanged: {}", SERVER_VERSION);
                Ok((false, false))
            } else {
                // Version changed - check if minor bump
                let stored_version = Version::parse(&stored_str)?;
                let needs_backup = current.has_minor_bump(&stored_version);

                if needs_backup {
                    info!(
                        "Minor version bump detected: {} -> {} (backup required)",
                        stored_str, SERVER_VERSION
                    );
                } else {
                    info!(
                        "Patch version bump detected: {} -> {} (no backup needed)",
                        stored_str, SERVER_VERSION
                    );
                }

                Ok((needs_backup, true))
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_version_parse() {
        let v = Version::parse("0.1.0").unwrap();
        assert_eq!(v.major, 0);
        assert_eq!(v.minor, 1);
        assert_eq!(v.patch, 0);

        let v = Version::parse("1.2.3").unwrap();
        assert_eq!(v.major, 1);
        assert_eq!(v.minor, 2);
        assert_eq!(v.patch, 3);
    }

    #[test]
    fn test_version_parse_invalid() {
        assert!(Version::parse("1.2").is_err());
        assert!(Version::parse("1.2.3.4").is_err());
        assert!(Version::parse("a.b.c").is_err());
    }

    #[test]
    fn test_has_minor_bump() {
        let v1 = Version::parse("0.1.0").unwrap();
        let v2 = Version::parse("0.1.1").unwrap();
        let v3 = Version::parse("0.2.0").unwrap();
        let v4 = Version::parse("1.0.0").unwrap();

        // Patch bump - no minor bump
        assert!(!v2.has_minor_bump(&v1));

        // Minor bump
        assert!(v3.has_minor_bump(&v1));
        assert!(v3.has_minor_bump(&v2));

        // Major bump
        assert!(v4.has_minor_bump(&v1));
        assert!(v4.has_minor_bump(&v3));
    }

    #[test]
    fn test_version_display() {
        let v = Version::parse("1.2.3").unwrap();
        assert_eq!(v.to_string(), "1.2.3");
    }
}
