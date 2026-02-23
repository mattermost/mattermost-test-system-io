//! Application configuration loaded from environment variables.

use std::env;
use std::path::PathBuf;

use secrecy::{ExposeSecret, SecretString};

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
    /// S3 access key ID (None = use IAM role / default AWS credential chain)
    pub access_key: Option<String>,
    /// S3 secret access key (None = use IAM role / default AWS credential chain)
    pub secret_key: Option<String>,
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

/// Authentication configuration.
#[derive(Debug, Clone)]
pub struct AuthSettings {
    /// Admin key for bootstrap operations (creating first API key)
    pub admin_key: Option<String>,
}

/// Feature flags, UI, and upload configuration (`TSIO_FEATURE_*`).
///
/// Field naming convention: `{group}_{name}` where group identifies the
/// logical area (e.g. `html_`, `search_`, `upload_`). This keeps the
/// struct flat while making groupings obvious at a glance:
///
/// ```text
/// config.features.html_view_enabled   ← TSIO_FEATURE_HTML_VIEW_ENABLED
/// config.features.search_min_length   ← TSIO_FEATURE_SEARCH_MIN_LENGTH
/// config.features.upload_max_size     ← TSIO_FEATURE_UPLOAD_MAX_SIZE
/// config.features.upload_timeout_ms   ← TSIO_FEATURE_UPLOAD_TIMEOUT_MS
/// ```
#[derive(Debug, Clone)]
pub struct FeatureSettings {
    /// Enable HTML view tabs in the frontend (default: true)
    pub html_view_enabled: bool,
    /// Minimum characters required for search API (default: 2)
    pub search_min_length: usize,
    /// Maximum JSON payload size in bytes (default: 10MB)
    pub upload_max_size: usize,
    /// Upload timeout in milliseconds (default: 1 hour)
    pub upload_timeout_ms: u64,
}

/// GitHub Actions OIDC configuration for CI/CD token-based auth.
#[derive(Debug, Clone)]
pub struct GitHubOidcSettings {
    /// Enable GitHub OIDC authentication
    pub enabled: bool,
    /// Allowed repository patterns (e.g., "org/repo", "org/*")
    pub allowed_repos: Vec<String>,
    /// OIDC issuer URL
    pub issuer: String,
    /// Expected audience claim (optional)
    pub audience: Option<String>,
}

/// GitHub OAuth configuration for web UI login.
///
/// `client_secret` and `session_secret` are wrapped in `SecretString`
/// to prevent accidental logging and ensure memory zeroization on drop.
#[derive(Clone)]
pub struct GitHubOAuthSettings {
    /// Enable GitHub OAuth login
    pub enabled: bool,
    /// GitHub OAuth App client ID
    pub client_id: Option<String>,
    /// GitHub OAuth App client secret (SecretString — never logged)
    pub client_secret: Option<SecretString>,
    /// Allowed GitHub organizations (empty = allow all)
    pub allowed_orgs: Vec<String>,
    /// Secret key for signing session JWTs (SecretString — never logged)
    pub session_secret: SecretString,
    /// Access token TTL in seconds (default: 900 = 15 min)
    pub access_token_ttl_secs: u64,
    /// Refresh token TTL in seconds (default: 604800 = 7 days)
    pub refresh_token_ttl_secs: u64,
    /// OAuth callback redirect URL (optional, defaults to /api/v1/auth/github/callback)
    pub redirect_url: Option<String>,
}

impl std::fmt::Debug for GitHubOAuthSettings {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("GitHubOAuthSettings")
            .field("enabled", &self.enabled)
            .field("client_id", &self.client_id)
            .field("client_secret", &"[REDACTED]")
            .field("allowed_orgs", &self.allowed_orgs)
            .field("session_secret", &"[REDACTED]")
            .field("access_token_ttl_secs", &self.access_token_ttl_secs)
            .field("refresh_token_ttl_secs", &self.refresh_token_ttl_secs)
            .field("redirect_url", &self.redirect_url)
            .finish()
    }
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
    /// Authentication settings
    pub auth: AuthSettings,
    /// Feature flags
    pub features: FeatureSettings,
    /// GitHub Actions OIDC configuration
    pub github_oidc: GitHubOidcSettings,
    /// GitHub OAuth configuration
    pub github_oauth: GitHubOAuthSettings,
}

impl Config {
    /// Load configuration from environment variables.
    ///
    /// In development mode (RUST_ENV=development):
    /// - All variables have sensible defaults
    /// - Only RUST_ENV is required
    ///
    /// In production mode (RUST_ENV=production):
    /// - TSIO_DB_URL is required
    /// - S3 configuration is required
    /// - Server will NOT start if using development defaults
    ///
    /// Environment variables:
    /// - `RUST_ENV`: Environment (development/production) - REQUIRED
    ///
    /// Server settings (`TSIO_SERVER_*`):
    /// - `TSIO_SERVER_HOST`: Server host (default: 127.0.0.1)
    /// - `TSIO_SERVER_PORT`: Server port (default: 8080)
    /// - `TSIO_SERVER_STATIC_DIR`: Static assets directory for production
    /// - `TSIO_SERVER_WORKERS`: Number of worker threads, 0=auto (default: 4 dev, 0 prod)
    /// - `TSIO_SERVER_BACKLOG`: Pending connection queue size (default: 128 dev, 2048 prod)
    /// - `TSIO_SERVER_MAX_CONNECTIONS`: Max connections per worker (default: 256 dev, 25000 prod)
    /// - `TSIO_SERVER_MAX_CONNECTION_RATE`: Max new connections/sec/worker (default: 256)
    ///
    /// Database settings (`TSIO_DB_*`):
    /// - `TSIO_DB_URL`: PostgreSQL connection string (required in production)
    /// - `TSIO_DB_MAX_CONNECTIONS`: Max database connections (default: 20 dev, 50 prod)
    /// - `TSIO_DB_MIN_CONNECTIONS`: Min database connections (default: 2 dev, 5 prod)
    /// - `TSIO_DB_IDLE_TIMEOUT_SECS`: Idle connection timeout (default: 300)
    /// - `TSIO_DB_MAX_LIFETIME_SECS`: Max connection lifetime (default: 1800)
    /// - `TSIO_DB_CONNECT_TIMEOUT_SECS`: Connection timeout (default: 10)
    /// - `TSIO_DB_ACQUIRE_TIMEOUT_SECS`: Acquire timeout (default: 10)
    ///
    /// Storage settings (`TSIO_S3_*`):
    /// - `TSIO_S3_ENDPOINT`: S3 endpoint URL (for MinIO/custom S3)
    /// - `TSIO_S3_BUCKET`: S3 bucket name
    /// - `TSIO_S3_REGION`: S3 region
    /// - `TSIO_S3_ACCESS_KEY`: S3 access key ID
    /// - `TSIO_S3_SECRET_KEY`: S3 secret access key
    ///
    /// Auth settings (`TSIO_AUTH_*`):
    /// - `TSIO_AUTH_ADMIN_KEY`: Admin key for bootstrap operations (optional)
    ///
    /// Feature settings (`TSIO_FEATURE_*`):
    /// - `TSIO_FEATURE_HTML_VIEW_ENABLED`: Enable HTML view tabs in frontend (default: true)
    /// - `TSIO_FEATURE_SEARCH_MIN_LENGTH`: Minimum characters for search API (default: 2)
    /// - `TSIO_FEATURE_UPLOAD_MAX_SIZE`: Max JSON payload size in bytes (default: 10MB)
    /// - `TSIO_FEATURE_UPLOAD_TIMEOUT_MS`: Upload timeout in milliseconds (default: 1 hour)
    ///
    /// GitHub OIDC settings (`TSIO_GITHUB_OIDC_*`):
    /// - `TSIO_GITHUB_OIDC_ENABLED`: Enable GitHub Actions OIDC (default: false)
    /// - `TSIO_GITHUB_OIDC_ALLOWED_REPOS`: Comma-separated repo patterns (e.g., "org/repo,org/*")
    /// - `TSIO_GITHUB_OIDC_ISSUER`: OIDC issuer URL (default: https://token.actions.githubusercontent.com)
    /// - `TSIO_GITHUB_OIDC_AUDIENCE`: Expected audience claim (optional)
    ///
    /// GitHub OAuth settings (`TSIO_GITHUB_OAUTH_*`):
    /// - `TSIO_GITHUB_OAUTH_ENABLED`: Enable GitHub OAuth login (default: false)
    /// - `TSIO_GITHUB_OAUTH_CLIENT_ID`: GitHub OAuth App client ID
    /// - `TSIO_GITHUB_OAUTH_CLIENT_SECRET`: GitHub OAuth App client secret
    /// - `TSIO_GITHUB_OAUTH_ALLOWED_ORGS`: Comma-separated allowed org names (empty = allow all)
    /// - `TSIO_GITHUB_OAUTH_SESSION_SECRET`: Secret for signing session JWTs (required if OAuth enabled)
    /// - `TSIO_GITHUB_OAUTH_ACCESS_TOKEN_TTL_SECS`: Access token TTL in seconds (default: 900 = 15 min)
    /// - `TSIO_GITHUB_OAUTH_REFRESH_TOKEN_TTL_SECS`: Refresh token TTL in seconds (default: 604800 = 7 days)
    /// - `TSIO_GITHUB_OAUTH_REDIRECT_URL`: OAuth callback URL (optional)
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

        // Auth settings
        let auth = Self::load_auth_settings(&environment);

        // Feature and upload settings
        let features = Self::load_feature_settings()?;

        // GitHub OIDC settings
        let github_oidc = Self::load_github_oidc_settings();

        // GitHub OAuth settings
        let github_oauth = Self::load_github_oauth_settings(&environment)?;

        let config = Config {
            environment,
            database,
            storage,
            server,
            auth,
            features,
            github_oidc,
            github_oauth,
        };

        // Validate production configuration
        if environment.is_production() {
            config.validate_production()?;
        }

        Ok(config)
    }

    fn load_server_settings(environment: &Environment) -> Result<ServerSettings, ConfigError> {
        let host = env::var("TSIO_SERVER_HOST").unwrap_or_else(|_| defaults::DEV_HOST.to_string());

        let port = env::var("TSIO_SERVER_PORT")
            .unwrap_or_else(|_| defaults::DEV_PORT.to_string())
            .parse::<u16>()
            .map_err(|_| {
                ConfigError::InvalidValue("TSIO_SERVER_PORT must be a valid port number")
            })?;

        let static_dir = env::var("TSIO_SERVER_STATIC_DIR").ok().map(PathBuf::from);

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
                .map_err(|_| {
                    ConfigError::InvalidValue("TSIO_SERVER_WORKERS must be a valid number")
                })?,
            backlog: env::var("TSIO_SERVER_BACKLOG")
                .unwrap_or_else(|_| default_backlog.to_string())
                .parse()
                .map_err(|_| {
                    ConfigError::InvalidValue("TSIO_SERVER_BACKLOG must be a valid number")
                })?,
            max_connections: env::var("TSIO_SERVER_MAX_CONNECTIONS")
                .unwrap_or_else(|_| default_max_conn.to_string())
                .parse()
                .map_err(|_| {
                    ConfigError::InvalidValue("TSIO_SERVER_MAX_CONNECTIONS must be a valid number")
                })?,
            max_connection_rate: env::var("TSIO_SERVER_MAX_CONNECTION_RATE")
                .unwrap_or_else(|_| defaults::DEV_SERVER_MAX_CONNECTION_RATE.to_string())
                .parse()
                .map_err(|_| {
                    ConfigError::InvalidValue(
                        "TSIO_SERVER_MAX_CONNECTION_RATE must be a valid number",
                    )
                })?,
        })
    }

    fn load_database_settings(environment: &Environment) -> Result<DatabaseSettings, ConfigError> {
        let url =
            env::var("TSIO_DB_URL").unwrap_or_else(|_| defaults::DEV_DATABASE_URL.to_string());

        let (default_max_conn, default_min_conn) = if environment.is_development() {
            (
                defaults::DEV_DB_MAX_CONNECTIONS,
                defaults::DEV_DB_MIN_CONNECTIONS,
            )
        } else {
            (
                defaults::PROD_DB_MAX_CONNECTIONS,
                defaults::PROD_DB_MIN_CONNECTIONS,
            )
        };

        Ok(DatabaseSettings {
            url,
            max_connections: env::var("TSIO_DB_MAX_CONNECTIONS")
                .unwrap_or_else(|_| default_max_conn.to_string())
                .parse()
                .map_err(|_| {
                    ConfigError::InvalidValue("TSIO_DB_MAX_CONNECTIONS must be a valid number")
                })?,
            min_connections: env::var("TSIO_DB_MIN_CONNECTIONS")
                .unwrap_or_else(|_| default_min_conn.to_string())
                .parse()
                .map_err(|_| {
                    ConfigError::InvalidValue("TSIO_DB_MIN_CONNECTIONS must be a valid number")
                })?,
            connect_timeout_secs: env::var("TSIO_DB_CONNECT_TIMEOUT_SECS")
                .unwrap_or_else(|_| defaults::DEV_DB_CONNECT_TIMEOUT_SECS.to_string())
                .parse()
                .map_err(|_| {
                    ConfigError::InvalidValue("TSIO_DB_CONNECT_TIMEOUT_SECS must be a valid number")
                })?,
            acquire_timeout_secs: env::var("TSIO_DB_ACQUIRE_TIMEOUT_SECS")
                .unwrap_or_else(|_| defaults::DEV_DB_ACQUIRE_TIMEOUT_SECS.to_string())
                .parse()
                .map_err(|_| {
                    ConfigError::InvalidValue("TSIO_DB_ACQUIRE_TIMEOUT_SECS must be a valid number")
                })?,
            idle_timeout_secs: env::var("TSIO_DB_IDLE_TIMEOUT_SECS")
                .unwrap_or_else(|_| defaults::DEV_DB_IDLE_TIMEOUT_SECS.to_string())
                .parse()
                .map_err(|_| {
                    ConfigError::InvalidValue("TSIO_DB_IDLE_TIMEOUT_SECS must be a valid number")
                })?,
            max_lifetime_secs: env::var("TSIO_DB_MAX_LIFETIME_SECS")
                .unwrap_or_else(|_| defaults::DEV_DB_MAX_LIFETIME_SECS.to_string())
                .parse()
                .map_err(|_| {
                    ConfigError::InvalidValue("TSIO_DB_MAX_LIFETIME_SECS must be a valid number")
                })?,
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
            access_key: env::var("TSIO_S3_ACCESS_KEY").ok().or_else(|| {
                if environment.is_development() {
                    Some(defaults::DEV_S3_ACCESS_KEY.to_string())
                } else {
                    None
                }
            }),
            secret_key: env::var("TSIO_S3_SECRET_KEY").ok().or_else(|| {
                if environment.is_development() {
                    Some(defaults::DEV_S3_SECRET_KEY.to_string())
                } else {
                    None
                }
            }),
        }
    }

    fn load_auth_settings(environment: &Environment) -> AuthSettings {
        let admin_key = if environment.is_development() {
            Some(
                env::var("TSIO_AUTH_ADMIN_KEY")
                    .unwrap_or_else(|_| defaults::DEV_ADMIN_KEY.to_string()),
            )
        } else {
            env::var("TSIO_AUTH_ADMIN_KEY").ok()
        };

        AuthSettings { admin_key }
    }

    fn load_feature_settings() -> Result<FeatureSettings, ConfigError> {
        let html_view_enabled = env::var("TSIO_FEATURE_HTML_VIEW_ENABLED")
            .map(|v| v.to_lowercase() != "false" && v != "0")
            .unwrap_or(true);

        let search_min_length = env::var("TSIO_FEATURE_SEARCH_MIN_LENGTH")
            .unwrap_or_else(|_| defaults::DEV_MIN_SEARCH_LENGTH.to_string())
            .parse()
            .map_err(|_| {
                ConfigError::InvalidValue("TSIO_FEATURE_SEARCH_MIN_LENGTH must be a valid number")
            })?;

        let upload_max_size = env::var("TSIO_FEATURE_UPLOAD_MAX_SIZE")
            .unwrap_or_else(|_| defaults::DEV_MAX_UPLOAD_SIZE.to_string())
            .parse()
            .map_err(|_| {
                ConfigError::InvalidValue("TSIO_FEATURE_UPLOAD_MAX_SIZE must be a valid number")
            })?;

        let upload_timeout_ms = env::var("TSIO_FEATURE_UPLOAD_TIMEOUT_MS")
            .unwrap_or_else(|_| defaults::DEV_UPLOAD_TIMEOUT_MS.to_string())
            .parse()
            .map_err(|_| {
                ConfigError::InvalidValue("TSIO_FEATURE_UPLOAD_TIMEOUT_MS must be a valid number")
            })?;

        Ok(FeatureSettings {
            html_view_enabled,
            search_min_length,
            upload_max_size,
            upload_timeout_ms,
        })
    }

    fn load_github_oidc_settings() -> GitHubOidcSettings {
        let enabled = env::var("TSIO_GITHUB_OIDC_ENABLED")
            .map(|v| v.to_lowercase() == "true" || v == "1")
            .unwrap_or(false);

        let allowed_repos = env::var("TSIO_GITHUB_OIDC_ALLOWED_REPOS")
            .map(|v| {
                v.split(',')
                    .map(|s| s.trim().to_string())
                    .filter(|s| !s.is_empty())
                    .collect()
            })
            .unwrap_or_default();

        let issuer = env::var("TSIO_GITHUB_OIDC_ISSUER")
            .unwrap_or_else(|_| "https://token.actions.githubusercontent.com".to_string());

        let audience = env::var("TSIO_GITHUB_OIDC_AUDIENCE").ok();

        GitHubOidcSettings {
            enabled,
            allowed_repos,
            issuer,
            audience,
        }
    }

    fn load_github_oauth_settings(
        environment: &Environment,
    ) -> Result<GitHubOAuthSettings, ConfigError> {
        let enabled = env::var("TSIO_GITHUB_OAUTH_ENABLED")
            .map(|v| v.to_lowercase() == "true" || v == "1")
            .unwrap_or(false);

        let client_id = env::var("TSIO_GITHUB_OAUTH_CLIENT_ID").ok();
        let client_secret = env::var("TSIO_GITHUB_OAUTH_CLIENT_SECRET")
            .ok()
            .map(SecretString::from);

        let allowed_orgs = env::var("TSIO_GITHUB_OAUTH_ALLOWED_ORGS")
            .map(|v| {
                v.split(',')
                    .map(|s| s.trim().to_string())
                    .filter(|s| !s.is_empty())
                    .collect()
            })
            .unwrap_or_default();

        // Session secret: required if OAuth is enabled in production
        let session_secret_raw =
            env::var("TSIO_GITHUB_OAUTH_SESSION_SECRET").unwrap_or_else(|_| {
                if environment.is_development() {
                    "dev-session-secret-do-not-use-in-production".to_string()
                } else {
                    String::new()
                }
            });
        let session_secret = SecretString::from(session_secret_raw);

        let access_token_ttl_secs = env::var("TSIO_GITHUB_OAUTH_ACCESS_TOKEN_TTL_SECS")
            .unwrap_or_else(|_| "900".to_string()) // 15 minutes
            .parse()
            .map_err(|_| {
                ConfigError::InvalidValue(
                    "TSIO_GITHUB_OAUTH_ACCESS_TOKEN_TTL_SECS must be a valid number",
                )
            })?;

        let refresh_token_ttl_secs = env::var("TSIO_GITHUB_OAUTH_REFRESH_TOKEN_TTL_SECS")
            .unwrap_or_else(|_| "604800".to_string()) // 7 days
            .parse()
            .map_err(|_| {
                ConfigError::InvalidValue(
                    "TSIO_GITHUB_OAUTH_REFRESH_TOKEN_TTL_SECS must be a valid number",
                )
            })?;

        let redirect_url = env::var("TSIO_GITHUB_OAUTH_REDIRECT_URL").ok();

        Ok(GitHubOAuthSettings {
            enabled,
            client_id,
            client_secret,
            allowed_orgs,
            session_secret,
            access_token_ttl_secs,
            refresh_token_ttl_secs,
            redirect_url,
        })
    }

    /// Validate that production configuration does not use development defaults.
    fn validate_production(&self) -> Result<(), ConfigError> {
        let mut errors = Vec::new();

        if self.database.url == defaults::DEV_DATABASE_URL {
            errors.push(format!(
                "TSIO_DB_URL is using development default '{}'. Set a production PostgreSQL URL.",
                defaults::DEV_DATABASE_URL
            ));
        }

        // Check if using dev S3 credentials in production (None = IAM role, which is fine)
        if self.storage.access_key.as_deref() == Some(defaults::DEV_S3_ACCESS_KEY)
            || self.storage.secret_key.as_deref() == Some(defaults::DEV_S3_SECRET_KEY)
        {
            errors.push(
                "TSIO_S3_ACCESS_KEY/TSIO_S3_SECRET_KEY are using development defaults. Set production S3 credentials or remove them to use IAM role."
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

        // Check OAuth session secret in production
        if self.github_oauth.enabled {
            let secret = self.github_oauth.session_secret.expose_secret();
            if secret.is_empty() {
                errors.push(
                    "TSIO_GITHUB_OAUTH_SESSION_SECRET is required when GitHub OAuth is enabled in production."
                        .to_string(),
                );
            } else if secret == "dev-session-secret-do-not-use-in-production" {
                errors.push(
                    "TSIO_GITHUB_OAUTH_SESSION_SECRET is using development default. Set a secure session secret."
                        .to_string(),
                );
            } else if secret.len() < 32 {
                errors.push(
                    "TSIO_GITHUB_OAUTH_SESSION_SECRET must be at least 32 characters for secure HS256 signing."
                        .to_string(),
                );
            }
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
            access_key: Some("testkey".to_string()),
            secret_key: Some("testsecret".to_string()),
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

    fn test_feature_settings() -> FeatureSettings {
        FeatureSettings {
            html_view_enabled: true,
            search_min_length: 2,
            upload_max_size: 1024,
            upload_timeout_ms: 3600000,
        }
    }

    fn test_github_oidc_settings() -> GitHubOidcSettings {
        GitHubOidcSettings {
            enabled: false,
            allowed_repos: vec![],
            issuer: "https://token.actions.githubusercontent.com".to_string(),
            audience: None,
        }
    }

    fn test_github_oauth_settings() -> GitHubOAuthSettings {
        GitHubOAuthSettings {
            enabled: false,
            client_id: None,
            client_secret: None,
            allowed_orgs: vec![],
            session_secret: SecretString::from(
                "test-session-secret-at-least-32-chars!!".to_string(),
            ),
            access_token_ttl_secs: 900,
            refresh_token_ttl_secs: 604800,
            redirect_url: None,
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
                access_key: Some(defaults::DEV_S3_ACCESS_KEY.to_string()),
                secret_key: Some(defaults::DEV_S3_SECRET_KEY.to_string()),
            },
            server: test_server_settings(),
            auth: AuthSettings {
                admin_key: Some(defaults::DEV_ADMIN_KEY.to_string()),
            },
            features: test_feature_settings(),
            github_oidc: test_github_oidc_settings(),
            github_oauth: test_github_oauth_settings(),
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
                endpoint: None, // Use AWS S3 with IAM role
                bucket: "prod-reports".to_string(),
                region: "us-west-2".to_string(),
                access_key: None,
                secret_key: None,
            },
            server: ServerSettings {
                static_dir: Some(PathBuf::from("/app/static")),
                ..test_server_settings()
            },
            auth: AuthSettings { admin_key: None },
            features: test_feature_settings(),
            github_oidc: test_github_oidc_settings(),
            github_oauth: test_github_oauth_settings(),
        };

        let result = config.validate_production();
        assert!(result.is_ok());
    }
}
