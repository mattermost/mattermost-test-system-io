// Package db implements the `tsioctl db` subcommand family.
package db

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/apikey"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
	tsiodb "github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
)

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
			// DBs should only ever move forward via `migrate`.
			if cfg.Environment != "development" && !force {
				return fmt.Errorf("refusing to reset: TSIO_ENVIRONMENT=%q (pass --force to override; never run this against production)", cfg.Environment)
			}
			if err := tsiodb.Reset(cfg.DatabaseURL); err != nil {
				return err
			}
			fmt.Println("database reset complete.")
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Override the TSIO_ENVIRONMENT safety check (never use in production)")
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
			// the reset command's TSIO_ENVIRONMENT gate.
			if cfg.Environment != "development" && !force {
				return fmt.Errorf("refusing to seed: TSIO_ENVIRONMENT=%q (pass --force to override; the printed API key grants uploader access)", cfg.Environment)
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
	c.Flags().BoolVar(&force, "force", false, "Override the TSIO_ENVIRONMENT safety check (never use in production)")
	return c
}
