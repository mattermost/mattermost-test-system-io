package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/backfill"
)

func TestBackfillFlagsOptions(t *testing.T) {
	reportID, groupID := uuid.New(), uuid.New()
	f := backfillFlags{
		batchSize: 10, maxBatches: 3, sleep: time.Second, repo: " mattermost/mattermost ",
		since: "2026-01-01", until: "2026-02-01T00:00:00Z",
		afterReportID: reportID.String(), afterGroupID: groupID.String(),
		checkpointFile: "/tmp/cp.json", format: "json",
		dryRun: true, force: true, skipS3: true, skipStage: true, skipCases: true,
	}
	got, err := f.options()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if got.BatchSize != 10 || got.MaxBatches != 3 || got.SleepInterval != time.Second ||
		got.Repository != "mattermost/mattermost" || !got.Since.Equal(since) || !got.Until.Equal(until) ||
		got.AfterReportID != reportID || got.AfterGroupID != groupID || got.CheckpointFile != "/tmp/cp.json" ||
		!got.DryRun || !got.Force || !got.SkipS3 || !got.SkipStage || !got.SkipCases {
		t.Fatalf("options mapped wrong: %+v", got)
	}

	for name, bad := range map[string]backfillFlags{
		"format": {format: "table"},
		"since":  {format: "text", since: "yesterday"},
		"until":  {format: "text", until: "01/02/2026"},
		"report": {format: "text", afterReportID: "nope"},
		"group":  {format: "text", afterGroupID: "nope"},
	} {
		if _, err := bad.options(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestBackfillCmdDefaults(t *testing.T) {
	fl := newBackfillCmd().Flags()
	for flag, want := range map[string]string{
		"batch-size": "50", "sleep": "200ms", "max-batches": "0", "format": "text", "dry-run": "false",
	} {
		if got := fl.Lookup(flag).DefValue; got != want {
			t.Errorf("--%s default = %q, want %q", flag, got, want)
		}
	}
}

func TestLoadBackfillStorageConfig_flagsOverrideEnv(t *testing.T) {
	t.Setenv("TSIO_S3_BUCKET", "env-bucket")
	t.Setenv("TSIO_S3_ENDPOINT", "http://env:9000")
	t.Setenv("TSIO_S3_REGION", "")
	t.Setenv("TSIO_S3_FORCE_PATH_STYLE", "false")
	cmd := newBackfillCmd()
	for flag, v := range map[string]string{"s3-bucket": "flag-bucket", "s3-force-path-style": "true"} {
		if err := cmd.Flags().Set(flag, v); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := loadBackfillStorageConfig(cmd)
	if err != nil {
		t.Fatalf("loadBackfillStorageConfig: %v", err)
	}
	if cfg.Bucket != "flag-bucket" || cfg.Endpoint != "http://env:9000" || cfg.Region != "us-east-1" || !cfg.ForcePathStyle {
		t.Fatalf("unexpected storage config: %+v", cfg)
	}

	t.Setenv("TSIO_S3_BUCKET", "")
	if _, err := loadBackfillStorageConfig(newBackfillCmd()); err == nil {
		t.Fatal("expected error without bucket")
	}
}

func TestRenderBackfillSummary(t *testing.T) {
	s := backfill.Summary{DryRun: true, ReportsScanned: 2, CasesUpdated: 5, LastReportID: uuid.New()}
	var buf bytes.Buffer
	if err := renderBackfillSummary(&buf, s, "json"); err != nil {
		t.Fatalf("json: %v", err)
	}
	var got backfill.Summary
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil || got != s {
		t.Fatalf("json round trip = (%+v, %v), want %+v", got, err, s)
	}
	buf.Reset()
	if err := renderBackfillSummary(&buf, s, "text"); err != nil {
		t.Fatalf("text: %v", err)
	}
	for _, want := range []string{"dry_run=true", "reports: scanned=2", "cases: updated=5", s.LastReportID.String()} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("text output missing %q:\n%s", want, buf.String())
		}
	}
}
