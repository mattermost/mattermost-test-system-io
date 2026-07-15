//go:build e2e
// +build e2e

package orchestratione2e

import (
	"context"
	"net/http"
	"testing"
)

// TestBeginRunIdempotency exercises the begin-run idempotency contract:
// composite-identity collision with the same dispatch-units list is a 200
// idempotent replay; composite-identity collision with a different list is a
// 409 with code BEGIN_RUN_HASH_MISMATCH.
func TestBeginRunIdempotency(t *testing.T) {
	env, tok := startEnv(t)

	units := []string{
		"tests/x.spec.ts",
		"tests/y.spec.ts",
	}

	// --- First call: 201 Created ---
	first := postJSON(t, env, tok, "/api/v1/orchestration/begin", beginRunBody(units, nil))
	firstBody := expectStatus(t, first, http.StatusCreated)
	firstRunID, _ := firstBody["run_id"].(string)
	if firstRunID == "" {
		t.Fatalf("first response missing run_id: %v", firstBody)
	}

	// --- Same identity, same dispatch units: 200 OK ---
	second := postJSON(t, env, tok, "/api/v1/orchestration/begin", beginRunBody(units, nil))
	secondBody := expectStatus(t, second, http.StatusOK)
	secondRunID, _ := secondBody["run_id"].(string)
	if secondRunID != firstRunID {
		t.Fatalf("idempotent retry returned different run_id: first=%s second=%s",
			firstRunID, secondRunID)
	}
	if jsonNumberInt(t, secondBody["total_units"]) != jsonNumberInt(t, firstBody["total_units"]) {
		t.Fatalf("idempotent retry total_units mismatch")
	}
	if secondBody["status"] != firstBody["status"] {
		t.Fatalf("idempotent retry status mismatch: first=%v second=%v",
			firstBody["status"], secondBody["status"])
	}

	// --- Same identity, different dispatch units: 409 BEGIN_RUN_HASH_MISMATCH ---
	differentUnits := append([]string{}, units...)
	differentUnits = append(differentUnits, "tests/z-extra.spec.ts")
	third := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(differentUnits, nil))
	if third.StatusCode != http.StatusConflict {
		t.Fatalf("hash-mismatch retry: status = %d, want 409; body=%s",
			third.StatusCode, readBodyString(third))
	}
	if code := errorCode(t, third); code != "BEGIN_RUN_HASH_MISMATCH" {
		t.Fatalf("hash-mismatch error code = %q, want BEGIN_RUN_HASH_MISMATCH", code)
	}

	// --- DB: still exactly one orchestration_runs row for that identity. ---
	var n int
	err := env.Pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM orchestration_runs
		 WHERE repository = $1 AND commit_sha = $2 AND gh_run_id = $3
		   AND name = $4 AND gh_run_attempt = $5
	`, defaultRepository, defaultCommit, defaultGHRunID, defaultName, defaultRunAttempt).Scan(&n)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 orchestration_runs row, got %d", n)
	}
}
