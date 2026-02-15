//! Database module providing connection management, migrations, and queries.

pub mod api_keys;
pub mod github_oidc_policies;
pub mod html_files;
pub mod json_files;
pub mod refresh_tokens;
pub mod screenshots;
pub mod test_jobs;
pub mod test_reports;
pub mod test_results;
pub mod users;

use sea_orm::{ConnectOptions, Database, DatabaseConnection};
use std::time::Duration;
use tracing::info;

use crate::config::Config;
use crate::error::{AppError, AppResult};
use crate::migration::Migrator;
use sea_orm_migration::MigratorTrait;

/// Database connection pool wrapper using SeaORM.
#[derive(Clone)]
pub struct DbPool {
    conn: DatabaseConnection,
}

impl DbPool {
    /// Create a new database pool from configuration.
    pub async fn new(config: &Config) -> AppResult<Self> {
        let db = &config.database;

        let mut opt = ConnectOptions::new(&db.url);
        opt.max_connections(db.max_connections)
            .min_connections(db.min_connections)
            .connect_timeout(Duration::from_secs(db.connect_timeout_secs))
            .acquire_timeout(Duration::from_secs(db.acquire_timeout_secs))
            .idle_timeout(Duration::from_secs(db.idle_timeout_secs))
            .max_lifetime(Duration::from_secs(db.max_lifetime_secs))
            .sqlx_logging(false);

        let conn = Database::connect(opt)
            .await
            .map_err(|e| AppError::Database(format!("Failed to connect to database: {}", e)))?;

        info!(
            "Database connection pool: max={}, min={}, idle_timeout={}s, max_lifetime={}s",
            db.max_connections, db.min_connections, db.idle_timeout_secs, db.max_lifetime_secs
        );

        Ok(DbPool { conn })
    }

    /// Run pending database migrations.
    pub async fn run_migrations(&self) -> AppResult<()> {
        Migrator::up(&self.conn, None)
            .await
            .map_err(|e| AppError::Database(format!("Failed to run migrations: {}", e)))?;
        info!("Database migrations complete");
        Ok(())
    }

    /// Get the database connection.
    pub fn connection(&self) -> &DatabaseConnection {
        &self.conn
    }
}
