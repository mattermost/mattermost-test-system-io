//go:build e2e

// Round-3 task 7: prove pg_try_advisory_xact_lock actually serializes
// AlertEvaluate against a real Postgres. The reviewer had no DB, so this
// concurrency code had never executed. Two tests:
//
//  1. Deterministic: hold the advisory lock in a test transaction, assert the
//     handler returns 409, roll back, assert the next call succeeds (the lock
//     is released on rollback — the handler's defer tx.Rollback path).
//  2. Concurrent: fire two AlertEvaluate calls at once and assert exactly one
//     proceeds (200) and the other gets 409, and the loser leaves no partial
//     firing rows.
package triage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// postJSONStatus is a goroutine-safe variant of postJSON that returns the raw
// status code (the shared postJSON uses t.Fatalf, which is not goroutine-safe).
func postJSONStatus(env *testenv.Env, key, path string, body any) (int, map[string]any) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, env.ServerURL+path, bytes.NewReader(raw))
	if err != nil {
		return 0, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// seedManyCases inserts `n` test cases across the window so loadMasterAlertData
// takes long enough that two concurrent evaluates genuinely overlap.
func seedManyCases(t *testing.T, env *testenv.Env, n int) {
	t.Helper()
	_, err := env.Pool.Exec(t.Context(), `
		INSERT INTO report_groups (framework, name, status, repository, branch, commit_sha, gh_run_id, created_at)
		SELECT 'playwright', 'lock-' || g, 'completed', 'mattermost/mattermost', 'main',
		       'locksha' || g, 'lockrun-' || g, now() - (g % 12 || ' days')::interval
		FROM generate_series(1, $1) AS g
	`, n)
	if err != nil {
		t.Fatalf("seed groups: %v", err)
	}
	_, err = env.Pool.Exec(t.Context(), `
		WITH rep AS (
			INSERT INTO reports (report_group_id, name, status, total_cases, failed_cases)
			SELECT id, 'shard', 'complete', 1, 1 FROM report_groups WHERE name LIKE 'lock-%'
			RETURNING id
		), s AS (
			INSERT INTO suites (report_id, title, total_count, failed_count, ordinal)
			SELECT id, 's', 1, 1, 0 FROM rep
			RETURNING id
		)
		INSERT INTO test_cases (suite_id, title, full_title, status, external_test_id, ordinal)
		SELECT id, 'c', 'c', 'failed', 'MM-LOCK-' || row_number() OVER (), 0 FROM s
	`)
	if err != nil {
		t.Fatalf("seed cases: %v", err)
	}
}

func TestAlertEvaluateAdvisoryLockBlocksAndReleases(t *testing.T) {
	env := testenv.Start(t)
	seedTwelveDayStreak(t, env)
	key := env.IssueAPIKey(t, "w7-lock")

	// Hold the advisory lock in a test transaction — the same key the handler
	// uses (hashtext of the NORMALIZED repo slug).
	tx, err := env.Pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var locked bool
	if err := tx.QueryRow(t.Context(), `SELECT pg_try_advisory_xact_lock(hashtext($1))`, "mattermost/mattermost").Scan(&locked); err != nil || !locked {
		t.Fatalf("acquire lock: %v (locked=%v)", err, locked)
	}

	// While held, the handler must 409.
	res := postJSON(t, env, key, "/api/v1/triage/alerts/evaluate?repo=mattermost", map[string]any{})
	if res["status"].(float64) != 409 {
		t.Fatalf("expected 409 while lock held, got %v", res["status"])
	}

	// Roll back the test transaction — the lock is released.
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Now the handler must succeed.
	res2 := postJSON(t, env, key, "/api/v1/triage/alerts/evaluate?repo=mattermost", map[string]any{})
	if res2["status"].(float64) != 200 {
		t.Fatalf("expected 200 after rollback, got %v", res2["status"])
	}
}

func TestAlertEvaluateConcurrentLock(t *testing.T) {
	env := testenv.Start(t)
	// A real streak (MM-T7001) so the winner writes a firing row, plus many
	// cases so loadMasterAlertData is slow enough that the two evaluates overlap.
	seedTwelveDayStreak(t, env)
	seedManyCases(t, env, 3000)
	key := env.IssueAPIKey(t, "w7-concurrent")

	// Fire two evaluates at once. Exactly one may proceed; the other must 409.
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, _ := postJSONStatus(env, key, "/api/v1/triage/alerts/evaluate?repo=mattermost", map[string]any{})
			statuses[i] = status
		}(i)
	}
	wg.Wait()

	var ok, conflict int
	for _, s := range statuses {
		switch s {
		case 200:
			ok++
		case 409:
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("expected exactly one 200 and one 409, got statuses %v", statuses)
	}

	// The loser must leave no partial firing rows: the streak rule fired once
	// (by the winner), not twice.
	var fireCount int
	if err := env.Pool.QueryRow(t.Context(), `
		SELECT count(*) FROM alert_firings
		WHERE rule = 'new_failing_streak' AND resolved_at IS NULL
	`).Scan(&fireCount); err != nil {
		t.Fatalf("count firings: %v", err)
	}
	if fireCount != 1 {
		t.Fatalf("firing rows = %d, want 1 (the 409 loser must write nothing)", fireCount)
	}

	// A third call immediately after must succeed — the lock was released on
	// the winner's commit.
	third := postJSON(t, env, key, "/api/v1/triage/alerts/evaluate?repo=mattermost", map[string]any{})
	if third["status"].(float64) != 200 {
		t.Fatalf("expected 200 on the third call (lock released), got %v", third["status"])
	}
}
