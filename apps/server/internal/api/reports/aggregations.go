package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// Test-case status strings — mirror the CHECK constraint on test_cases.status
// and the internal/ingest status constants. Duplicated at this layer to avoid
// an API package → ingest package dependency.
const (
	statusPassed   = "passed"
	statusFailed   = "failed"
	statusSkipped  = "skipped"
	statusFlaky    = "flaky"
	statusTimedOut = "timedOut"
)

// ---------- web-shape DTOs ----------

type testStats struct {
	Total      int    `json:"total"`
	Passed     int    `json:"passed"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	Flaky      int    `json:"flaky"`
	DurationMs *int64 `json:"duration_ms,omitempty"`
	// wall_clock_ms covers the numbered-shard batch (they run in parallel).
	// retest_wall_clock_ms covers the retest shards alone — they typically
	// start after the numbered batch finishes, so summing them into one
	// span would overstate the real CI wall-clock.
	WallClockMs       *int64 `json:"wall_clock_ms,omitempty"`
	RetestWallClockMs *int64 `json:"retest_wall_clock_ms,omitempty"`
}

// orchestrationCounts is the wire-shape projection of the materialized
// dispatch-unit counters on orchestration_runs. Field names mirror the
// orchestration RunCounts payload so the frontend can reuse the same
// rendering as the OrchestrationTab's CountsRow.
type orchestrationCounts struct {
	Pending          int `json:"pending"`
	Leased           int `json:"leased"`
	CompletedPass    int `json:"completed_pass"`
	CompletedFail    int `json:"completed_fail"`
	CompletedSkipped int `json:"completed_skipped"`
	Abandoned        int `json:"abandoned"`
	RetestEligible   int `json:"retest_eligible"`
}

// orchestrationTestCounts is the test-case-level rollup derived by
// walking every attempt's `test_cases` JSONB array for a given
// orchestration_runs row. Counts use the same any-passed-AND-any-failed
// → flaky rule the OrchestrationTab applies client-side, so listing
// rows can show test-level numbers (rather than spec-file counts) while
// shard reports are still uploading.
type orchestrationTestCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Flaky   int `json:"flaky"`
	Skipped int `json:"skipped"`
	Total   int `json:"total"`
}

// orchestrationSummary surfaces the live orchestration_runs status and
// counts alongside the existing canonical test_stats on each report-index
// row. Emitted only when an orchestration_run row matches the report_group's
// composite identity; when omitted, the UI falls back to canonical stats
// alone.
type orchestrationSummary struct {
	Status     string              `json:"status"`
	TotalUnits int                 `json:"total_units"`
	Counts     orchestrationCounts `json:"counts"`
	// Tests is omitted while no attempts have reported test_cases yet
	// (e.g. all units still pending). When present, it carries the
	// rolling test-case rollup so listing rows can quote test-level
	// numbers during in-flight runs.
	Tests *orchestrationTestCounts `json:"tests,omitempty"`
}

type reportSummary struct {
	ID            string                `json:"id"`
	ShortID       string                `json:"short_id"`
	Name          string                `json:"name"`
	Status        string                `json:"status"`
	Framework     string                `json:"framework"`
	TestStats     *testStats            `json:"test_stats,omitempty"`
	Orchestration *orchestrationSummary `json:"orchestration,omitempty"`
	Repository    string                `json:"repository"`
	Branch        string                `json:"branch"`
	Commit        string                `json:"commit"`
	GHRunID       string                `json:"gh_run_id"`
	GHPRNumber    *int                  `json:"gh_pr_number,omitempty"`
	GHRunAttempt  string                `json:"gh_run_attempt"`
	CreatedAt     string                `json:"created_at"`
}

type reportEntry struct {
	ID          string  `json:"id"`
	ShortID     string  `json:"short_id"`
	GHJobID     string  `json:"gh_job_id,omitempty"`
	GHJobName   string  `json:"gh_job_name,omitempty"`
	DisplayName string  `json:"display_name"`
	Status      string  `json:"status"`
	CreatedAt   *string `json:"created_at,omitempty"`
	UpdatedAt   *string `json:"updated_at,omitempty"`
}

type reportDetail struct {
	reportSummary
	UpdatedAt    string        `json:"updated_at"`
	Reports      []reportEntry `json:"reports"`
	ErrorMessage *string       `json:"error_message,omitempty"`
}

type runEntry struct {
	ReportID      string                `json:"report_id"`
	Framework     string                `json:"framework"`
	Name          string                `json:"name"`
	Status        string                `json:"status"`
	Branch        string                `json:"branch"`
	Commit        string                `json:"commit"`
	ShortSHA      string                `json:"short_sha"`
	RunNumber     string                `json:"run_number,omitempty"`
	GHRunAttempt  string                `json:"gh_run_attempt"`
	GHRunID       string                `json:"gh_run_id,omitempty"`
	GHPRNumber    *int                  `json:"gh_pr_number,omitempty"`
	TestStats     *testStats            `json:"test_stats,omitempty"`
	Orchestration *orchestrationSummary `json:"orchestration,omitempty"`
	CreatedAt     string                `json:"created_at"`
	URLPath       string                `json:"url_path"`
}

type repoGroup struct {
	Repository     string     `json:"repository"`
	RepositoryName string     `json:"repository_name"`
	LatestRunAt    string     `json:"latest_run_at"`
	Runs           []runEntry `json:"runs"`
}

type individualReportSummary struct {
	ID      string `json:"id"`
	ShortID string `json:"short_id"`
	// ReportGroupID is the parent group; GroupName is the report group's
	// name (e.g. "playwright-orchestrated-test") — i.e. the matrix-target
	// name shared across every worker in the run. The UI renders this as
	// the row label, then `/ gh_job_name` as the per-shard worker tag.
	ReportGroupID string                `json:"report_group_id"`
	GroupName     string                `json:"group_name"`
	Name          string                `json:"name"`
	Status        string                `json:"status"`
	GHJobID       string                `json:"gh_job_id,omitempty"`
	GHJobName     string                `json:"gh_job_name,omitempty"`
	Repository    string                `json:"repository"`
	Branch        string                `json:"branch"`
	Commit        string                `json:"commit"`
	TestStats     *testStats            `json:"test_stats,omitempty"`
	Orchestration *orchestrationSummary `json:"orchestration,omitempty"`
	CreatedAt     string                `json:"created_at"`
}

// ---------- shape mappers ----------

func fmtTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// shortID returns the 13-char prefix of a UUIDv7 — the full 48-bit
// millisecond timestamp plus the leading hyphen and a nibble of rand_a.
// Unique across same-millisecond writes; sorts chronologically.
func shortID(s string) string {
	if len(s) > 13 {
		return s[:13]
	}
	return s
}

// shortSHA returns the 7-char prefix of a git commit SHA — matches the
// git/GitHub convention used across the UI and CLI.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func statsFromAgg(s groupStats) *testStats {
	return &testStats{
		Total:             s.TotalCases,
		Passed:            s.Passed,
		Failed:            s.Failed,
		Skipped:           s.Skipped,
		Flaky:             s.Flaky,
		DurationMs:        s.DurationMs,
		WallClockMs:       s.NumberedWallClockMs,
		RetestWallClockMs: s.RetestWallClockMs,
	}
}

func toReportSummary(g groupDTO, s groupStats) reportSummary {
	return reportSummary{
		ID:           g.ID.String(),
		ShortID:      shortID(g.ID.String()),
		Name:         g.Name,
		Status:       g.Status,
		Framework:    g.Framework,
		TestStats:    statsFromAgg(s),
		Repository:   g.Repository,
		Branch:       g.Branch,
		Commit:       g.CommitSHA,
		GHRunID:      g.GHRunID,
		GHPRNumber:   g.GHPRNumber,
		GHRunAttempt: g.GHRunAttempt,
		CreatedAt:    fmtTime(g.CreatedAt),
	}
}

func toReportDetail(g groupDTO, entries []reportEntryDTO, s groupStats) reportDetail {
	summary := toReportSummary(g, s)
	out := reportDetail{
		reportSummary: summary,
		UpdatedAt:     fmtTime(g.UpdatedAt),
		Reports:       make([]reportEntry, 0, len(entries)),
	}
	for _, e := range entries {
		created := fmtTime(e.CreatedAt)
		updated := fmtTime(e.UpdatedAt)
		out.Reports = append(out.Reports, reportEntry{
			ID:          e.ID.String(),
			ShortID:     shortID(e.ID.String()),
			GHJobID:     derefOrEmpty(e.GHJobID),
			GHJobName:   derefOrEmpty(e.GHJobName),
			DisplayName: firstNonEmptyStr(derefOrEmpty(e.GHJobName), e.Name),
			Status:      e.Status,
			CreatedAt:   &created,
			UpdatedAt:   &updated,
		})
	}
	return out
}

func toRunEntry(g groupDTO, s groupStats) runEntry {
	branch := stripRefPrefix(g.Branch)
	return runEntry{
		ReportID:     g.ID.String(),
		Framework:    g.Framework,
		Name:         g.Name,
		Status:       g.Status,
		Branch:       branch,
		Commit:       g.CommitSHA,           // full 40-char SHA, used for filtering
		ShortSHA:     shortSHA(g.CommitSHA), // 7-char prefix, display only
		GHRunAttempt: g.GHRunAttempt,
		GHRunID:      g.GHRunID,
		GHPRNumber:   g.GHPRNumber,
		TestStats:    statsFromAgg(s),
		CreatedAt:    fmtTime(g.CreatedAt),
		// Consolidated-view URL shape: {repo-slug}/{branch}/{short-sha}/{name}.
		// Each segment is URL-encoded so names containing spaces or slashes
		// stay as one path segment. Short SHA keeps the URL compact; any
		// prefix length resolves because the filter matches against the
		// full commit.
		URLPath: "/reports/" +
			url.PathEscape(repositoryDisplayName(g.Repository)) + "/" +
			url.PathEscape(branch) + "/" +
			url.PathEscape(shortSHA(g.CommitSHA)) + "/" +
			url.PathEscape(g.Name),
	}
}

// stripRefPrefix turns refs/heads/main → main, refs/tags/v1 → v1, and leaves
// bare names untouched. Matches how the web renders branch labels.
func stripRefPrefix(b string) string {
	for _, p := range []string{"refs/heads/", "refs/tags/"} {
		if strings.HasPrefix(b, p) {
			return b[len(p):]
		}
	}
	return b
}

func toIndividualSummary(g groupDTO, e reportEntryDTO, stats groupStats) individualReportSummary {
	return individualReportSummary{
		ID:            e.ID.String(),
		ShortID:       shortID(e.ID.String()),
		ReportGroupID: g.ID.String(),
		GroupName:     g.Name,
		Name:          firstNonEmptyStr(derefOrEmpty(e.GHJobName), e.Name),
		Status:        e.Status,
		GHJobID:       derefOrEmpty(e.GHJobID),
		GHJobName:     derefOrEmpty(e.GHJobName),
		Repository:    g.Repository,
		Branch:        g.Branch,
		Commit:        g.CommitSHA,
		TestStats:     statsFromAgg(stats),
		CreatedAt:     fmtTime(e.CreatedAt),
	}
}

// ---------- aggregation handlers ----------

// Grouped serves GET /api/v1/reports/grouped. Groups by repository; within a
// repo, runs are returned newest-first (by group created_at). The repo order
// is also newest-first so the dashboard's "recent activity" panel is stable.
func (h *Handlers) Grouped(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(), `
		SELECT `+reportGroupSelectCols+` FROM report_groups ORDER BY created_at DESC
	`)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()

	orchLookup := newOrchestrationLookup()
	byRepo := map[string]*repoGroup{}
	order := []string{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			api.WriteError(w, r, err)
			return
		}
		stats, err := aggregateGroupStats(r.Context(), h.Pool, g.ID)
		if err != nil {
			api.WriteError(w, r, err)
			return
		}
		orch, err := orchLookup.getForGroup(r.Context(), h.Pool, g)
		if err != nil {
			api.WriteError(w, r, err)
			return
		}
		key := g.Repository
		if key == "" {
			key = "unknown"
		}
		bucket, ok := byRepo[key]
		if !ok {
			bucket = &repoGroup{
				Repository:     key,
				RepositoryName: repositoryDisplayName(key),
				LatestRunAt:    fmtTime(g.CreatedAt),
				Runs:           []runEntry{},
			}
			byRepo[key] = bucket
			order = append(order, key)
		}
		entry := toRunEntry(g, stats)
		entry.Orchestration = orch
		bucket.Runs = append(bucket.Runs, entry)
	}
	if err := rows.Err(); err != nil {
		api.WriteError(w, r, err)
		return
	}

	groups := make([]repoGroup, 0, len(order))
	for _, k := range order {
		groups = append(groups, *byRepo[k])
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func repositoryDisplayName(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 && i < len(key)-1 {
		return key[i+1:]
	}
	return key
}

// Individual serves GET /api/v1/reports/individual?limit=&offset=. Flat per-job
// list: one row per reports table entry (= one row per shard).
func (h *Handlers) Individual(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	offset := parseOffset(r.URL.Query().Get("offset"))

	var total int
	if err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM reports`).Scan(&total); err != nil {
		api.WriteError(w, r, err)
		return
	}

	rows, err := h.Pool.Query(r.Context(), `
		SELECT r.id, r.name, r.status, r.gh_job_id, r.gh_job_name, r.created_at, r.updated_at,
		       r.total_suites, r.total_cases, r.passed_cases, r.failed_cases, r.skipped_cases, r.flaky_cases, r.duration_ms,
		       g.id, g.framework, g.name, g.status, g.repository, g.branch, g.commit_sha,
		       g.gh_run_id, g.gh_run_attempt, g.gh_pr_number, g.environment_metadata,
		       g.created_at, g.updated_at
		FROM reports r
		JOIN report_groups g ON g.id = r.report_group_id
		ORDER BY r.created_at DESC LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()

	orchLookup := newOrchestrationLookup()
	out := make([]individualReportSummary, 0)
	for rows.Next() {
		var e reportEntryDTO
		var rs groupStats
		var dur *int64
		var g groupDTO
		var env []byte
		if err := rows.Scan(
			&e.ID, &e.Name, &e.Status, &e.GHJobID, &e.GHJobName, &e.CreatedAt, &e.UpdatedAt,
			&rs.TotalSuites, &rs.TotalCases, &rs.Passed, &rs.Failed, &rs.Skipped, &rs.Flaky, &dur,
			&g.ID, &g.Framework, &g.Name, &g.Status, &g.Repository, &g.Branch, &g.CommitSHA,
			&g.GHRunID, &g.GHRunAttempt, &g.GHPRNumber, &env,
			&g.CreatedAt, &g.UpdatedAt,
		); err != nil {
			api.WriteError(w, r, err)
			return
		}
		rs.DurationMs = dur
		_ = env
		summary := toIndividualSummary(g, e, rs)
		orch, err := orchLookup.getForGroup(r.Context(), h.Pool, g)
		if err != nil {
			api.WriteError(w, r, err)
			return
		}
		summary.Orchestration = orch
		out = append(out, summary)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reports": out,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// Consolidated serves GET /api/v1/reports/consolidated.
//
// Splitting it out mechanically hides the request-shape contract across helpers;
// the flow is linear and each stage is self-contained.
//
//nolint:gocyclo // Single top-down handler: query → fetch → aggregate → shape → write.
func (h *Handlers) Consolidated(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repository := strings.TrimSpace(q.Get("repository"))
	branch := strings.TrimSpace(q.Get("branch"))
	commit := strings.TrimSpace(q.Get("commit"))
	name := strings.TrimSpace(q.Get("name"))
	if repository == "" || branch == "" || commit == "" || name == "" {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
			"repository, branch, commit, and name are all required")
		return
	}
	var pinnedAttempt *string
	if v := q.Get("run_attempt"); v != "" {
		pinnedAttempt = &v
	}
	var pinnedGroup *string
	if v := q.Get("gid"); v != "" {
		pinnedGroup = &v
	}

	// The URL carries the repository slug (e.g. "mattermost"); the DB stores
	// the full "owner/repo". Match on suffix so "mattermost" hits
	// "mattermost/mattermost". The commit comparator supports prefix matching
	// so short SHAs in the URL still find the record.
	//
	// The LATERAL subquery attaches each test_case's linked screenshots as a
	// JSON array so the web can render per-attempt galleries without a
	// follow-up request per failed attempt.
	rows, err := h.Pool.Query(r.Context(), `
		SELECT r.id, g.commit_sha, g.gh_run_attempt, r.created_at, g.id,
		       COALESCE(s.title, '') || ' › ' || tc.title AS full_title,
		       tc.status, COALESCE(tc.duration_ms, 0), tc.error_message, tc.error_stack,
		       COALESCE(ss.shots, '[]'::jsonb) AS screenshots
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		JOIN reports r ON r.id = s.report_id
		JOIN report_groups g ON g.id = r.report_group_id
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(
				jsonb_build_object(
					'file_path', rs.s3_key,
					'screenshot_type', COALESCE(rs.screenshot_type, '')
				)
				ORDER BY rs.sequence, rs.created_at
			) AS shots
			FROM report_screenshots rs
			WHERE rs.case_id = tc.id
		) ss ON TRUE
		WHERE (g.repository = $1 OR g.repository LIKE '%/' || $1)
		  AND g.branch = $2
		  AND g.commit_sha LIKE $3 || '%'
		  AND g.name = $4
		  AND ($5::text IS NULL OR g.gh_run_attempt = $5::text)
		  AND ($6::uuid IS NULL OR g.id = $6::uuid)
	`, repository, branch, commit, name, pinnedAttempt, pinnedGroup)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()

	type shotInfo struct {
		FilePath       string `json:"file_path"`
		ScreenshotType string `json:"screenshot_type"`
	}
	type caseInput struct {
		ReportID     uuid.UUID
		CommitSHA    string
		RunAttempt   string
		CreatedAt    time.Time
		GroupID      uuid.UUID
		FullTitle    string
		Status       string
		DurationMs   int64
		ErrorMessage *string
		ErrorStack   *string
		Screenshots  []shotInfo
	}
	var inputs []caseInput
	for rows.Next() {
		var ci caseInput
		var shotsJSON []byte
		if err := rows.Scan(
			&ci.ReportID, &ci.CommitSHA, &ci.RunAttempt, &ci.CreatedAt, &ci.GroupID,
			&ci.FullTitle, &ci.Status, &ci.DurationMs, &ci.ErrorMessage, &ci.ErrorStack,
			&shotsJSON,
		); err != nil {
			api.WriteError(w, r, err)
			return
		}
		if len(shotsJSON) > 0 {
			_ = json.Unmarshal(shotsJSON, &ci.Screenshots)
		}
		inputs = append(inputs, ci)
	}
	if err := rows.Err(); err != nil {
		api.WriteError(w, r, err)
		return
	}

	filters := map[string]any{
		"repository":  repository,
		"target_name": name,
		"commit_sha":  commit,
		"tool_name":   "",
	}
	if len(inputs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"filters":                filters,
			"overall_status":         statusPassed,
			"total_specs":            0,
			"passed":                 0,
			"failed":                 0,
			"skipped":                0,
			"flaky":                  0,
			"contributing_reports":   []string{},
			"latest_commit_sha":      commit,
			"latest_run_attempt":     1,
			"available_run_attempts": []int{1},
			"specs":                  []any{},
		})
		return
	}

	seenReport := map[uuid.UUID]bool{}
	contributing := make([]string, 0)
	contributingIDs := make([]uuid.UUID, 0)
	for _, c := range inputs {
		if !seenReport[c.GroupID] {
			seenReport[c.GroupID] = true
			contributing = append(contributing, c.GroupID.String())
			contributingIDs = append(contributingIDs, c.GroupID)
		}
	}
	latestCommit := inputs[0].CommitSHA
	latestTime := inputs[0].CreatedAt
	for _, c := range inputs[1:] {
		if c.CreatedAt.After(latestTime) {
			latestTime = c.CreatedAt
			latestCommit = c.CommitSHA
		}
	}
	latestAttempt := 0
	attemptSet := map[int]bool{}
	for _, c := range inputs {
		if c.CommitSHA != latestCommit {
			continue
		}
		n := atoiDefault(c.RunAttempt, 1)
		attemptSet[n] = true
		if n > latestAttempt {
			latestAttempt = n
		}
	}
	if latestAttempt == 0 {
		latestAttempt = 1
	}
	availableAttempts := make([]int, 0, len(attemptSet))
	for n := range attemptSet {
		availableAttempts = append(availableAttempts, n)
	}
	sort.Ints(availableAttempts)

	byTitle := map[string][]caseInput{}
	for _, c := range inputs {
		byTitle[c.FullTitle] = append(byTitle[c.FullTitle], c)
	}

	type historyEntry struct {
		// report_id is the per-shard report row (not the group). The web
		// looks this up in the ReportDetail.reports[] array for each
		// contributing group to render the shard's display_name.
		ReportID     string  `json:"report_id"`
		CommitSHA    string  `json:"commit_sha"`
		RunAttempt   int     `json:"run_attempt"`
		Status       string  `json:"status"`
		DurationMs   int64   `json:"duration_ms"`
		ErrorMessage *string `json:"error_message,omitempty"`
		ErrorStack   *string `json:"error_stack,omitempty"`
		// errors_json is a JSON-encoded array matching the format
		// InlineErrorDisplay parses on the web side. Constructed here from
		// error_message + error_stack so the web can pipe it through
		// unchanged alongside the single-report spec shape.
		ErrorsJSON  string     `json:"errors_json,omitempty"`
		CreatedAt   string     `json:"created_at"`
		Screenshots []shotInfo `json:"screenshots,omitempty"`
	}
	type consolidatedSpec struct {
		FullTitle        string         `json:"full_title"`
		Status           string         `json:"status"`
		SourceCommitSHA  string         `json:"source_commit_sha"`
		SourceRunAttempt int            `json:"source_run_attempt"`
		IsFromLatest     bool           `json:"is_from_latest"`
		DurationMs       int64          `json:"duration_ms"`
		ErrorMessage     *string        `json:"error_message,omitempty"`
		History          []historyEntry `json:"history"`
	}

	titles := make([]string, 0, len(byTitle))
	for t := range byTitle {
		titles = append(titles, t)
	}
	sort.Strings(titles)

	specs := make([]consolidatedSpec, 0, len(titles))
	passed, failed, skipped, flaky := 0, 0, 0, 0
	var durationSum int64
	for _, title := range titles {
		cases := byTitle[title]
		sort.SliceStable(cases, func(i, j int) bool {
			a, b := cases[i], cases[j]
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.After(b.CreatedAt)
			}
			return atoiDefault(a.RunAttempt, 1) > atoiDefault(b.RunAttempt, 1)
		})
		winner := cases[0]
		winAttempt := atoiDefault(winner.RunAttempt, 1)
		history := make([]historyEntry, 0, len(cases))
		for _, c := range cases {
			history = append(history, historyEntry{
				ReportID:     c.ReportID.String(),
				CommitSHA:    c.CommitSHA,
				RunAttempt:   atoiDefault(c.RunAttempt, 1),
				Status:       c.Status,
				DurationMs:   c.DurationMs,
				ErrorMessage: c.ErrorMessage,
				ErrorStack:   c.ErrorStack,
				ErrorsJSON:   buildErrorsJSON(c.ErrorMessage, c.ErrorStack),
				CreatedAt:    fmtTime(c.CreatedAt),
				Screenshots:  c.Screenshots,
			})
		}
		specs = append(specs, consolidatedSpec{
			FullTitle:        title,
			Status:           winner.Status,
			SourceCommitSHA:  winner.CommitSHA,
			SourceRunAttempt: winAttempt,
			IsFromLatest:     winner.CommitSHA == latestCommit && winAttempt == latestAttempt,
			DurationMs:       winner.DurationMs,
			ErrorMessage:     winner.ErrorMessage,
			History:          history,
		})
		durationSum += winner.DurationMs
		switch winner.Status {
		case statusPassed:
			passed++
		case statusFailed, statusTimedOut:
			failed++
		case statusSkipped:
			skipped++
		case statusFlaky:
			flaky++
		default:
			passed++
		}
	}
	overall := statusPassed
	if failed > 0 {
		overall = statusFailed
	} else if flaky > 0 {
		overall = statusFlaky
	}

	// Per-class wall clock across contributing groups: take the MAX span so
	// multi-attempt views still reflect the worst-case shard batch duration.
	// Single-attempt views degrade to that group's spans.
	var wallClockMs, retestWallClockMs *int64
	for _, gid := range contributingIDs {
		numbered, retest, err := aggregateWallClockSpans(r.Context(), h.Pool, gid)
		if err != nil {
			api.WriteError(w, r, err)
			return
		}
		if numbered != nil && (wallClockMs == nil || *numbered > *wallClockMs) {
			v := *numbered
			wallClockMs = &v
		}
		if retest != nil && (retestWallClockMs == nil || *retest > *retestWallClockMs) {
			v := *retest
			retestWallClockMs = &v
		}
	}

	resp := map[string]any{
		"filters":                filters,
		"overall_status":         overall,
		"total_specs":            len(specs),
		"passed":                 passed,
		"failed":                 failed,
		"skipped":                skipped,
		"flaky":                  flaky,
		"contributing_reports":   contributing,
		"latest_commit_sha":      latestCommit,
		"latest_run_attempt":     latestAttempt,
		"available_run_attempts": availableAttempts,
		"duration_ms":            durationSum,
		"specs":                  specs,
	}
	if wallClockMs != nil {
		resp["wall_clock_ms"] = *wallClockMs
	}
	if retestWallClockMs != nil {
		resp["retest_wall_clock_ms"] = *retestWallClockMs
	}
	writeJSON(w, http.StatusOK, resp)
}

// SuiteSpecs serves GET /api/v1/reports/{id}/suites/{suiteID}/specs. The {id}
// is the report_group; the suite belongs to one report entry under it.
func (h *Handlers) SuiteSpecs(w http.ResponseWriter, r *http.Request) {
	if _, err := uuid.Parse(chi.URLParam(r, "id")); err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	suiteID, err := uuid.Parse(chi.URLParam(r, "suiteID"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}

	// Retries of the same spec produce one test_cases row per retry (the
	// extractor flattens Playwright's Results[] that way). Group by full_title
	// so the web gets one TestSpec with multiple TestResults — matching the
	// shape its flaky / "N attempts" rendering already expects.
	rows, err := h.Pool.Query(r.Context(), `
		SELECT tc.id, tc.title, tc.full_title, tc.status, tc.retry_count, tc.duration_ms,
		       tc.error_message, tc.error_stack, tc.attachments, tc.ordinal, s.file
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		WHERE tc.suite_id = $1
		ORDER BY tc.ordinal, tc.retry_count
	`, suiteID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()

	type caseRow struct {
		ID         uuid.UUID
		Title      string
		FullTitle  string
		Status     string
		Retry      int
		Duration   *int64
		ErrorMsg   *string
		ErrorStack *string
		Attach     []byte
		Ordinal    int
		FilePath   *string
	}
	caseRows := []caseRow{}
	for rows.Next() {
		var c caseRow
		if err := rows.Scan(&c.ID, &c.Title, &c.FullTitle, &c.Status, &c.Retry,
			&c.Duration, &c.ErrorMsg, &c.ErrorStack, &c.Attach, &c.Ordinal, &c.FilePath); err != nil {
			api.WriteError(w, r, err)
			return
		}
		caseRows = append(caseRows, c)
	}
	if err := rows.Err(); err != nil {
		api.WriteError(w, r, err)
		return
	}

	// Fetch all screenshots for every case row. Later aggregated at the spec
	// level (across retries) for the top-level gallery.
	screensByCase := map[uuid.UUID][]map[string]any{}
	if len(caseRows) > 0 {
		ids := make([]uuid.UUID, 0, len(caseRows))
		for _, c := range caseRows {
			ids = append(ids, c.ID)
		}
		ssRows, err := h.Pool.Query(r.Context(), `
			SELECT case_id, s3_key, screenshot_type, sequence
			FROM report_screenshots
			WHERE case_id = ANY($1)
			ORDER BY sequence
		`, ids)
		if err == nil {
			for ssRows.Next() {
				var caseID uuid.UUID
				var s3Key string
				var stype *string
				var seq int
				if err := ssRows.Scan(&caseID, &s3Key, &stype, &seq); err != nil {
					break
				}
				t := ""
				if stype != nil {
					t = *stype
				}
				screensByCase[caseID] = append(screensByCase[caseID], map[string]any{
					"file_path":       s3Key,
					"screenshot_type": t,
				})
			}
			ssRows.Close()
		}
	}

	// Preserve first-seen order for stable rendering (matches ordinal-asc).
	groupOrder := []string{}
	groupByKey := map[string][]caseRow{}
	for _, c := range caseRows {
		key := c.FullTitle
		if key == "" {
			// Extractor guarantees full_title; fall back defensively per-row.
			key = c.ID.String()
		}
		if _, seen := groupByKey[key]; !seen {
			groupOrder = append(groupOrder, key)
		}
		groupByKey[key] = append(groupByKey[key], c)
	}

	out := make([]map[string]any, 0, len(groupOrder))
	for _, key := range groupOrder {
		cases := groupByKey[key]
		// Final retry wins status; a test that eventually passed (status
		// 'passed'/'flaky'/'skipped' on the last attempt) is ok=true, which
		// the web uses to distinguish flaky from failed specs.
		final := cases[len(cases)-1]
		ok := final.Status == statusPassed || final.Status == statusFlaky || final.Status == statusSkipped

		results := make([]map[string]any, 0, len(cases))
		aggScreens := []map[string]any{}
		seenShot := map[string]bool{}
		for _, c := range cases {
			dur := int64(0)
			if c.Duration != nil {
				dur = *c.Duration
			}
			results = append(results, map[string]any{
				"id":           c.ID.String(),
				"status":       c.Status,
				"duration_ms":  dur,
				"retry":        c.Retry,
				"start_time":   "",
				"project_id":   "",
				"project_name": "",
				"errors_json":  buildErrorsJSON(c.ErrorMsg, c.ErrorStack),
				"attachments":  attachmentsOrEmpty(c.Attach),
			})
			for _, shot := range screensByCase[c.ID] {
				path, _ := shot["file_path"].(string)
				if seenShot[path] {
					continue
				}
				seenShot[path] = true
				aggScreens = append(aggScreens, shot)
			}
		}

		first := cases[0]
		filePath := ""
		if first.FilePath != nil {
			filePath = *first.FilePath
		}
		spec := map[string]any{
			"id":        first.ID.String(),
			"title":     first.Title,
			"ok":        ok,
			"spec_id":   first.ID.String(),
			"file_path": filePath,
			"line":      0,
			"column":    0,
			"results":   results,
		}
		if len(aggScreens) > 0 {
			spec["screenshots"] = aggScreens
		}
		out = append(out, spec)
	}
	writeJSON(w, http.StatusOK, map[string]any{"specs": out})
}

// attachmentsOrEmpty decodes the test_cases.attachments JSONB into the array
// shape the web expects, returning an empty array on NULL or decode failure.
func attachmentsOrEmpty(raw []byte) []any {
	if len(raw) == 0 {
		return []any{}
	}
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		return []any{}
	}
	return out
}

// Search serves GET /api/v1/reports/{id}/search?q=&limit=. Searches across
// every test case under the report_group.
func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	query := r.URL.Query().Get("q")
	minLen := h.SearchMinLength
	if minLen <= 0 {
		minLen = 3
	}
	if len(query) < minLen {
		api.WriteError(w, r, api.ErrQueryTooShort)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100, 500)

	rows, err := h.Pool.Query(r.Context(), `
		SELECT s.id, s.title, s.file, tc.id, tc.title, tc.status
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		JOIN reports r ON r.id = s.report_id
		WHERE r.report_group_id = $1 AND tc.title ILIKE '%' || $2 || '%'
		ORDER BY s.ordinal, tc.ordinal
		LIMIT $3
	`, groupID, query, limit)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()

	type suiteBucket struct {
		SuiteID    string           `json:"suite_id"`
		SuiteTitle string           `json:"suite_title"`
		SuiteFile  *string          `json:"suite_file_path"`
		ReportID   string           `json:"report_id"`
		Matches    []map[string]any `json:"matches"`
	}
	buckets := map[string]*suiteBucket{}
	total := 0
	for rows.Next() {
		var (
			sid, tcid      uuid.UUID
			sTitle, tTitle string
			sFile          *string
			status         string
		)
		if err := rows.Scan(&sid, &sTitle, &sFile, &tcid, &tTitle, &status); err != nil {
			api.WriteError(w, r, err)
			return
		}
		key := sid.String()
		b, ok := buckets[key]
		if !ok {
			b = &suiteBucket{
				SuiteID:    key,
				SuiteTitle: sTitle,
				SuiteFile:  sFile,
				ReportID:   groupID.String(),
				Matches:    []map[string]any{},
			}
			buckets[key] = b
		}
		b.Matches = append(b.Matches, map[string]any{
			"test_case_id": tcid.String(),
			"title":        tTitle,
			"full_title":   sTitle + " › " + tTitle,
			"status":       status,
			"match_tokens": []string{query},
		})
		total++
	}

	results := make([]*suiteBucket, 0, len(buckets))
	for _, b := range buckets {
		results = append(results, b)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query":             query,
		"search_min_length": minLen,
		"total_matches":     total,
		"results":           results,
	})
}

// ---------- small helpers ----------

func derefOr(p *string, dflt string) string {
	if p == nil {
		return dflt
	}
	return *p
}

func derefOrEmpty(p *string) string { return derefOr(p, "") }

func parseOffset(v string) int {
	n := parseLimit(v, 0, 1_000_000)
	if n < 0 {
		return 0
	}
	return n
}

func atoiDefault(s string, dflt int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
		return n
	}
	return dflt
}

// orchestrationKey is the identity tuple used to memoize per-request
// orchestration_runs lookups. Five fields uniquely identify a run.
type orchestrationKey struct {
	repository, commitSHA, ghRunID, name, ghRunAttempt string
}

// aggregateOrchestrationSummary returns the live orchestration_runs status
// and counts that match the supplied composite identity, or nil when no
// orchestration_runs row exists for that tuple. Used by Grouped, Individual,
// and Detail to decorate report-index rows with the in-flight run's progress.
func aggregateOrchestrationSummary(
	ctx context.Context, pool *pgxpool.Pool,
	repository, commitSHA, ghRunID, name, ghRunAttempt string,
) (*orchestrationSummary, error) {
	var (
		runID  uuid.UUID
		status string
		total  int
		c      orchestrationCounts
	)
	err := pool.QueryRow(ctx, `
		SELECT id, status, total_units,
		       pending_count, leased_count,
		       completed_pass_count, completed_fail_count,
		       completed_skipped_count, abandoned_count,
		       retest_eligible_count
		  FROM orchestration_runs
		 WHERE repository = $1
		   AND commit_sha = $2
		   AND gh_run_id  = $3
		   AND name       = $4
		   AND gh_run_attempt = $5
		 LIMIT 1
	`, repository, commitSHA, ghRunID, name, ghRunAttempt).Scan(
		&runID, &status, &total,
		&c.Pending, &c.Leased,
		&c.CompletedPass, &c.CompletedFail,
		&c.CompletedSkipped, &c.Abandoned,
		&c.RetestEligible,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tests, err := aggregateOrchestrationTestCounts(ctx, pool, runID)
	if err != nil {
		return nil, err
	}
	return &orchestrationSummary{
		Status:     status,
		TotalUnits: total,
		Counts:     c,
		Tests:      tests,
	}, nil
}

// aggregateOrchestrationTestCounts walks every reported test_case across
// all attempts of every dispatch_unit on the run and rolls them up to
// per-test-case statuses, applying the same any-passed-AND-any-failed →
// flaky rule the OrchestrationTab uses on the client. Returns nil when
// no attempts have yet reported test_cases (a fresh run with all units
// pending), which the listing rows render as "no test stats available".
func aggregateOrchestrationTestCounts(
	ctx context.Context, pool *pgxpool.Pool, runID uuid.UUID,
) (*orchestrationTestCounts, error) {
	var t orchestrationTestCounts
	err := pool.QueryRow(ctx, `
		WITH per_test AS (
			SELECT
				a.dispatch_unit_id,
				tc->>'full_title' AS full_title,
				BOOL_OR(tc->>'status' IN ('passed', 'flaky')) AS ever_passed,
				BOOL_OR(tc->>'status' IN ('failed', 'timedOut', 'interrupted')) AS ever_failed,
				BOOL_OR(tc->>'status' = 'skipped') AS ever_skipped
			  FROM attempts a
			 CROSS JOIN LATERAL jsonb_array_elements(a.test_cases) AS tc
			 WHERE a.run_id = $1
			   AND a.test_cases IS NOT NULL
			 GROUP BY a.dispatch_unit_id, tc->>'full_title'
		)
		SELECT
			COUNT(*) FILTER (WHERE ever_passed AND NOT ever_failed) AS passed,
			COUNT(*) FILTER (WHERE ever_failed AND NOT ever_passed) AS failed,
			COUNT(*) FILTER (WHERE ever_passed AND ever_failed) AS flaky,
			COUNT(*) FILTER (WHERE ever_skipped AND NOT ever_passed AND NOT ever_failed) AS skipped,
			COUNT(*) AS total
		  FROM per_test
	`, runID).Scan(&t.Passed, &t.Failed, &t.Flaky, &t.Skipped, &t.Total)
	if err != nil {
		return nil, err
	}
	if t.Total == 0 {
		return nil, nil
	}
	return &t, nil
}

// orchestrationLookup is a per-request memo over aggregateOrchestrationSummary
// so a single Grouped or Individual response that visits the same composite
// identity twice (rare but defensible) issues exactly one DB read per tuple.
type orchestrationLookup struct {
	cache map[orchestrationKey]*orchestrationSummary
}

func newOrchestrationLookup() *orchestrationLookup {
	return &orchestrationLookup{cache: map[orchestrationKey]*orchestrationSummary{}}
}

// getForGroup pulls the orchestration summary for a report_group's identity
// tuple. Returns (nil, nil) when no orchestration_run matches — that's the
// common case for legacy report uploads with no orchestration coverage.
func (o *orchestrationLookup) getForGroup(
	ctx context.Context, pool *pgxpool.Pool, g groupDTO,
) (*orchestrationSummary, error) {
	key := orchestrationKey{
		repository:   g.Repository,
		commitSHA:    g.CommitSHA,
		ghRunID:      g.GHRunID,
		name:         g.Name,
		ghRunAttempt: g.GHRunAttempt,
	}
	if v, ok := o.cache[key]; ok {
		return v, nil
	}
	v, err := aggregateOrchestrationSummary(ctx, pool,
		key.repository, key.commitSHA, key.ghRunID, key.name, key.ghRunAttempt)
	if err != nil {
		return nil, err
	}
	o.cache[key] = v
	return v, nil
}

// buildErrorsJSON produces the JSON-encoded string the web's inline-error
// renderer consumes: `[{"message":..,"estack":..}]`. Returns "" when the
// test_case recorded no error, which the renderer treats as "nothing to show".
func buildErrorsJSON(message, stack *string) string {
	msg := derefOrEmpty(message)
	stk := derefOrEmpty(stack)
	if msg == "" && stk == "" {
		return ""
	}
	entry := map[string]string{}
	if msg != "" {
		entry["message"] = msg
	}
	if stk != "" {
		entry["estack"] = stk
	}
	b, err := json.Marshal([]map[string]string{entry})
	if err != nil {
		return ""
	}
	return string(b)
}
