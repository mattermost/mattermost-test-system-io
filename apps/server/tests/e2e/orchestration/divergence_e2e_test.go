//go:build e2e
// +build e2e

package orchestratione2e

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// TestDivergenceBetweenOrchestrationAndArtifacts asserts that the same
// composite identity ends up with disagreeing per-spec outcomes between the
// orchestration view (the run failed the spec) and the canonical artifact
// view (a Playwright report uploaded for the same group says the spec
// passed). The UI computes the divergence client-side by joining the
// orchestration attempts against the consolidated test_cases; this test
// only has to prove that BOTH data sources return the disagreeing values
// so a UI test could detect it.
//
// The artifact side is staged directly through the test pgxpool — the
// reports/begin + register + upload flow is deliberately heavyweight
// (it exercises S3 + JSON consolidation), and the divergence-detection
// contract is concerned with comparing the two sources, not with the
// upload pipeline.
func TestDivergenceBetweenOrchestrationAndArtifacts(t *testing.T) {
	env, tok := startEnv(t)

	specPath := "tests/divergence.spec.ts"
	units := []string{specPath}

	// --- Orchestration side: begin run, fail the spec. ---
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(units, map[string]any{
			"lease_timeout_ms": 60_000,
			"idle_timeout_ms":  60_000,
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	checkResp := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-divergence-A", 1))
	checkBody := expectStatus(t, checkResp, http.StatusOK)
	if len(checkBody["units"].([]any)) != 1 {
		t.Fatalf("checkout: expected 1 unit, got %v", checkBody["units"])
	}

	completeResp := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-A", "job-divergence-A", failResults([]string{units[0]})))
	expectStatus(t, completeResp, http.StatusOK)

	// --- Artifact side: synthesize a passed test_cases row under a
	// report_group sharing the SAME composite identity as the orchestration
	// run. orchestration_begin does not yet upsert the report_group itself
	// (a follow-up task on the orchestration domain), so we insert it here
	// to mimic the future state where both records line up by composite key. ---
	ctx := context.Background()
	var groupID string
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha,
		                           gh_run_id, gh_run_attempt)
		VALUES ('playwright', $1, $2, $3, $4, $5, $6)
		ON CONFLICT (repository, commit_sha, gh_run_id, name, gh_run_attempt)
			DO UPDATE SET branch = EXCLUDED.branch, updated_at = now()
		RETURNING id
	`, defaultName, defaultRepository, defaultBranch, defaultCommit,
		defaultGHRunID, defaultRunAttempt).Scan(&groupID); err != nil {
		t.Fatalf("upsert report_group: %v", err)
	}

	var reportID string
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO reports (report_group_id, name, status,
		                     gh_job_id, gh_job_name,
		                     total_suites, total_cases,
		                     passed_cases, failed_cases, skipped_cases, flaky_cases)
		VALUES ($1, $2, 'complete', 'artifact-job-A', 'artifact-shard-A',
		        1, 1, 1, 0, 0, 0)
		RETURNING id
	`, groupID, defaultName).Scan(&reportID); err != nil {
		t.Fatalf("insert reports: %v", err)
	}

	var suiteID string
	if err := env.Pool.QueryRow(ctx, `
		INSERT INTO suites (report_id, title, file, ordinal,
		                    total_count, passed_count, failed_count, skipped_count, flaky_count)
		VALUES ($1, $2, $3, 0, 1, 1, 0, 0, 0)
		RETURNING id
	`, reportID, "Divergence Suite", specPath).Scan(&suiteID); err != nil {
		t.Fatalf("insert suite: %v", err)
	}

	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO test_cases (suite_id, title, full_title, status, ordinal)
		VALUES ($1, $2, $3, 'passed', 0)
	`, suiteID, "passes in artifacts", "Divergence Suite › passes in artifacts"); err != nil {
		t.Fatalf("insert test_case: %v", err)
	}

	// --- Assert orchestration side: the spec is failed. ---
	stat := getJSON(t, env, tok, statusURL(identity(nil)))
	statBody := expectStatus(t, stat, http.StatusOK)
	c := counts(t, statBody)
	if c["completed_fail"] != 1 {
		t.Fatalf("orchestration counts = %s, want completed_fail=1", fmtCounts(c))
	}

	// --- Assert artifact side: the consolidated endpoint reports passed. ---
	q := url.Values{}
	q.Set("repository", defaultRepository)
	q.Set("branch", defaultBranch)
	q.Set("commit", defaultCommit)
	q.Set("name", defaultName)
	consResp := getJSON(t, env, tok, "/api/v1/reports/consolidated?"+q.Encode())
	consBody := expectStatus(t, consResp, http.StatusOK)
	specs, ok := consBody["specs"].([]any)
	if !ok || len(specs) == 0 {
		t.Fatalf("consolidated: specs missing or empty: %v", consBody)
	}
	var found bool
	for _, sp := range specs {
		m, ok := sp.(map[string]any)
		if !ok {
			continue
		}
		if m["full_title"] != "Divergence Suite › passes in artifacts" {
			continue
		}
		if m["status"] != "passed" {
			t.Fatalf("artifact spec status = %v, want passed", m["status"])
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("consolidated payload did not include the synthesized spec; got %v", specs)
	}

	// --- The two endpoints disagree for the same identity: orchestration
	// failed the spec, artifacts passed it. The UI's useDivergences hook
	// joins these two payloads on spec_path/file_path and surfaces the
	// disagreement via DivergenceBadge. This test establishes that both
	// data sources return the disagreeing data — the visual surface is
	// covered by the divergence_badge vitest test. ---
}
