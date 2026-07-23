package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LinkScreenshots matches still-unlinked report_screenshots rows to their
// test_cases. It resolves the two common orderings:
//
//  1. **JSON-first** (attachments stored, screenshots arrive later): match
//     each staged screenshot's basename against the attachment paths
//     persisted in test_cases.attachments. Also rewrites each matched
//     attachment's s3_key in place so the detail view doesn't need a join.
//  2. **Screenshots-first** (Cypress-style path matching): fall back to
//     matching test_name (derived from folder path) against
//     test_cases.full_title — exact, "/" → " > " normalization, and
//     Playwright project-suffix tolerance.
//
// Idempotent — safe to invoke from both UploadJSON and UploadScreenshots.
// Returns the number of screenshots newly linked.
func LinkScreenshots(ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID) (int, error) {
	shots, err := loadUnlinkedScreenshots(ctx, pool, reportID)
	if err != nil {
		return 0, err
	}
	if len(shots) == 0 {
		return 0, nil
	}

	cases, err := loadTestCases(ctx, pool, reportID)
	if err != nil {
		return 0, err
	}
	if len(cases) == 0 {
		return 0, nil
	}

	linked := 0

	// ── Pass 1: basename match against test_cases.attachments[].path ──
	// This is the Playwright path — the JSON already records the attachment
	// path, and the uploader streams files with matching basenames.
	byBasename := map[string][]uuid.UUID{}
	for _, s := range shots {
		byBasename[basename(s.filename)] = append(byBasename[basename(s.filename)], s.id)
	}

	for _, c := range cases {
		if len(c.attachments) == 0 {
			continue
		}
		var attachments []map[string]any
		if err := json.Unmarshal(c.attachments, &attachments); err != nil {
			continue
		}
		changed := false
		for i, att := range attachments {
			p, ok := att["path"].(string)
			if !ok {
				continue
			}
			bn := basename(p)
			ids, ok := byBasename[bn]
			if !ok || len(ids) == 0 {
				continue
			}
			shotID := ids[0]
			// Pop the id so the same screenshot isn't double-claimed when two
			// attachments happen to share a basename.
			byBasename[bn] = ids[1:]
			// Find the s3_key for that id.
			var s3Key string
			for _, s := range shots {
				if s.id == shotID {
					s3Key = s.s3Key
					break
				}
			}
			if _, err := pool.Exec(ctx,
				`UPDATE report_screenshots SET case_id = $1 WHERE id = $2 AND case_id IS NULL`,
				c.id, shotID); err != nil {
				return linked, fmt.Errorf("link screenshot %s: %w", shotID, err)
			}
			attachments[i]["s3_key"] = s3Key
			attachments[i]["missing"] = false
			changed = true
			linked++
		}
		if changed {
			b, _ := json.Marshal(attachments)
			if _, err := pool.Exec(ctx,
				`UPDATE test_cases SET attachments = $1 WHERE id = $2`, b, c.id); err != nil {
				return linked, fmt.Errorf("update test_case attachments %s: %w", c.id, err)
			}
		}
	}

	// ── Pass 2: path-based fallback (Cypress "Suite/Test.png" style) ──
	// Anything still unlinked falls through to folder-name matching against
	// full_title with the normalization rules documented at the top.
	remaining, err := loadUnlinkedScreenshots(ctx, pool, reportID)
	if err != nil {
		return linked, err
	}
	for _, s := range remaining {
		candidates := candidateTestNames(s.testName)
		matched := false
		for _, c := range cases {
			for _, cand := range candidates {
				if c.fullTitle == cand || strings.HasPrefix(c.fullTitle, cand+" [") {
					if _, err := pool.Exec(ctx,
						`UPDATE report_screenshots SET case_id = $1 WHERE id = $2 AND case_id IS NULL`,
						c.id, s.id); err != nil {
						return linked, fmt.Errorf("link screenshot %s: %w", s.id, err)
					}
					linked++
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
	}
	return linked, nil
}

// candidateTestNames produces the alternate forms the screenshot's derived
// test_name may take in test_cases.full_title. Covers:
//   - exact match (Playwright "Suite > Test")
//   - "/" → " > " normalization (older Cypress folder-style paths)
//   - Cypress Mochawesome format: strip leading spec-file segment(s) and
//     replace " -- " separators with spaces. Cypress writes screenshots as
//     "<spec-file>/<Suite> -- <Test> (failed).png"; Mochawesome's fullTitle
//     concatenates describe/it titles with a single space, so the two only
//     align after both transforms.
func candidateTestNames(testName string) []string {
	out := []string{testName, strings.ReplaceAll(testName, "/", " > ")}
	if i := strings.LastIndex(testName, "/"); i >= 0 {
		tail := testName[i+1:]
		if tail != "" {
			out = append(out, tail)
			if strings.Contains(tail, " -- ") {
				out = append(out, strings.ReplaceAll(tail, " -- ", " "))
			}
		}
	} else if strings.Contains(testName, " -- ") {
		out = append(out, strings.ReplaceAll(testName, " -- ", " "))
	}
	return out
}

type stagedShot struct {
	id       uuid.UUID
	filename string
	s3Key    string
	testName string
}

type loadedCase struct {
	id          uuid.UUID
	fullTitle   string
	attachments []byte
}

func loadUnlinkedScreenshots(ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID) ([]stagedShot, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, filename, s3_key, test_name
		FROM report_screenshots
		WHERE report_id = $1 AND case_id IS NULL
	`, reportID)
	if err != nil {
		return nil, fmt.Errorf("select unlinked screenshots: %w", err)
	}
	defer rows.Close()
	var out []stagedShot
	for rows.Next() {
		var s stagedShot
		if err := rows.Scan(&s.id, &s.filename, &s.s3Key, &s.testName); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func loadTestCases(ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID) ([]loadedCase, error) {
	rows, err := pool.Query(ctx, `
		SELECT tc.id, tc.full_title, tc.attachments
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		WHERE s.report_id = $1
	`, reportID)
	if err != nil {
		return nil, fmt.Errorf("select test_cases for report: %w", err)
	}
	defer rows.Close()
	var out []loadedCase
	for rows.Next() {
		var c loadedCase
		if err := rows.Scan(&c.id, &c.fullTitle, &c.attachments); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeriveTestNameFromPath pulls the folder-level identity out of a screenshot
// filepath the way Cypress/Playwright/Detox emit them. Expected shapes:
//
//	"<spec-file>/<Suite> -- <Test> (failed).png"         Cypress
//	"<Suite>/<Test>-<retry-index>.png"                   Cypress (older)
//	"<Suite-chain-joined-by-">>>">/<leaf>.png"           Playwright
//	"<session>/<fullName>/testFnFailure.png"             Detox
//	"<session>/<fullName>/DETOX_VISIBILITY_…__SCREEN.png" Detox
//
// We strip the extension + any trailing "-N" / " (failed)" markers and
// return the result; the screenshot linker then normalizes "/" to " > " and
// matches against test_cases.full_title. The path-parsing is intentionally
// lenient — a match that fails here still lets the file stay in S3 for
// later manual linking.
//
// Detox puts the Jest fullName in the parent folder and uses fixed leaf
// names (testStart / testFnFailure / testDone / DETOX_VISIBILITY_*). For
// those leaves we return the parent folder basename so LinkScreenshots can
// exact-match full_title.
func DeriveTestNameFromPath(relativePath string) string {
	p := relativePath
	if i := strings.LastIndex(p, "."); i >= 0 {
		p = p[:i]
	}
	for _, suffix := range []string{" (failed)", " (attempted)"} {
		p = strings.TrimSuffix(p, suffix)
	}
	if i := strings.LastIndex(p, "-"); i > 0 {
		tail := p[i+1:]
		if isAllDigits(tail) {
			p = p[:i]
		}
	}

	leaf := basename(p)
	if isDetoxArtifactLeaf(leaf) {
		if i := strings.LastIndex(p, "/"); i >= 0 {
			parent := p[:i]
			if j := strings.LastIndex(parent, "/"); j >= 0 {
				return parent[j+1:]
			}
			return parent
		}
	}
	return p
}

func isDetoxArtifactLeaf(name string) bool {
	switch name {
	case "testStart", "testFnFailure", "testDone",
		"beforeAllFailure", "afterAllFailure",
		"beforeEachFailure", "afterEachFailure":
		return true
	}
	return strings.HasPrefix(name, "DETOX_VISIBILITY_")
}

// DeriveScreenshotType labels Detox's three well-known screenshot kinds so
// the web's test detail page can order them correctly.
func DeriveScreenshotType(relativePath string) string {
	base := basename(relativePath)
	switch {
	case strings.Contains(base, "testStart"):
		return "testStart"
	case strings.Contains(base, "testFnFailure"):
		return "testFnFailure"
	case strings.Contains(base, "testDone"):
		return "testDone"
	default:
		return ""
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
