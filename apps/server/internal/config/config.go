// Package config loads the server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	HTTPListenAddr string `env:"TSIO_HTTP_LISTEN_ADDR" envDefault:":8080"`

	// Database connection. Supply either TSIO_DATABASE_URL directly, or the
	// split TSIO_DB_HOST/PORT/USER/PASSWORD/NAME fields — in ECS, the password
	// is injected from Secrets Manager as a separate env var, so a composed
	// URL cannot be written by the task definition. Load() assembles the URL
	// from the split fields when TSIO_DATABASE_URL is unset.
	DatabaseURL   string `env:"TSIO_DATABASE_URL"`
	DBHost        string `env:"TSIO_DB_HOST"`
	DBPort        string `env:"TSIO_DB_PORT" envDefault:"5432"`
	DBUser        string `env:"TSIO_DB_USER"`
	DBPassword    string `env:"TSIO_DB_PASSWORD,unset"`
	DBName        string `env:"TSIO_DB_NAME"`
	DBSSLMode     string `env:"TSIO_DB_SSLMODE" envDefault:"require"`
	DBAutoMigrate bool   `env:"TSIO_DB_AUTO_MIGRATE" envDefault:"true"`

	// DBMaxConns caps the pgx pool size per process. With multiple app tasks
	// pointing at one Postgres, keep the aggregate (tasks × this value) under
	// the server's max_connections.
	DBMaxConns int `env:"TSIO_DB_MAX_CONNS" envDefault:"20"`
	// DBStatementTimeoutMs bounds any single SQL statement server-side so a
	// slow query cannot hold a pool connection until the load balancer times
	// the request out. 0 disables the timeout.
	DBStatementTimeoutMs int `env:"TSIO_DB_STATEMENT_TIMEOUT_MS" envDefault:"30000"`

	// S3 credentials are optional: when running in ECS/EC2, the AWS SDK picks
	// up credentials from the task role automatically. Explicit keys are used
	// for local dev against MinIO or for S3 accounts that don't match the
	// caller's role.
	S3Endpoint       string `env:"TSIO_S3_ENDPOINT"`
	S3Region         string `env:"TSIO_S3_REGION" envDefault:"us-east-1"`
	S3Bucket         string `env:"TSIO_S3_BUCKET,required"`
	S3AccessKey      string `env:"TSIO_S3_ACCESS_KEY,unset"`
	S3SecretKey      string `env:"TSIO_S3_SECRET_KEY,unset"`
	S3ForcePathStyle bool   `env:"TSIO_S3_FORCE_PATH_STYLE" envDefault:"false"`

	GitHubOAuthClientID     string `env:"TSIO_GITHUB_OAUTH_CLIENT_ID"`
	GitHubOAuthClientSecret string `env:"TSIO_GITHUB_OAUTH_CLIENT_SECRET,unset"`
	GitHubOAuthRedirectURL  string `env:"TSIO_GITHUB_OAUTH_REDIRECT_URL"`

	GitHubActionsOIDCIssuer string `env:"TSIO_GITHUB_ACTIONS_OIDC_ISSUER" envDefault:"https://token.actions.githubusercontent.com"`
	// Empty audience disables aud-claim validation. Production MUST set this
	// to the server's expected audience to prevent cross-service token replay.
	GitHubActionsOIDCAudience string `env:"TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE" envDefault:""`

	SessionSecret       string        `env:"TSIO_SESSION_SECRET,required,unset"`
	SessionTTL          time.Duration `env:"TSIO_SESSION_TTL" envDefault:"720h"`
	RefreshTokenTTL     time.Duration `env:"TSIO_REFRESH_TOKEN_TTL" envDefault:"720h"`
	APIKeyRotationGrace time.Duration `env:"TSIO_APIKEY_ROTATION_GRACE" envDefault:"24h"`

	// Admin key gates the privileged setup endpoints (e.g.
	// POST /api/v1/auth/oidc-policies). Default is a known-bad placeholder
	// that production MUST override.
	AdminKey string `env:"TSIO_ADMIN_KEY" envDefault:"dev-admin-key-do-not-use-in-production"`

	// Comma-separated `pattern=role` list applied as github_oidc_policies rows
	// at startup (ON CONFLICT DO NOTHING on name). Typically used by ephemeral
	// staging stacks that wipe the DB on each deploy; production seeds via the
	// HTTP admin endpoint instead. Example: "mattermost/*=uploader".
	BootstrapOIDCPolicies string `env:"TSIO_BOOTSTRAP_OIDC_POLICIES"`

	// Path the OpenAPI request validator loads at startup. Relative paths are
	// resolved against the process cwd. Production images set this to an
	// absolute path so the file is discoverable regardless of WORKDIR.
	OpenAPISpecPath string `env:"TSIO_OPENAPI_SPEC_PATH" envDefault:"api/openapi.yaml"`

	MaxUploadBytes   int64 `env:"TSIO_MAX_UPLOAD_BYTES" envDefault:"1073741824"`  // 1 GiB
	MaxArtifactBytes int64 `env:"TSIO_MAX_ARTIFACT_BYTES" envDefault:"104857600"` // 100 MiB

	LogLevel  string `env:"TSIO_LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"TSIO_LOG_FORMAT" envDefault:"json"` // json | text

	CORSAllowedOrigins []string `env:"TSIO_CORS_ALLOWED_ORIGINS" envSeparator:","`

	// ReadRequestTimeout bounds how long a public read request (report and
	// orchestration-status GETs) may run before its context is canceled and
	// the in-flight DB query aborted. Write, upload, and WebSocket routes are
	// intentionally exempt. Keep it comfortably below the load balancer idle
	// timeout so slow reads surface as a fast error instead of a 504.
	ReadRequestTimeout time.Duration `env:"TSIO_READ_REQUEST_TIMEOUT" envDefault:"30s"`

	// Client-facing tunables surfaced via GET /api/v1/config.
	UploadTimeoutMs int  `env:"TSIO_UPLOAD_TIMEOUT_MS" envDefault:"3600000"` // 1h
	HTMLViewEnabled bool `env:"TSIO_HTML_VIEW_ENABLED" envDefault:"false"`
	SearchMinLength int  `env:"TSIO_SEARCH_MIN_LENGTH" envDefault:"3"`

	// Build metadata surfaced via GET /api/v1/info. Typically set via ldflags
	// in apps/server/cmd/tsio/main.go; these env vars allow overrides for tests.
	Environment string `env:"TSIO_ENVIRONMENT" envDefault:"development"`
	RepoURL     string `env:"TSIO_REPO_URL" envDefault:"https://github.com/mattermost/mattermost-test-system-io"`
}

// Load reads .env (best-effort; searches upward from CWD), then parses the
// process environment into Config.
func Load() (Config, error) {
	loadDotenv()

	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse env: %w", err)
	}
	if cfg.DatabaseURL == "" {
		assembled, err := assembleDatabaseURL(cfg)
		if err != nil {
			return Config{}, err
		}
		cfg.DatabaseURL = assembled
	}
	// Clear split fields now that DatabaseURL is set; the password in
	// particular should not linger on the struct after load.
	cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode = "", "", "", "", "", ""
	if len(cfg.CORSAllowedOrigins) == 0 {
		// Dev fallback: Vite dev server at :3000. Production sets
		// TSIO_CORS_ALLOWED_ORIGINS explicitly.
		cfg.CORSAllowedOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"}
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// assembleDatabaseURL builds a postgres:// DSN from the split TSIO_DB_* fields.
// Returns an error if any required part is missing — callers that supply
// TSIO_DATABASE_URL directly never reach this path.
func assembleDatabaseURL(cfg Config) (string, error) {
	missing := []string{}
	if cfg.DBHost == "" {
		missing = append(missing, "TSIO_DB_HOST")
	}
	if cfg.DBUser == "" {
		missing = append(missing, "TSIO_DB_USER")
	}
	if cfg.DBPassword == "" {
		missing = append(missing, "TSIO_DB_PASSWORD")
	}
	if cfg.DBName == "" {
		missing = append(missing, "TSIO_DB_NAME")
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("TSIO_DATABASE_URL is not set and the following split fields are also missing: %v", missing)
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.DBUser, cfg.DBPassword),
		Host:   cfg.DBHost + ":" + cfg.DBPort,
		Path:   "/" + cfg.DBName,
	}
	q := u.Query()
	q.Set("sslmode", cfg.DBSSLMode)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// loadDotenv looks for a `.env` file in CWD and then in up to three parent
// directories (handy when running `go run ./cmd/tsio` from apps/server while
// the `.env` lives at the repo root). Silently ignored if not found.
func loadDotenv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for range 4 {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			_ = godotenv.Load(candidate)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func (c Config) validate() error {
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("invalid TSIO_LOG_FORMAT %q (want json|text)", c.LogFormat)
	}
	if c.MaxUploadBytes <= 0 {
		return errors.New("TSIO_MAX_UPLOAD_BYTES must be > 0")
	}
	if c.MaxArtifactBytes <= 0 {
		return errors.New("TSIO_MAX_ARTIFACT_BYTES must be > 0")
	}
	return nil
}
