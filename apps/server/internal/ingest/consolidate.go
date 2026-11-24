package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

// Totals summarizes what Consolidate wrote to the DB, so the caller can
// broadcast the suites_available event and (for the seed script) return a
// useful diagnostic to the uploader.
type Totals struct {
	Suites      int
	Cases       int
	Passed      int
	Failed      int
	Skipped     int
	Flaky       int
	DurationMs  int64
	StartTime   *time.Time
	WallClockMs *int64
}

// Consolidate persists a batch of extracted suites to the database and rolls
// their counts up into the parent reports row. Idempotency: callers should
// invoke once per (report_id, uploaded batch of JSON files) — re-running
// against the same suites will duplicate rows, so delete first or guard with
// the report's json_upload_status.
//
// Attachments referenced by each test case are resolved against
// report_screenshots (the staging table UploadScreenshots writes to) by
// basename match; successful matches update report_screenshots.case_id so
// the per-spec screenshot lookup in SuiteSpecs is cheap.
//
// Test identities and failure signatures are resolved and committed first in a
// short dedicated READ COMMITTED transaction so shared `tests` row locks are
// released before inserting suites, test_cases, and screenshot links.
func Consolidate(
	ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID,
	suites []ExtractedSuite, reportStart, reportEnd *time.Time,
) (Totals, error) {
	screenshotsByBasename, err := loadScreenshotsByBasename(ctx, pool, reportID)
	if err != nil {
		return Totals{}, err
	}
	meta, err := loadReportMeta(ctx, pool, reportID)
	if err != nil {
		return Totals{}, err
	}

	caseInputs, suiteOffsets := buildCaseIdentityInputs(suites)
	identities, err := resolveCaseIdentitiesTx(ctx, pool, meta, caseInputs)
	if err != nil {
		return Totals{}, err
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Totals{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	totals := Totals{}
	earliestStartTime := reportStart
	latestEndTime := reportEnd

	// Track which staging screenshots we linked so we can UPDATE them in one
	// pass after the test_cases rows are in.
	linked := map[uuid.UUID]uuid.UUID{} // screenshotID → caseID

	for i, s := range suites {
		suitePassed, suiteFailed, suiteSkipped, suiteFlaky, suiteUnique := countStatuses(s.Cases)
		var suiteDuration int64
		for _, c := range s.Cases {
			suiteDuration += c.DurationMs
			if c.StartTime != nil {
				if earliestStartTime == nil || c.StartTime.Before(*earliestStartTime) {
					earliestStartTime = c.StartTime
				}
				end := c.StartTime.Add(time.Duration(c.DurationMs) * time.Millisecond)
				if latestEndTime == nil || end.After(*latestEndTime) {
					latestEndTime = &end
				}
			}
		}

		var suiteID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO suites (report_id, title, file, duration_ms,
			                    total_count, passed_count, failed_count, skipped_count, flaky_count,
			                    start_time, ordinal)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			RETURNING id
		`, reportID, sanitizeUTF8(s.Title), sanitizeUTF8Ptr(s.FilePath), suiteDuration,
			suiteUnique, suitePassed, suiteFailed, suiteSkipped, suiteFlaky,
			s.StartTime, i).Scan(&suiteID)
		if err != nil {
			return Totals{}, fmt.Errorf("insert suite %q: %w", s.Title, err)
		}

		offset := suiteOffsets[i]
		if err := insertSuiteCasesTx(
			ctx, tx, suiteID, s.Cases, identities[offset:offset+len(s.Cases)],
			screenshotsByBasename, linked,
		); err != nil {
			return Totals{}, err
		}

		totals.Suites++
		totals.Cases += suiteUnique
		totals.Passed += suitePassed
		totals.Failed += suiteFailed
		totals.Skipped += suiteSkipped
		totals.Flaky += suiteFlaky
		totals.DurationMs += suiteDuration
	}

	// Wall-clock spans the earliest start to the latest end across every test
	// case (or the report-level stats.start/end when present).
	var wallClockMs *int64
	if earliestStartTime != nil && latestEndTime != nil {
		ms := latestEndTime.Sub(*earliestStartTime).Milliseconds()
		if ms > 0 {
			wallClockMs = &ms
		}
	}
	totals.StartTime = earliestStartTime
	totals.WallClockMs = wallClockMs

	// Roll totals into the parent reports row so the home-page test_stats
	// aggregate (SUM(passed_cases) etc.) has real numbers. duration_ms is the
	// per-case-sum; wall_clock_ms is the framework-reported span. The web's
	// TestStats renders them separately.
	if _, err := tx.Exec(ctx, `
		UPDATE reports
		SET total_suites = $2, total_cases = $3,
		    passed_cases = $4, failed_cases = $5, skipped_cases = $6, flaky_cases = $7,
		    duration_ms = $8, wall_clock_ms = $9, start_time = $10, updated_at = now()
		WHERE id = $1
	`, reportID, totals.Suites, totals.Cases,
		totals.Passed, totals.Failed, totals.Skipped, totals.Flaky,
		totals.DurationMs, wallClockMs, earliestStartTime); err != nil {
		return Totals{}, fmt.Errorf("update reports counts: %w", err)
	}

	// Flush JSON-attachment ↔ screenshot links. Done inside the same tx so the
	// test_cases and their screenshot references commit together.
	for screenshotID, caseID := range linked {
		if _, err := tx.Exec(ctx,
			`UPDATE report_screenshots SET case_id = $1 WHERE id = $2 AND case_id IS NULL`,
			caseID, screenshotID); err != nil {
			return Totals{}, fmt.Errorf("link screenshot %s: %w", screenshotID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Totals{}, fmt.Errorf("commit: %w", err)
	}
	return totals, nil
}

type stagedScreenshot struct {
	id    uuid.UUID
	s3Key string
}

// loadScreenshotsByBasename indexes every report_screenshots row for the
// report by the file's basename (last path segment). Multiple files with the
// same basename (different subdirs) share the map entry — last-write-wins,
// which is fine because Playwright paths are unique across results.
func loadScreenshotsByBasename(ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID) (map[string]stagedScreenshot, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, filename, s3_key FROM report_screenshots WHERE report_id = $1`, reportID)
	if err != nil {
		return nil, fmt.Errorf("load staged screenshots: %w", err)
	}
	defer rows.Close()
	out := map[string]stagedScreenshot{}
	for rows.Next() {
		var id uuid.UUID
		var filename, s3Key string
		if err := rows.Scan(&id, &filename, &s3Key); err != nil {
			return nil, err
		}
		out[basename(filename)] = stagedScreenshot{id: id, s3Key: s3Key}
	}
	return out, rows.Err()
}

// basename returns the last segment of a / path or the whole string.
func basename(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// countStatuses collapses results for the same test (by full_title) down
// to one "unique" case and returns the per-suite
// passed/failed/skipped/flaky/unique counts.
//
// Why dedupe by full_title and not just by retry=0: Playwright's internal
// retries within ONE JSON file are retry=0,1,2,...; filtering by retry=0
// dedupes those correctly. But when a single shard report is built from
// MULTIPLE Playwright JSON files (e.g. an orchestration retest re-runs
// the same spec under the same gh_job_id), each file independently
// produces retry=0 entries for the same test, and the retry filter does
// not catch the duplicates. Grouping by full_title catches both cases.
//
// The collapsed status uses the LAST entry per title (the test's terminal
// outcome) and promotes to `flaky` when at least one earlier entry was a
// failure but the final result is `passed` — same semantics as
// Playwright's `test.outcome()`. A final entry whose Outcome the framework
// gave as `flaky` is flaky whatever its own status.
func countStatuses(cases []ExtractedCase) (passed, failed, skipped, flaky, unique int) {
	type group struct {
		statuses       []string
		frameworkFlaky bool
	}
	byTitle := make(map[string]*group, len(cases))
	order := make([]string, 0, len(cases))
	for _, c := range cases {
		fullTitle := sanitizeUTF8(c.FullTitle)
		status := sanitizeUTF8(c.Status)
		if strings.TrimSpace(fullTitle) == "" || status == "" {
			continue
		}
		g, ok := byTitle[fullTitle]
		if !ok {
			g = &group{}
			byTitle[fullTitle] = g
			order = append(order, fullTitle)
		}
		g.statuses = append(g.statuses, status)
		if sanitizeUTF8(c.Outcome) == StatusFlaky {
			g.frameworkFlaky = true
		}
	}
	for _, k := range order {
		g := byTitle[k]
		verdict := teststatus.TestVerdict(g.statuses, g.frameworkFlaky)
		if verdict == "" {
			continue
		}
		unique++
		switch verdict {
		case teststatus.Passed:
			passed++
		case teststatus.Failed:
			failed++
		case teststatus.Skipped:
			skipped++
		case teststatus.Flaky:
			flaky++
		}
	}
	return
}

// encodeAttachments returns the JSON blob we write to test_cases.attachments,
// or NULL when there are none.
func encodeAttachments(atts []ExtractedAttachment) any {
	if len(atts) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(atts))
	for _, a := range atts {
		m := map[string]any{
			"path":     sanitizeUTF8(a.Path),
			"retry":    a.Retry,
			"sequence": a.Sequence,
			"missing":  a.Missing,
		}
		if a.ContentType != nil {
			m["content_type"] = sanitizeUTF8(*a.ContentType)
		}
		if a.S3Key != nil {
			m["s3_key"] = sanitizeUTF8(*a.S3Key)
		}
		out = append(out, m)
	}
	b, _ := json.Marshal(out)
	return b
}

func encodeTags(tags []string) []byte {
	if len(tags) == 0 {
		return []byte("[]")
	}
	clean := make([]string, len(tags))
	for i, t := range tags {
		clean[i] = sanitizeUTF8(t)
	}
	return encodeJSONArray(clean)
}

func encodeAnnotations(anns []json.RawMessage) []byte {
	if len(anns) == 0 {
		return []byte("[]")
	}
	out := make([]any, 0, len(anns))
	for _, raw := range anns {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return []byte("[]")
		}
		var trailing any
		if err := dec.Decode(&trailing); err != io.EOF {
			return []byte("[]")
		}
		out = append(out, sanitizeJSONValue(v))
	}
	return encodeJSONArray(out)
}

func sanitizeJSONValue(v any) any {
	switch x := v.(type) {
	case string:
		return sanitizeUTF8(x)
	case []any:
		for i, elem := range x {
			x[i] = sanitizeJSONValue(elem)
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		var dirty []string
		for k, val := range x {
			if sanitizeUTF8(k) == k {
				out[k] = sanitizeJSONValue(val)
			} else {
				dirty = append(dirty, k)
			}
		}
		if len(dirty) > 0 {
			// Preserve already-clean keys and resolve dirty-key collisions deterministically.
			slices.Sort(dirty)
			for _, k := range dirty {
				cleanK := sanitizeUTF8(k)
				if _, exists := out[cleanK]; !exists {
					out[cleanK] = sanitizeJSONValue(x[k])
				}
			}
		}
		return out
	default:
		return v
	}
}

// encodeJSONArray returns a non-null JSON array blob for NOT NULL jsonb columns.
func encodeJSONArray[T any](items []T) []byte {
	if len(items) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(items)
	if err != nil {
		return []byte("[]")
	}
	return b
}

// resolveCaseIdentitiesTx commits test and signature upserts in a short
// dedicated READ COMMITTED transaction so shared `tests` row locks are not
// held across suite, test_case, and screenshot writes.
func resolveCaseIdentitiesTx(
	ctx context.Context, pool *pgxpool.Pool, meta IdentityMeta, caseInputs []CaseIdentityInput,
) ([]TestIdentity, error) {
	if len(caseInputs) == 0 {
		return nil, nil
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin identity tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	identities, err := ResolveCaseIdentities(ctx, tx, meta, caseInputs)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit identity tx: %w", err)
	}
	return identities, nil
}

func loadReportMeta(ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID) (IdentityMeta, error) {
	var meta IdentityMeta
	err := pool.QueryRow(ctx, `
		SELECT r.report_group_id, g.repository, g.framework, r.created_at
		FROM reports r
		JOIN report_groups g ON g.id = r.report_group_id
		WHERE r.id = $1
	`, reportID).Scan(&meta.ReportGroupID, &meta.Repository, &meta.Framework, &meta.SeenAt)
	if err != nil {
		return IdentityMeta{}, fmt.Errorf("fetch report metadata: %w", err)
	}
	return meta, nil
}

func buildCaseIdentityInputs(suites []ExtractedSuite) ([]CaseIdentityInput, []int) {
	total := 0
	for _, s := range suites {
		total += len(s.Cases)
	}
	inputs := make([]CaseIdentityInput, 0, total)
	offsets := make([]int, len(suites))
	for si, s := range suites {
		offsets[si] = len(inputs)
		file := derefString(s.FilePath)
		for _, c := range s.Cases {
			inputs = append(inputs, CaseIdentityInput{
				File:         file,
				Title:        c.Title,
				FullTitle:    c.FullTitle,
				Tags:         c.Tags,
				Status:       sanitizeUTF8(c.Status),
				ErrorMessage: derefString(c.ErrorMessage),
				ErrorStack:   derefString(c.ErrorStack),
				StartTime:    c.StartTime,
			})
		}
	}
	return inputs, offsets
}

func insertSuiteCasesTx(
	ctx context.Context, tx pgx.Tx, suiteID uuid.UUID, cases []ExtractedCase,
	identities []TestIdentity, screenshotsByBasename map[string]stagedScreenshot,
	linked map[uuid.UUID]uuid.UUID,
) error {
	if len(cases) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	perCaseLinks := make([][]uuid.UUID, len(cases))
	for ci, c := range cases {
		// Resolve each attachment path to an uploaded screenshot before serializing
		// and queue the report_screenshots→test_case link after the batch INSERT.
		resolved := make([]ExtractedAttachment, len(c.Attachments))
		for ai, a := range c.Attachments {
			resolved[ai] = a
			cleanPath := sanitizeUTF8(a.Path)
			if sh, ok := screenshotsByBasename[basename(cleanPath)]; ok {
				k := sh.s3Key
				resolved[ai].S3Key = &k
				resolved[ai].Missing = false
				perCaseLinks[ci] = append(perCaseLinks[ci], sh.id)
			} else {
				resolved[ai].Missing = true
			}
		}
		batch.Queue(`
			INSERT INTO test_cases (
				suite_id, title, full_title, status, retry_count, duration_ms,
				error_message, error_stack, project, tags, annotations, attachments,
				ordinal, test_id, signature_id
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			RETURNING id
		`, suiteID, sanitizeUTF8(c.Title), sanitizeUTF8(c.FullTitle), sanitizeUTF8(c.Status), c.RetryCount, c.DurationMs,
			sanitizeUTF8Ptr(c.ErrorMessage), sanitizeUTF8Ptr(c.ErrorStack), sanitizeUTF8Ptr(c.Project),
			encodeTags(c.Tags), encodeAnnotations(c.Annotations), encodeAttachments(resolved),
			c.Sequence, identities[ci].TestID, identities[ci].SignatureID)
	}
	br := tx.SendBatch(ctx, batch)
	for ci, c := range cases {
		var caseID uuid.UUID
		if err := br.QueryRow().Scan(&caseID); err != nil {
			_ = br.Close()
			return fmt.Errorf("insert test_case %q: %w", c.Title, err)
		}
		for _, sid := range perCaseLinks[ci] {
			linked[sid] = caseID
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("close test_cases batch: %w", err)
	}
	return nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
