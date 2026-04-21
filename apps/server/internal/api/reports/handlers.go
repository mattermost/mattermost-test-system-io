// Package reports serves /api/v1/reports* endpoints: the stateless upload
// lifecycle (begin/register/upload/complete, see stateless.go), view handlers
// (List/Detail/Suites/SuiteSpecs/Cases/JSONFile/Delete) against the
// composite-identity report_groups + per-job reports schema, and the
// aggregation endpoints (see aggregations.go).
package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

// Handlers bundles the report-related handlers.
type Handlers struct {
	Pool             *pgxpool.Pool
	Store            storage.ObjectStore
	Publisher        *events.Publisher
	Logger           *slog.Logger
	MaxUploadBytes   int64
	MaxArtifactBytes int64
	PresignTTL       time.Duration
	SearchMinLength  int
}

// ---------- DTOs ----------

// groupDTO is the report_group row as returned to the web. Field names match
// the report-summary and report-detail response shapes the dashboard consumes.
type groupDTO struct {
	ID                  uuid.UUID
	Framework           string
	Name                string
	Status              string
	Repository          string
	Branch              string
	CommitSHA           string
	GHRunID             string
	GHRunAttempt        string
	GHPRNumber          *int
	EnvironmentMetadata *json.RawMessage
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// reportEntryDTO is a per-job upload inside a report_group.
type reportEntryDTO struct {
	ID        uuid.UUID
	Name      string
	Status    string
	GHJobID   *string
	GHJobName *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const reportGroupSelectCols = `
	id, framework, name, status, repository, branch, commit_sha,
	gh_run_id, gh_run_attempt, gh_pr_number, environment_metadata,
	created_at, updated_at
`

const reportGroupSelectColsPrefixed = `
	g.id, g.framework, g.name, g.status, g.repository, g.branch, g.commit_sha,
	g.gh_run_id, g.gh_run_attempt, g.gh_pr_number, g.environment_metadata,
	g.created_at, g.updated_at
`

func scanGroup(s interface{ Scan(dst ...any) error }) (groupDTO, error) {
	var g groupDTO
	var env []byte
	if err := s.Scan(
		&g.ID, &g.Framework, &g.Name, &g.Status, &g.Repository, &g.Branch, &g.CommitSHA,
		&g.GHRunID, &g.GHRunAttempt, &g.GHPRNumber, &env,
		&g.CreatedAt, &g.UpdatedAt,
	); err != nil {
		return groupDTO{}, err
	}
	if len(env) > 0 {
		raw := json.RawMessage(env)
		g.EnvironmentMetadata = &raw
	}
	return g, nil
}

func fetchGroup(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (groupDTO, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+reportGroupSelectCols+` FROM report_groups WHERE id = $1 LIMIT 1`, id)
	g, err := scanGroup(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return groupDTO{}, api.ErrNotFound
		}
		return groupDTO{}, err
	}
	return g, nil
}

func fetchGroupReports(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) ([]reportEntryDTO, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, status, gh_job_id, gh_job_name, created_at, updated_at
		FROM reports WHERE report_group_id = $1 ORDER BY created_at
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]reportEntryDTO, 0)
	for rows.Next() {
		var e reportEntryDTO
		if err := rows.Scan(&e.ID, &e.Name, &e.Status, &e.GHJobID, &e.GHJobName, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// aggregateGroupStats walks child reports and sums their test-case counts +
// computes real wall-clock spans. Parallel shards run simultaneously so the
// group's wall-clock is max(shard_end) − min(shard_start), not sum; retest
// shards typically start after the numbered batch finishes so we expose them
// separately.
type groupStats struct {
	TotalSuites         int
	TotalCases          int
	Passed              int
	Failed              int
	Skipped             int
	Flaky               int
	DurationMs          *int64 // sum of per-case durations across all shards
	NumberedWallClockMs *int64 // span: min(start) → max(end) over non-retest shards
	RetestWallClockMs   *int64 // same span, over retest shards only
}

// retestNamePattern flags shards whose gh_job_name looks like a retest run
// (Playwright "retest-" / Cypress "run-failed-tests"). Case-insensitive.
const retestNamePattern = `(?i)retest|run[-_ ]?failed`

func aggregateGroupStats(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) (groupStats, error) {
	var s groupStats
	var dur *int64
	if err := pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(total_suites),0),
			COALESCE(SUM(total_cases),0),
			COALESCE(SUM(passed_cases),0),
			COALESCE(SUM(failed_cases),0),
			COALESCE(SUM(skipped_cases),0),
			COALESCE(SUM(flaky_cases),0),
			SUM(duration_ms)
		FROM reports WHERE report_group_id = $1
	`, groupID).Scan(&s.TotalSuites, &s.TotalCases, &s.Passed, &s.Failed, &s.Skipped, &s.Flaky, &dur); err != nil {
		return groupStats{}, err
	}
	s.DurationMs = dur

	numbered, retest, err := aggregateWallClockSpans(ctx, pool, groupID)
	if err != nil {
		return groupStats{}, err
	}
	s.NumberedWallClockMs = numbered
	s.RetestWallClockMs = retest
	return s, nil
}

// aggregateWallClockSpans classifies each shard as numbered vs retest by
// gh_job_name, then returns (max_end - min_start) in ms for each class.
// Shards with no start_time or wall_clock_ms recorded are excluded.
func aggregateWallClockSpans(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) (*int64, *int64, error) {
	rows, err := pool.Query(ctx, `
		SELECT
			start_time,
			wall_clock_ms,
			(COALESCE(gh_job_name, '') ~* $2) AS is_retest
		FROM reports
		WHERE report_group_id = $1 AND start_time IS NOT NULL AND wall_clock_ms IS NOT NULL
	`, groupID, retestNamePattern)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var numberedMinStart, numberedMaxEnd, retestMinStart, retestMaxEnd *time.Time
	for rows.Next() {
		var start time.Time
		var wallMs int64
		var isRetest bool
		if err := rows.Scan(&start, &wallMs, &isRetest); err != nil {
			return nil, nil, err
		}
		end := start.Add(time.Duration(wallMs) * time.Millisecond)
		if isRetest {
			if retestMinStart == nil || start.Before(*retestMinStart) {
				t := start
				retestMinStart = &t
			}
			if retestMaxEnd == nil || end.After(*retestMaxEnd) {
				t := end
				retestMaxEnd = &t
			}
		} else {
			if numberedMinStart == nil || start.Before(*numberedMinStart) {
				t := start
				numberedMinStart = &t
			}
			if numberedMaxEnd == nil || end.After(*numberedMaxEnd) {
				t := end
				numberedMaxEnd = &t
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return spanMs(numberedMinStart, numberedMaxEnd), spanMs(retestMinStart, retestMaxEnd), nil
}

func spanMs(start, end *time.Time) *int64 {
	if start == nil || end == nil {
		return nil
	}
	ms := end.Sub(*start).Milliseconds()
	if ms <= 0 {
		return nil
	}
	return &ms
}

// ---------- View handlers ----------

// List serves GET /api/v1/reports?limit=&offset=.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	offset := parseOffset(r.URL.Query().Get("offset"))

	var total int
	if err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM report_groups`).Scan(&total); err != nil {
		api.WriteError(w, r, err)
		return
	}

	rows, err := h.Pool.Query(r.Context(), `
		SELECT `+reportGroupSelectCols+`
		FROM report_groups ORDER BY created_at DESC LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()

	summaries := make([]reportSummary, 0)
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
		summaries = append(summaries, toReportSummary(g, stats))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reports": summaries,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// Detail serves GET /api/v1/reports/{id}. The {id} may be either a
// report_group.id OR a reports.id (per-shard entry). When it matches a shard
// row, we resolve up to the parent group so the web can render either link
// shape. The web distinguishes individual-vs-group views by comparing the
// requested ID against the returned `id`.
func (h *Handlers) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	g, err := fetchGroup(r.Context(), h.Pool, id)
	if errors.Is(err, api.ErrNotFound) {
		g, err = fetchGroupByReportID(r.Context(), h.Pool, id)
	}
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	entries, err := fetchGroupReports(r.Context(), h.Pool, g.ID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	stats, err := aggregateGroupStats(r.Context(), h.Pool, g.ID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toReportDetail(g, entries, stats))
}

// fetchGroupByReportID resolves a report_group from a per-shard reports.id.
// Returns api.ErrNotFound when the id matches neither a group nor a shard.
func fetchGroupByReportID(ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID) (groupDTO, error) {
	row := pool.QueryRow(ctx, `
		SELECT `+reportGroupSelectColsPrefixed+`
		FROM report_groups g JOIN reports r ON r.report_group_id = g.id
		WHERE r.id = $1 LIMIT 1
	`, reportID)
	g, err := scanGroup(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return groupDTO{}, api.ErrNotFound
		}
		return groupDTO{}, err
	}
	return g, nil
}

// Suites serves GET /api/v1/reports/{id}/suites. Returns the flat suite list
// across every report entry under the group, plus a `reports` sidebar so the
// UI can filter by entry.
func (h *Handlers) Suites(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	entries, err := fetchGroupReports(r.Context(), h.Pool, id)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"suites": []any{}})
		return
	}
	entryIDs := make([]uuid.UUID, len(entries))
	entryNames := make(map[uuid.UUID]string, len(entries))
	entryNumber := make(map[uuid.UUID]int, len(entries))
	for i, e := range entries {
		entryIDs[i] = e.ID
		entryNames[e.ID] = firstNonEmptyStr(derefOrEmpty(e.GHJobName), e.Name)
		entryNumber[e.ID] = i + 1
	}

	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, report_id, parent_suite_id, title, file, line, col, duration_ms,
		        total_count, passed_count, failed_count, skipped_count, flaky_count,
		        start_time, ordinal
		 FROM suites WHERE report_id = ANY($1)
		 ORDER BY parent_suite_id NULLS FIRST, ordinal`, entryIDs)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()

	suites := make([]map[string]any, 0)
	for rows.Next() {
		var sid, rid uuid.UUID
		var parent uuid.NullUUID
		var title string
		var file *string
		var line, col *int
		var dur *int64
		var total, passed, failed, skipped, flaky int
		var startTime *time.Time
		var ordinal int
		if err := rows.Scan(&sid, &rid, &parent, &title, &file, &line, &col, &dur,
			&total, &passed, &failed, &skipped, &flaky, &startTime, &ordinal); err != nil {
			api.WriteError(w, r, err)
			return
		}
		m := map[string]any{
			"id":            sid,
			"report_id":     rid,
			"report_name":   entryNames[rid],
			"report_number": entryNumber[rid],
			"title":         title,
			"file_path":     file,
			"ordinal":       ordinal,
			"line":          line,
			"col":           col,
			"duration_ms":   dur,
			"specs_count":   total,
			"passed_count":  passed,
			"failed_count":  failed,
			"skipped_count": skipped,
			"flaky_count":   flaky,
		}
		if startTime != nil {
			m["start_time"] = startTime.UTC().Format(time.RFC3339)
		}
		if parent.Valid {
			m["parent_suite_id"] = parent.UUID
		} else {
			m["parent_suite_id"] = nil
		}
		suites = append(suites, m)
	}

	reportsSidebar := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		reportsSidebar = append(reportsSidebar, map[string]any{
			"report_id":     e.ID,
			"report_name":   firstNonEmptyStr(derefOrEmpty(e.GHJobName), e.Name),
			"report_number": entryNumber[e.ID],
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"suites":  suites,
		"reports": reportsSidebar,
	})
}

// Cases serves GET /api/v1/reports/{id}/cases.
func (h *Handlers) Cases(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	statusFilter := r.URL.Query().Get("status")
	var sf *string
	if statusFilter != "" {
		sf = &statusFilter
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT tc.id, tc.suite_id, tc.title, tc.status, tc.retry_count, tc.duration_ms,
		       tc.error_message, tc.error_stack, tc.annotations, tc.ordinal
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		JOIN reports r ON r.id = s.report_id
		WHERE r.report_group_id = $1 AND ($2::text IS NULL OR tc.status = $2::text)
		ORDER BY s.ordinal, tc.ordinal
	`, id, sf)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	defer rows.Close()

	out := make([]map[string]any, 0)
	for rows.Next() {
		var cid, sid uuid.UUID
		var title, status string
		var retry, ordinal int
		var dur *int64
		var em, es *string
		var annotations []byte
		if err := rows.Scan(&cid, &sid, &title, &status, &retry, &dur, &em, &es, &annotations, &ordinal); err != nil {
			api.WriteError(w, r, err)
			return
		}
		var ann any
		_ = json.Unmarshal(annotations, &ann)
		out = append(out, map[string]any{
			"id":            cid,
			"suite_id":      sid,
			"title":         title,
			"status":        status,
			"retry_count":   retry,
			"duration_ms":   dur,
			"error_message": em,
			"error_stack":   es,
			"annotations":   ann,
			"ordinal":       ordinal,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// JSONFile redirects to a presigned URL for the first JSON file in the group's
// first report (best-effort; lets the UI fetch *something*).
func (h *Handlers) JSONFile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	var key string
	err = h.Pool.QueryRow(r.Context(), `
		SELECT rjf.object_key
		FROM report_json_files rjf
		JOIN reports r ON r.id = rjf.report_id
		WHERE r.report_group_id = $1
		ORDER BY rjf.created_at
		LIMIT 1
	`, id).Scan(&key)
	if err != nil {
		api.WriteError(w, r, api.ErrNotFound)
		return
	}
	url, err := h.Store.PresignGet(r.Context(), key, h.PresignTTL)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// Delete removes a report group (cascades to reports/suites/cases/artifacts).
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	if _, err := h.Pool.Exec(r.Context(), `DELETE FROM report_groups WHERE id = $1`, id); err != nil {
		api.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Upload serves the legacy single-shot multipart bundle upload. The new flow is
// stateless (begin/register/upload/complete) — this endpoint exists for
// backward compatibility but returns 410 Gone until a bundle adapter is wired.
func (h *Handlers) Upload(w http.ResponseWriter, _ *http.Request) {
	api.WriteErrorCode(w, http.StatusGone, "ENDPOINT_RETIRED",
		"use POST /reports/begin + /reports/register + /reports/upload/{id}/{uid}/json + /reports/complete")
}

// ---------- helpers ----------

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func parseLimit(v string, dflt, maxN int) int {
	if v == "" {
		return dflt
	}
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil || n <= 0 {
		return dflt
	}
	if n > maxN {
		return maxN
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
