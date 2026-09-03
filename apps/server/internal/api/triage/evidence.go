package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/testhistory"
)

const (
	maxEvidenceRows   = 2000
	maxHistoryLookups = 15
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
	// W9 — the run configuration this group executed under (captured at
	// register; feature flags, edition, notable env). The agent and the
	// deterministic config-delta pre-tag read it from here.
	EnvironmentMetadata json.RawMessage `json:"environment_metadata,omitempty"`
}

type evidenceShot struct {
	S3Key          string `json:"s3_key"`
	ScreenshotType string `json:"screenshot_type,omitempty"`
	URL            string `json:"url"`
}

type evidenceFailure struct {
	ExternalTestID   *string                     `json:"external_test_id,omitempty"`
	FullTitle        string                      `json:"full_title"`
	Title            string                      `json:"title"`
	File             *string                     `json:"file,omitempty"`
	Status           string                      `json:"status"`
	RetryCount       int                         `json:"retry_count"`
	DurationMs       int64                       `json:"duration_ms"`
	ErrorMessage     *string                     `json:"error_message,omitempty"`
	ErrorStack       *string                     `json:"error_stack,omitempty"`
	Screenshots      []evidenceShot              `json:"screenshots"`
	History          *testhistory.HistorySummary `json:"history,omitempty"`
	HistoryError     *string                     `json:"history_error,omitempty"`
	DistinctPRs      *int                        `json:"distinct_prs,omitempty"`
	DistinctBranches *int                        `json:"distinct_branches,omitempty"`
	Suggested        Suggestion                  `json:"suggested"`
	// W9 — captured run-config keys that differ from the last passing run
	// for this test. Absent when either side has no captured config.
	ConfigDelta []string `json:"config_delta,omitempty"`
}

// Evidence serves GET /api/v1/triage/evidence — one payload an agent needs to
// decide whether a run's failures are flakes, without a rerun.
//
// It is the reports API (this run's errors and screenshots) joined to the
// history API (what usually happens to this test). The deterministic
// suggestion is computed here so every consumer branches on the same rules.
func (h *Handlers) Evidence(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx := r.Context()

	g, err := h.findEvidenceGroup(ctx, q.Get("group_id"), q.Get("repository"),
		q.Get("commit_sha"), q.Get("gh_run_id"), q.Get("name"), q.Get("gh_run_attempt"))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	baseline := orDefault(q.Get("baseline_branch"), "main")
	historySince, err := parseSince(orDefault(q.Get("window"), "30d"))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	elsewhereSince, err := parseSince(orDefault(q.Get("elsewhere_window"), "24h"))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}

	failures, rowTruncated, err := h.loadEvidenceFailures(ctx, g.ID)
	if err != nil {
		h.logError("triage evidence failures", err)
		api.WriteError(w, r, api.ErrInternal)
		return
	}

	clusters, clusterTruncated := clusterFailures(failures)
	lookups := 0
	excludePR := -1
	if g.GHPRNumber != nil {
		excludePR = *g.GHPRNumber
	}
	for i := range clusters {
		c := &clusters[i]
		f := &c.Representative
		// W9 — run-config delta vs the last passing run for this test.
		// Computed before Suggest so the deterministic pre-tag can fire;
		// a lookup failure degrades to nil (never a signal on its own).
		f.ConfigDelta = h.configDeltaFor(ctx, g, f)
		if f.ExternalTestID == nil || lookups >= maxHistoryLookups {
			c.Suggested = Suggest(signalsFor(f))
			f.Suggested = c.Suggested
			continue
		}
		lookups++
		testID := *f.ExternalTestID
		summary, histErr := testhistory.LookupSummary(ctx, h.Pool, testID, g.Repository, baseline, g.Framework, historySince)
		if histErr != nil {
			msg := histErr.Error()
			f.HistoryError = &msg
		} else {
			f.History = &summary
		}
		prs, branches, elseErr := testhistory.LookupElsewhereCounts(ctx, h.Pool, testID, g.Repository, excludePR, elsewhereSince)
		if elseErr == nil {
			f.DistinctPRs = &prs
			f.DistinctBranches = &branches
		}
		c.Suggested = Suggest(signalsFor(f))
		f.Suggested = c.Suggested
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"group":         g,
		"failure_count": len(failures),
		"cluster_count": len(clusters),
		"clusters":      clusters,
		"truncated":     rowTruncated || clusterTruncated,
		"lookups":       lookups,
		"max_lookups":   maxHistoryLookups,
	})
}

func signalsFor(f *evidenceFailure) Signals {
	s := Signals{Status: f.Status, HasStableID: f.ExternalTestID != nil}
	if f.HistoryError == nil && f.History != nil {
		s.HistoryOK = true
		s.Runs = f.History.Runs
		s.Failed = f.History.Failed
		s.Flaky = f.History.Flaky
		s.Flips = f.History.Flips
		s.FailureRate = f.History.FailureRate
		s.FailingSinceCommit = f.History.FailingSinceCommit != nil
	}
	if f.DistinctPRs != nil {
		s.ElsewhereOK = true
		s.DistinctPRs = *f.DistinctPRs
	}
	s.ConfigDeltaKeys = f.ConfigDelta
	return s
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
		h.logError("triage evidence group lookup", err)
		return g, api.ErrInternal
	}
	return g, nil
}

type rawFailure struct {
	ExternalTestID *string
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
		SELECT tc.external_test_id,
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
	for rows.Next() {
		var raw rawFailure
		if err := rows.Scan(&raw.ExternalTestID, &raw.FullTitle, &raw.Title, &raw.File,
			&raw.Status, &raw.RetryCount, &raw.DurationMs, &raw.ErrorMessage, &raw.ErrorStack, &raw.ShotsJSON); err != nil {
			return nil, false, err
		}
		shots := parseShots(raw.ShotsJSON)
		f := evidenceFailure{
			ExternalTestID: raw.ExternalTestID,
			FullTitle:      raw.FullTitle,
			Title:          raw.Title,
			File:           raw.File,
			Status:         raw.Status,
			RetryCount:     raw.RetryCount,
			DurationMs:     raw.DurationMs,
			ErrorMessage:   raw.ErrorMessage,
			ErrorStack:     raw.ErrorStack,
			Screenshots:    shots,
		}
		key := raw.FullTitle
		if raw.ExternalTestID != nil && *raw.ExternalTestID != "" {
			key = "id:" + *raw.ExternalTestID
		}
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

	truncated := len(order) > maxEvidenceRows
	if truncated {
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

// configDeltaFor returns the captured run-config keys whose values differ
// between this group and the most recent earlier PASSING run for this test
// on the same branch. No captured config on either side → nil (fail closed:
// absence of evidence is never a signal).
func (h *Handlers) configDeltaFor(ctx context.Context, g evidenceGroup, f *evidenceFailure) []string {
	if len(g.EnvironmentMetadata) == 0 || f == nil || f.ExternalTestID == nil {
		return nil
	}
	var baselineEnv []byte
	err := h.Pool.QueryRow(ctx, `
		SELECT g2.environment_metadata
		FROM report_groups g2
		JOIN reports r ON r.report_group_id = g2.id
		JOIN suites s ON s.report_id = r.id
		JOIN test_cases tc ON tc.suite_id = s.id
		WHERE tc.external_test_id = $1
		  AND (g2.repository = $2 OR split_part(g2.repository, '/', 2) = $2)
		  AND g2.branch = $3
		  AND g2.created_at < (SELECT created_at FROM report_groups WHERE id::text = $4)
		  AND tc.status = 'passed'
		  AND g2.environment_metadata IS NOT NULL
		ORDER BY g2.created_at DESC
		LIMIT 1
	`, *f.ExternalTestID, g.Repository, g.Branch, g.ID).Scan(&baselineEnv)
	if err != nil || len(baselineEnv) == 0 {
		return nil
	}
	return envDeltaKeys(g.EnvironmentMetadata, baselineEnv)
}

// envDeltaKeys is the pure W9 compare: keys whose values differ between two
// captured configs. Malformed JSON on either side → nil, never an error.
func envDeltaKeys(current, baseline []byte) []string {
	var cur, base map[string]any
	if json.Unmarshal(current, &cur) != nil || json.Unmarshal(baseline, &base) != nil {
		return nil
	}
	if cur == nil || base == nil {
		return nil
	}
	keys := map[string]bool{}
	for k, v := range cur {
		bv, ok := base[k]
		if !ok || !reflect.DeepEqual(v, bv) {
			keys[k] = true
		}
	}
	for k := range base {
		if _, ok := cur[k]; !ok {
			keys[k] = true
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
