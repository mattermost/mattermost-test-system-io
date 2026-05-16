package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

// newOrchestrationCmd returns the root `orchestration` command and its
// subcommands.
func newOrchestrationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orchestration",
		Short: "Test shard orchestration admin commands",
	}
	cmd.AddCommand(orchestrationPruneCmd(), orchestrationReconcileCmd())
	return cmd
}

// connectDB opens a pgx pool against the configured TSIO_DATABASE_URL.
// Mirrors the pattern used by the keys subcommand family.
func connectDB(ctx context.Context) (*pgxpool.Pool, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return db.NewPool(ctx, cfg.DatabaseURL)
}

// connectStorage builds an ObjectStore from configuration. Returns nil and a
// nil error when storage is intentionally disabled (no S3 wiring) — callers
// can then skip object-store cleanup with a warning.
func connectStorage(ctx context.Context) (storage.ObjectStore, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return storage.New(ctx, storage.Config{
		Endpoint:       cfg.S3Endpoint,
		Region:         cfg.S3Region,
		Bucket:         cfg.S3Bucket,
		AccessKey:      cfg.S3AccessKey,
		SecretKey:      cfg.S3SecretKey,
		ForcePathStyle: cfg.S3ForcePathStyle,
	})
}

// parseRetention accepts standard Go durations (e.g. `720h`, `30m`) plus the
// shorthand `Nd` (days) and `Nw` (weeks). The shorthand is permitted because
// admin operators reach for `30d` more naturally than `720h` when reasoning
// about retention policies.
func parseRetention(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("duration is empty")
	}
	if n := len(s); n >= 2 {
		suffix := s[n-1]
		if suffix == 'd' || suffix == 'w' {
			num, err := strconv.ParseInt(s[:n-1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse %q: %w", s, err)
			}
			if num < 0 {
				return 0, fmt.Errorf("duration must be non-negative: %q", s)
			}
			unit := 24 * time.Hour
			if suffix == 'w' {
				unit = 7 * 24 * time.Hour
			}
			return time.Duration(num) * unit, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w (accepted: Go durations like 720h, plus Nd/Nw shorthand)", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration must be non-negative: %q", s)
	}
	return d, nil
}

func orchestrationPruneCmd() *cobra.Command {
	var olderThan string
	c := &cobra.Command{
		Use:   "prune",
		Short: "Prune terminal orchestration runs and their object-store artifacts",
		Long: `Prune deletes orchestration_runs whose terminal_at is older than the
given retention window, along with their object-store subtree at
orchestration/<run_uuid>/. Cascades through dispatch_units / leases /
attempts via FK ON DELETE CASCADE.

Duration accepts Go duration syntax (e.g. 720h, 90m) plus the Nd / Nw
shorthand for days and weeks (e.g. 30d, 4w).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			retention, err := parseRetention(olderThan)
			if err != nil {
				return fmt.Errorf("--older-than: %w", err)
			}
			ctx := cmd.Context()
			pool, err := connectDB(ctx)
			if err != nil {
				return err
			}
			defer pool.Close()

			// Object-store cleanup is best-effort: if it fails (or storage is
			// not configured), we still proceed with the DB delete and surface
			// a warning so the operator can re-run a manual cleanup.
			store, storeErr := connectStorage(ctx)
			if storeErr != nil {
				fmt.Fprintf(os.Stderr, "warning: object store unavailable, skipping artifact cleanup: %v\n", storeErr)
			}

			cutoff := time.Now().Add(-retention)
			rows, err := pool.Query(ctx, `
				SELECT id
				  FROM orchestration_runs
				 WHERE terminal_at IS NOT NULL
				   AND terminal_at < $1
			`, cutoff)
			if err != nil {
				return fmt.Errorf("select runs: %w", err)
			}
			runIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return fmt.Errorf("scan runs: %w", err)
			}

			if len(runIDs) == 0 {
				fmt.Printf("Pruned 0 runs older than %s (cutoff %s)\n", olderThan, cutoff.UTC().Format(time.RFC3339))
				return nil
			}

			// Delete object-store subtrees first so that an interrupted run
			// leaves at most orphaned keys, never orphaned DB rows.
			if store != nil {
				for _, id := range runIDs {
					prefix := "orchestration/" + id + "/"
					keys, err := store.List(ctx, prefix)
					if err != nil {
						fmt.Fprintf(os.Stderr, "warning: list %s: %v (continuing)\n", prefix, err)
						continue
					}
					for _, key := range keys {
						if err := store.Delete(ctx, key); err != nil {
							fmt.Fprintf(os.Stderr, "warning: delete %s: %v (continuing)\n", key, err)
						}
					}
				}
			}

			// Delete exactly the set we cleaned artifacts for. Re-running the
			// cutoff predicate here would race against rows that crossed the
			// boundary between the SELECT above and this DELETE, deleting
			// their DB rows without first cleaning their object-store keys.
			// FK ON DELETE CASCADE on dispatch_units / leases / attempts
			// handles the dependent rows transparently.
			tag, err := pool.Exec(ctx, `
				DELETE FROM orchestration_runs
				 WHERE id = ANY($1)
			`, runIDs)
			if err != nil {
				return fmt.Errorf("delete runs: %w", err)
			}
			fmt.Printf("Pruned %d runs older than %s (cutoff %s)\n", tag.RowsAffected(), olderThan, cutoff.UTC().Format(time.RFC3339))
			return nil
		},
	}
	c.Flags().StringVar(&olderThan, "older-than", "30d", "Prune runs whose terminal_at is older than this duration (e.g. 7d, 30d, 720h)")
	return c
}

// counters captures the materialized totals on an orchestration_runs row.
// Used to print before/after lines for the reconcile command.
type counters struct {
	Pending          int
	Leased           int
	CompletedPass    int
	CompletedFail    int
	CompletedSkipped int
	Abandoned        int
	RetestEligible   int
	Total            int
}

func orchestrationReconcileCmd() *cobra.Command {
	var ghRunAttempt string
	c := &cobra.Command{
		Use:   "reconcile <repository> <commit_sha> <gh_run_id> <name>",
		Short: "Recompute materialized counters on an orchestration_runs row",
		Long: `Reconcile recomputes pending/leased/completed_*/abandoned counters and
retest_eligible_count by aggregating dispatch_units for the run identified
by the composite identity tuple. Forensic only — runtime keeps these
counters in sync transactionally; reach for this when a CHECK constraint
violation or a manual edit has left a row inconsistent.`,
		Args: cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			repository, commitSHA, ghRunID, name := args[0], args[1], args[2], args[3]
			ctx := cmd.Context()
			pool, err := connectDB(ctx)
			if err != nil {
				return err
			}
			defer pool.Close()

			var (
				runID  string
				before counters
			)
			err = pool.QueryRow(ctx, `
				SELECT id,
				       pending_count,
				       leased_count,
				       completed_pass_count,
				       completed_fail_count,
				       completed_skipped_count,
				       abandoned_count,
				       retest_eligible_count,
				       total_units
				  FROM orchestration_runs
				 WHERE repository = $1
				   AND commit_sha = $2
				   AND gh_run_id = $3
				   AND name = $4
				   AND gh_run_attempt = $5
			`, repository, commitSHA, ghRunID, name, ghRunAttempt).Scan(
				&runID,
				&before.Pending,
				&before.Leased,
				&before.CompletedPass,
				&before.CompletedFail,
				&before.CompletedSkipped,
				&before.Abandoned,
				&before.RetestEligible,
				&before.Total,
			)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("orchestration run not found for identity (%s, %s, %s, %s, attempt=%s)",
						repository, commitSHA, ghRunID, name, ghRunAttempt)
				}
				return fmt.Errorf("lookup run: %w", err)
			}

			var after counters
			err = pool.QueryRow(ctx, `
				SELECT
				    COUNT(*) FILTER (WHERE state = 'pending'),
				    COUNT(*) FILTER (WHERE state = 'leased'),
				    COUNT(*) FILTER (WHERE state = 'completed_pass'),
				    COUNT(*) FILTER (WHERE state = 'completed_fail'),
				    COUNT(*) FILTER (WHERE state = 'completed_skipped'),
				    COUNT(*) FILTER (WHERE state = 'abandoned'),
				    COUNT(*) FILTER (
				        WHERE state = 'completed_fail'
				          AND fail_count <= (
				              SELECT retest_budget FROM orchestration_runs WHERE id = $1
				          )
				    ),
				    COUNT(*)
				  FROM dispatch_units
				 WHERE run_id = $1
			`, runID).Scan(
				&after.Pending,
				&after.Leased,
				&after.CompletedPass,
				&after.CompletedFail,
				&after.CompletedSkipped,
				&after.Abandoned,
				&after.RetestEligible,
				&after.Total,
			)
			if err != nil {
				return fmt.Errorf("aggregate dispatch_units: %w", err)
			}

			_, err = pool.Exec(ctx, `
				UPDATE orchestration_runs
				   SET pending_count           = $2,
				       leased_count            = $3,
				       completed_pass_count    = $4,
				       completed_fail_count    = $5,
				       completed_skipped_count = $6,
				       abandoned_count         = $7,
				       retest_eligible_count   = $8,
				       updated_at              = now()
				 WHERE id = $1
			`,
				runID,
				after.Pending,
				after.Leased,
				after.CompletedPass,
				after.CompletedFail,
				after.CompletedSkipped,
				after.Abandoned,
				after.RetestEligible,
			)
			if err != nil {
				return fmt.Errorf("update counters: %w", err)
			}

			fmt.Printf("Reconciled run %s\n", runID)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "COUNTER\tBEFORE\tAFTER\tDELTA"); err != nil {
				return err
			}
			rowsToPrint := []struct {
				name          string
				before, after int
			}{
				{"pending_count", before.Pending, after.Pending},
				{"leased_count", before.Leased, after.Leased},
				{"completed_pass_count", before.CompletedPass, after.CompletedPass},
				{"completed_fail_count", before.CompletedFail, after.CompletedFail},
				{"completed_skipped_count", before.CompletedSkipped, after.CompletedSkipped},
				{"abandoned_count", before.Abandoned, after.Abandoned},
				{"retest_eligible_count", before.RetestEligible, after.RetestEligible},
				{"total_units (read-only)", before.Total, after.Total},
			}
			for _, r := range rowsToPrint {
				if _, err := fmt.Fprintf(w, "%s\t%d\t%d\t%+d\n", r.name, r.before, r.after, r.after-r.before); err != nil {
					return err
				}
			}
			return w.Flush()
		},
	}
	c.Flags().StringVar(&ghRunAttempt, "gh-run-attempt", "1", "GitHub Actions run attempt (composite-identity tiebreaker)")
	return c
}
