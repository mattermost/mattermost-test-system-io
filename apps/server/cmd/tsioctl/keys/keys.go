// Package keys implements the `tsioctl keys` subcommand family.
package keys

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/apikey"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
)

// New returns the root `keys` command.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage API keys",
	}
	cmd.AddCommand(issueCmd(), listCmd(), rotateCmd(), revokeCmd())
	return cmd
}

func connect(ctx context.Context) (*apikey.Repo, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	return &apikey.Repo{Pool: pool}, func() { pool.Close() }, nil
}

func issueCmd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "issue",
		Short: "Issue a new API key (plaintext printed once)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			ctx := cmd.Context()
			repo, closer, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closer()

			iss, err := apikey.Issue()
			if err != nil {
				return err
			}
			row, err := repo.Insert(ctx, name, iss)
			if err != nil {
				return err
			}
			fmt.Printf("API key issued\n")
			fmt.Printf("  id:         %s\n", row.ID)
			fmt.Printf("  name:       %s\n", row.Name)
			fmt.Printf("  key_prefix: %s\n", row.KeyPrefix)
			fmt.Printf("  plaintext:  %s\n", iss.PlainText)
			fmt.Fprintln(os.Stderr, "\nStore the plaintext now — it is not shown again.")
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "Human label for the key")
	return c
}

func listCmd() *cobra.Command {
	var status string
	c := &cobra.Command{
		Use:   "list",
		Short: "List API keys",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			repo, closer, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closer()

			var filter *apikey.Status
			if status != "" {
				s := apikey.Status(status)
				filter = &s
			}
			rows, err := repo.List(ctx, filter)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "ID\tNAME\tPREFIX\tSTATUS\tCREATED\tLAST USED"); err != nil {
				return err
			}
			for _, r := range rows {
				last := "-"
				if r.LastUsedAt != nil {
					last = r.LastUsedAt.Format(time.RFC3339)
				}
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					r.ID, r.Name, r.KeyPrefix, r.Status,
					r.CreatedAt.Format(time.RFC3339), last); err != nil {
					return err
				}
			}
			return w.Flush()
		},
	}
	c.Flags().StringVar(&status, "status", "", "Filter by status: active|rotating|revoked")
	return c
}

func rotateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate <id>",
		Short: "Mark an API key rotating and issue a replacement (plaintext printed once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := uuid.Parse(args[0])
			if err != nil {
				return fmt.Errorf("invalid uuid: %w", err)
			}
			ctx := cmd.Context()
			repo, closer, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closer()

			iss, err := apikey.Issue()
			if err != nil {
				return err
			}
			row, err := repo.RotateWithReplacement(ctx, id, "(rotated)", iss)
			if err != nil {
				return err
			}
			fmt.Printf("Rotated. Old key marked 'rotating'.\n")
			fmt.Printf("  new id:       %s\n", row.ID)
			fmt.Printf("  new prefix:   %s\n", row.KeyPrefix)
			fmt.Printf("  new plaintext: %s\n", iss.PlainText)
			return nil
		},
	}
}

func revokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := uuid.Parse(args[0])
			if err != nil {
				return fmt.Errorf("invalid uuid: %w", err)
			}
			ctx := cmd.Context()
			repo, closer, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closer()

			if err := repo.Revoke(ctx, id); err != nil {
				return err
			}
			fmt.Println("revoked.")
			return nil
		},
	}
}
