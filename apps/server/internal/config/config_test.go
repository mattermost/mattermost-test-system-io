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
	if cfg.S3Region != "us-east-1" {
		t.Errorf("S3Region default = %q", cfg.S3Region)
	}
	if !cfg.DBAutoMigrate {
		t.Error("DBAutoMigrate should default to true")
	}
}

func TestLoad_assemblesDatabaseURLFromParts(t *testing.T) {
	t.Setenv("TSIO_DB_HOST", "db.internal")
	t.Setenv("TSIO_DB_PORT", "5433")
	t.Setenv("TSIO_DB_USER", "tsio")
	t.Setenv("TSIO_DB_PASSWORD", "p@ss/w:ord")
	t.Setenv("TSIO_DB_NAME", "tsio")
	t.Setenv("TSIO_DB_SSLMODE", "disable")
	t.Setenv("TSIO_S3_BUCKET", "reports")
	t.Setenv("TSIO_SESSION_SECRET", "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "postgres://tsio:p%40ss%2Fw%3Aord@db.internal:5433/tsio?sslmode=disable"
	if cfg.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
	if cfg.DBPassword != "" {
		t.Error("split DBPassword should be cleared after assembly")
	}
}

func TestLoad_directURLWinsOverParts(t *testing.T) {
	t.Setenv("TSIO_DATABASE_URL", "postgres://direct@host/db")
	t.Setenv("TSIO_DB_HOST", "should-be-ignored")
	t.Setenv("TSIO_DB_USER", "ignored")
	t.Setenv("TSIO_DB_PASSWORD", "ignored")
	t.Setenv("TSIO_DB_NAME", "ignored")
	t.Setenv("TSIO_S3_BUCKET", "reports")
	t.Setenv("TSIO_SESSION_SECRET", "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://direct@host/db" {
		t.Errorf("DatabaseURL = %q (should match TSIO_DATABASE_URL verbatim)", cfg.DatabaseURL)
	}
}

func TestLoad_missingDatabaseConfigErrors(t *testing.T) {
	t.Setenv("TSIO_S3_BUCKET", "reports")
	t.Setenv("TSIO_SESSION_SECRET", "test-secret")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when neither TSIO_DATABASE_URL nor split fields are set")
	}
}

func TestLoad_partialDatabasePartsErrors(t *testing.T) {
	t.Setenv("TSIO_DB_HOST", "db.internal")
	t.Setenv("TSIO_DB_USER", "tsio")
	// Missing TSIO_DB_PASSWORD and TSIO_DB_NAME.
	t.Setenv("TSIO_S3_BUCKET", "reports")
	t.Setenv("TSIO_SESSION_SECRET", "test-secret")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when some but not all split DB fields are set")
	}
}

func TestLoad_s3KeysOptional(t *testing.T) {
	// S3 keys are optional so the AWS SDK can fall back to ECS task-role
	// credentials. Load() must succeed without them.
	t.Setenv("TSIO_DATABASE_URL", "postgres://user:pass@localhost/db?sslmode=disable")
	t.Setenv("TSIO_S3_BUCKET", "reports")
	t.Setenv("TSIO_SESSION_SECRET", "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.S3AccessKey != "" || cfg.S3SecretKey != "" {
		t.Errorf("expected empty S3 keys, got access=%q secret=%q", cfg.S3AccessKey, cfg.S3SecretKey)
	}
}
