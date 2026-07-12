// Stateless per-job upload flow.
//
//	POST /api/v1/reports/begin                           — upsert report_group by composite key
//	POST /api/v1/reports/register                        — create a report entry + declare files
//	POST /api/v1/reports/upload/{rid}/{uid}/json         — streaming multipart
//	POST /api/v1/reports/upload/{rid}/{uid}/screenshots  — streaming multipart
//
// Each shard in a parallel CI matrix authenticates with its own GitHub
// Actions OIDC token and registers itself independently; no pre-registration
// or shared state across shards is required.
//
// Group lifecycle: /reports/begin declares total_reports_expected (the
// shard count). The group auto-finalizes to `completed` once that many
// child reports reach `complete`. Groups that go idle past the staleness
// window are flipped to `incomplete` by the reaper.

package reports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/ingest"
)

// ---------- request/response bodies ----------

type beginBody struct {
	Repository           string `json:"repository"`
	Commit               string `json:"commit"`
	GHRunID              string `json:"gh_run_id"`
	GHRunAttempt         string `json:"gh_run_attempt"`
	Framework            string `json:"framework"`
	Name                 string `json:"name"`
	Branch               string `json:"branch"`
	GHPRNumber           *int   `json:"gh_pr_number,omitempty"`
	TotalReportsExpected int    `json:"total_reports_expected"`
}

type declaredFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type registerBody struct {
	Repository           string          `json:"repository"`
	Commit               string          `json:"commit"`
	GHRunID              string          `json:"gh_run_id"`
	GHRunAttempt         string          `json:"gh_run_attempt"`
	Framework            string          `json:"framework"`
	Name                 string          `json:"name"`
	Branch               string          `json:"branch"`
	GHPRNumber           *int            `json:"gh_pr_number,omitempty"`
	TotalReportsExpected *int            `json:"total_reports_expected,omitempty"`
	GHJobID              string          `json:"gh_job_id"`
	GHJobName            string          `json:"gh_job_name"`
	JSONFiles            []declaredFile  `json:"json_files"`
	Screenshots          []declaredFile  `json:"screenshots"`
	EnvironmentMetadata  json.RawMessage `json:"environment_metadata,omitempty"`
}

// ---------- handlers ----------

// Begin serves POST /api/v1/reports/begin. Upserts a report_group by composite
// key. Returns 200 with {report_id} whether the group already existed or was
// just created — the operation is idempotent.
//
// total_reports_expected is required and must be > 0. First non-null write
// wins; subsequent calls with a different value return 409 EXPECTED_REPORTS_MISMATCH
// to surface workflow misconfigurations rather than silently keeping the
// first-wins value.
func (h *Handlers) Begin(w http.ResponseWriter, r *http.Request) {
	if _, err := authapi.SubjectFromContext(r.Context()); err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}
	var body beginBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if err := validateGroupKey(body.Repository, body.Commit, body.GHRunID, body.Framework, body.Name, body.Branch); err != nil {
		api.WriteError(w, r, err)
		return
	}
	if body.TotalReportsExpected <= 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
			"total_reports_expected is required and must be > 0")
		return
	}
	runAttempt := firstNonEmptyStr(body.GHRunAttempt, "1")
	total := body.TotalReportsExpected

	groupID, created, err := upsertReportGroup(r.Context(), h.Pool,
		body.Repository, body.Commit, body.GHRunID, runAttempt, body.Framework,
		body.Name, body.Branch, body.GHPRNumber, &total, nil)
	if err != nil {
		if errors.Is(err, errExpectedReportsMismatch) {
			api.WriteErrorCode(w, http.StatusConflict, "EXPECTED_REPORTS_MISMATCH",
				err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}
	if created {
		ref := refFromBranch(body.Branch)
		actor := actorFromContext(r.Context())
		h.Publisher.ReportCreated(groupID, body.Framework, body.Repository, ref,
			body.Commit, actor, body.GHRunID, body.GHPRNumber, time.Now().UTC())
	}
	h.refreshGroupSummaryBestEffort(r.Context(), groupID)
	writeJSON(w, http.StatusOK, map[string]any{"report_id": groupID.String()})
}

// Register serves POST /api/v1/reports/register. Looks up (or upserts) the
// report_group then creates one reports row under it. Declared file paths are
// validated (no traversal, size caps). Returns the uuids the uploader needs
// to stream files back to the server.
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	sub, err := authapi.SubjectFromContext(r.Context())
	if err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}
	var body registerBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if err := validateGroupKey(body.Repository, body.Commit, body.GHRunID, body.Framework, body.Name, body.Branch); err != nil {
		api.WriteError(w, r, err)
		return
	}
	if body.TotalReportsExpected != nil && *body.TotalReportsExpected <= 0 {
		api.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
			"total_reports_expected, when supplied, must be > 0")
		return
	}
	runAttempt := firstNonEmptyStr(body.GHRunAttempt, "1")

	var env json.RawMessage
	if len(body.EnvironmentMetadata) > 0 {
		env = body.EnvironmentMetadata
	}
	groupID, created, err := upsertReportGroup(r.Context(), h.Pool,
		body.Repository, body.Commit, body.GHRunID, runAttempt, body.Framework,
		body.Name, body.Branch, body.GHPRNumber, body.TotalReportsExpected, env)
	if err != nil {
		if errors.Is(err, errExpectedReportsMismatch) {
			api.WriteErrorCode(w, http.StatusConflict, "EXPECTED_REPORTS_MISMATCH",
				err.Error())
			return
		}
		api.WriteError(w, r, err)
		return
	}
	if created {
		ref := refFromBranch(body.Branch)
		actor := actorFromContext(r.Context())
		h.Publisher.ReportCreated(groupID, body.Framework, body.Repository, ref,
			body.Commit, actor, body.GHRunID, body.GHPRNumber, time.Now().UTC())
	}

	// Validate declared files; reject anything that'd traverse or exceeds caps.
	acceptedJSON, rejectedJSON := validateDeclaredFiles(body.JSONFiles, h.MaxUploadBytes)
	acceptedSS, rejectedSS := validateDeclaredFiles(body.Screenshots, h.MaxArtifactBytes)

	// Create (or find existing) reports row for this gh_job_id.
	var apiKeyID *uuid.UUID
	var oidcSub *string
	if sub.Kind == "apikey" {
		apiKeyID = &sub.APIKeyID
	}
	if sub.Kind == "oidc" {
		s := sub.OIDCSubject
		oidcSub = &s
	}
	var jobID *string
	if body.GHJobID != "" {
		jobID = &body.GHJobID
	}
	var jobName *string
	if body.GHJobName != "" {
		jobName = &body.GHJobName
	}
	name := firstNonEmptyStr(body.GHJobName, body.Name)

	var (
		reportID      uuid.UUID
		reportCreated bool
	)
	err = h.Pool.QueryRow(r.Context(), `
		INSERT INTO reports (report_group_id, name, status, gh_job_id, gh_job_name,
		                     json_upload_status, screenshots_upload_status,
		                     uploaded_by_api_key_id, uploaded_by_oidc_subject)
		VALUES ($1,$2,'processing',$3,$4,'started',$5,$6,$7)
		ON CONFLICT (report_group_id, gh_job_id) WHERE gh_job_id IS NOT NULL
		  DO UPDATE SET status='processing',
		                gh_job_name=EXCLUDED.gh_job_name,
		                updated_at=now()
		RETURNING id, (xmax = 0) AS created
	`, groupID, name, jobID, jobName,
		ssStartedIf(len(acceptedSS)), apiKeyID, oidcSub).Scan(&reportID, &reportCreated)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	// Count existing reports in group (diagnostic; web surfaces it).
	var reportsInGroup int
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM reports WHERE report_group_id = $1`, groupID).Scan(&reportsInGroup)

	if reportCreated {
		h.Publisher.ReportRegistered(groupID, reportID, name, body.GHJobID, body.GHJobName, "processing", time.Now().UTC())
	} else {
		h.Publisher.ReportEntryUpdated(groupID, reportID, "processing", time.Now().UTC())
	}
	h.refreshGroupSummaryBestEffort(r.Context(), groupID)

	writeJSON(w, http.StatusOK, map[string]any{
		"report_id":            groupID.String(),
		"upload_id":            reportID.String(),
		"reports_in_group":     reportsInGroup,
		"accepted_json_files":  fileNames(acceptedJSON),
		"rejected_json_files":  rejectedJSON,
		"accepted_screenshots": fileNames(acceptedSS),
		"rejected_screenshots": rejectedSS,
	})
}

// UploadJSON serves POST /api/v1/reports/upload/{rid}/{uid}/json. Streams each
// part of the multipart body to S3 and records a report_json_files row per
// accepted file.
func (h *Handlers) UploadJSON(w http.ResponseWriter, r *http.Request) {
	if _, err := authapi.SubjectFromContext(r.Context()); err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}
	groupID, err := uuid.Parse(chi.URLParam(r, "rid"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	reportID, err := uuid.Parse(chi.URLParam(r, "uid"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	if err := requireReportInGroup(r.Context(), h.Pool, groupID, reportID); err != nil {
		api.WriteError(w, r, err)
		return
	}

	uploaded, total, err := h.streamJSONFiles(r, reportID)
	if err != nil {
		_, _ = h.Pool.Exec(r.Context(),
			`UPDATE reports SET json_upload_status='failed', status='failed', error_message=$2, updated_at=now() WHERE id=$1`,
			reportID, err.Error())
		h.Publisher.ReportEntryUpdated(groupID, reportID, "failed", time.Now().UTC())
		api.WriteError(w, r, fmt.Errorf("%w: %v", api.ErrBadRequest, err))
		return
	}
	_, _ = h.Pool.Exec(r.Context(),
		`UPDATE reports SET json_upload_status='completed', updated_at=now() WHERE id=$1`, reportID)
	bumpGroupLastUpload(r.Context(), h.Pool, groupID)

	// Extract the uploaded JSON into suites + test_cases and update the
	// report's aggregate counts. Fire suites_available if anything parsed;
	// ReportEntryUpdated("processing") even on zero-suite JSON so the UI's
	// status icon still ticks forward.
	suiteCount, extractErr := h.extractReport(r.Context(), groupID, reportID)
	if extractErr != nil && h.Logger != nil {
		h.Logger.Warn("json extraction failed",
			slog.String("report_id", reportID.String()),
			slog.String("error", extractErr.Error()))
	}
	autoCompleted := h.tryAutoFinalize(r.Context(), groupID, reportID, "json")
	if !autoCompleted {
		h.Publisher.ReportEntryUpdated(groupID, reportID, "processing", time.Now().UTC())
	}
	if suiteCount > 0 {
		h.Publisher.SuitesAvailable(reportID, suiteCount)
	}
	h.refreshGroupSummaryBestEffort(r.Context(), groupID)

	writeJSON(w, http.StatusOK, map[string]any{
		"files_uploaded": uploaded,
		"files_total":    total,
		"suites_parsed":  suiteCount,
	})
}

// UploadScreenshots serves POST /api/v1/reports/upload/{rid}/{uid}/screenshots.
func (h *Handlers) UploadScreenshots(w http.ResponseWriter, r *http.Request) {
	if _, err := authapi.SubjectFromContext(r.Context()); err != nil {
		api.WriteError(w, r, api.ErrUnauthorized)
		return
	}
	groupID, err := uuid.Parse(chi.URLParam(r, "rid"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	reportID, err := uuid.Parse(chi.URLParam(r, "uid"))
	if err != nil {
		api.WriteError(w, r, api.ErrBadRequest)
		return
	}
	if err := requireReportInGroup(r.Context(), h.Pool, groupID, reportID); err != nil {
		api.WriteError(w, r, err)
		return
	}

	uploaded, total, err := h.streamScreenshots(r, reportID)
	if err != nil {
		_, _ = h.Pool.Exec(r.Context(),
			`UPDATE reports SET screenshots_upload_status='failed', status='failed', error_message=$2, updated_at=now() WHERE id=$1`,
			reportID, err.Error())
		h.Publisher.ReportEntryUpdated(groupID, reportID, "failed", time.Now().UTC())
		api.WriteError(w, r, fmt.Errorf("%w: %v", api.ErrBadRequest, err))
		return
	}
	_, _ = h.Pool.Exec(r.Context(),
		`UPDATE reports SET screenshots_upload_status='completed', updated_at=now() WHERE id=$1`, reportID)
	bumpGroupLastUpload(r.Context(), h.Pool, groupID)

	// Best-effort link against whatever test_cases exist already. If JSON hasn't
	// been extracted yet, the linker runs again at end-of-UploadJSON and picks
	// these up from the still-unlinked pool.
	if linked, err := ingest.LinkScreenshots(r.Context(), h.Pool, reportID); err != nil && h.Logger != nil {
		h.Logger.Warn("screenshot link failed", slog.String("report_id", reportID.String()), slog.String("error", err.Error()))
	} else if linked > 0 && h.Logger != nil {
		h.Logger.Debug("screenshots linked", slog.Int("count", linked), slog.String("report_id", reportID.String()))
	}

	if !h.tryAutoFinalize(r.Context(), groupID, reportID, "screenshots") {
		h.Publisher.ReportEntryUpdated(groupID, reportID, "processing", time.Now().UTC())
	}
	h.refreshGroupSummaryBestEffort(r.Context(), groupID)

	writeJSON(w, http.StatusOK, map[string]any{
		"files_uploaded": uploaded,
		"files_total":    total,
	})
}

// ---------- internals ----------

// errExpectedReportsMismatch signals that an upsert was attempted with a
// total_reports_expected value that disagrees with what the existing row
// already stores. Surfaced as 409 EXPECTED_REPORTS_MISMATCH.
var errExpectedReportsMismatch = errors.New("total_reports_expected does not match existing report_group")

// bumpGroupLastUpload best-effort marks a report_group as recently active so
// the staleness reaper won't reap it. Called at the tail of every successful
// per-shard upload (json or screenshots). Errors are swallowed — a missed
// bump just means the reaper might mark the group `incomplete` slightly
// sooner; data correctness is unaffected.
func bumpGroupLastUpload(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) {
	_, _ = pool.Exec(ctx,
		`UPDATE report_groups SET last_upload_at = now() WHERE id = $1`, groupID)
}

// upsertReportGroup upserts by the composite grouping key and returns
// (id, createdNow). branch only updates when the existing row has an empty
// branch — /begin passes branch="" but /register knows the real value.
//
// totalReportsExpected is nullable: nil means "the caller didn't declare a
// shard count." On insert with nil, the column stays NULL (group
// auto-completes on any per-shard upload finalization). On update the
// existing value wins (frozen-on-insert). When both stored and requested
// are non-nil and disagree, returns errExpectedReportsMismatch so a
// mismatched workflow surfaces as 409 instead of silently first-winning.
func upsertReportGroup(
	ctx context.Context, pool *pgxpool.Pool,
	repository, commit, runID, runAttempt, framework, name, branch string,
	prNumber *int, totalReportsExpected *int, env json.RawMessage,
) (uuid.UUID, bool, error) {
	var id uuid.UUID
	var created bool
	var storedTotal *int
	err := pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt, gh_pr_number, total_reports_expected, environment_metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, NULLIF($10::text,'')::jsonb)
		ON CONFLICT (repository, commit_sha, gh_run_id, name, gh_run_attempt)
		  DO UPDATE SET updated_at = now(),
		                branch = CASE WHEN report_groups.branch = '' THEN EXCLUDED.branch ELSE report_groups.branch END,
		                gh_pr_number = COALESCE(report_groups.gh_pr_number, EXCLUDED.gh_pr_number),
		                environment_metadata = COALESCE(report_groups.environment_metadata, EXCLUDED.environment_metadata)
		RETURNING id, (xmax = 0) AS created, total_reports_expected
	`, framework, name, repository, branch, commit, runID, runAttempt, prNumber, totalReportsExpected, string(env)).Scan(&id, &created, &storedTotal)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("upsert report_group: %w", err)
	}
	if !created && totalReportsExpected != nil && storedTotal != nil && *storedTotal != *totalReportsExpected {
		return uuid.Nil, false, fmt.Errorf("%w: requested=%d stored=%d",
			errExpectedReportsMismatch, *totalReportsExpected, *storedTotal)
	}
	return id, created, nil
}

// refFromBranch turns "main" → "refs/heads/main" so the event payload looks
// like a real GitHub OIDC ref.
func refFromBranch(branch string) string {
	if branch == "" {
		return ""
	}
	if strings.HasPrefix(branch, "refs/") {
		return branch
	}
	return "refs/heads/" + branch
}

// extractReport pulls every uploaded JSON file for reportID from S3, runs the
// framework-specific parser, consolidates into suites + test_cases, and runs
// the screenshot linker to resolve anything UploadScreenshots already staged.
// Returns the number of suites persisted.
//
// Multi-batch idempotency: if the same JSON files are re-uploaded (or
// additional batches arrive), we wipe the existing suites for the report
// first so we don't duplicate rows — cascades clear test_cases.
func (h *Handlers) extractReport(ctx context.Context, groupID, reportID uuid.UUID) (int, error) {
	var framework string
	if err := h.Pool.QueryRow(ctx,
		`SELECT g.framework FROM report_groups g
		 WHERE g.id = $1`, groupID).Scan(&framework); err != nil {
		return 0, fmt.Errorf("fetch framework: %w", err)
	}

	jsonRows, err := h.Pool.Query(ctx,
		`SELECT object_key FROM report_json_files WHERE report_id = $1 ORDER BY created_at`, reportID)
	if err != nil {
		return 0, fmt.Errorf("list report_json_files: %w", err)
	}
	var keys []string
	for jsonRows.Next() {
		var k string
		if err := jsonRows.Scan(&k); err != nil {
			jsonRows.Close()
			return 0, err
		}
		keys = append(keys, k)
	}
	jsonRows.Close()
	if len(keys) == 0 {
		return 0, nil
	}

	if _, err := h.Pool.Exec(ctx, `DELETE FROM suites WHERE report_id = $1`, reportID); err != nil {
		return 0, fmt.Errorf("wipe suites: %w", err)
	}

	var allSuites []ingest.ExtractedSuite
	var reportStart, reportEnd *time.Time
	seq := 0
	for _, key := range keys {
		rc, _, err := h.Store.Get(ctx, key)
		if err != nil {
			if h.Logger != nil {
				h.Logger.Warn("fetch json from store",
					slog.String("object_key", key), slog.String("error", err.Error()))
			}
			continue
		}
		body, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			if h.Logger != nil {
				h.Logger.Warn("read json body",
					slog.String("object_key", key), slog.String("error", readErr.Error()))
			}
			continue
		}
		suites, ps, pe := ingest.Extract(framework, body, &seq)
		if len(suites) == 0 {
			suites, ps, pe = ingest.Extract("", body, &seq)
		}
		allSuites = append(allSuites, suites...)
		reportStart = earlier(reportStart, ps)
		reportEnd = later(reportEnd, pe)
	}

	// Collapse duplicate suites by (file, title) before consolidation. A
	// shard that uploads multiple JSONs for the same spec — e.g. an
	// orchestration worker that ran a spec, failed, and ran the retest under
	// the same gh_job_id — would otherwise produce two suite rows on the
	// dashboard for the same file.
	allSuites = ingest.MergeSuitesByFile(allSuites)

	totals, err := ingest.Consolidate(ctx, h.Pool, reportID, allSuites, reportStart, reportEnd)
	if err != nil {
		return 0, fmt.Errorf("consolidate: %w", err)
	}
	if _, err := ingest.LinkScreenshots(ctx, h.Pool, reportID); err != nil && h.Logger != nil {
		h.Logger.Warn("link screenshots post-extract",
			slog.String("report_id", reportID.String()), slog.String("error", err.Error()))
	}
	return totals.Suites, nil
}

func earlier(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case b.Before(*a):
		return b
	default:
		return a
	}
}

func later(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case b.After(*a):
		return b
	default:
		return a
	}
}

// actorFromContext pulls the OIDC subject (or session user) from the request
// context for ReportCreated event payloads. Empty when no auth subject.
func actorFromContext(ctx context.Context) string {
	if sub, err := authapi.SubjectFromContext(ctx); err == nil {
		if sub.OIDCClaims != nil {
			return sub.OIDCClaims.Subject
		}
		return sub.OIDCSubject
	}
	return ""
}

func requireReportInGroup(ctx context.Context, pool *pgxpool.Pool, groupID, reportID uuid.UUID) error {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM reports WHERE id=$1 AND report_group_id=$2)`,
		reportID, groupID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return api.ErrNotFound
	}
	return nil
}

// streamJSONFiles reads `files` parts from the multipart body, writes each to
// S3 under reports/{reportID}/json/{path}, and records a report_json_files row
// per accepted file.
func (h *Handlers) streamJSONFiles(r *http.Request, reportID uuid.UUID) (int, int, error) {
	return h.streamMultipart(r, reportID, "json", h.MaxUploadBytes,
		func(ctx context.Context, objectKey, _ string, size int64, sum string) error {
			_, err := h.Pool.Exec(ctx, `
				INSERT INTO report_json_files (report_id, object_key, size_bytes, sha256)
				VALUES ($1,$2,$3,$4) ON CONFLICT (object_key) DO NOTHING
			`, reportID, objectKey, size, sum)
			return err
		})
}

// streamScreenshots reads `files` parts from the multipart body, writes each
// to S3 under reports/{reportID}/screenshots/{path}, and inserts a
// report_screenshots staging row (case_id NULL until LinkScreenshots runs).
func (h *Handlers) streamScreenshots(r *http.Request, reportID uuid.UUID) (int, int, error) {
	return h.streamMultipart(r, reportID, "screenshots", h.MaxArtifactBytes,
		func(ctx context.Context, objectKey, relativePath string, size int64, _ string) error {
			testName := ingest.DeriveTestNameFromPath(relativePath)
			screenshotType := ingest.DeriveScreenshotType(relativePath)
			var stype *string
			if screenshotType != "" {
				stype = &screenshotType
			}
			_, err := h.Pool.Exec(ctx, `
				INSERT INTO report_screenshots (report_id, filename, s3_key, size_bytes, test_name, screenshot_type)
				VALUES ($1,$2,$3,$4,$5,$6)
				ON CONFLICT (s3_key) DO NOTHING
			`, reportID, relativePath, objectKey, size, testName, stype)
			return err
		})
}

// streamMultipart reads parts named "files", writes each to S3 at
// reports/{reportID}/{kind}/{relativePath}, and invokes onFile for each
// accepted upload so the caller can record a typed DB row. Returns
// (uploaded_count, total_count, error).
func (h *Handlers) streamMultipart(
	r *http.Request, reportID uuid.UUID, kind string, maxBytes int64,
	onFile func(ctx context.Context, objectKey, relativePath string, size int64, sum string) error,
) (int, int, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return 0, 0, fmt.Errorf("multipart: %w", err)
	}
	uploaded := 0
	total := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return uploaded, total, err
		}
		total++
		// part.FileName() passes the filename through filepath.Base, which
		// silently strips directory components — fatal when the uploader is
		// sending relative paths like "Suite-test-chrome/test-failed-1.png".
		// Parse the raw Content-Disposition header ourselves so the directory
		// survives. See Go mime/multipart source for the stripping.
		name := rawFileName(part.Header.Get("Content-Disposition"))
		if name == "" || !safeRelativePath(name) {
			_ = part.Close()
			continue
		}
		objectKey := fmt.Sprintf("reports/%s/%s/%s", reportID, kind, name)
		contentType := part.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		buf, size, sum, err := readLimited(part, maxBytes)
		_ = part.Close()
		if err != nil {
			return uploaded, total, err
		}
		if err := h.Store.Put(r.Context(), objectKey, strings.NewReader(string(buf)), contentType, size); err != nil {
			return uploaded, total, fmt.Errorf("store.Put %s: %w", objectKey, err)
		}
		if onFile != nil {
			if err := onFile(r.Context(), objectKey, name, size, sum); err != nil {
				return uploaded, total, err
			}
		}
		uploaded++
	}
	return uploaded, total, nil
}

// readLimited reads up to maxBytes from r and returns the bytes, size, and
// hex-encoded sha256. Errors if maxBytes is exceeded.
func readLimited(r io.Reader, maxBytes int64) ([]byte, int64, string, error) {
	h := sha256.New()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 32*1024)
	var size int64
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			if size+int64(n) > maxBytes {
				return nil, 0, "", fmt.Errorf("file exceeds %d bytes", maxBytes)
			}
			size += int64(n)
			buf = append(buf, tmp[:n]...)
			_, _ = h.Write(tmp[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, "", err
		}
	}
	return buf, size, hex.EncodeToString(h.Sum(nil)), nil
}

// validateGroupKey ensures the composite identity fields are populated. Branch
// is intentionally NOT required — /reports/begin doesn't carry it;
// /reports/register is where it's set.
func validateGroupKey(repository, commit, runID, framework, name, _ string) error {
	if repository == "" || commit == "" || runID == "" || name == "" {
		return fmt.Errorf("%w: repository, commit, gh_run_id, and name are required", api.ErrBadRequest)
	}
	switch framework {
	case "playwright", "cypress", "detox", "maestro":
	default:
		return fmt.Errorf("%w: framework must be playwright|cypress|detox|maestro", api.ErrBadRequest)
	}
	return nil
}

// validateDeclaredFiles filters a declared-file list into accepted + rejected
// (with a reason) buckets.
func validateDeclaredFiles(fs []declaredFile, maxBytes int64) ([]declaredFile, []map[string]any) {
	accepted := make([]declaredFile, 0, len(fs))
	rejected := make([]map[string]any, 0)
	for _, f := range fs {
		if !safeRelativePath(f.Path) {
			rejected = append(rejected, map[string]any{"path": f.Path, "reason": "UNSAFE_PATH"})
			continue
		}
		if f.Size > maxBytes {
			rejected = append(rejected, map[string]any{"path": f.Path, "reason": "TOO_LARGE"})
			continue
		}
		accepted = append(accepted, f)
	}
	return accepted, rejected
}

// rawFileName parses the `filename` parameter out of a Content-Disposition
// header without Go's filepath.Base mangling, so uploads that carry a
// relative directory (e.g. "Suite/test-failed-1.png") keep their path.
func rawFileName(contentDisposition string) string {
	if contentDisposition == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(contentDisposition)
	if err != nil {
		return ""
	}
	return params["filename"]
}

// safeRelativePath rejects absolute paths, .. traversals, and empty segments.
func safeRelativePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	cleaned := path.Clean(p)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return false
	}
	// `path.Clean` doesn't resolve symbolic cases; an extra guard:
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

func fileNames(fs []declaredFile) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Path
	}
	return out
}

func ssStartedIf(n int) *string {
	if n == 0 {
		return nil
	}
	s := "started"
	return &s
}

// tryAutoFinalize finalizes the report (and possibly its group) once an
// upload pipeline step (json or screenshots) just landed. It first checks
// whether this report's pipeline is now fully complete (json + optional
// screenshots), and if so flips the row from `processing` to `complete`
// and publishes ReportEntryUpdated("complete"). When that flip happens, it
// then attempts to flip the parent group from `in_progress` to `completed`
// once count(reports.complete) >= total_reports_expected. Returns true
// when the per-report flip occurred — callers should skip publishing the
// usual ReportEntryUpdated("processing") event so the UI doesn't see a
// "complete → processing" sequence.
func (h *Handlers) tryAutoFinalize(ctx context.Context, groupID, reportID uuid.UUID, source string) bool {
	flipped, err := tryAutoCompleteReport(ctx, h.Pool, reportID)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("auto-complete report failed",
				slog.String("report_id", reportID.String()),
				slog.String("source", source),
				slog.String("error", err.Error()))
		}
		return false
	}
	if !flipped {
		return false
	}
	h.Publisher.ReportEntryUpdated(groupID, reportID, "complete", time.Now().UTC())
	if _, err := tryAutoCompleteGroup(ctx, h.Pool, groupID); err != nil && h.Logger != nil {
		h.Logger.Warn("auto-complete group failed",
			slog.String("group_id", groupID.String()),
			slog.String("error", err.Error()))
	}
	return true
}

// tryAutoCompleteReport flips a report from `processing` to `complete` when
// its upload pipeline has fully landed: json_upload_status='completed' AND
// screenshots_upload_status is either 'completed' or NULL (no screenshots
// were declared at /reports/register time). Idempotent — repeated calls on
// an already-`complete` row affect zero rows. Returns true when this call
// performed the flip; the caller publishes ReportEntryUpdated and considers
// auto-completing the parent group.
func tryAutoCompleteReport(ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID) (bool, error) {
	res, err := pool.Exec(ctx, `
		UPDATE reports
		   SET status = 'complete', updated_at = now()
		 WHERE id = $1
		   AND status = 'processing'
		   AND json_upload_status = 'completed'
		   AND (screenshots_upload_status IS NULL
		        OR screenshots_upload_status = 'completed')
	`, reportID)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

// tryAutoCompleteGroup flips a report_group from `in_progress` to `completed`.
// Dual-mode predicate:
//
//   - When total_reports_expected is set, flip when count(reports.complete) >=
//     total_reports_expected. The controller declared a shard count up front
//     and we wait for that many reports to land.
//   - When total_reports_expected is NULL (group was seeded via /reports/register
//     without a prior /reports/begin), flip on any per-shard upload finalization.
//     Without a declared count, "we have data" is the strongest terminal signal
//     we can offer.
//
// Idempotent — a repeat call after the flip is a no-op. The upload handlers
// gate this call on a successful per-report auto-complete, which guarantees
// the count has just incremented and the predicate has a chance of becoming
// true.
func tryAutoCompleteGroup(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) (bool, error) {
	res, err := pool.Exec(ctx, `
		UPDATE report_groups
		   SET status = 'completed', updated_at = now()
		 WHERE id = $1
		   AND status = 'in_progress'
		   AND (
		       total_reports_expected IS NULL
		       OR (SELECT count(*) FROM reports
		            WHERE report_group_id = $1 AND status = 'complete') >= total_reports_expected
		   )
	`, groupID)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}
