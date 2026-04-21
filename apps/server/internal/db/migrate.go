// Package db holds the Postgres connection pool, transaction helper, and
// migration runner. Migrations are embedded via apps/server/migrations and
// driven through golang-migrate with pgx/v5 + iofs source.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	// Postgres driver (pgx v5) registered via blank import.
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/migrations"
)

// migrateDatabaseURL converts a plain Postgres URL (postgres://...) to the
// pgx/v5 driver URL that golang-migrate expects (pgx5://...).
func migrateDatabaseURL(postgresURL string) string {
	const oldPrefix = "postgres://"
	if len(postgresURL) > len(oldPrefix) && postgresURL[:len(oldPrefix)] == oldPrefix {
		return "pgx5://" + postgresURL[len(oldPrefix):]
	}
	const oldPrefix2 = "postgresql://"
	if len(postgresURL) > len(oldPrefix2) && postgresURL[:len(oldPrefix2)] == oldPrefix2 {
		return "pgx5://" + postgresURL[len(oldPrefix2):]
	}
	return postgresURL
}

// newMigrate opens a migrate instance backed by the embedded migrations FS.
func newMigrate(databaseURL string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("migrate: open source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateDatabaseURL(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("migrate: open: %w", err)
	}
	return m, nil
}

// Migrate applies every unapplied migration forward. Idempotent — returns nil
// when the database is already at the latest version.
func Migrate(databaseURL string) error {
	m, err := newMigrate(databaseURL)
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}

// Status returns the currently-applied version and whether the migration state
// is "dirty" (a previous run failed halfway). version is 0 and err is nil when
// no migration has ever been applied.
func Status(databaseURL string) (version uint, dirty bool, err error) {
	m, openErr := newMigrate(databaseURL)
	if openErr != nil {
		return 0, false, openErr
	}
	defer func() { _, _ = m.Close() }()

	v, d, sErr := m.Version()
	if sErr != nil {
		if errors.Is(sErr, migrate.ErrNilVersion) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("migrate: status: %w", sErr)
	}
	return v, d, nil
}

// Reset is a destructive dev helper: unconditionally drops and recreates the
// `public` schema (wiping both the data and golang-migrate's tracking table),
// then runs every migration forward. Works from any prior state — orphaned
// tables left by an older migration tool, a half-applied run, or an empty DB.
// Do NOT call this from a serving process. Use it from tsioctl.
func Reset(databaseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: connect for reset: %w", err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`); err != nil {
		pool.Close()
		return fmt.Errorf("migrate: drop schema: %w", err)
	}
	pool.Close()

	return Migrate(databaseURL)
}
