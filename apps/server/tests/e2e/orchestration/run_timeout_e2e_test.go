//go:build e2e
// +build e2e

package orchestratione2e

import (
	"net/http"
	"testing"
	"time"
)

// TestRunLevelTimeout asserts the reaper-driven run-timeout transition: a run
// whose run-level deadline elapses before any worker reports flips to
// `timed_out`, all units become `abandoned`, and subsequent /checkout and
// /complete calls bounce with the documented error codes.
func TestRunLevelTimeout(t *testing.T) {
	env, tok := startEnv(t)

	// 5 dispatch units, each a single spec. Run-level timeout 1 s.
	units := []string{
		"tests/u-0.spec.ts",
		"tests/u-1.spec.ts",
		"tests/u-2.spec.ts",
		"tests/u-3.spec.ts",
		"tests/u-4.spec.ts",
	}
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(units, map[string]any{
			"run_timeout_ms":   1000,
			"lease_timeout_ms": 60000,
		}))
	expectStatus(t, beginResp, http.StatusCreated)

	// Wait for the reaper (500 ms tick) to mark the run timed_out. Poll for up
	// to 5 s — robust against varying CI latency.
	pollUntil(t, 5*time.Second, 100*time.Millisecond, "run to time out", func() bool {
		stat := getJSON(t, env, tok, statusURL(identity(nil)))
		if stat.StatusCode != http.StatusOK {
			return false
		}
		body := decodeJSON(t, stat)
		return body["status"] == "timed_out"
	})

	// /status snapshot: timed_out, terminal_at set, all 5 abandoned.
	stat := getJSON(t, env, tok, statusURL(identity(nil)))
	body := expectStatus(t, stat, http.StatusOK)
	if body["status"] != "timed_out" {
		t.Fatalf("status = %v, want timed_out", body["status"])
	}
	if body["terminal_at"] == nil {
		t.Fatalf("terminal_at = nil; expected timestamp")
	}
	c := counts(t, body)
	if c["pending"] != 0 || c["leased"] != 0 ||
		c["completed_pass"] != 0 || c["completed_fail"] != 0 ||
		c["completed_skipped"] != 0 || c["abandoned"] != 5 {
		t.Fatalf("counts = %s, want abandoned=5 only", fmtCounts(c))
	}

	// /checkout returns 409 RUN_NOT_IN_PROGRESS now.
	checkResp := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-X", "job-x", 1))
	if checkResp.StatusCode != http.StatusConflict {
		t.Fatalf("checkout after timeout: status = %d, want 409; body=%s",
			checkResp.StatusCode, readBodyString(checkResp))
	}
	if code := errorCode(t, checkResp); code != "RUN_NOT_IN_PROGRESS" {
		t.Fatalf("checkout error code = %q, want RUN_NOT_IN_PROGRESS", code)
	}

	// /complete from a worker that never had a lease → 404.
	completeResp := postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("playwright-shard-X", "job-x", passResults([]string{units[0]})))
	if completeResp.StatusCode != http.StatusNotFound {
		t.Fatalf("complete with no lease: status = %d, want 404; body=%s",
			completeResp.StatusCode, readBodyString(completeResp))
	}
}
