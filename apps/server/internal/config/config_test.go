package config

import (
	"testing"
	"time"
)

func TestValidate_defaults(t *testing.T) {
	c := Config{
		LogFormat:        "json",
		MaxUploadBytes:   1024,
		MaxArtifactBytes: 512,
	}
	if err := c.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidate_rejectsBadLogFormat(t *testing.T) {
	c := Config{LogFormat: "xml", MaxUploadBytes: 1, MaxArtifactBytes: 1}
	if err := c.validate(); err == nil {
		t.Fatal("expected error for bad log format")
	}
}

func TestValidate_rejectsZeroMaxUpload(t *testing.T) {
	c := Config{LogFormat: "json", MaxUploadBytes: 0, MaxArtifactBytes: 1}
	if err := c.validate(); err == nil {
		t.Fatal("expected error for zero MaxUploadBytes")
	}
}

func TestValidate_rejectsNegativeMaxArtifact(t *testing.T) {
	c := Config{LogFormat: "json", MaxUploadBytes: 1, MaxArtifactBytes: -1}
	if err := c.validate(); err == nil {
		t.Fatal("expected error for negative MaxArtifactBytes")
	}
}

func TestLoad_readsEnv(t *testing.T) {
	// Set every required env var; Load() should populate without error.
	t.Setenv("TSIO_DATABASE_URL", "postgres://user:pass@localhost/db?sslmode=disable")
	t.Setenv("TSIO_S3_BUCKET", "reports")
	t.Setenv("TSIO_S3_ACCESS_KEY", "minioadmin")
	t.Setenv("TSIO_S3_SECRET_KEY", "minioadmin")
	t.Setenv("TSIO_SESSION_SECRET", "test-secret")
	t.Setenv("TSIO_LOG_LEVEL", "debug")
	t.Setenv("TSIO_LOG_FORMAT", "text")
	t.Setenv("TSIO_SESSION_TTL", "48h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL == "" {
		t.Error("DatabaseURL empty")
	}
	if cfg.S3Bucket != "reports" {
		t.Errorf("S3Bucket = %q", cfg.S3Bucket)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q", cfg.LogFormat)
	}
	if cfg.SessionTTL != 48*time.Hour {
		t.Errorf("SessionTTL = %v", cfg.SessionTTL)
	}
	// Verify defaults fill in for unset optionals.
	if cfg.S3Region != "us-east-1" {
		t.Errorf("S3Region default = %q", cfg.S3Region)
	}
	if !cfg.DBAutoMigrate {
		t.Error("DBAutoMigrate should default to true")
	}
}
