package db

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const testDSN = "postgres://tsio:tsio@localhost:5432/tsio?sslmode=disable"

func TestWithMaxConns(t *testing.T) {
	cfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	WithMaxConns(50)(cfg)
	if cfg.MaxConns != 50 {
		t.Errorf("MaxConns = %d, want 50", cfg.MaxConns)
	}
}

func TestWithMaxConns_ignoresNonPositive(t *testing.T) {
	cfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.MaxConns = 20
	WithMaxConns(0)(cfg)
	WithMaxConns(-5)(cfg)
	if cfg.MaxConns != 20 {
		t.Errorf("MaxConns = %d, want unchanged 20", cfg.MaxConns)
	}
}

func TestWithStatementTimeout(t *testing.T) {
	cfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	WithStatementTimeout(15000)(cfg)
	if got := cfg.ConnConfig.RuntimeParams["statement_timeout"]; got != "15000" {
		t.Errorf("statement_timeout = %q, want \"15000\"", got)
	}
}

func TestWithStatementTimeout_ignoresNonPositive(t *testing.T) {
	cfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	WithStatementTimeout(0)(cfg)
	if _, ok := cfg.ConnConfig.RuntimeParams["statement_timeout"]; ok {
		t.Error("statement_timeout should not be set for non-positive value")
	}
}
