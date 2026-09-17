package db

import (
	"fmt"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
	tsiodb "github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/identity"
	"github.com/spf13/cobra"
)

func backfillIdentitiesCmd() *cobra.Command {
	var repository, since string
	cmd := &cobra.Command{Use: "backfill-identities", Short: "Idempotently backfill identities and observations", RunE: func(cmd *cobra.Command, _ []string) error {
		start := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
		if since != "" {
			var err error
			start, err = time.Parse(time.RFC3339, since)
			if err != nil {
				return fmt.Errorf("--since must be RFC3339: %w", err)
			}
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		pool, err := tsiodb.NewPool(cmd.Context(), cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		count, err := identity.Backfill(cmd.Context(), pool, repository, start)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "backfilled %d report groups\n", count)
		return err
	}}
	cmd.Flags().StringVar(&repository, "repository", "", "Filter by repository slug")
	cmd.Flags().StringVar(&since, "since", "", "Only groups created since this RFC3339 timestamp")
	return cmd
}
