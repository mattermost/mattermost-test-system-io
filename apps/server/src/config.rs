//! Application configuration loaded from environment variables.

use std::env;
use std::path::PathBuf;

/// HTTP header name for API key authentication.
pub const API_KEY_HEADER: &str = "X-API-Key";

/// HTTP header name for admin key (bootstrap).
pub const ADMIN_KEY_HEADER: &str = "X-Admin-Key";

/// Development default values - NEVER use in production.
pub mod defaults {
    pub const DEV_DATABASE_URL: &str = "file:./data/dev.db";
    pub const DEV_ADMIN_KEY: &str = "dev-admin-key-do-not-use-in-production";
    pub const DEV_HOST: &str = "127.0.0.1";
    pub const DEV_PORT: u16 = 8080;
    pub const DEV_DATA_DIR: &str = "./data/files";
    pub const DEV_BACKUP_DIR: &str = "./data/backups";
    pub const DEV_MAX_UPLOAD_SIZE: usize = 52_428_800; // 50MB
    pub const DEV_MAX_CONCURRENT_UPLOADS: usize = 10; // Max concurrent upload requests
    pub const DEV_ARTIFACT_RETENTION_HOURS: u64 = 1; // 1 hour in development
    pub const PROD_ARTIFACT_RETENTION_HOURS: u64 = 168; // 7 days in production
}

/// Runtime environment.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Environment {
    Development,
    Production,
}

impl Environment {
    /// Parse environment from string.
    pub fn from_str(s: &str) -> Option<Self> {
        match s.to_lowercase().as_str() {
            "development" | "dev" => Some(Self::Development),
            "production" | "prod" => Some(Self::Production),
            _ => None,
        }
    }

    /// Check if this is a development environment.
    pub fn is_development(&self) -> bool {
        matches!(self, Self::Development)
    }

    /// Check if this is a production environment.
    pub fn is_production(&self) -> bool {
        matches!(self, Self::Production)
    }
}

impl std::fmt::Display for Environment {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Development => write!(f, "development"),
            Self::Production => write!(f, "production"),
        }
    }
}

/// Application configuration.
#[derive(Debug, Clone)]
pub struct Config {
    /// Runtime environment
    pub environment: Environment,
    /// Server host address
    pub host: String,
    /// Server port
    pub port: u16,
    /// Database URL (file path)
    pub database_url: String,
    /// Directory for storing uploaded report files
    pub data_dir: PathBuf,
    /// Directory for database backups
    pub backup_dir: PathBuf,
    /// Directory for static frontend assets (production only)
    pub static_dir: Option<PathBuf>,
    /// Admin key for bootstrap operations (creating first API key)
    pub admin_key: Option<String>,
    /// Maximum upload size in bytes (default: 50MB)
    pub max_upload_size: usize,
    /// Maximum concurrent upload requests (default: 10)
    pub max_concurrent_uploads: usize,
    /// Artifact retention in hours (default: 1 hour dev, 7 days production)
    pub artifact_retention_hours: u64,
}

impl Config {
    /// Load configuration from environment variables.
    ///
    /// In development mode (RUST_ENV=development):
    /// - All variables have sensible defaults
    /// - Only RUST_ENV is required
    ///
    /// In production mode (RUST_ENV=production):
    /// - RRV_DATABASE_URL is required
    /// - Server will NOT start if database URL matches development default
    ///
    /// Environment variables:
    /// - `RUST_ENV`: Environment (development/production) - REQUIRED
    /// - `RRV_HOST`: Server host (default: 127.0.0.1)
    /// - `RRV_PORT`: Server port (default: 8080)
    /// - `RRV_DATABASE_URL`: Database connection URL (required in production)
    /// - `RRV_ADMIN_KEY`: Admin key for bootstrap operations (optional, for creating first API key)
    /// - `RRV_DATA_DIR`: Report storage directory (default: ./data/files)
    /// - `RRV_BACKUP_DIR`: Backup directory (default: ./data/backups)
    /// - `RRV_STATIC_DIR`: Static assets directory for production
    /// - `RRV_MAX_UPLOAD_SIZE`: Max upload size in bytes (default: 50MB)
    /// - `RRV_MAX_CONCURRENT_UPLOADS`: Max concurrent upload requests (default: 10)
    /// - `RRV_ARTIFACT_RETENTION_HOURS`: Artifact retention in hours (default: 1 hour dev, 168 hours prod)
    pub fn from_env() -> Result<Self, ConfigError> {
        // Parse environment - required
        let env_str = env::var("RUST_ENV").map_err(|_| ConfigError::MissingEnvVar("RUST_ENV"))?;

        let environment = Environment::from_str(&env_str).ok_or(ConfigError::InvalidValue(
            "RUST_ENV must be 'development' or 'production'",
        ))?;

        // Load values with defaults
        let host = env::var("RRV_HOST").unwrap_or_else(|_| defaults::DEV_HOST.to_string());

        let port = env::var("RRV_PORT")
            .unwrap_or_else(|_| defaults::DEV_PORT.to_string())
            .parse::<u16>()
            .map_err(|_| ConfigError::InvalidValue("RRV_PORT must be a valid port number"))?;

        let database_url =
            env::var("RRV_DATABASE_URL").unwrap_or_else(|_| defaults::DEV_DATABASE_URL.to_string());

        // Admin key is optional - used for bootstrap operations
        let admin_key = if environment.is_development() {
            Some(env::var("RRV_ADMIN_KEY").unwrap_or_else(|_| defaults::DEV_ADMIN_KEY.to_string()))
        } else {
            env::var("RRV_ADMIN_KEY").ok()
        };

        let data_dir = PathBuf::from(
            env::var("RRV_DATA_DIR").unwrap_or_else(|_| defaults::DEV_DATA_DIR.to_string()),
        );

        let backup_dir = PathBuf::from(
            env::var("RRV_BACKUP_DIR").unwrap_or_else(|_| defaults::DEV_BACKUP_DIR.to_string()),
        );

        let max_upload_size = env::var("RRV_MAX_UPLOAD_SIZE")
            .unwrap_or_else(|_| defaults::DEV_MAX_UPLOAD_SIZE.to_string())
            .parse::<usize>()
            .map_err(|_| ConfigError::InvalidValue("RRV_MAX_UPLOAD_SIZE must be a valid number"))?;

        let max_concurrent_uploads = env::var("RRV_MAX_CONCURRENT_UPLOADS")
            .unwrap_or_else(|_| defaults::DEV_MAX_CONCURRENT_UPLOADS.to_string())
            .parse::<usize>()
            .map_err(|_| {
                ConfigError::InvalidValue("RRV_MAX_CONCURRENT_UPLOADS must be a valid number")
            })?;

        let static_dir = env::var("RRV_STATIC_DIR").ok().map(PathBuf::from);

        // Artifact retention defaults based on environment
        let default_retention = if environment.is_development() {
            defaults::DEV_ARTIFACT_RETENTION_HOURS
        } else {
            defaults::PROD_ARTIFACT_RETENTION_HOURS
        };

        let artifact_retention_hours = env::var("RRV_ARTIFACT_RETENTION_HOURS")
            .unwrap_or_else(|_| default_retention.to_string())
            .parse::<u64>()
            .map_err(|_| {
                ConfigError::InvalidValue("RRV_ARTIFACT_RETENTION_HOURS must be a valid number")
            })?;

        let config = Config {
            environment,
            host,
            port,
            database_url,
            data_dir,
            backup_dir,
            static_dir,
            admin_key,
            max_upload_size,
            max_concurrent_uploads,
            artifact_retention_hours,
        };

        // Validate production configuration
        if environment.is_production() {
            config.validate_production()?;
        }

        Ok(config)
    }

    /// Validate that production configuration does not use development defaults.
    fn validate_production(&self) -> Result<(), ConfigError> {
        let mut errors = Vec::new();

        if self.database_url == defaults::DEV_DATABASE_URL {
            errors.push(format!(
                "RRV_DATABASE_URL is using development default '{}'. Set a production database URL.",
                defaults::DEV_DATABASE_URL
            ));
        }

        // Check if database URL looks like a local file in production (warning)
        if self.database_url.starts_with("file:") && !self.database_url.contains("/app/") {
            errors.push(format!(
                "RRV_DATABASE_URL '{}' appears to be a local file path. Use an absolute path in production.",
                self.database_url
            ));
        }

        // Warn if admin key is using development default in production
        if let Some(ref key) = self.admin_key {
            if key == defaults::DEV_ADMIN_KEY {
                errors.push(
                    "RRV_ADMIN_KEY is using development default. Set a secure admin key or remove it."
                        .to_string(),
                );
            }
        }

        if !errors.is_empty() {
            return Err(ConfigError::ProductionValidation(errors));
        }

        Ok(())
    }

    /// Get the server bind address.
    pub fn bind_address(&self) -> String {
        format!("{}:{}", self.host, self.port)
    }

    /// Check if running in development mode.
    pub fn is_development(&self) -> bool {
        self.environment.is_development()
    }
}

/// Configuration errors.
#[derive(Debug, thiserror::Error)]
pub enum ConfigError {
    #[error("Missing required environment variable: {0}")]
    MissingEnvVar(&'static str),

    #[error("Invalid configuration value: {0}")]
    InvalidValue(&'static str),

    #[error("Production configuration validation failed:\n{}", .0.iter().map(|e| format!("  - {}", e)).collect::<Vec<_>>().join("\n"))]
    ProductionValidation(Vec<String>),
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_bind_address() {
        let config = Config {
            environment: Environment::Development,
            host: "0.0.0.0".to_string(),
            port: 3000,
            database_url: "file:test.db".to_string(),
            data_dir: PathBuf::from("./data"),
            backup_dir: PathBuf::from("./backups"),
            static_dir: None,
            admin_key: Some("test-key".to_string()),
            max_upload_size: 1024,
            max_concurrent_uploads: 10,
            artifact_retention_hours: 1,
        };

        assert_eq!(config.bind_address(), "0.0.0.0:3000");
    }

    #[test]
    fn test_environment_parsing() {
        assert_eq!(
            Environment::from_str("development"),
            Some(Environment::Development)
        );
        assert_eq!(Environment::from_str("dev"), Some(Environment::Development));
        assert_eq!(
            Environment::from_str("production"),
            Some(Environment::Production)
        );
        assert_eq!(Environment::from_str("prod"), Some(Environment::Production));
        assert_eq!(Environment::from_str("invalid"), None);
    }

    #[test]
    fn test_production_validation_fails_with_dev_defaults() {
        let config = Config {
            environment: Environment::Production,
            host: "0.0.0.0".to_string(),
            port: 8080,
            database_url: defaults::DEV_DATABASE_URL.to_string(),
            data_dir: PathBuf::from("./data"),
            backup_dir: PathBuf::from("./backups"),
            static_dir: None,
            admin_key: Some(defaults::DEV_ADMIN_KEY.to_string()),
            max_upload_size: 1024,
            max_concurrent_uploads: 10,
            artifact_retention_hours: 168,
        };

        let result = config.validate_production();
        assert!(result.is_err());

        if let Err(ConfigError::ProductionValidation(errors)) = result {
            assert!(errors.len() >= 2);
        }
    }

    #[test]
    fn test_production_validation_passes_with_proper_config() {
        let config = Config {
            environment: Environment::Production,
            host: "0.0.0.0".to_string(),
            port: 8080,
            database_url: "file:/app/data/prod.db".to_string(),
            data_dir: PathBuf::from("/app/data"),
            backup_dir: PathBuf::from("/app/backups"),
            static_dir: Some(PathBuf::from("/app/static")),
            admin_key: None, // No admin key in production is fine
            max_upload_size: 1024,
            max_concurrent_uploads: 10,
            artifact_retention_hours: 168,
        };

        let result = config.validate_production();
        assert!(result.is_ok());
    }
}
