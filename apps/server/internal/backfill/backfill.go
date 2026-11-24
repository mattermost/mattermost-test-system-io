// Package backfill fills SEC-11519 columns (report_groups.stage,
// test_cases.test_id / signature_id, and S3-only case fields) on reports
// ingested before those columns existed. Runs are keyset-paginated,
// checkpointed, throttled, and idempotent: rows are updated in place so
// test_cases.id and report_screenshots.case_id never change.
package backfill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/ingest"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

const (
	// DefaultBatchSize is used when Options.BatchSize is 0.
	DefaultBatchSize = 50
	// MaxBatchSize caps rows per batch to keep transactions short.
	MaxBatchSize = 500
	// DefaultSleepInterval is the CLI's default pause between batches.
	DefaultSleepInterval = 200 * time.Millisecond

	maxJSONBytes = 256 << 20
)

var errTooLarge = errors.New("object exceeds backfill read limit")

// Options configures Run. Zero values are safe: BatchSize 0 means
// DefaultBatchSize, SleepInterval 0 disables throttling, MaxBatches 0 is
// unlimited, and nil cursors resume from CheckpointFile when it exists.
type Options struct {
	BatchSize      int
	SleepInterval  time.Duration
	Sleep          func(context.Context, time.Duration) error
	MaxBatches     int
	Repository     string
	Since, Until   *time.Time
	AfterReportID  uuid.UUID
	AfterGroupID   uuid.UUID
	CheckpointFile string
	DryRun         bool
	Force          bool
	SkipS3         bool
	SkipStage      bool
	SkipCases      bool
	Logger         *slog.Logger
}

// Summary reports what Run scanned and changed (or would change on DryRun).
type Summary struct {
	DryRun              bool      `json:"dry_run"`
	StageGroupsScanned  int       `json:"stage_groups_scanned"`
	StageGroupsUpdated  int       `json:"stage_groups_updated"`
	ReportsScanned      int       `json:"reports_scanned"`
	ReportsUpdated      int       `json:"reports_updated"`
	ReportsSkipped      int       `json:"reports_skipped"`
	S3FilesParsed       int       `json:"s3_files_parsed"`
	S3FilesMissing      int       `json:"s3_files_missing"`
	CasesUpdated        int       `json:"cases_updated"`
	CasesEnrichedFromS3 int       `json:"cases_enriched_from_s3"`
	BatchesRun          int       `json:"batches_run"`
	LastGroupID         uuid.UUID `json:"last_group_id"`
	LastReportID        uuid.UUID `json:"last_report_id"`
	DurationMs          int64     `json:"duration_ms"`
}

type checkpoint struct {
	LastReportID uuid.UUID `json:"last_report_id"`
	LastGroupID  uuid.UUID `json:"last_group_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type runner struct {
	pool  *pgxpool.Pool
	store storage.ObjectStore
	opts  Options
	sum   Summary
}

// Run backfills stages (phase A) then test cases (phase B). store may be nil,
// in which case cases are backfilled from DB columns only. On error the
// partial Summary is returned and the checkpoint stays at the last committed
// batch.
func Run(ctx context.Context, pool *pgxpool.Pool, store storage.ObjectStore, opts Options) (Summary, error) {
	start := time.Now()
	if err := opts.normalize(); err != nil {
		return Summary{}, err
	}
	r := &runner{pool: pool, store: store, opts: opts, sum: Summary{
		DryRun: opts.DryRun, LastGroupID: opts.AfterGroupID, LastReportID: opts.AfterReportID,
	}}
	err := r.run(ctx)
	r.sum.DurationMs = time.Since(start).Milliseconds()
	return r.sum, err
}

func (o *Options) normalize() error {
	if o.BatchSize == 0 {
		o.BatchSize = DefaultBatchSize
	}
	switch {
	case o.BatchSize < 1 || o.BatchSize > MaxBatchSize:
		return fmt.Errorf("batch size must be in [1, %d], got %d", MaxBatchSize, o.BatchSize)
	case o.SleepInterval < 0:
		return fmt.Errorf("sleep interval must be >= 0, got %s", o.SleepInterval)
	case o.MaxBatches < 0:
		return fmt.Errorf("max batches must be >= 0, got %d", o.MaxBatches)
	case o.Since != nil && o.Until != nil && !o.Since.Before(*o.Until):
		return errors.New("since must be before until")
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	cp, err := loadCheckpoint(o.CheckpointFile)
	if err != nil {
		return err
	}
	if o.AfterGroupID == uuid.Nil {
		o.AfterGroupID = cp.LastGroupID
	}
	if o.AfterReportID == uuid.Nil {
		o.AfterReportID = cp.LastReportID
	}
	return nil
}

func (r *runner) run(ctx context.Context) error {
	if !r.opts.SkipStage {
		if err := r.loop(ctx, "stage", r.stageBatch); err != nil {
			return err
		}
	}
	if !r.opts.SkipCases {
		return r.loop(ctx, "cases", r.casesBatch)
	}
	return nil
}

// loop runs batch until it returns a short batch or MaxBatches is reached,
// saving the checkpoint after every committed batch and sleeping in between.
func (r *runner) loop(ctx context.Context, phase string, batch func(context.Context) (int, error)) error {
	for !r.maxed() {
		n, err := batch(ctx)
		if err != nil {
			return fmt.Errorf("%s backfill: %w", phase, err)
		}
		if n == 0 {
			return nil
		}
		r.sum.BatchesRun++
		r.opts.Logger.Info("backfill batch", "phase", phase, "rows", n, "batches_run", r.sum.BatchesRun,
			"last_group_id", r.sum.LastGroupID, "last_report_id", r.sum.LastReportID)
		if err := r.saveCheckpoint(); err != nil {
			return err
		}
		if n < r.opts.BatchSize || r.maxed() {
			return nil
		}
		if err := r.opts.Sleep(ctx, r.opts.SleepInterval); err != nil {
			return err
		}
	}
	return nil
}

func (r *runner) maxed() bool {
	return r.opts.MaxBatches > 0 && r.sum.BatchesRun >= r.opts.MaxBatches
}

func (r *runner) stageBatch(ctx context.Context) (int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, COALESCE(event_name, ''), branch, gh_pr_number
		FROM report_groups
		WHERE id > $1 AND stage = 'other'
		  AND ($2::text = '' OR repository = $2)
		  AND ($3::timestamptz IS NULL OR created_at >= $3)
		  AND ($4::timestamptz IS NULL OR created_at < $4)
		ORDER BY id
		LIMIT $5
	`, r.sum.LastGroupID, r.opts.Repository, r.opts.Since, r.opts.Until, r.opts.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("select report_groups: %w", err)
	}
	var ids []uuid.UUID
	var stages []string
	var last uuid.UUID
	n := 0
	for rows.Next() {
		var event, branch string
		var pr *int
		if err := rows.Scan(&last, &event, &branch, &pr); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan report_group: %w", err)
		}
		n++
		if stage := reports.ResolveRunStage("", event, branch, pr); stage != reports.RunStageOther {
			ids, stages = append(ids, last), append(stages, stage)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate report_groups: %w", err)
	}
	updated, err := r.updateStages(ctx, ids, stages)
	if err != nil {
		return 0, err
	}
	r.sum.StageGroupsScanned += n
	r.sum.StageGroupsUpdated += updated
	if n > 0 {
		r.sum.LastGroupID = last
	}
	return n, nil
}

func (r *runner) updateStages(ctx context.Context, ids []uuid.UUID, stages []string) (int, error) {
	if r.opts.DryRun || len(ids) == 0 {
		return len(ids), nil
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE report_groups g SET stage = u.stage
		FROM UNNEST($1::uuid[], $2::text[]) AS u(id, stage)
		WHERE g.id = u.id AND g.stage = 'other'
	`, ids, stages)
	if err != nil {
		return 0, fmt.Errorf("update report_groups stage: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

type reportRow struct {
	ID         uuid.UUID
	GroupID    uuid.UUID
	Repository string
	Framework  string
	CreatedAt  time.Time
}

func (r *runner) casesBatch(ctx context.Context) (int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.id, r.report_group_id, g.repository, g.framework, r.created_at
		FROM reports r
		JOIN report_groups g ON g.id = r.report_group_id
		WHERE r.id > $1
		  AND ($2::text = '' OR g.repository = $2)
		  AND ($3::timestamptz IS NULL OR r.created_at >= $3)
		  AND ($4::timestamptz IS NULL OR r.created_at < $4)
		  AND ($6::boolean OR EXISTS (
		      SELECT 1 FROM suites s JOIN test_cases tc ON tc.suite_id = s.id
		      WHERE s.report_id = r.id
		        AND (tc.test_id IS NULL OR (tc.signature_id IS NULL
		             AND tc.status NOT IN ('passed', 'flaky', 'skipped', 'pending')))))
		ORDER BY r.id
		LIMIT $5
	`, r.sum.LastReportID, r.opts.Repository, r.opts.Since, r.opts.Until, r.opts.BatchSize, r.opts.Force)
	if err != nil {
		return 0, fmt.Errorf("select reports: %w", err)
	}
	batch, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reportRow])
	if err != nil {
		return 0, fmt.Errorf("scan reports: %w", err)
	}
	for _, rep := range batch {
		if err := r.backfillReport(ctx, rep); err != nil {
			return 0, fmt.Errorf("report %s: %w", rep.ID, err)
		}
	}
	if len(batch) > 0 {
		r.sum.LastReportID = batch[len(batch)-1].ID
	}
	return len(batch), nil
}

type dbCase struct {
	ID           uuid.UUID
	File         *string
	Title        string
	FullTitle    string
	Status       string
	RetryCount   int
	ErrorMessage *string
	ErrorStack   *string
	Project      *string
	Tags         []byte
	Annotations  []byte
	TestID       *uuid.UUID
	SignatureID  *uuid.UUID
}

type extCase struct {
	file string
	c    ingest.ExtractedCase
}

// reportPlan lists the cases to rewrite and the identity inputs to resolve;
// idCases[k] is the case index that inputs[k] belongs to.
type reportPlan struct {
	dirty    []int
	idCases  []int
	inputs   []ingest.CaseIdentityInput
	enriched int
}

func (r *runner) backfillReport(ctx context.Context, rep reportRow) error {
	r.sum.ReportsScanned++
	cases, err := r.loadCases(ctx, rep.ID)
	if err != nil {
		return err
	}
	ext, err := r.loadExtracted(ctx, rep)
	if err != nil {
		return err
	}
	p := r.plan(cases, ext)
	if len(p.dirty) == 0 {
		r.sum.ReportsSkipped++
		return nil
	}
	if !r.opts.DryRun {
		if err := r.write(ctx, rep, cases, p); err != nil {
			return err
		}
	}
	r.sum.ReportsUpdated++
	r.sum.CasesUpdated += len(p.dirty)
	r.sum.CasesEnrichedFromS3 += p.enriched
	return nil
}

func (r *runner) loadCases(ctx context.Context, reportID uuid.UUID) ([]dbCase, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT tc.id, s.file, tc.title, tc.full_title, tc.status, tc.retry_count,
		       tc.error_message, tc.error_stack, tc.project, tc.tags, tc.annotations,
		       tc.test_id, tc.signature_id
		FROM suites s
		JOIN test_cases tc ON tc.suite_id = s.id
		WHERE s.report_id = $1
		ORDER BY s.ordinal, s.id, tc.ordinal, tc.id
	`, reportID)
	if err != nil {
		return nil, fmt.Errorf("select test_cases: %w", err)
	}
	cases, err := pgx.CollectRows(rows, pgx.RowToStructByPos[dbCase])
	if err != nil {
		return nil, fmt.Errorf("scan test_cases: %w", err)
	}
	return cases, nil
}

// loadExtracted re-parses the report's S3 JSON files. If any object is missing
// (or oversized) the whole report falls back to DB-only fields, so surviving
// files (e.g. firefox) are never matched onto rows from the lost one (e.g.
// chromium). Any other store error aborts the run so a transient outage never
// advances the checkpoint past unread reports.
func (r *runner) loadExtracted(ctx context.Context, rep reportRow) ([]extCase, error) {
	if r.opts.SkipS3 || r.store == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT object_key FROM report_json_files WHERE report_id = $1 ORDER BY created_at, id`, rep.ID)
	if err != nil {
		return nil, fmt.Errorf("select report_json_files: %w", err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scan report_json_files: %w", err)
	}
	var suites []ingest.ExtractedSuite
	seq := 0
	for _, key := range keys {
		body, err := r.readObject(ctx, key)
		if isObjectNotFound(err) || errors.Is(err, errTooLarge) {
			r.sum.S3FilesMissing++
			r.opts.Logger.Warn("s3 json unusable; using DB fields only for this report",
				"report_id", rep.ID, "object_key", key, "error", err)
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read s3 %s: %w", key, err)
		}
		r.sum.S3FilesParsed++
		s, _, _ := ingest.Extract(rep.Framework, body, &seq)
		if len(s) == 0 {
			s, _, _ = ingest.Extract("", body, &seq)
		}
		if len(s) == 0 {
			r.opts.Logger.Warn("s3 json yielded no suites", "report_id", rep.ID, "object_key", key)
		}
		suites = append(suites, s...)
	}
	var out []extCase
	for _, s := range ingest.MergeSuitesByFile(suites) {
		for _, c := range s.Cases {
			out = append(out, extCase{file: deref(s.FilePath), c: c})
		}
	}
	return out, nil
}

func (r *runner) readObject(ctx context.Context, key string) ([]byte, error) {
	rc, _, err := r.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, maxJSONBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxJSONBytes {
		return nil, errTooLarge
	}
	return body, nil
}

// plan enriches cases in place from their matched S3 cases and selects the
// rows to rewrite: enriched rows plus rows missing test_id or, for failures,
// signature_id (every row when Force).
func (r *runner) plan(cases []dbCase, ext []extCase) reportPlan {
	dbKeys := make([][4]string, len(cases))
	for i, c := range cases {
		dbKeys[i] = tierKeys(deref(c.File), c.FullTitle, c.Title, c.Status, c.RetryCount)
	}
	extKeys := make([][4]string, len(ext))
	for j, e := range ext {
		extKeys[j] = tierKeys(e.file, e.c.FullTitle, e.c.Title, e.c.Status, e.c.RetryCount)
	}
	var p reportPlan
	for i, j := range matchCases(dbKeys, extKeys) {
		c := &cases[i]
		enriched := j >= 0 && enrich(c, ext[j].c)
		if enriched {
			p.enriched++
		}
		needID := r.opts.Force || c.TestID == nil || (teststatus.IsFail(c.Status) && c.SignatureID == nil)
		if needID {
			p.idCases = append(p.idCases, i)
			p.inputs = append(p.inputs, identityInput(*c, ext, j))
		}
		if enriched || needID {
			p.dirty = append(p.dirty, i)
		}
	}
	return p
}

func identityInput(c dbCase, ext []extCase, match int) ingest.CaseIdentityInput {
	var tags []string
	_ = json.Unmarshal(c.Tags, &tags) // non-array tags carry no Zephyr key
	in := ingest.CaseIdentityInput{
		File: deref(c.File), Title: c.Title, FullTitle: c.FullTitle, Tags: tags, Status: c.Status,
		ErrorMessage: deref(c.ErrorMessage), ErrorStack: deref(c.ErrorStack),
	}
	if match >= 0 {
		in.StartTime = ext[match].c.StartTime
	}
	return in
}

// tierKeys returns the match keys from most to least specific:
// (file, fullTitle, status, retry), (file, fullTitle, status),
// (fullTitle, status), (title, status). Keys without a title are "".
func tierKeys(file, fullTitle, title, status string, retry int) [4]string {
	f, ft, t := ingest.NormalizeTestFile(file), ingest.NormalizeTestTitle(fullTitle), ingest.NormalizeTestTitle(title)
	var k [4]string
	if ft != "" {
		k[0] = strings.Join([]string{f, ft, status, strconv.Itoa(retry)}, "\x00")
		k[1] = strings.Join([]string{f, ft, status}, "\x00")
		k[2] = ft + "\x00" + status
	}
	if t != "" {
		k[3] = t + "\x00" + status
	}
	return k
}

// matchCases pairs each DB case with at most one extracted case, tier by
// tier, consuming extracted cases first-in-first-out. Unmatched cases get -1.
func matchCases(dbKeys, extKeys [][4]string) []int {
	match := make([]int, len(dbKeys))
	for i := range match {
		match[i] = -1
	}
	used := make([]bool, len(extKeys))
	for tier := range 4 {
		queue := map[string][]int{}
		for j, k := range extKeys {
			if !used[j] && k[tier] != "" {
				queue[k[tier]] = append(queue[k[tier]], j)
			}
		}
		for i, k := range dbKeys {
			if q := queue[k[tier]]; match[i] < 0 && k[tier] != "" && len(q) > 0 {
				match[i], used[q[0]], queue[k[tier]] = q[0], true, q[1:]
			}
		}
	}
	return match
}

// enrich copies S3-only fields onto empty DB columns and reports any change.
func enrich(c *dbCase, e ingest.ExtractedCase) bool {
	changed := false
	if deref(c.ErrorStack) == "" && deref(e.ErrorStack) != "" {
		c.ErrorStack, changed = ingest.SanitizeUTF8Ptr(e.ErrorStack), true
	}
	if deref(c.Project) == "" && deref(e.Project) != "" {
		c.Project, changed = ingest.SanitizeUTF8Ptr(e.Project), true
	}
	if emptyJSON(c.Tags) && len(e.Tags) > 0 {
		c.Tags, changed = ingest.EncodeTags(e.Tags), true
	}
	if emptyJSON(c.Annotations) {
		if anns := ingest.EncodeAnnotations(e.Annotations); !emptyJSON(anns) {
			c.Annotations, changed = anns, true
		}
	}
	return changed
}

// write resolves identities in their own short transaction (as Consolidate
// does), then updates the rows and attributes signatures in a second one.
func (r *runner) write(ctx context.Context, rep reportRow, cases []dbCase, p reportPlan) error {
	meta := ingest.IdentityMeta{Repository: rep.Repository, Framework: rep.Framework, SeenAt: rep.CreatedAt}
	identities, err := ingest.ResolveCaseIdentitiesTx(ctx, r.pool, meta, p.inputs)
	if err != nil {
		return err
	}
	for k, i := range p.idCases {
		if r.opts.Force || cases[i].TestID == nil {
			cases[i].TestID = &identities[k].TestID
		}
		if r.opts.Force || cases[i].SignatureID == nil {
			cases[i].SignatureID = identities[k].SignatureID
		}
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin update tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	batch := &pgx.Batch{}
	assigned := make([]ingest.TestIdentity, 0, len(p.dirty))
	for _, i := range p.dirty {
		c := cases[i]
		// Guarded so values another writer committed after loadCases win:
		// identities only fill NULLs unless Force, enrichment only fills blanks.
		batch.Queue(`
			UPDATE test_cases SET
				test_id      = CASE WHEN $8::boolean THEN $2 ELSE COALESCE(test_id, $2) END,
				signature_id = CASE WHEN $8::boolean THEN $3 ELSE COALESCE(signature_id, $3) END,
				error_stack  = CASE WHEN NULLIF(error_stack, '') IS NULL THEN $4 ELSE error_stack END,
				project      = CASE WHEN NULLIF(project, '') IS NULL THEN $5 ELSE project END,
				tags         = CASE WHEN tags = '[]'::jsonb THEN $6 ELSE tags END,
				annotations  = CASE WHEN annotations = '[]'::jsonb THEN $7 ELSE annotations END
			WHERE id = $1
		`, c.ID, c.TestID, c.SignatureID, c.ErrorStack, c.Project, c.Tags, c.Annotations, r.opts.Force)
		assigned = append(assigned, ingest.TestIdentity{SignatureID: c.SignatureID})
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("update test_cases: %w", err)
	}
	if err := ingest.AttributeFailureSignaturesTx(ctx, tx, rep.GroupID, assigned); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit update tx: %w", err)
	}
	return nil
}

func (r *runner) saveCheckpoint() error {
	if r.opts.DryRun || r.opts.CheckpointFile == "" {
		return nil
	}
	return saveCheckpoint(r.opts.CheckpointFile, checkpoint{
		LastReportID: r.sum.LastReportID, LastGroupID: r.sum.LastGroupID, UpdatedAt: time.Now().UTC(),
	})
}

func loadCheckpoint(path string) (checkpoint, error) {
	var cp checkpoint
	if path == "" {
		return cp, nil
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return cp, nil
	}
	if err != nil {
		return cp, fmt.Errorf("read checkpoint: %w", err)
	}
	if err := json.Unmarshal(b, &cp); err != nil {
		return cp, fmt.Errorf("parse checkpoint %s: %w", path, err)
	}
	return cp, nil
}

// saveCheckpoint writes cp to a temp file in the same directory and renames it
// over path, so a crash never leaves a truncated checkpoint.
func saveCheckpoint(path string, cp checkpoint) error {
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create checkpoint temp: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close checkpoint: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename checkpoint: %w", err)
	}
	return nil
}

// isObjectNotFound reports whether err means the key is absent (not that the
// bucket is missing or the store is unreachable).
func isObjectNotFound(err error) bool {
	var noBucket *types.NoSuchBucket
	if err == nil || errors.As(err, &noBucket) {
		return false
	}
	var noKey *types.NoSuchKey
	var notFound *types.NotFound
	var status interface{ HTTPStatusCode() int }
	return errors.Is(err, storage.ErrNotFound) || errors.As(err, &noKey) || errors.As(err, &notFound) ||
		(errors.As(err, &status) && status.HTTPStatusCode() == http.StatusNotFound)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func emptyJSON(b []byte) bool {
	s := strings.TrimSpace(string(b))
	return s == "" || s == "[]" || s == "null"
}
