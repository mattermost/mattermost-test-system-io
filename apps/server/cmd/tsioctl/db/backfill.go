package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
)

// Migration 28 deliberately ships DDL only. It adds test_cases.file,
// test_cases.project and test_cases.stable_key plus the trigger that maintains
// the key, all of which are catalog-only and finish in milliseconds on a table
// of any size. The two operations whose cost is proportional to the table —
// populating the new columns on pre-existing rows, and building the index the
// history queries read — are here instead, because neither can run inside a
// migration: golang-migrate executes each file as one implicit transaction, so
// a loop cannot commit between batches and CREATE INDEX CONCURRENTLY is
// rejected outright.
//
// Run this once after deploying migration 28. It is safe to interrupt and
// re-run: each batch commits on its own, and the WHERE clause only selects
// rows that still need work, so a second run resumes where the first stopped.
// Rows written after the migration need nothing — the trigger fills them in on
// insert.

const backfillBatchDefault = 5000

// backfillStableKeySQL updates one batch of rows that predate migration 28.
//
// It sets only file and project; stable_key is left to the BEFORE UPDATE
// trigger, so the key's definition stays in exactly one place. project is
// NULL for every pre-existing row and stays NULL: the ingest path threw
// Playwright's projectName away before this change, so it was never stored
// and cannot be recovered. Those rows keep their unprefixed key, which is the
// same key they had before — old history stays joinable to itself, and new
// rows start a correctly-disambiguated series.
//
// ctid is the batching handle rather than a key range: it needs no index,
// never collides with concurrent inserts (which are already correct), and the
// LIMIT keeps each statement's lock footprint to one batch of rows.
const backfillStableKeySQL = `
	WITH batch AS (
		SELECT tc.ctid AS ctid, s.file AS file
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		WHERE tc.stable_key IS NULL
		  AND s.file IS NOT NULL
		LIMIT $1
	)
	UPDATE test_cases tc
	SET file = batch.file
	FROM batch
	WHERE tc.ctid = batch.ctid
`

// backfillNoFileSQL handles rows whose suite carries no file at all. They still
// need a stable_key — the fallback branch produces one from the title alone —
// and touching the row is what makes the trigger compute it. Setting file to
// itself is a no-op write whose only purpose is to fire the trigger.
const backfillNoFileSQL = `
	WITH batch AS (
		SELECT ctid FROM test_cases WHERE stable_key IS NULL LIMIT $1
	)
	UPDATE test_cases tc
	SET file = tc.file
	FROM batch
	WHERE tc.ctid = batch.ctid
`

// The index the history lookups read: (stable_key -> recent rows). Built
// CONCURRENTLY so it never blocks ingest, and after the backfill so it is
// built once over populated data rather than maintained through it.
const createStableKeyIndexSQL = `
	CREATE INDEX CONCURRENTLY IF NOT EXISTS test_cases_stable_key_idx
	    ON test_cases (stable_key)
`

func backfillStableKeyCmd() *cobra.Command {
	var batchSize int
	var pause time.Duration
	cmd := &cobra.Command{
		Use:   "backfill-stable-key",
		Short: "Populate test_cases.stable_key on rows predating migration 28, then index it",
		Long: "Backfills test_cases.file in committed batches so the migration-28 trigger " +
			"computes stable_key for pre-existing rows, then builds the stable_key index " +
			"concurrently. Safe to interrupt and re-run; rows written after migration 28 " +
			"already have their key and are skipped.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if batchSize <= 0 {
				return fmt.Errorf("--batch-size must be positive, got %d", batchSize)
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()
			return runStableKeyBackfill(ctx, pool, batchSize, pause)
		},
	}
	cmd.Flags().IntVar(&batchSize, "batch-size", backfillBatchDefault,
		"rows per committed batch")
	cmd.Flags().DurationVar(&pause, "pause", 100*time.Millisecond,
		"delay between batches, to leave headroom for ingest")
	return cmd
}

func runStableKeyBackfill(ctx context.Context, pool *pgxpool.Pool, batchSize int, pause time.Duration) error {
	var remaining int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM test_cases WHERE stable_key IS NULL`).Scan(&remaining); err != nil {
		return fmt.Errorf("count rows needing backfill: %w", err)
	}
	// The number this migration's lock cost would have depended on, measured
	// against the database actually being migrated rather than guessed.
	fmt.Printf("rows needing stable_key: %d (batch size %d)\n", remaining, batchSize)

	// Both loops terminate because the trigger cannot leave stable_key NULL:
	// title and full_title are NOT NULL (migration 5), so the fallback branch
	// always yields a value and a backfilled row drops out of the next batch.
	// The ceiling is a guard against that reasoning being wrong in some future
	// schema — an unbounded UPDATE loop against production is not something to
	// leave resting on an invariant declared three migrations away.
	ceiling := remaining*2 + int64(batchSize)
	var total int64
	for _, stmt := range []string{backfillStableKeySQL, backfillNoFileSQL} {
		for {
			tag, err := pool.Exec(ctx, stmt, batchSize)
			if err != nil {
				return fmt.Errorf("backfill batch (%d rows done): %w", total, err)
			}
			n := tag.RowsAffected()
			if n == 0 {
				break
			}
			total += n
			if total > ceiling {
				return fmt.Errorf(
					"backfill wrote %d rows for %d needing it and stable_key is still NULL: "+
						"the trigger is not populating the key, aborting rather than looping",
					total, remaining)
			}
			fmt.Printf("  backfilled %d / %d\n", total, remaining)
			if pause > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(pause):
				}
			}
		}
	}
	fmt.Printf("backfill complete: %d rows\n", total)

	// CONCURRENTLY cannot run inside a transaction; pool.Exec issues it
	// outside one. It is slower than a plain CREATE INDEX and can leave an
	// INVALID index behind if it fails — re-running this command drops
	// nothing, so an invalid index must be dropped by hand before retrying.
	fmt.Println("building test_cases_stable_key_idx concurrently...")
	if _, err := pool.Exec(ctx, createStableKeyIndexSQL); err != nil {
		return fmt.Errorf("create stable_key index: %w", err)
	}
	fmt.Println("index ready.")
	return nil
}
