//go:build e2e
// +build e2e

package orchestratione2e

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestConsolidatedMatchesByRepositorySuffix is a regression test for the
// repository-matching rewrite in aggregations.go: /reports/consolidated
// replaced a leading-wildcard `repository LIKE '%/' || $1` with
// `split_part(repository, '/', 2) = $1`, backed by a new functional index.
// This proves the trailing-segment match still works — a caller passing
// just "orch-e2e" must still find a report_group stored as the full
// "mattermost/orch-e2e" slug.
func TestConsolidatedMatchesByRepositorySuffix(t *testing.T) {
	env, tok := startEnv(t)

	shortRepo, ok := strings.CutPrefix(defaultRepository, "mattermost/")
	if !ok {
		t.Fatalf("defaultRepository = %q, want a mattermost/... prefix for this test", defaultRepository)
	}

	specPath := "tests/suffix_match.spec.ts"
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
		VALUES ($1, $2, 'complete', 'suffix-job-A', 'suffix-shard-A',
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
	`, reportID, "Suffix Match Suite", specPath).Scan(&suiteID); err != nil {
		t.Fatalf("insert suite: %v", err)
	}

	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO test_cases (suite_id, title, full_title, status, ordinal)
		VALUES ($1, $2, $3, 'passed', 0)
	`, suiteID, "passes", "Suffix Match Suite › passes"); err != nil {
		t.Fatalf("insert test_case: %v", err)
	}

	// Query with the SHORT repo name, not the full "owner/repo" slug stored
	// on the row — this is the branch that used to rely on the leading-
	// wildcard LIKE and now relies on split_part + the new functional index.
	q := url.Values{}
	q.Set("repository", shortRepo)
	q.Set("branch", defaultBranch)
	q.Set("commit", defaultCommit)
	q.Set("name", defaultName)
	resp := getJSON(t, env, tok, "/api/v1/reports/consolidated?"+q.Encode())
	body := expectStatus(t, resp, http.StatusOK)

	specs, ok := body["specs"].([]any)
	if !ok || len(specs) == 0 {
		t.Fatalf("consolidated by short repo %q: specs missing or empty: %v", shortRepo, body)
	}
	var found bool
	for _, sp := range specs {
		m, ok := sp.(map[string]any)
		if !ok {
			continue
		}
		if m["full_title"] == "Suffix Match Suite › passes" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("consolidated by short repo %q did not find the spec; got %v", shortRepo, specs)
	}
}
