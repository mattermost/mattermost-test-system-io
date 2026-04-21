package ingest

import (
	"encoding/json"
	"strings"
	"time"
)

// Extract dispatches to the framework-specific parser and returns the parsed
// suites plus the report-level start/end times when the framework includes
// them (Cypress mochawesome reports wall-clock; Playwright/Detox only
// per-test). seq is a running counter the caller keeps across all JSON files
// in a single upload so case ordinals stay globally monotonic.
func Extract(framework string, body []byte, seq *int) (suites []ExtractedSuite, reportStart, reportEnd *time.Time) {
	framework = strings.ToLower(strings.TrimSpace(framework))
	switch framework {
	case "playwright":
		suites = extractPlaywright(body, seq)
	case "cypress":
		suites = extractCypress(body, seq)
	case "detox":
		suites = extractDetox(body, seq)
	default:
		suites = autoDetect(body, seq)
	}
	reportStart, reportEnd = parseReportStats(body)
	return
}

// autoDetect runs structural sniffing across the three supported frameworks,
// returning the first extractor whose parse yields at least one suite.
// Defensive fallback when the framework column is mis-set.
func autoDetect(body []byte, seq *int) []ExtractedSuite {
	for _, f := range []func([]byte, *int) []ExtractedSuite{extractPlaywright, extractCypress, extractDetox} {
		scratch := *seq
		if out := f(body, &scratch); len(out) > 0 {
			*seq = scratch
			return out
		}
	}
	return nil
}

// parseReportStats extracts Cypress-style `stats.start`/`stats.end` (or
// Playwright's future equivalent) from the raw JSON without re-parsing the
// whole document. Returns (nil, nil) when absent.
func parseReportStats(body []byte) (*time.Time, *time.Time) {
	var v struct {
		Stats struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, nil
	}
	parse := func(s string) *time.Time {
		if s == "" {
			return nil
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			u := t.UTC()
			return &u
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			u := t.UTC()
			return &u
		}
		return nil
	}
	return parse(v.Stats.Start), parse(v.Stats.End)
}
