//go:build e2e
// +build e2e

package orchestratione2e

import (
	"net/http"
	"strings"
	"testing"
)

// TestOrchestrationScreenshotUpload exercises POST /api/v1/orchestration/screenshots
// against a real lease, then asserts the uploaded key is reachable via the
// existing /files/* redirect. Negative path: a worker without a lease gets 404.
func TestOrchestrationScreenshotUpload(t *testing.T) {
	env, tok := startEnv(t)

	// Begin and check out a unit with a known spec_path.
	specPath := "tests/login.spec.ts"
	beginResp := postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{specPath, "tests/other.spec.ts"}, nil))
	expectStatus(t, beginResp, http.StatusCreated)

	checkResp := postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("playwright-shard-A", "job-shot-A", 1))
	checkBody := expectStatus(t, checkResp, http.StatusOK)
	if len(checkBody["units"].([]any)) != 1 {
		t.Fatalf("expected 1 unit, got %v", checkBody["units"])
	}

	// --- Happy path upload. ---
	pngBytes := []byte("\x89PNG\r\n\x1a\nfake-screenshot-bytes")
	uploadResp := uploadScreenshot(t, env, tok,
		"playwright-shard-A", "job-shot-A",
		specPath, "login_test/failure-1.png", pngBytes)
	uploadBody := expectStatus(t, uploadResp, http.StatusCreated)

	key, _ := uploadBody["key"].(string)
	if !strings.HasPrefix(key, "orchestration/") {
		t.Fatalf("key = %q, want orchestration/ prefix", key)
	}
	if !strings.Contains(key, "/screenshots/") {
		t.Fatalf("key = %q, missing /screenshots/ segment", key)
	}
	runID := runUUID(t, env, identity(nil)).String()
	wantPrefix := "orchestration/" + runID + "/"
	if !strings.HasPrefix(key, wantPrefix) {
		t.Fatalf("key = %q, want prefix %q", key, wantPrefix)
	}
	if jsonNumberInt(t, uploadBody["size_bytes"]) != len(pngBytes) {
		t.Fatalf("size_bytes = %v, want %d", uploadBody["size_bytes"], len(pngBytes))
	}

	// --- /files/{key} redirects (302) to a presigned URL. ---
	filesResp := getJSON(t, env, "" /* no auth needed */, "/files/"+key)
	if filesResp.StatusCode != http.StatusFound {
		t.Fatalf("/files/key status = %d, want 302; body=%s",
			filesResp.StatusCode, readBodyString(filesResp))
	}
	if loc := filesResp.Header.Get("Location"); loc == "" {
		t.Fatalf("/files redirect missing Location header")
	}

	// Verify the FakeStore actually has the bytes.
	if !env.Store.Has(key) {
		t.Fatalf("FakeStore missing key %q", key)
	}

	// --- Negative: an unregistered worker (no lease) gets 404. ---
	noLeaseResp := uploadScreenshot(t, env, tok,
		"playwright-shard-Z", "00000000",
		specPath, "x.png", []byte("x"))
	if noLeaseResp.StatusCode != http.StatusNotFound {
		t.Fatalf("no-lease upload: status = %d, want 404; body=%s",
			noLeaseResp.StatusCode, readBodyString(noLeaseResp))
	}
}
