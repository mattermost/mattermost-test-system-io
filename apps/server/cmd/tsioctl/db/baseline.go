package db

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
)

//go:embed baseline.sql
var baselineSQL string

func baselineCmd() *cobra.Command {
	var repo, branch string
	var days, minRuns int
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "baseline",
		Short: "Read stored-test coverage, suite outcomes and a non-causal policy replay as JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !strings.Contains(repo, "/") || days < 1 || days > 3650 || minRuns < 1 || timeout <= 0 {
				return errors.New("require full owner/repo, days 1..3650, positive min-runs and timeout")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			result, err := readBaseline(ctx, pool, repo, branch, days, minRuns)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "mattermost/mattermost", "exact repository owner/name")
	cmd.Flags().StringVar(&branch, "branch", "master", "trusted branch (PR groups are excluded)")
	cmd.Flags().IntVar(&days, "days", 90, "observation and per-PR lookback window in days")
	cmd.Flags().IntVar(&minRuns, "min-runs", 5, "minimum prior master observations for policy replay")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "maximum query duration")
	return cmd
}

func readBaseline(ctx context.Context, pool *pgxpool.Pool, repo, branch string, days, minRuns int) (json.RawMessage, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('tsio.baseline_repo',$1,true),
	    set_config('tsio.baseline_branch',$2,true), set_config('tsio.baseline_days',$3,true),
	    set_config('tsio.baseline_min_runs',$4,true)`, repo, branch, strconv.Itoa(days), strconv.Itoa(minRuns)); err != nil {
		return nil, err
	}
	var result json.RawMessage
	if err := tx.QueryRow(ctx, baselineSQL).Scan(&result); err != nil {
		return nil, fmt.Errorf("baseline query: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
