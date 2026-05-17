// Package db implements the `tsioctl db` subcommand family.
package db

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/apikey"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
	tsiodb "github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
)

// isLocalDatabaseTarget reports whether the DSN points at a loopback /
// link-local / RFC1918 host. A DSN we can't parse is treated as not-local so
// the safety check fails closed.
func isLocalDatabaseTarget(dsn string) bool {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") {
		return host != ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// guardDestructive enforces TWO conditions before a destructive db op runs:
// the environment must be "development" AND the database target must be a
// local/private host. --force bypasses both. The two-layer check prevents a
// stray TSIO_ENVIRONMENT=development in a debug shell from being enough to
// blow away a remote DB.
func guardDestructive(cfg config.Config, op string, force bool) error {
	if force {
		return nil
	}
	if cfg.Environment != "development" {
		return fmt.Errorf("refusing to %s: TSIO_ENVIRONMENT=%q (pass --force to override; never run this against production)", op, cfg.Environment)
	}
	if !isLocalDatabaseTarget(cfg.DatabaseURL) {
		return fmt.Errorf("refusing to %s: database target is not local/private (pass --force if you really mean it)", op)
	}
	return nil
}

// New returns the root `db` command.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database utilities",
	}
	cmd.AddCommand(migrateCmd(), statusCmd(), resetCmd(), seedCmd())
	return cmd
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending migrations (idempotent forward-only)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := tsiodb.Migrate(cfg.DatabaseURL); err != nil {
				return err
			}
			fmt.Println("migrations applied.")
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show applied migration version",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			v, dirty, err := tsiodb.Status(cfg.DatabaseURL)
			if err != nil {
				return err
			}
			if v == 0 {
				fmt.Println("no migrations applied yet")
				return nil
			}
			fmt.Printf("version=%d dirty=%v\n", v, dirty)
			if dirty {
				return errors.New("schema is dirty — a previous migration failed partway; manual fix required")
			}
			return nil
		},
	}
}

func resetCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "reset",
		Short: "Destructive: drop schema and re-apply every migration (dev only)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// Refuse outside development unless --force is set. Staging/prod
			// DBs should only ever move forward via `migrate`. The
			// guardDestructive check also requires the DSN to point at a
			// local/private host so a stray TSIO_ENVIRONMENT=development in
			// a debug shell isn't enough to wipe a remote DB.
			if err := guardDestructive(cfg, "reset", force); err != nil {
				return err
			}
			if err := tsiodb.Reset(cfg.DatabaseURL); err != nil {
				return err
			}
			fmt.Println("database reset complete.")
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Override destructive safety checks (environment + local/private DB target); never use in production")
	return c
}

func seedCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "seed",
		Short: "Insert dev fixtures (a development API key)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// seed mints a plaintext API key and prints it to stdout — fine
			// for local dev, dangerous in CI or shared environments. Mirror
			// the reset command's TSIO_ENVIRONMENT + local-DSN gate via
			// guardDestructive.
			if err := guardDestructive(cfg, "seed", force); err != nil {
				return err
			}
			ctx := cmd.Context()
			pool, err := tsiodb.NewPool(ctx, cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()

			repo := &apikey.Repo{Pool: pool}
			iss, err := apikey.Issue()
			if err != nil {
				return err
			}
			row, err := repo.Insert(ctx, "dev", iss)
			if err != nil {
				return err
			}
			fmt.Printf("seeded: api_key id=%s\n", row.ID)
			fmt.Printf("TSIO_API_KEY=%s\n", iss.PlainText)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Override destructive safety checks (environment + local/private DB target); never use in production")
	return c
}
