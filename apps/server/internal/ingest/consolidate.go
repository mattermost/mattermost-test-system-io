package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testreport"
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
// The write is transactional so partial failures don't leave orphan rows.
func Consolidate(
	ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID,
	suites []ExtractedSuite, reportStart, reportEnd *time.Time,
) (Totals, error) {
	screenshotsByBasename, err := loadScreenshotsByBasename(ctx, pool, reportID)
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
		`, reportID, s.Title, s.FilePath, suiteDuration,
			suiteUnique, suitePassed, suiteFailed, suiteSkipped, suiteFlaky,
			s.StartTime, i).Scan(&suiteID)
		if err != nil {
			return Totals{}, fmt.Errorf("insert suite %q: %w", s.Title, err)
		}

		for _, c := range s.Cases {
			// Resolve each attachment path to an uploaded screenshot (if any)
			// before serializing. This populates s3_key/missing on the
			// attachment JSON and queues the report_screenshots→test_case
			// link that happens after the INSERT.
			resolved := make([]ExtractedAttachment, len(c.Attachments))
			var perCaseLinks []uuid.UUID
			for i, a := range c.Attachments {
				resolved[i] = a
				if sh, ok := screenshotsByBasename[basename(a.Path)]; ok {
					k := sh.s3Key
					resolved[i].S3Key = &k
					resolved[i].Missing = false
					perCaseLinks = append(perCaseLinks, sh.id)
				} else {
					resolved[i].Missing = true
				}
			}
			attachmentsJSON := encodeAttachments(resolved)
			var caseID uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO test_cases (suite_id, title, full_title, status, retry_count, duration_ms,
				                        error_message, error_stack, attachments, ordinal, external_test_id, file)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
				RETURNING id
			`, suiteID, c.Title, c.FullTitle, c.Status, c.RetryCount, c.DurationMs,
				c.ErrorMessage, c.ErrorStack, attachmentsJSON, c.Sequence,
				// The MM-T id is written at ingest, not only by migration 27's
				// one-time backfill. Without this every row after the migration
				// carried a NULL id and the 30-day attribution window emptied
				// within a month of deploy — silently, for every repository.
				testreport.ExternalTestID(c.Title, c.FullTitle),
				// Denormalized from the suite (s.FilePath, in scope from the
				// outer loop) so stable_key's generated expression — which
				// cannot reach across to suites.file — can disambiguate two
				// same-titled tests in different files.
				s.FilePath).Scan(&caseID); err != nil {
				return Totals{}, fmt.Errorf("insert test_case %q: %w", c.Title, err)
			}
			for _, sid := range perCaseLinks {
				linked[sid] = caseID
			}
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
// Playwright's `test.outcome()`.
func countStatuses(cases []ExtractedCase) (passed, failed, skipped, flaky, unique int) {
	type group struct {
		final      ExtractedCase
		everFailed bool
	}
	byTitle := make(map[string]*group, len(cases))
	order := make([]string, 0, len(cases))
	for _, c := range cases {
		isFailure := c.Status == StatusFailed || c.Status == StatusTimedOut
		g, ok := byTitle[c.FullTitle]
		if !ok {
			g = &group{final: c, everFailed: isFailure}
			byTitle[c.FullTitle] = g
			order = append(order, c.FullTitle)
			continue
		}
		g.final = c
		g.everFailed = g.everFailed || isFailure
	}
	for _, k := range order {
		g := byTitle[k]
		unique++
		switch {
		case g.final.Status == StatusFlaky:
			flaky++
		case g.final.Status == StatusPassed && g.everFailed:
			flaky++
		case g.final.Status == StatusPassed:
			passed++
		case g.final.Status == StatusFailed, g.final.Status == StatusTimedOut:
			failed++
		case g.final.Status == StatusSkipped:
			skipped++
		default:
			passed++
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
			"path":     a.Path,
			"retry":    a.Retry,
			"sequence": a.Sequence,
			"missing":  a.Missing,
		}
		if a.ContentType != nil {
			m["content_type"] = *a.ContentType
		}
		if a.S3Key != nil {
			m["s3_key"] = *a.S3Key
		}
		out = append(out, m)
	}
	b, _ := json.Marshal(out)
	return b
}
