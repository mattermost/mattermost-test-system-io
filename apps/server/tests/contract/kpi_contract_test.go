//go:build e2e
// +build e2e

package contract

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/kpi"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestKPIContract(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())
	doc, opts := loadSpec(t)
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	c := &specClient{env: env, doc: doc, router: router, opts: opts}

	// Empty DB -> 200 conforming to OpenAPI.
	exchange(t, c, http.MethodGet, "/api/v1/kpi/summary", "", nil, nil, http.StatusOK, true)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/flaky", "", nil, nil, http.StatusOK, true)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/prs/red", "", nil, nil, http.StatusOK, true)

	// Invalid query params -> 400 conforming to OpenAPI (validateReq=false since request intentionally violates schema).
	exchange(t, c, http.MethodGet, "/api/v1/kpi/summary?window=99d", "", nil, nil, http.StatusBadRequest, false)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/summary?stage=invalid", "", nil, nil, http.StatusBadRequest, false)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/summary?sort=bogus", "", nil, nil, http.StatusBadRequest, false)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/flaky?window=5d", "", nil, nil, http.StatusBadRequest, false)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/flaky?limit=500", "", nil, nil, http.StatusBadRequest, false)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/prs/red?window=5d", "", nil, nil, http.StatusBadRequest, false)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/prs/red?sort=duration_desc", "", nil, nil, http.StatusBadRequest, false)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/prs/red?limit=500", "", nil, nil, http.StatusBadRequest, false)

	// Seed a real flaky test across 2 commits + PR duration + main pass rate + configured PR false-red rerun, refresh rollups, and verify 200 contract.
	ctx := t.Context()
	now := time.Now().UTC()
	var testID uuid.UUID
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO tests (repository, framework, test_key, file, full_title)
		VALUES ('mattermost/mattermost', 'playwright', 'h:contract-flaky-1', 'e2e/login.spec.ts', 'Login > works')
		RETURNING id
	`).Scan(&testID); err != nil {
		t.Fatalf("insert test: %v", err)
	}

	seedGroup := func(stage, lane, sha, runID, attempt string, prNum *int, durMs *int64, createdAt time.Time, statuses []string, withOrch bool) {
		var gid, rid, sid uuid.UUID
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO report_groups (
				framework, name, status, repository, branch, commit_sha, gh_run_id, gh_run_attempt,
				gh_pr_number, stage, total_duration_ms, created_at, updated_at, last_upload_at
			) VALUES ('playwright', $1, 'completed', 'mattermost/mattermost', 'main', $2, $3, $4, $5, $6, $7, $8, $8, $8)
			RETURNING id
		`, lane, sha, runID, attempt, prNum, stage, durMs, createdAt).Scan(&gid); err != nil {
			t.Fatalf("insert group: %v", err)
		}
		if withOrch {
			if _, err := env.Pool.Exec(ctx, `
				INSERT INTO orchestration_runs (
					repository, commit_sha, gh_run_id, name, gh_run_attempt, framework,
					lease_timeout_ms, idle_timeout_ms, status,
					pending_count, completed_pass_count, total_units,
					dispatch_units_hash, started_at, last_activity_at, terminal_at, owner_oidc_subject
				) VALUES (
					'mattermost/mattermost', $1, $2, $3, $4, 'playwright',
					60000, 60000, 'completed',
					0, 1, 1,
					'\xdeadbeef'::bytea, $5, $5, $5, 'contract-oidc'
				)
			`, sha, runID, lane, attempt, createdAt); err != nil {
				t.Fatalf("insert orchestration_run: %v", err)
			}
		}
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO reports (report_group_id, name, status, created_at, updated_at)
			VALUES ($1, $2, 'complete', $3, $3) RETURNING id
		`, gid, lane, createdAt).Scan(&rid); err != nil {
			t.Fatalf("insert report: %v", err)
		}
		if err := env.Pool.QueryRow(ctx, `
			INSERT INTO suites (report_id, title, file, ordinal)
			VALUES ($1, 'Login', 'e2e/login.spec.ts', 0) RETURNING id
		`, rid).Scan(&sid); err != nil {
			t.Fatalf("insert suite: %v", err)
		}
		for i, st := range statuses {
			if _, err := env.Pool.Exec(ctx, `
				INSERT INTO test_cases (suite_id, title, full_title, status, retry_count, ordinal, test_id)
				VALUES ($1, 'works', 'Login > works', $2, $3, $3, $4)
			`, sid, st, i, testID); err != nil {
				t.Fatalf("insert test_case: %v", err)
			}
		}
		if err := kpi.RefreshGroupRollups(ctx, env.Pool, gid); err != nil {
			t.Fatalf("RefreshGroupRollups: %v", err)
		}
	}

	pr := 42
	dur := int64(12345)
	seedGroup("main", "e2e-main", "sha-contract-1", "run-1", "1", nil, nil, now.Add(-2*time.Hour), []string{"failed", "passed"}, false)
	seedGroup("pr", "e2e-pr", "sha-contract-2", "run-2", "1", &pr, &dur, now.Add(-2*time.Hour), []string{"failed", "passed"}, true)
	seedGroup("pr", "playwright-full", "sha-contract-pr-red", "run-3", "1", &pr, &dur, now.Add(-30*time.Hour), []string{"failed"}, false)
	seedGroup("pr", "playwright-full", "sha-contract-pr-red", "run-3", "2", &pr, &dur, now.Add(-29*time.Hour), []string{"passed"}, false)
	seedGroup("pr", "playwright-full", "sha-contract-pr-live", "run-4", "1", &pr, &dur, now.Add(-2*time.Hour), []string{"failed"}, false)

	rawSummary := exchange(t, c, http.MethodGet, "/api/v1/kpi/summary?app=mattermost&window=30d&sort=lane_asc", "", nil, nil, http.StatusOK, true)
	var summary kpi.SummaryResponse
	if err := json.Unmarshal(rawSummary, &summary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if summary.E2EDuration.SampleCount != 1 {
		t.Fatalf("e2e_duration.sample_count = %d, want 1", summary.E2EDuration.SampleCount)
	}
	if summary.E2EDuration.P50Ms == nil || *summary.E2EDuration.P50Ms != 12345 {
		t.Fatalf("e2e_duration.p50_ms = %v, want 12345", summary.E2EDuration.P50Ms)
	}
	if summary.E2EDuration.P90Ms == nil || *summary.E2EDuration.P90Ms != 12345 {
		t.Fatalf("e2e_duration.p90_ms = %v, want 12345", summary.E2EDuration.P90Ms)
	}
	exchange(t, c, http.MethodGet, "/api/v1/kpi/flaky?app=mattermost&window=14d&sort=flake_rate_desc&page=1&limit=10", "", nil, nil, http.StatusOK, true)
	exchange(t, c, http.MethodGet, "/api/v1/kpi/prs/red?app=mattermost&window=30d&sort=created_desc&page=1&limit=10", "", nil, nil, http.StatusOK, true)
}
