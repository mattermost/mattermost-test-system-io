package reports

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

const (
	historyMaxTests   = 50
	historyMaxWindow  = 30 * 24 * time.Hour
	historyMaxRows    = 20000
	historyErrorChars = 400
)

// historyRequest names the tests whose past executions the caller wants.
type historyRequest struct {
	Repository string `json:"repository"`
	Since      string `json:"since"`
	Until      string `json:"until"`
	Tests      []struct {
		File  string `json:"file"`
		Title string `json:"title"`
	} `json:"tests"`
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

// History serves POST /api/v1/reports/history: every execution of the named
// tests in completed report groups of one repository inside a time window,
// newest first. It is the read that CI triage needs to tell "this test fails
// on trunk / on other PRs" from "this test fails only here", and it reads the
// ordinary report tables, so it needs no extra bookkeeping at ingest.
func (h *Handlers) History(w http.ResponseWriter, r *http.Request) {
	var req historyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if req.Repository == "" || len(req.Tests) == 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "repository and at least one test are required")
		return
	}
	if len(req.Tests) > historyMaxTests {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "at most 50 tests per request")
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
	if !since.Before(until) || until.Sub(since) > historyMaxWindow {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "window must be positive and at most 30 days")
		return
	}
	files := make([]string, 0, len(req.Tests))
	titles := make([]string, 0, len(req.Tests))
	seen := make(map[[2]string]bool, len(req.Tests))
	for _, t := range req.Tests {
		if t.File == "" || t.Title == "" {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "each test needs file and title")
			return
		}
		// A repeated pair would join every matching row twice.
		if seen[[2]string{t.File, t.Title}] {
			api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "duplicate test: "+t.File+" / "+t.Title)
			return
		}
		seen[[2]string{t.File, t.Title}] = true
		files = append(files, t.File)
		titles = append(titles, t.Title)
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT s.file, c.title, c.status, c.retry_count, g.id, g.name, g.branch, g.gh_pr_number, g.commit_sha, g.created_at,
		       left(COALESCE(c.error_message, ''), $6)
		FROM report_groups g
		JOIN reports r ON r.report_group_id = g.id
		JOIN suites s ON s.report_id = r.id
		JOIN test_cases c ON c.suite_id = s.id
		JOIN unnest($4::text[], $5::text[]) AS t(file, title) ON t.file = s.file AND t.title = c.title
		WHERE g.repository = $1 AND g.status = 'completed' AND g.created_at >= $2 AND g.created_at < $3
		ORDER BY g.created_at DESC, g.id, s.ordinal, c.ordinal
		LIMIT $7
	`, req.Repository, since, until, files, titles, historyErrorChars, historyMaxRows+1)
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
	// One row past the cap was requested only to know whether the cap cut anything.
	truncated := len(out) > historyMaxRows
	if truncated {
		out = out[:historyMaxRows]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"since":        since,
		"until":        until,
		"truncated":    truncated,
		"observations": out,
	})
}
