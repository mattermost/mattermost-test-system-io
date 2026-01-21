//! Application configuration loaded from environment variables.

use std::env;
use std::path::PathBuf;

/// HTTP header name for API key authentication.
pub const API_KEY_HEADER: &str = "X-API-Key";

/// HTTP header name for admin key (bootstrap).
pub const ADMIN_KEY_HEADER: &str = "X-Admin-Key";

/// Development default values - NEVER use in production.
pub mod defaults {
    pub const DEV_DATABASE_URL: &str = "postgres://tsio:tsio@localhost:6432/tsio";
    pub const DEV_ADMIN_KEY: &str = "dev-admin-key-do-not-use-in-production";
    pub const DEV_HOST: &str = "127.0.0.1";
    pub const DEV_PORT: u16 = 8080;
    pub const DEV_MAX_UPLOAD_SIZE: usize = 10_485_760; // 10MB max for JSON payloads
    pub const DEV_UPLOAD_TIMEOUT_MS: u64 = 3_600_000; // 1 hour default for upload timeout
    pub const DEV_MIN_SEARCH_LENGTH: usize = 2; // Minimum characters for search API

    // S3/MinIO defaults for development
    pub const DEV_S3_ENDPOINT: &str = "http://localhost:9100";
    pub const DEV_S3_BUCKET: &str = "reports";
    pub const DEV_S3_REGION: &str = "us-east-1";
    pub const DEV_S3_ACCESS_KEY: &str = "minioadmin";
    pub const DEV_S3_SECRET_KEY: &str = "minioadmin";

    // Database pool defaults (smaller in dev to avoid "too many open files")
    pub const DEV_DB_MAX_CONNECTIONS: u32 = 20;
    pub const DEV_DB_MIN_CONNECTIONS: u32 = 2;
    pub const DEV_DB_IDLE_TIMEOUT_SECS: u64 = 300;
    pub const DEV_DB_MAX_LIFETIME_SECS: u64 = 1800;
    pub const DEV_DB_CONNECT_TIMEOUT_SECS: u64 = 10;
    pub const DEV_DB_ACQUIRE_TIMEOUT_SECS: u64 = 10;

    // Production database pool defaults
    pub const PROD_DB_MAX_CONNECTIONS: u32 = 50;
    pub const PROD_DB_MIN_CONNECTIONS: u32 = 5;

    // Actix server defaults (smaller in dev to avoid "too many open files")
    pub const DEV_SERVER_WORKERS: usize = 4;
    pub const DEV_SERVER_BACKLOG: u32 = 128;
    pub const DEV_SERVER_MAX_CONNECTIONS: usize = 256;
    pub const DEV_SERVER_MAX_CONNECTION_RATE: usize = 256;

    // Production Actix server defaults
    pub const PROD_SERVER_BACKLOG: u32 = 2048;
    pub const PROD_SERVER_MAX_CONNECTIONS: usize = 25000;
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

/// Database configuration including connection URL and pool settings.
#[derive(Debug, Clone)]
pub struct DatabaseSettings {
    /// PostgreSQL connection string
    pub url: String,
    /// Maximum number of connections in the pool
    pub max_connections: u32,
    /// Minimum number of connections in the pool
    pub min_connections: u32,
    /// Connection timeout in seconds
    pub connect_timeout_secs: u64,
    /// Acquire timeout in seconds
    pub acquire_timeout_secs: u64,
    /// Idle connection timeout in seconds
    pub idle_timeout_secs: u64,
    /// Maximum connection lifetime in seconds
    pub max_lifetime_secs: u64,
}

/// S3/MinIO storage configuration.
#[derive(Debug, Clone)]
pub struct StorageSettings {
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

/// HTTP server configuration.
#[derive(Debug, Clone)]
pub struct ServerSettings {
    /// Server host address
    pub host: String,
    /// Server port
    pub port: u16,
    /// Directory for static frontend assets (production only)
    pub static_dir: Option<PathBuf>,
    /// Number of worker threads (0 = number of CPUs)
    pub workers: usize,
    /// Maximum pending connections in the accept queue
    pub backlog: u32,
    /// Maximum concurrent connections per worker
    pub max_connections: usize,
    /// Maximum new connections per second per worker
    pub max_connection_rate: usize,
}

impl ServerSettings {
    /// Get the server bind address.
    pub fn bind_address(&self) -> String {
        format!("{}:{}", self.host, self.port)
    }
}

/// Upload-related configuration.
#[derive(Debug, Clone)]
pub struct UploadSettings {
    /// Maximum JSON payload size in bytes (default: 10MB)
    pub max_size: usize,
    /// Upload timeout in milliseconds (default: 1 hour)
    pub timeout_ms: u64,
}

/// Authentication and feature flag configuration.
#[derive(Debug, Clone)]
pub struct AuthSettings {
    /// Admin key for bootstrap operations (creating first API key)
    pub admin_key: Option<String>,
    /// Enable HTML view tabs in the frontend (default: true)
    pub enable_html_view: bool,
    /// Minimum characters required for search API (default: 2)
    pub min_search_length: usize,
}

/// Application configuration.
#[derive(Debug, Clone)]
pub struct Config {
    /// Runtime environment
    pub environment: Environment,
    /// Database configuration
    pub database: DatabaseSettings,
    /// S3/MinIO storage configuration
    pub storage: StorageSettings,
    /// HTTP server configuration
    pub server: ServerSettings,
    /// Upload configuration
    pub upload: UploadSettings,
    /// Authentication and feature flags
    pub auth: AuthSettings,
}

impl Config {
    /// Load configuration from environment variables.
    ///
    /// In development mode (RUST_ENV=development):
    /// - All variables have sensible defaults
    /// - Only RUST_ENV is required
    ///
    /// In production mode (RUST_ENV=production):
    /// - TSIO_DATABASE_URL is required
    /// - S3 configuration is required
    /// - Server will NOT start if using development defaults
    ///
    /// Environment variables:
    /// - `RUST_ENV`: Environment (development/production) - REQUIRED
    ///
    /// Server settings:
    /// - `TSIO_HOST`: Server host (default: 127.0.0.1)
    /// - `TSIO_PORT`: Server port (default: 8080)
    /// - `TSIO_STATIC_DIR`: Static assets directory for production
    /// - `TSIO_SERVER_WORKERS`: Number of worker threads, 0=auto (default: 4 dev, 0 prod)
    /// - `TSIO_SERVER_BACKLOG`: Pending connection queue size (default: 128 dev, 2048 prod)
    /// - `TSIO_SERVER_MAX_CONNECTIONS`: Max connections per worker (default: 256 dev, 25000 prod)
    /// - `TSIO_SERVER_MAX_CONNECTION_RATE`: Max new connections/sec/worker (default: 256)
    ///
    /// Database settings:
    /// - `TSIO_DATABASE_URL`: PostgreSQL connection string (required in production)
    /// - `TSIO_DB_MAX_CONNECTIONS`: Max database connections (default: 20 dev, 50 prod)
    /// - `TSIO_DB_MIN_CONNECTIONS`: Min database connections (default: 2 dev, 5 prod)
    /// - `TSIO_DB_IDLE_TIMEOUT_SECS`: Idle connection timeout (default: 300)
    /// - `TSIO_DB_MAX_LIFETIME_SECS`: Max connection lifetime (default: 1800)
    /// - `TSIO_DB_CONNECT_TIMEOUT_SECS`: Connection timeout (default: 10)
    /// - `TSIO_DB_ACQUIRE_TIMEOUT_SECS`: Acquire timeout (default: 10)
    ///
    /// Storage settings:
    /// - `TSIO_S3_ENDPOINT`: S3 endpoint URL (for MinIO/custom S3)
    /// - `TSIO_S3_BUCKET`: S3 bucket name
    /// - `TSIO_S3_REGION`: S3 region
    /// - `TSIO_S3_ACCESS_KEY`: S3 access key ID
    /// - `TSIO_S3_SECRET_KEY`: S3 secret access key
    ///
    /// Upload settings:
    /// - `TSIO_MAX_UPLOAD_SIZE`: Max JSON payload size in bytes (default: 10MB)
    /// - `TSIO_UPLOAD_TIMEOUT_MS`: Upload timeout in milliseconds (default: 1 hour)
    ///
    /// Auth settings:
    /// - `TSIO_ADMIN_KEY`: Admin key for bootstrap operations (optional)
    /// - `TSIO_ENABLE_HTML_VIEW`: Enable HTML view tabs in frontend (default: true)
    /// - `TSIO_MIN_SEARCH_LENGTH`: Minimum characters for search API (default: 2)
    pub fn from_env() -> Result<Self, ConfigError> {
        // Parse environment - required
        let env_str = env::var("RUST_ENV").map_err(|_| ConfigError::MissingEnvVar("RUST_ENV"))?;

        let environment = Environment::parse(&env_str).ok_or(ConfigError::InvalidValue(
            "RUST_ENV must be 'development' or 'production'",
        ))?;

        // Server settings
        let server = Self::load_server_settings(&environment)?;

        // Database settings
        let database = Self::load_database_settings(&environment)?;

        // Storage settings
        let storage = Self::load_storage_settings(&environment);

        // Upload settings
        let upload = Self::load_upload_settings()?;

        // Auth settings
        let auth = Self::load_auth_settings(&environment)?;

        let config = Config {
            environment,
            database,
            storage,
            server,
            upload,
            auth,
        };

        // Validate production configuration
        if environment.is_production() {
            config.validate_production()?;
        }

        Ok(config)
    }

    fn load_server_settings(environment: &Environment) -> Result<ServerSettings, ConfigError> {
        let host = env::var("TSIO_HOST").unwrap_or_else(|_| defaults::DEV_HOST.to_string());

        let port = env::var("TSIO_PORT")
            .unwrap_or_else(|_| defaults::DEV_PORT.to_string())
            .parse::<u16>()
            .map_err(|_| ConfigError::InvalidValue("TSIO_PORT must be a valid port number"))?;

        let static_dir = env::var("TSIO_STATIC_DIR").ok().map(PathBuf::from);

        let (default_workers, default_backlog, default_max_conn) = if environment.is_development() {
            (
                defaults::DEV_SERVER_WORKERS,
                defaults::DEV_SERVER_BACKLOG,
                defaults::DEV_SERVER_MAX_CONNECTIONS,
            )
        } else {
            (
                0, // 0 = num_cpus
                defaults::PROD_SERVER_BACKLOG,
                defaults::PROD_SERVER_MAX_CONNECTIONS,
            )
        };

        Ok(ServerSettings {
            host,
            port,
            static_dir,
            workers: env::var("TSIO_SERVER_WORKERS")
                .unwrap_or_else(|_| default_workers.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_SERVER_WORKERS must be a valid number"))?,
            backlog: env::var("TSIO_SERVER_BACKLOG")
                .unwrap_or_else(|_| default_backlog.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_SERVER_BACKLOG must be a valid number"))?,
            max_connections: env::var("TSIO_SERVER_MAX_CONNECTIONS")
                .unwrap_or_else(|_| default_max_conn.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_SERVER_MAX_CONNECTIONS must be a valid number"))?,
            max_connection_rate: env::var("TSIO_SERVER_MAX_CONNECTION_RATE")
                .unwrap_or_else(|_| defaults::DEV_SERVER_MAX_CONNECTION_RATE.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_SERVER_MAX_CONNECTION_RATE must be a valid number"))?,
        })
    }

    fn load_database_settings(environment: &Environment) -> Result<DatabaseSettings, ConfigError> {
        let url = env::var("TSIO_DATABASE_URL")
            .unwrap_or_else(|_| defaults::DEV_DATABASE_URL.to_string());

        let (default_max_conn, default_min_conn) = if environment.is_development() {
            (defaults::DEV_DB_MAX_CONNECTIONS, defaults::DEV_DB_MIN_CONNECTIONS)
        } else {
            (defaults::PROD_DB_MAX_CONNECTIONS, defaults::PROD_DB_MIN_CONNECTIONS)
        };

        Ok(DatabaseSettings {
            url,
            max_connections: env::var("TSIO_DB_MAX_CONNECTIONS")
                .unwrap_or_else(|_| default_max_conn.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_DB_MAX_CONNECTIONS must be a valid number"))?,
            min_connections: env::var("TSIO_DB_MIN_CONNECTIONS")
                .unwrap_or_else(|_| default_min_conn.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_DB_MIN_CONNECTIONS must be a valid number"))?,
            connect_timeout_secs: env::var("TSIO_DB_CONNECT_TIMEOUT_SECS")
                .unwrap_or_else(|_| defaults::DEV_DB_CONNECT_TIMEOUT_SECS.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_DB_CONNECT_TIMEOUT_SECS must be a valid number"))?,
            acquire_timeout_secs: env::var("TSIO_DB_ACQUIRE_TIMEOUT_SECS")
                .unwrap_or_else(|_| defaults::DEV_DB_ACQUIRE_TIMEOUT_SECS.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_DB_ACQUIRE_TIMEOUT_SECS must be a valid number"))?,
            idle_timeout_secs: env::var("TSIO_DB_IDLE_TIMEOUT_SECS")
                .unwrap_or_else(|_| defaults::DEV_DB_IDLE_TIMEOUT_SECS.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_DB_IDLE_TIMEOUT_SECS must be a valid number"))?,
            max_lifetime_secs: env::var("TSIO_DB_MAX_LIFETIME_SECS")
                .unwrap_or_else(|_| defaults::DEV_DB_MAX_LIFETIME_SECS.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_DB_MAX_LIFETIME_SECS must be a valid number"))?,
        })
    }

    fn load_storage_settings(environment: &Environment) -> StorageSettings {
        StorageSettings {
            endpoint: env::var("TSIO_S3_ENDPOINT").ok().or_else(|| {
                if environment.is_development() {
                    Some(defaults::DEV_S3_ENDPOINT.to_string())
                } else {
                    None
                }
            }),
            bucket: env::var("TSIO_S3_BUCKET")
                .unwrap_or_else(|_| defaults::DEV_S3_BUCKET.to_string()),
            region: env::var("TSIO_S3_REGION")
                .unwrap_or_else(|_| defaults::DEV_S3_REGION.to_string()),
            access_key: env::var("TSIO_S3_ACCESS_KEY")
                .unwrap_or_else(|_| defaults::DEV_S3_ACCESS_KEY.to_string()),
            secret_key: env::var("TSIO_S3_SECRET_KEY")
                .unwrap_or_else(|_| defaults::DEV_S3_SECRET_KEY.to_string()),
        }
    }

    fn load_upload_settings() -> Result<UploadSettings, ConfigError> {
        Ok(UploadSettings {
            max_size: env::var("TSIO_MAX_UPLOAD_SIZE")
                .unwrap_or_else(|_| defaults::DEV_MAX_UPLOAD_SIZE.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_MAX_UPLOAD_SIZE must be a valid number"))?,
            timeout_ms: env::var("TSIO_UPLOAD_TIMEOUT_MS")
                .unwrap_or_else(|_| defaults::DEV_UPLOAD_TIMEOUT_MS.to_string())
                .parse()
                .map_err(|_| ConfigError::InvalidValue("TSIO_UPLOAD_TIMEOUT_MS must be a valid number"))?,
        })
    }

    fn load_auth_settings(environment: &Environment) -> Result<AuthSettings, ConfigError> {
        let admin_key = if environment.is_development() {
            Some(env::var("TSIO_ADMIN_KEY").unwrap_or_else(|_| defaults::DEV_ADMIN_KEY.to_string()))
        } else {
            env::var("TSIO_ADMIN_KEY").ok()
        };

        let enable_html_view = env::var("TSIO_ENABLE_HTML_VIEW")
            .map(|v| v.to_lowercase() != "false" && v != "0")
            .unwrap_or(true);

        let min_search_length = env::var("TSIO_MIN_SEARCH_LENGTH")
            .unwrap_or_else(|_| defaults::DEV_MIN_SEARCH_LENGTH.to_string())
            .parse()
            .map_err(|_| ConfigError::InvalidValue("TSIO_MIN_SEARCH_LENGTH must be a valid number"))?;

        Ok(AuthSettings {
            admin_key,
            enable_html_view,
            min_search_length,
        })
    }

    /// Validate that production configuration does not use development defaults.
    fn validate_production(&self) -> Result<(), ConfigError> {
        let mut errors = Vec::new();

        if self.database.url == defaults::DEV_DATABASE_URL {
            errors.push(format!(
                "TSIO_DATABASE_URL is using development default '{}'. Set a production PostgreSQL URL.",
                defaults::DEV_DATABASE_URL
            ));
        }

        // Check if using dev S3 credentials in production
        if self.storage.access_key == defaults::DEV_S3_ACCESS_KEY
            || self.storage.secret_key == defaults::DEV_S3_SECRET_KEY
        {
            errors.push(
                "TSIO_S3_ACCESS_KEY/TSIO_S3_SECRET_KEY are using development defaults. Set production S3 credentials."
                    .to_string(),
            );
        }

        // Warn if admin key is using development default in production
        if let Some(ref key) = self.auth.admin_key
            && key == defaults::DEV_ADMIN_KEY
        {
            errors.push(
                "TSIO_ADMIN_KEY is using development default. Set a secure admin key or remove it."
                    .to_string(),
            );
        }

        if !errors.is_empty() {
            return Err(ConfigError::ProductionValidation(errors));
        }

        Ok(())
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

    fn _test_storage_settings() -> StorageSettings {
        StorageSettings {
            endpoint: Some("http://localhost:9000".to_string()),
            bucket: "test".to_string(),
            region: "us-east-1".to_string(),
            access_key: "testkey".to_string(),
            secret_key: "testsecret".to_string(),
        }
    }

    fn test_database_settings() -> DatabaseSettings {
        DatabaseSettings {
            url: "postgres://test:test@localhost:5432/test".to_string(),
            max_connections: 20,
            min_connections: 2,
            connect_timeout_secs: 10,
            acquire_timeout_secs: 10,
            idle_timeout_secs: 300,
            max_lifetime_secs: 1800,
        }
    }

    fn test_server_settings() -> ServerSettings {
        ServerSettings {
            host: "0.0.0.0".to_string(),
            port: 3000,
            static_dir: None,
            workers: 4,
            backlog: 128,
            max_connections: 256,
            max_connection_rate: 256,
        }
    }

    fn test_upload_settings() -> UploadSettings {
        UploadSettings {
            max_size: 1024,
            timeout_ms: 3600000,
        }
    }

    fn test_auth_settings() -> AuthSettings {
        AuthSettings {
            admin_key: Some("test-key".to_string()),
            enable_html_view: true,
            min_search_length: 2,
        }
    }

    #[test]
    fn test_bind_address() {
        let server = test_server_settings();
        assert_eq!(server.bind_address(), "0.0.0.0:3000");
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
            database: DatabaseSettings {
                url: defaults::DEV_DATABASE_URL.to_string(),
                ..test_database_settings()
            },
            storage: StorageSettings {
                endpoint: None,
                bucket: "reports".to_string(),
                region: "us-east-1".to_string(),
                access_key: defaults::DEV_S3_ACCESS_KEY.to_string(),
                secret_key: defaults::DEV_S3_SECRET_KEY.to_string(),
            },
            server: test_server_settings(),
            upload: test_upload_settings(),
            auth: AuthSettings {
                admin_key: Some(defaults::DEV_ADMIN_KEY.to_string()),
                ..test_auth_settings()
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
            database: DatabaseSettings {
                url: "postgres://user:pass@prod-db:5432/tsio".to_string(),
                ..test_database_settings()
            },
            storage: StorageSettings {
                endpoint: None, // Use AWS S3 in production
                bucket: "prod-reports".to_string(),
                region: "us-west-2".to_string(),
                access_key: "AKIA...".to_string(),
                secret_key: "secret...".to_string(),
            },
            server: ServerSettings {
                static_dir: Some(PathBuf::from("/app/static")),
                ..test_server_settings()
            },
            upload: test_upload_settings(),
            auth: AuthSettings {
                admin_key: None,
                ..test_auth_settings()
            },
        };

        let result = config.validate_production();
        assert!(result.is_ok());
    }
}
