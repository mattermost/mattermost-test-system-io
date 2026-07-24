package db

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolOption tweaks the pgxpool.Config before the pool is opened. Options are
// variadic so existing callers (CLI tools, tests) keep the zero-config
// defaults while the server can thread through operator-tunable knobs.
type PoolOption func(*pgxpool.Config)

// WithMaxConns overrides the pool's maximum connection count. Non-positive
// values are ignored so a missing/invalid env knob falls back to the default.
func WithMaxConns(n int) PoolOption {
	return func(cfg *pgxpool.Config) {
		if n > 0 {
			cfg.MaxConns = int32(n)
		}
	}
}

// WithStatementTimeout sets a per-statement timeout (in milliseconds) on every
// connection in the pool. This bounds any single query so a slow read cannot
// hold a connection indefinitely and starve the pool — the query is aborted
// server-side and the connection returns to the pool. Non-positive values are
// ignored (no timeout). Applies per-statement, so it does not affect long
// multi-statement work like the background JSON extractor.
func WithStatementTimeout(ms int) PoolOption {
	return func(cfg *pgxpool.Config) {
		if ms > 0 {
			cfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(ms)
		}
	}
}

// NewPool constructs a pgx connection pool with sane defaults, applying any
// supplied options after the defaults so callers can override them.
func NewPool(ctx context.Context, databaseURL string, opts ...PoolOption) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnIdleTime = 10 * time.Minute
	cfg.MaxConnLifetime = 1 * time.Hour
	cfg.ConnConfig.RuntimeParams["application_name"] = "tsio"
	for _, opt := range opts {
		opt(cfg)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgx pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}
