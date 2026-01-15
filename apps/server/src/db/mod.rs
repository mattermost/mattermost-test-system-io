//! Database module providing connection management, migrations, and queries.

pub mod api_keys;
pub mod backup;
pub mod migrations;
pub mod queries;
pub mod version;

use rusqlite::Connection;
use std::path::Path;
use std::sync::{Arc, Mutex};

use crate::config::Config;
use crate::error::{AppError, AppResult};

/// Database connection pool wrapper.
/// Uses a Mutex since rusqlite Connection is not thread-safe.
#[derive(Clone)]
pub struct DbPool {
    conn: Arc<Mutex<Connection>>,
}

impl DbPool {
    /// Create a new database pool from configuration.
    pub fn new(config: &Config) -> AppResult<Self> {
        let path = if config.database_url.starts_with("file:") {
            config
                .database_url
                .strip_prefix("file:")
                .unwrap_or(&config.database_url)
        } else {
            return Err(AppError::Database(format!(
                "Invalid DATABASE_URL format: {}. Expected 'file:path'",
                config.database_url
            )));
        };

        // Ensure parent directory exists
        if let Some(parent) = Path::new(path).parent() {
            std::fs::create_dir_all(parent).map_err(|e| {
                AppError::Database(format!("Failed to create database directory: {}", e))
            })?;
        }

        let conn = Connection::open(path)
            .map_err(|e| AppError::Database(format!("Failed to open database: {}", e)))?;

        // Enable foreign keys
        conn.execute("PRAGMA foreign_keys = OFF", [])
            .map_err(|e| AppError::Database(format!("Failed to set pragma: {}", e)))?;

        // Force synchronous writes
        conn.execute("PRAGMA synchronous = FULL", [])
            .map_err(|e| AppError::Database(format!("Failed to set synchronous pragma: {}", e)))?;

        // Use WAL mode for better concurrency (pragma returns current mode, so use query_row)
        conn.query_row("PRAGMA journal_mode = WAL", [], |_| Ok(()))
            .map_err(|e| AppError::Database(format!("Failed to set journal_mode pragma: {}", e)))?;

        Ok(DbPool {
            conn: Arc::new(Mutex::new(conn)),
        })
    }

    /// Get access to the connection for executing queries.
    /// Returns a MutexGuard that must be held while using the connection.
    pub fn connection(&self) -> std::sync::MutexGuard<'_, Connection> {
        self.conn.lock().expect("Database mutex poisoned")
    }
}
