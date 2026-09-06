package testhistory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

const (
	maxEvidenceRows = 2000
	statusFlaky     = "flaky"
)

type evidenceGroup struct {
	ID           string `json:"id"`
	Repository   string `json:"repository"`
	Branch       string `json:"branch"`
	CommitSHA    string `json:"commit_sha"`
	GHRunID      string `json:"gh_run_id"`
	GHRunAttempt string `json:"gh_run_attempt"`
	GHPRNumber   *int   `json:"gh_pr_number,omitempty"`
	Framework    string `json:"framework"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	// The run configuration this group executed under, as captured at
	// register time (feature flags, edition, notable env).
	EnvironmentMetadata json.RawMessage `json:"environment_metadata,omitempty"`
}

type evidenceShot struct {
	S3Key          string `json:"s3_key"`
	ScreenshotType string `json:"screenshot_type,omitempty"`
	URL            string `json:"url"`
}

type evidenceFailure struct {
	ExternalTestID *string `json:"external_test_id,omitempty"`
	// StableKey is the identity to hand to /tests/history: the MM-T id where
	// one exists, the full title where it does not. Present on every failure,
	// so a consumer never has to decide which a repository uses.
	StableKey    string         `json:"stable_key"`
	FullTitle    string         `json:"full_title"`
	Title        string         `json:"title"`
	File         *string        `json:"file,omitempty"`
	Status       string         `json:"status"`
	RetryCount   int            `json:"retry_count"`
	DurationMs   int64          `json:"duration_ms"`
	ErrorMessage *string        `json:"error_message,omitempty"`
	ErrorStack   *string        `json:"error_stack,omitempty"`
	Screenshots  []evidenceShot `json:"screenshots"`
}

// Evidence serves GET /api/v1/tests/evidence — one run's failures with their
// error, stack and screenshots, grouped by normalized error text.
//
// Identify the run with `group_id`, or with the same composite identity the
// upload used: repository + commit_sha + gh_run_id + name (+ gh_run_attempt,
// default "1").
func (h *Handlers) Evidence(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx := r.Context()

	g, err := h.findEvidenceGroup(ctx, q.Get("group_id"), q.Get("repository"),
		q.Get("commit_sha"), q.Get("gh_run_id"), q.Get("name"), q.Get("gh_run_attempt"))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	failures, rowTruncated, err := h.loadEvidenceFailures(ctx, g.ID)
	if err != nil {
		h.logError("tests evidence failures", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}
	clusters, clusterTruncated := clusterFailures(failures)

	writeJSON(w, http.StatusOK, map[string]any{
		"group":         g,
		"failure_count": len(failures),
		"cluster_count": len(clusters),
		"clusters":      clusters,
		"truncated":     rowTruncated || clusterTruncated,
	})
}

func (h *Handlers) findEvidenceGroup(ctx context.Context, groupID, repo, commit, runID, name, attempt string) (evidenceGroup, error) {
	var g evidenceGroup
	var id uuid.UUID
	var err error
	if groupID != "" {
		id, err = uuid.Parse(groupID)
		if err != nil {
			return g, fmt.Errorf("%w: group_id must be a UUID", api.ErrBadRequest)
		}
		err = h.Pool.QueryRow(ctx, `
			SELECT id::text, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
			       gh_pr_number, framework, name, status, environment_metadata
			FROM report_groups WHERE id = $1
		`, id).Scan(&g.ID, &g.Repository, &g.Branch, &g.CommitSHA, &g.GHRunID, &g.GHRunAttempt,
			&g.GHPRNumber, &g.Framework, &g.Name, &g.Status, &g.EnvironmentMetadata)
	} else {
		if repo == "" || commit == "" || runID == "" || name == "" {
			return g, fmt.Errorf("%w: group_id or repository+commit_sha+gh_run_id+name is required", api.ErrBadRequest)
		}
		if attempt == "" {
			attempt = "1"
		}
		err = h.Pool.QueryRow(ctx, `
			SELECT id::text, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
			       gh_pr_number, framework, name, status, environment_metadata
			FROM report_groups
			WHERE (repository = $1 OR split_part(repository, '/', 2) = $1)
			  AND commit_sha = $2 AND gh_run_id = $3 AND name = $4 AND gh_run_attempt = $5
		`, repo, commit, runID, name, attempt).Scan(&g.ID, &g.Repository, &g.Branch, &g.CommitSHA,
			&g.GHRunID, &g.GHRunAttempt, &g.GHPRNumber, &g.Framework, &g.Name, &g.Status, &g.EnvironmentMetadata)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return g, api.ErrNotFound
		}
		h.logError("tests evidence group lookup", err)
		return g, api.ErrInternal
	}
	return g, nil
}

type rawFailure struct {
	ExternalTestID *string
	StableKey      string
	FullTitle      string
	Title          string
	File           *string
	Status         string
	RetryCount     int
	DurationMs     int64
	ErrorMessage   *string
	ErrorStack     *string
	ShotsJSON      []byte
}

func (h *Handlers) loadEvidenceFailures(ctx context.Context, groupID string) ([]evidenceFailure, bool, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT tc.external_test_id, tc.stable_key,
		       COALESCE(NULLIF(tc.full_title, ''), tc.title),
		       tc.title,
		       s.file,
		       tc.status,
		       tc.retry_count,
		       COALESCE(tc.duration_ms, 0),
		       tc.error_message,
		       tc.error_stack,
		       COALESCE(ss.shots, '[]'::jsonb)
		FROM test_cases tc
		JOIN suites s ON s.id = tc.suite_id
		JOIN reports r ON r.id = s.report_id
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(jsonb_build_object(
				's3_key', rs.s3_key,
				'screenshot_type', COALESCE(rs.screenshot_type, '')
			) ORDER BY rs.sequence, rs.created_at) AS shots
			FROM report_screenshots rs
			WHERE rs.case_id = tc.id
		) ss ON TRUE
		WHERE r.report_group_id = $1::uuid
		  AND tc.status IN ('failed', 'timedOut', 'interrupted', 'flaky')
		ORDER BY s.ordinal, tc.ordinal
		LIMIT $2
	`, groupID, maxEvidenceRows+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	byKey := map[string]evidenceFailure{}
	order := []string{}
	// Counted separately from len(order) because rows merge: a sharded suite
	// reports the same test once per shard, and those collapse into one key.
	// Truncation is a fact about what the query returned, so measuring it by
	// distinct keys under-reports it exactly when sharding is heaviest.
	scanned := 0
	for rows.Next() {
		var raw rawFailure
		if err := rows.Scan(&raw.ExternalTestID, &raw.StableKey, &raw.FullTitle, &raw.Title, &raw.File,
			&raw.Status, &raw.RetryCount, &raw.DurationMs, &raw.ErrorMessage, &raw.ErrorStack, &raw.ShotsJSON); err != nil {
			return nil, false, err
		}
		scanned++
		f := evidenceFailure{
			ExternalTestID: raw.ExternalTestID,
			StableKey:      raw.StableKey,
			FullTitle:      raw.FullTitle,
			Title:          raw.Title,
			File:           raw.File,
			Status:         raw.Status,
			RetryCount:     raw.RetryCount,
			DurationMs:     raw.DurationMs,
			ErrorMessage:   raw.ErrorMessage,
			ErrorStack:     raw.ErrorStack,
			Screenshots:    parseShots(raw.ShotsJSON),
		}
		// stable_key already resolves MM-T-id-else-title, so merging shards by
		// it is the same identity /tests/history uses.
		key := raw.StableKey
		if existing, ok := byKey[key]; ok {
			byKey[key] = mergeFailure(existing, f)
			continue
		}
		byKey[key] = f
		order = append(order, key)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	// The query asks for maxEvidenceRows+1 precisely so that overflow is
	// visible here.
	truncated := scanned > maxEvidenceRows
	if len(order) > maxEvidenceRows {
		order = order[:maxEvidenceRows]
	}
	out := make([]evidenceFailure, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out, truncated, nil
}

func mergeFailure(a, b evidenceFailure) evidenceFailure {
	// Prefer a hard failure's error over a flaky sibling, and keep whichever
	// row actually captured screenshots.
	if a.Status == statusFlaky && b.Status != statusFlaky {
		b.Screenshots = firstShots(b.Screenshots, a.Screenshots)
		return b
	}
	if len(a.Screenshots) == 0 && len(b.Screenshots) > 0 {
		a.Screenshots = b.Screenshots
	}
	if a.ErrorMessage == nil && b.ErrorMessage != nil {
		a.ErrorMessage = b.ErrorMessage
		a.ErrorStack = b.ErrorStack
	}
	return a
}

func firstShots(a, b []evidenceShot) []evidenceShot {
	if len(a) > 0 {
		return a
	}
	return b
}

func parseShots(raw []byte) []evidenceShot {
	type shotRow struct {
		S3Key          string `json:"s3_key"`
		ScreenshotType string `json:"screenshot_type"`
	}
	var rows []shotRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return []evidenceShot{}
	}
	out := make([]evidenceShot, 0, len(rows))
	for _, r := range rows {
		if r.S3Key == "" {
			continue
		}
		out = append(out, evidenceShot{
			S3Key:          r.S3Key,
			ScreenshotType: r.ScreenshotType,
			URL:            "/files/" + r.S3Key,
		})
	}
	return out
}
