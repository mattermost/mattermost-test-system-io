//! Database module providing connection management, migrations, and queries.

pub mod api_keys;
pub mod queries;
pub mod upload_files;

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
        let mut opt = ConnectOptions::new(&config.database_url);
        opt.max_connections(100)
            .min_connections(5)
            .connect_timeout(Duration::from_secs(10))
            .acquire_timeout(Duration::from_secs(10))
            .idle_timeout(Duration::from_secs(600))
            .max_lifetime(Duration::from_secs(1800))
            .sqlx_logging(false);

        let conn = Database::connect(opt)
            .await
            .map_err(|e| AppError::Database(format!("Failed to connect to database: {}", e)))?;

        info!("Database connection established");

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
