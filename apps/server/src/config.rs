//! Application configuration loaded from environment variables.

use std::env;
use std::path::PathBuf;

/// HTTP header name for API key authentication.
pub const API_KEY_HEADER: &str = "X-API-Key";

/// HTTP header name for admin key (bootstrap).
pub const ADMIN_KEY_HEADER: &str = "X-Admin-Key";

/// Development default values - NEVER use in production.
pub mod defaults {
    pub const DEV_DATABASE_URL: &str = "postgres://rrv:rrv@localhost:6432/rrv";
    pub const DEV_ADMIN_KEY: &str = "dev-admin-key-do-not-use-in-production";
    pub const DEV_HOST: &str = "127.0.0.1";
    pub const DEV_PORT: u16 = 8080;
    pub const DEV_MAX_UPLOAD_SIZE: usize = 52_428_800; // 50MB total per report
    pub const DEV_MAX_FILES_PER_REQUEST: usize = 20; // Max files per upload request (batching)
    pub const DEV_MAX_CONCURRENT_UPLOADS: usize = 10; // Max concurrent upload requests
    pub const DEV_UPLOAD_QUEUE_TIMEOUT_SECS: u64 = 30; // Queue timeout before rejecting

    // S3/MinIO defaults for development
    pub const DEV_S3_ENDPOINT: &str = "http://localhost:9100";
    pub const DEV_S3_BUCKET: &str = "reports";
    pub const DEV_S3_REGION: &str = "us-east-1";
    pub const DEV_S3_ACCESS_KEY: &str = "minioadmin";
    pub const DEV_S3_SECRET_KEY: &str = "minioadmin";
}

/// Runtime environment.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Environment {
    Development,
    Production,
}

impl Environment {
    /// Parse environment from string.
    pub fn parse(s: &str) -> Option<Self> {
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

/// S3 storage configuration.
#[derive(Debug, Clone)]
pub struct S3Config {
    /// S3 endpoint URL (for MinIO or custom S3-compatible services)
    pub endpoint: Option<String>,
    /// S3 bucket name
    pub bucket: String,
    /// S3 region
    pub region: String,
    /// S3 access key ID
    pub access_key: String,
    /// S3 secret access key
    pub secret_key: String,
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
    /// Database URL (PostgreSQL connection string)
    pub database_url: String,
    /// Directory for static frontend assets (production only)
    pub static_dir: Option<PathBuf>,
    /// Admin key for bootstrap operations (creating first API key)
    pub admin_key: Option<String>,
    /// Maximum upload size in bytes (default: 50MB)
    pub max_upload_size: usize,
    /// Maximum files per upload request for batching (default: 20)
    pub max_files_per_request: usize,
    /// Maximum concurrent upload batches (limits disk I/O, default: 10)
    pub max_concurrent_uploads: usize,
    /// Upload queue timeout in seconds (default: 30s)
    pub upload_queue_timeout_secs: u64,
    /// S3 storage configuration
    pub s3: S3Config,
}

impl Config {
    /// Load configuration from environment variables.
    ///
    /// In development mode (RUST_ENV=development):
    /// - All variables have sensible defaults
    /// - Only RUST_ENV is required
    ///
    /// In production mode (RUST_ENV=production):
    /// - DATABASE_URL is required
    /// - S3 configuration is required
    /// - Server will NOT start if using development defaults
    ///
    /// Environment variables:
    /// - `RUST_ENV`: Environment (development/production) - REQUIRED
    /// - `RRV_HOST`: Server host (default: 127.0.0.1)
    /// - `RRV_PORT`: Server port (default: 8080)
    /// - `DATABASE_URL`: PostgreSQL connection string (required in production)
    /// - `RRV_ADMIN_KEY`: Admin key for bootstrap operations (optional)
    /// - `RRV_STATIC_DIR`: Static assets directory for production
    /// - `RRV_MAX_UPLOAD_SIZE`: Max upload size in bytes (default: 50MB)
    /// - `RRV_MAX_FILES_PER_REQUEST`: Max files per batch upload (default: 20)
    /// - `RRV_MAX_CONCURRENT_UPLOADS`: Max concurrent upload batches (default: 10)
    /// - `RRV_UPLOAD_QUEUE_TIMEOUT_SECS`: Upload queue timeout in seconds (default: 30)
    /// - `S3_ENDPOINT`: S3 endpoint URL (for MinIO/custom S3)
    /// - `S3_BUCKET`: S3 bucket name
    /// - `S3_REGION`: S3 region
    /// - `S3_ACCESS_KEY`: S3 access key ID
    /// - `S3_SECRET_KEY`: S3 secret access key
    pub fn from_env() -> Result<Self, ConfigError> {
        // Parse environment - required
        let env_str = env::var("RUST_ENV").map_err(|_| ConfigError::MissingEnvVar("RUST_ENV"))?;

        let environment = Environment::parse(&env_str).ok_or(ConfigError::InvalidValue(
            "RUST_ENV must be 'development' or 'production'",
        ))?;

        // Load values with defaults
        let host = env::var("RRV_HOST").unwrap_or_else(|_| defaults::DEV_HOST.to_string());

        let port = env::var("RRV_PORT")
            .unwrap_or_else(|_| defaults::DEV_PORT.to_string())
            .parse::<u16>()
            .map_err(|_| ConfigError::InvalidValue("RRV_PORT must be a valid port number"))?;

        let database_url =
            env::var("DATABASE_URL").unwrap_or_else(|_| defaults::DEV_DATABASE_URL.to_string());

        // Admin key is optional - used for bootstrap operations
        let admin_key = if environment.is_development() {
            Some(env::var("RRV_ADMIN_KEY").unwrap_or_else(|_| defaults::DEV_ADMIN_KEY.to_string()))
        } else {
            env::var("RRV_ADMIN_KEY").ok()
        };

        let max_upload_size = env::var("RRV_MAX_UPLOAD_SIZE")
            .unwrap_or_else(|_| defaults::DEV_MAX_UPLOAD_SIZE.to_string())
            .parse::<usize>()
            .map_err(|_| ConfigError::InvalidValue("RRV_MAX_UPLOAD_SIZE must be a valid number"))?;

        let max_files_per_request = env::var("RRV_MAX_FILES_PER_REQUEST")
            .unwrap_or_else(|_| defaults::DEV_MAX_FILES_PER_REQUEST.to_string())
            .parse::<usize>()
            .map_err(|_| {
                ConfigError::InvalidValue("RRV_MAX_FILES_PER_REQUEST must be a valid number")
            })?;

        let max_concurrent_uploads = env::var("RRV_MAX_CONCURRENT_UPLOADS")
            .unwrap_or_else(|_| defaults::DEV_MAX_CONCURRENT_UPLOADS.to_string())
            .parse::<usize>()
            .map_err(|_| {
                ConfigError::InvalidValue("RRV_MAX_CONCURRENT_UPLOADS must be a valid number")
            })?;

        let upload_queue_timeout_secs = env::var("RRV_UPLOAD_QUEUE_TIMEOUT_SECS")
            .unwrap_or_else(|_| defaults::DEV_UPLOAD_QUEUE_TIMEOUT_SECS.to_string())
            .parse::<u64>()
            .map_err(|_| {
                ConfigError::InvalidValue("RRV_UPLOAD_QUEUE_TIMEOUT_SECS must be a valid number")
            })?;

        let static_dir = env::var("RRV_STATIC_DIR").ok().map(PathBuf::from);

        // S3 configuration
        let s3 = S3Config {
            endpoint: env::var("S3_ENDPOINT").ok().or_else(|| {
                if environment.is_development() {
                    Some(defaults::DEV_S3_ENDPOINT.to_string())
                } else {
                    None
                }
            }),
            bucket: env::var("S3_BUCKET").unwrap_or_else(|_| defaults::DEV_S3_BUCKET.to_string()),
            region: env::var("S3_REGION").unwrap_or_else(|_| defaults::DEV_S3_REGION.to_string()),
            access_key: env::var("S3_ACCESS_KEY")
                .unwrap_or_else(|_| defaults::DEV_S3_ACCESS_KEY.to_string()),
            secret_key: env::var("S3_SECRET_KEY")
                .unwrap_or_else(|_| defaults::DEV_S3_SECRET_KEY.to_string()),
        };

        let config = Config {
            environment,
            host,
            port,
            database_url,
            static_dir,
            admin_key,
            max_upload_size,
            max_files_per_request,
            max_concurrent_uploads,
            upload_queue_timeout_secs,
            s3,
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
                "DATABASE_URL is using development default '{}'. Set a production PostgreSQL URL.",
                defaults::DEV_DATABASE_URL
            ));
        }

        // Check if using dev S3 credentials in production
        if self.s3.access_key == defaults::DEV_S3_ACCESS_KEY
            || self.s3.secret_key == defaults::DEV_S3_SECRET_KEY
        {
            errors.push(
                "S3_ACCESS_KEY/S3_SECRET_KEY are using development defaults. Set production S3 credentials."
                    .to_string(),
            );
        }

        // Warn if admin key is using development default in production
        if let Some(ref key) = self.admin_key
            && key == defaults::DEV_ADMIN_KEY
        {
            errors.push(
                "RRV_ADMIN_KEY is using development default. Set a secure admin key or remove it."
                    .to_string(),
            );
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

    fn test_s3_config() -> S3Config {
        S3Config {
            endpoint: Some("http://localhost:9000".to_string()),
            bucket: "test".to_string(),
            region: "us-east-1".to_string(),
            access_key: "testkey".to_string(),
            secret_key: "testsecret".to_string(),
        }
    }

    #[test]
    fn test_bind_address() {
        let config = Config {
            environment: Environment::Development,
            host: "0.0.0.0".to_string(),
            port: 3000,
            database_url: "postgres://test:test@localhost:5432/test".to_string(),
            static_dir: None,
            admin_key: Some("test-key".to_string()),
            max_upload_size: 1024,
            max_files_per_request: 20,
            max_concurrent_uploads: 10,
            upload_queue_timeout_secs: 30,
            s3: test_s3_config(),
        };

        assert_eq!(config.bind_address(), "0.0.0.0:3000");
    }

    #[test]
    fn test_environment_parsing() {
        assert_eq!(
            Environment::parse("development"),
            Some(Environment::Development)
        );
        assert_eq!(Environment::parse("dev"), Some(Environment::Development));
        assert_eq!(
            Environment::parse("production"),
            Some(Environment::Production)
        );
        assert_eq!(Environment::parse("prod"), Some(Environment::Production));
        assert_eq!(Environment::parse("invalid"), None);
    }

    #[test]
    fn test_production_validation_fails_with_dev_defaults() {
        let config = Config {
            environment: Environment::Production,
            host: "0.0.0.0".to_string(),
            port: 8080,
            database_url: defaults::DEV_DATABASE_URL.to_string(),
            static_dir: None,
            admin_key: Some(defaults::DEV_ADMIN_KEY.to_string()),
            max_upload_size: 1024,
            max_files_per_request: 20,
            max_concurrent_uploads: 10,
            upload_queue_timeout_secs: 30,
            s3: S3Config {
                endpoint: None,
                bucket: "reports".to_string(),
                region: "us-east-1".to_string(),
                access_key: defaults::DEV_S3_ACCESS_KEY.to_string(),
                secret_key: defaults::DEV_S3_SECRET_KEY.to_string(),
            },
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
            database_url: "postgres://user:pass@prod-db:5432/rrv".to_string(),
            static_dir: Some(PathBuf::from("/app/static")),
            admin_key: None,
            max_upload_size: 1024,
            max_files_per_request: 20,
            max_concurrent_uploads: 10,
            upload_queue_timeout_secs: 30,
            s3: S3Config {
                endpoint: None, // Use AWS S3 in production
                bucket: "prod-reports".to_string(),
                region: "us-west-2".to_string(),
                access_key: "AKIA...".to_string(),
                secret_key: "secret...".to_string(),
            },
        };

        let result = config.validate_production();
        assert!(result.is_ok());
    }
}
