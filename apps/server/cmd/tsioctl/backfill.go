package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/backfill"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

type backfillFlags struct {
	batchSize      int
	maxBatches     int
	sleep          time.Duration
	repo           string
	since          string
	until          string
	afterReportID  string
	afterGroupID   string
	checkpointFile string
	format         string
	databaseURL    string
	dryRun         bool
	force          bool
	skipS3         bool
	skipStage      bool
	skipCases      bool
}

const formatJSON = "json"

// s3FlagEnv maps each --s3-* override flag to the env var it overlays.
var s3FlagEnv = map[string]string{
	"s3-bucket":           "TSIO_S3_BUCKET",
	"s3-endpoint":         "TSIO_S3_ENDPOINT",
	"s3-region":           "TSIO_S3_REGION",
	"s3-force-path-style": "TSIO_S3_FORCE_PATH_STYLE",
}

func newBackfillCmd() *cobra.Command {
	var f backfillFlags
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Backfill run stages, test identities, and failure signatures on existing reports",
		Long: `Fill report_groups.stage, test_cases.test_id / signature_id, and S3-only case
fields (error_stack, project, tags, annotations) on reports ingested before
SEC-11519. Rows are updated in place in throttled keyset batches; pass
--checkpoint-file to resume an interrupted run.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := f.options()
			if err != nil {
				return err
			}
			dsn, err := resolveShadowDatabaseURL(f.databaseURL, os.Getenv, config.LoadDatabaseURL)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			pool, err := db.NewPool(ctx, dsn)
			if err != nil {
				return err
			}
			defer pool.Close()
			var store storage.ObjectStore
			if !f.skipS3 && !f.skipCases {
				cfg, err := loadBackfillStorageConfig(cmd)
				if err != nil {
					return err
				}
				if store, err = storage.New(ctx, cfg); err != nil {
					return err
				}
			}
			opts.Logger = slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			sum, runErr := backfill.Run(ctx, pool, store, opts)
			if err := renderBackfillSummary(cmd.OutOrStdout(), sum, f.format); err != nil {
				return err
			}
			return runErr
		},
	}
	fl := cmd.Flags()
	fl.IntVar(&f.batchSize, "batch-size", backfill.DefaultBatchSize, fmt.Sprintf("Report groups (stage phase) or reports (cases phase) per batch (1..%d)", backfill.MaxBatchSize))
	fl.DurationVar(&f.sleep, "sleep", backfill.DefaultSleepInterval, "Pause between batches (0 disables throttling)")
	fl.IntVar(&f.maxBatches, "max-batches", 0, "Stop after this many batches (0 = unlimited)")
	fl.StringVar(&f.repo, "repo", "", "Optional exact repository filter (e.g. mattermost/mattermost)")
	fl.StringVar(&f.since, "since", "", "Only rows created at/after this time (RFC3339 or YYYY-MM-DD)")
	fl.StringVar(&f.until, "until", "", "Only rows created before this time (RFC3339 or YYYY-MM-DD)")
	fl.StringVar(&f.afterReportID, "after-report-id", "", "Resume cases after this report UUID (overrides checkpoint)")
	fl.StringVar(&f.afterGroupID, "after-group-id", "", "Resume stages after this report group UUID (overrides checkpoint)")
	fl.StringVar(&f.checkpointFile, "checkpoint-file", "", "JSON checkpoint loaded at start and saved after every batch")
	fl.BoolVar(&f.dryRun, "dry-run", false, "Report planned changes without writing rows or the checkpoint")
	fl.BoolVar(&f.force, "force", false, "Re-resolve identities for every case, not just rows missing them")
	fl.BoolVar(&f.skipS3, "skip-s3", false, "Do not read report JSON from S3 (DB-only backfill)")
	fl.BoolVar(&f.skipStage, "skip-stage", false, "Skip the report_groups.stage phase")
	fl.BoolVar(&f.skipCases, "skip-cases", false, "Skip the test_cases phase")
	fl.StringVar(&f.format, "format", "text", "Output format: text | json")
	fl.StringVar(&f.databaseURL, "database-url", "", "PostgreSQL connection URL override")
	fl.String("s3-bucket", "", "S3 bucket override (TSIO_S3_BUCKET)")
	fl.String("s3-endpoint", "", "S3 endpoint override (TSIO_S3_ENDPOINT)")
	fl.String("s3-region", "", "S3 region override (TSIO_S3_REGION)")
	fl.Bool("s3-force-path-style", false, "S3 path-style addressing override (TSIO_S3_FORCE_PATH_STYLE)")
	return cmd
}

func (f backfillFlags) options() (backfill.Options, error) {
	if f.format != "text" && f.format != formatJSON {
		return backfill.Options{}, fmt.Errorf("--format must be text or json, got %q", f.format)
	}
	since, err := parseTimeFlag("--since", f.since)
	if err != nil {
		return backfill.Options{}, err
	}
	until, err := parseTimeFlag("--until", f.until)
	if err != nil {
		return backfill.Options{}, err
	}
	afterReport, err := parseUUIDFlag("--after-report-id", f.afterReportID)
	if err != nil {
		return backfill.Options{}, err
	}
	afterGroup, err := parseUUIDFlag("--after-group-id", f.afterGroupID)
	if err != nil {
		return backfill.Options{}, err
	}
	return backfill.Options{
		BatchSize: f.batchSize, SleepInterval: f.sleep, MaxBatches: f.maxBatches,
		Repository: strings.TrimSpace(f.repo), Since: since, Until: until,
		AfterReportID: afterReport, AfterGroupID: afterGroup, CheckpointFile: f.checkpointFile,
		DryRun: f.dryRun, Force: f.force, SkipS3: f.skipS3, SkipStage: f.skipStage, SkipCases: f.skipCases,
	}, nil
}

func parseTimeFlag(name, v string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, v); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("%s must be RFC3339 or YYYY-MM-DD, got %q", name, v)
}

func parseUUIDFlag(name, v string) (uuid.UUID, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s: %w", name, err)
	}
	return id, nil
}

// loadBackfillStorageConfig overlays explicitly set --s3-* flags onto the
// TSIO_S3_* environment so config.LoadStorageConfig stays the single source
// of defaults and validation.
func loadBackfillStorageConfig(cmd *cobra.Command) (storage.Config, error) {
	for flag, key := range s3FlagEnv {
		if f := cmd.Flags().Lookup(flag); f != nil && f.Changed {
			if err := os.Setenv(key, f.Value.String()); err != nil {
				return storage.Config{}, err
			}
		}
	}
	return config.LoadStorageConfig()
}

func renderBackfillSummary(w io.Writer, s backfill.Summary, format string) error {
	if format == formatJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	_, err := fmt.Fprintf(w,
		"dry_run=%t batches=%d duration_ms=%d\n"+
			"stage_groups: scanned=%d updated=%d last_group_id=%s\n"+
			"reports: scanned=%d updated=%d skipped=%d last_report_id=%s\n"+
			"cases: updated=%d enriched_from_s3=%d\n"+
			"s3_files: parsed=%d missing=%d\n",
		s.DryRun, s.BatchesRun, s.DurationMs,
		s.StageGroupsScanned, s.StageGroupsUpdated, s.LastGroupID,
		s.ReportsScanned, s.ReportsUpdated, s.ReportsSkipped, s.LastReportID,
		s.CasesUpdated, s.CasesEnrichedFromS3,
		s.S3FilesParsed, s.S3FilesMissing)
	return err
}
