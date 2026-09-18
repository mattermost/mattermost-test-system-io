package reports

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

const (
	historyMaxFiles       = 50
	historyMaxWindow      = 30 * 24 * time.Hour
	historyDefaultPerPage = 50
	historyMaxPerPage     = 2000
	historyErrorChars     = 400
)

// historyRequest names the spec files whose past executions the caller wants.
//
// It is keyed on the file rather than on individual test titles on purpose. A
// title is edited far more often than the file it lives in — a reworded test is
// still the same test — and a caller keyed on the title would silently lose all
// of a test's history the moment somebody fixed a typo in it. The server
// therefore returns every execution recorded against the named files and leaves
// the caller to decide which rows describe the same test, on whatever rule it
// considers correct. That keeps this endpoint a plain read over the report
// tables, with no opinion about how the data will be interpreted.
type historyRequest struct {
	Repository string   `json:"repository"`
	Files      []string `json:"files"`
	Branch     string   `json:"branch"`
	Since      string   `json:"since"`
	Until      string   `json:"until"`
	Page       int      `json:"page"`
	PerPage    int      `json:"per_page"`
}

// historyObservation is one execution of one test in one completed report group.
type historyObservation struct {
	File         string    `json:"file"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	RetryCount   int       `json:"retry_count"`
	GroupID      uuid.UUID `json:"group_id"`
	Name         string    `json:"name"`
	Branch       string    `json:"branch"`
	GHPRNumber   *int      `json:"gh_pr_number"`
	CommitSHA    string    `json:"commit_sha"`
	CreatedAt    time.Time `json:"created_at"`
	ErrorExcerpt string    `json:"error_excerpt"`
}

// History serves POST /api/v1/reports/history: every execution recorded against
// the named spec files, in completed report groups of one repository inside a
// time window, newest first and paged.
//
// It is the read that CI triage needs to tell "this test fails on trunk / on
// other PRs too" from "this test fails only here". It reads the ordinary report
// tables, so it needs no extra bookkeeping at ingest, and it returns rows rather
// than verdicts, so the policy for reading them can change without a server
// deploy.
func (h *Handlers) History(w http.ResponseWriter, r *http.Request) {
	var req historyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if req.Repository == "" || len(req.Files) == 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "repository and at least one file are required")
		return
	}
	// The filter below is a set membership test, so a repeated file costs
	// nothing and cannot duplicate rows. Fold repeats away instead of making
	// the caller deduplicate a list it may have built from several failures in
	// the same spec.
	// Bound the array the caller actually sent, which is what the schema's
	// maxItems describes; deduplicating first would silently accept a longer one.
	if len(req.Files) > historyMaxFiles {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", fmt.Sprintf("at most %d files per request", historyMaxFiles))
		return
	}
	files := make([]string, 0, len(req.Files))
	seen := make(map[string]bool, len(req.Files))
	for _, f := range req.Files {
		if f == "" {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "files must not contain an empty path")
			return
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	page := req.Page
	if page == 0 {
		page = 1
	}
	if page < 1 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "page must be 1 or greater")
		return
	}
	perPage := req.PerPage
	if perPage == 0 {
		perPage = historyDefaultPerPage
	}
	if perPage < 1 || perPage > historyMaxPerPage {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", fmt.Sprintf("per_page must be between 1 and %d", historyMaxPerPage))
		return
	}
	until := time.Now().UTC()
	if req.Until != "" {
		t, err := time.Parse(time.RFC3339, req.Until)
		if err != nil {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "until must be RFC3339")
			return
		}
		until = t
	}
	since := until.Add(-14 * 24 * time.Hour)
	if req.Since != "" {
		t, err := time.Parse(time.RFC3339, req.Since)
		if err != nil {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "since must be RFC3339")
			return
		}
		since = t
	}
	// OFFSET needs the same result set on every page. Defaulting `until` to now
	// would move the window between requests, so a group completing mid-walk
	// could shift rows across a page boundary and the caller would see a row
	// twice or not at all. Later pages must therefore name the window the first
	// page reported.
	if page > 1 && req.Until == "" {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "until is required when page > 1: pass the value the first page returned so every page sees the same window")
		return
	}
	if !since.Before(until) || until.Sub(since) > historyMaxWindow {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "window must be positive and at most 30 days")
		return
	}
	// The ORDER BY is a total order — group id, suite id and case id break every
	// tie — so a row cannot shift between pages while the caller walks them.
	// One row past the page is requested only to answer has_more without a
	// second count query over the same four-table join.
	rows, err := h.Pool.Query(r.Context(), `
		SELECT s.file, c.title, c.status, c.retry_count, g.id, g.name, g.branch, g.gh_pr_number, g.commit_sha, g.created_at,
		       left(COALESCE(c.error_message, ''), $4)
		FROM report_groups g
		JOIN reports r ON r.report_group_id = g.id
		JOIN suites s ON s.report_id = r.id
		JOIN test_cases c ON c.suite_id = s.id
		WHERE g.repository = $1
		  AND g.status = 'completed'
		  AND g.created_at >= $2
		  AND g.created_at < $3
		  AND s.file = ANY($5::text[])
		  AND ($6 = '' OR g.branch = $6)
		ORDER BY g.created_at DESC, g.id, s.id, c.ordinal, c.id
		LIMIT $7 OFFSET $8
	`, req.Repository, since, until, historyErrorChars, files, req.Branch, perPage+1, (page-1)*perPage)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]historyObservation, 0)
	for rows.Next() {
		var o historyObservation
		if err := rows.Scan(&o.File, &o.Title, &o.Status, &o.RetryCount, &o.GroupID, &o.Name, &o.Branch, &o.GHPRNumber, &o.CommitSHA, &o.CreatedAt, &o.ErrorExcerpt); err != nil {
			api.WriteError(w, r, err)
			return
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		api.WriteError(w, r, err)
		return
	}
	hasMore := len(out) > perPage
	if hasMore {
		out = out[:perPage]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"since":        since,
		"until":        until,
		"page":         page,
		"per_page":     perPage,
		"has_more":     hasMore,
		"observations": out,
	})
}
