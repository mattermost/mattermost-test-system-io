// Package config loads the server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	HTTPListenAddr string `env:"TSIO_HTTP_LISTEN_ADDR" envDefault:":8080"`

	DatabaseURL   string `env:"TSIO_DATABASE_URL,required"`
	DBAutoMigrate bool   `env:"TSIO_DB_AUTO_MIGRATE" envDefault:"true"`

	S3Endpoint       string `env:"TSIO_S3_ENDPOINT"`
	S3Region         string `env:"TSIO_S3_REGION" envDefault:"us-east-1"`
	S3Bucket         string `env:"TSIO_S3_BUCKET,required"`
	S3AccessKey      string `env:"TSIO_S3_ACCESS_KEY,required,unset"`
	S3SecretKey      string `env:"TSIO_S3_SECRET_KEY,required,unset"`
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

	MaxUploadBytes   int64 `env:"TSIO_MAX_UPLOAD_BYTES" envDefault:"1073741824"`  // 1 GiB
	MaxArtifactBytes int64 `env:"TSIO_MAX_ARTIFACT_BYTES" envDefault:"104857600"` // 100 MiB

	LogLevel  string `env:"TSIO_LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"TSIO_LOG_FORMAT" envDefault:"json"` // json | text

	CORSAllowedOrigins []string `env:"TSIO_CORS_ALLOWED_ORIGINS" envSeparator:","`

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
