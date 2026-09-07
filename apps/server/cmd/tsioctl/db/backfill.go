package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
)

const backfillBatchDefault = 5000

// Also repair rows from installations which applied the earlier migration 28
// before external IDs were populated. The trigger recomputes stable_key. Do not
// infer project: old Playwright rows lost that dimension at ingestion, and
// merging them into a browser-specific history would manufacture evidence.
// The primary-key cursor prevents each committed batch rescanning all the rows
// already visited. Restarting begins at the first still-unfilled identity.
const backfillStableKeySQL = `
	WITH batch AS (
		SELECT tc.ctid, tc.id, s.file,
		       coalesce(substring(tc.full_title from '\yMM-T[0-9]+(?:_[0-9]+)?'),
		                substring(tc.title from '\yMM-T[0-9]+(?:_[0-9]+)?')) AS external_id
		FROM test_cases tc
		LEFT JOIN suites s ON s.id = tc.suite_id
		WHERE tc.id > $2::uuid AND (tc.stable_key IS NULL
		   OR (tc.external_test_id IS NULL AND
		       (tc.full_title ~ '\yMM-T[0-9]+' OR tc.title ~ '\yMM-T[0-9]+')))
		ORDER BY tc.id
		LIMIT $1
		FOR UPDATE OF tc
	), updated AS (
	UPDATE test_cases tc
	SET file = coalesce(tc.file, batch.file),
	    external_test_id = coalesce(tc.external_test_id, batch.external_id)
	FROM batch WHERE tc.ctid = batch.ctid
	RETURNING tc.id
	)
	SELECT count(*), (SELECT id::text FROM updated ORDER BY id DESC LIMIT 1)
	FROM updated
`

var historyIndexes = []struct{ name, definition string }{
	{"test_cases_external_id_idx", "ON test_cases (external_test_id) WHERE external_test_id IS NOT NULL"},
	{"report_groups_repo_branch_created_idx", "ON report_groups (repository, branch, created_at DESC)"},
	{"test_cases_stable_key_idx", "ON test_cases (stable_key)"},
}

func backfillStableKeyCmd() *cobra.Command {
	var batchSize int
	var pause time.Duration
	cmd := &cobra.Command{
		Use:   "backfill-stable-key",
		Short: "Backfill historical test identities in batches and build history indexes concurrently",
		Long: "Populate external IDs and file in committed batches, letting the migration-28 " +
			"trigger compute stable_key. Resume interrupted work and repair invalid concurrent " +
			"indexes. Historical Playwright project identity cannot be inferred; its old " +
			"unprefixed keys remain separate from new browser-specific history.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if batchSize <= 0 || pause < 0 {
				return errors.New("--batch-size must be positive and --pause non-negative")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			pool, err := pgxpool.New(cmd.Context(), cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()
			return runStableKeyBackfill(cmd.Context(), pool, batchSize, pause)
		},
	}
	cmd.Flags().IntVar(&batchSize, "batch-size", backfillBatchDefault, "rows per committed batch")
	cmd.Flags().DurationVar(&pause, "pause", 100*time.Millisecond, "delay between batches")
	return cmd
}

func runStableKeyBackfill(ctx context.Context, pool *pgxpool.Pool, batchSize int, pause time.Duration) error {
	if batchSize <= 0 || pause < 0 {
		return errors.New("invalid batch size or pause")
	}
	// Keep advisory lock and timeouts on one session, including concurrent index
	// builds (which PostgreSQL forbids inside a transaction).
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(114, 28)`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return errors.New("another stable-key backfill is running")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Close the physical connection so session locks/settings cannot leak
		// back to an application connection pool, even after cancellation.
		_ = conn.Conn().Close(cleanup)
	}()
	if _, err := conn.Exec(ctx, `SET lock_timeout = '10s'; SET statement_timeout = '5min'`); err != nil {
		return err
	}
	var initial int64
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM test_cases`).Scan(&initial); err != nil {
		return err
	}
	fmt.Printf("test rows before backfill: %d; batch size: %d\n", initial, batchSize)
	var total int64
	cursor := "00000000-0000-0000-0000-000000000000"
	for {
		var n int64
		var lastID *string
		err := conn.QueryRow(ctx, backfillStableKeySQL, batchSize, cursor).Scan(&n, &lastID)
		if err != nil {
			return fmt.Errorf("backfill batch (%d rows done): %w", total, err)
		}
		if n == 0 {
			break
		}
		cursor = *lastID
		total += n
		if total > initial+int64(batchSize) {
			return fmt.Errorf("backfill did not converge after %d writes; check stable-key trigger", total)
		}
		fmt.Printf("backfilled %d rows\n", total)
		if pause > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pause):
			}
		}
	}
	for _, idx := range historyIndexes {
		var valid bool
		err := conn.QueryRow(ctx, `SELECT i.indisvalid FROM pg_index i
		    WHERE i.indexrelid = to_regclass($1)`, idx.name).Scan(&valid)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && valid {
			continue
		}
		if err == nil {
			fmt.Printf("repairing interrupted index %s\n", idx.name)
			if _, err := conn.Exec(ctx, "DROP INDEX CONCURRENTLY "+idx.name); err != nil {
				return err
			}
		}
		fmt.Printf("building %s concurrently\n", idx.name)
		if _, err := conn.Exec(ctx, "CREATE INDEX CONCURRENTLY "+idx.name+" "+idx.definition); err != nil {
			return fmt.Errorf("build %s (safe to rerun): %w", idx.name, err)
		}
	}
	var missingKeys, unknownProjects int64
	if err := conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE tc.stable_key IS NULL),
	    count(*) FILTER (WHERE g.framework = 'playwright' AND nullif(tc.project, '') IS NULL)
	    FROM test_cases tc JOIN suites s ON s.id = tc.suite_id
	    JOIN reports r ON r.id = s.report_id JOIN report_groups g ON g.id = r.report_group_id`).
		Scan(&missingKeys, &unknownProjects); err != nil {
		return err
	}
	fmt.Printf("backfill complete: %d writes; missing keys: %d; Playwright rows without project identity: %d\n", total, missingKeys, unknownProjects)
	if missingKeys > 0 {
		return fmt.Errorf("%d rows still have no stable key", missingKeys)
	}
	if unknownProjects > 0 {
		fmt.Println("coverage boundary: old Playwright keys cannot support a new project-prefixed history; collect fresh baseline or replay retained raw reports with verified project identity")
	}
	return nil
}
