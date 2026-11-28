package backfill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/ingest"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

func TestOptionsNormalize(t *testing.T) {
	o := Options{}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize zero options: %v", err)
	}
	if o.BatchSize != DefaultBatchSize || o.Sleep == nil || o.Logger == nil {
		t.Fatalf("defaults not applied: %+v", o)
	}
	since := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	until := since.Add(-time.Hour)
	for name, bad := range map[string]Options{
		"batch below range":   {BatchSize: -1},
		"batch above range":   {BatchSize: MaxBatchSize + 1},
		"negative sleep":      {SleepInterval: -time.Millisecond},
		"negative maxBatches": {MaxBatches: -1},
		"since after until":   {Since: &since, Until: &until},
	} {
		if err := bad.normalize(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	ok := Options{BatchSize: MaxBatchSize}
	if err := ok.normalize(); err != nil {
		t.Fatalf("max batch size rejected: %v", err)
	}
}

func TestOptionsNormalize_checkpointCursors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.json")
	saved := checkpoint{LastReportID: uuid.New(), LastGroupID: uuid.New(), UpdatedAt: time.Now().UTC()}
	if err := saveCheckpoint(path, saved); err != nil {
		t.Fatalf("save: %v", err)
	}
	o := Options{CheckpointFile: path}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if o.AfterReportID != saved.LastReportID || o.AfterGroupID != saved.LastGroupID {
		t.Fatalf("checkpoint not loaded: %+v", o)
	}
	explicit := uuid.New()
	o = Options{CheckpointFile: path, AfterReportID: explicit}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize explicit: %v", err)
	}
	if o.AfterReportID != explicit || o.AfterGroupID != saved.LastGroupID {
		t.Fatalf("explicit cursor should win per field: %+v", o)
	}
}

func TestCheckpointRoundTripAndAtomicity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.json")
	if cp, err := loadCheckpoint(path); err != nil || cp != (checkpoint{}) {
		t.Fatalf("missing checkpoint = (%+v, %v), want zero", cp, err)
	}
	for range 3 {
		want := checkpoint{LastReportID: uuid.New(), LastGroupID: uuid.New(), UpdatedAt: time.Now().UTC().Truncate(time.Second)}
		if err := saveCheckpoint(path, want); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := loadCheckpoint(path)
		if err != nil || got != want {
			t.Fatalf("round trip = (%+v, %v), want %+v", got, err, want)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temp files left behind: %v (%v)", entries, err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCheckpoint(path); err == nil {
		t.Fatal("expected parse error for corrupt checkpoint")
	}
	if err := saveCheckpoint(filepath.Join(dir, "missing", "cp.json"), checkpoint{}); err == nil {
		t.Fatal("expected error writing into a missing directory")
	}
}

func TestMatchCases_multiTierFIFO(t *testing.T) {
	db := [][4]string{
		tierKeys("e2e/a.spec.ts", "suite a", "a", "failed", 1),
		tierKeys("e2e/a.spec.ts", "suite a", "a", "failed", 0),
		tierKeys("old/b.spec.ts", "suite b", "b", "passed", 0),
		tierKeys("", "", "c", "skipped", 0),
		tierKeys("e2e/x.spec.ts", "missing", "missing", "passed", 0),
		tierKeys("e2e/d.spec.ts", "dup", "dup", "failed", 2),
		tierKeys("e2e/d.spec.ts", "dup", "dup", "failed", 2),
	}
	ext := [][4]string{
		tierKeys("/github/workspace/e2e/a.spec.ts", " suite   a ", "a", "failed", 0),
		tierKeys("./e2e/a.spec.ts", "suite a", "a", "failed", 1),
		tierKeys("e2e/b.spec.ts", "suite b", "b", "passed", 0),
		tierKeys("e2e/c.spec.ts", "outer c", "c", "skipped", 0),
		tierKeys("e2e/d.spec.ts", "dup", "dup", "failed", 0),
		tierKeys("e2e/d.spec.ts", "dup", "dup", "failed", 1),
	}
	want := []int{1, 0, 2, 3, -1, 4, 5}
	if got := matchCases(db, ext); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("matchCases = %v, want %v", got, want)
	}
	if got := matchCases(db[:1], nil); got[0] != -1 {
		t.Fatalf("no extracted cases should yield -1, got %v", got)
	}
}

func TestEnrich_fillsOnlyEmptyFields(t *testing.T) {
	stack, project, kept := "Error\n\tat x\x00", "chromium", "existing stack"
	c := dbCase{Tags: []byte("[]"), Annotations: []byte("[]")}
	e := ingest.ExtractedCase{
		ErrorStack: &stack, Project: &project, Tags: []string{"@smoke"},
		Annotations: []json.RawMessage{json.RawMessage(`{"type":"issue"}`)},
	}
	if !enrich(&c, e) {
		t.Fatal("expected enrichment")
	}
	if *c.ErrorStack != "Error\n\tat x" || *c.Project != project ||
		string(c.Tags) != `["@smoke"]` || string(c.Annotations) != `[{"type":"issue"}]` {
		t.Fatalf("unexpected enrichment: %+v", c)
	}
	c = dbCase{ErrorStack: &kept, Project: &kept, Tags: []byte(`["a"]`), Annotations: []byte(`[{}]`)}
	if enrich(&c, e) || *c.ErrorStack != kept {
		t.Fatalf("non-empty fields must not change: %+v", c)
	}
}

type statusErr int

func (s statusErr) Error() string       { return fmt.Sprintf("status %d", int(s)) }
func (s statusErr) HTTPStatusCode() int { return int(s) }

func TestIsObjectNotFound(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":            {nil, false},
		"storage":        {fmt.Errorf("get: %w", storage.ErrNotFound), true},
		"no such key":    {fmt.Errorf("s3: %w", &types.NoSuchKey{}), true},
		"not found":      {&types.NotFound{}, true},
		"http 404":       {statusErr(404), true},
		"http 503":       {statusErr(503), false},
		"no such bucket": {errors.Join(&types.NoSuchBucket{}, statusErr(404)), false},
		"transient":      {errors.New("connection reset"), false},
	} {
		if got := isObjectNotFound(tc.err); got != tc.want {
			t.Errorf("%s: isObjectNotFound = %v, want %v", name, got, tc.want)
		}
	}
}

func TestNeedsBackfill(t *testing.T) {
	id := uuid.New()
	if needsBackfill(nil, false) || needsBackfill(nil, true) {
		t.Fatal("empty report should not need backfill")
	}
	done := []dbCase{
		{Status: "passed", TestID: &id},
		{Status: "failed", TestID: &id, SignatureID: &id},
	}
	if needsBackfill(done, false) {
		t.Fatal("fully populated report should not need backfill")
	}
	if !needsBackfill(done, true) {
		t.Fatal("force=true should need backfill when cases exist")
	}
	if !needsBackfill([]dbCase{{Status: "passed", TestID: nil}}, false) {
		t.Fatal("missing test_id should need backfill")
	}
	if !needsBackfill([]dbCase{{Status: "failed", TestID: &id, SignatureID: nil}}, false) {
		t.Fatal("failing case missing signature_id should need backfill")
	}
}
