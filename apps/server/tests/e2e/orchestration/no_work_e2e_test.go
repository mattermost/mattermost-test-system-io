//go:build e2e
// +build e2e

package orchestratione2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// minimalPlaywrightJSON is a valid one-test Playwright report: enough for a
// worker's shard report to reach `complete`.
const minimalPlaywrightJSON = `{"suites":[{"title":"a.spec.ts","file":"a.spec.ts","specs":[{"title":"works","tests":[{"projectName":"chrome","status":"expected","results":[{"status":"passed","duration":10,"retry":0,"startTime":"2026-01-01T00:00:00.000Z"}]}]}]}]}`

// TestLateWorkerWithNoWorkCompletesReportGroup reproduces mattermost run
// 39408679848: the queue drained before one of the N workers started, so its
// first checkout got 409 RUN_NOT_IN_PROGRESS and it exited without a report.
// The group expected N reports, got N-1, and was reaped to `incomplete` with
// every test passing. A worker that leased nothing now says so, and the group
// completes.
func TestLateWorkerWithNoWorkCompletesReportGroup(t *testing.T) {
	env, tok := startEnv(t)

	spec := "tests/a.spec.ts"
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{spec}, map[string]any{"total_reports_expected": 2})), http.StatusCreated)

	runAllAndUpload(t, env, tok, "worker-A", "job-A", []string{spec})
	if got := reportGroupStatus(t, env); got != "in_progress" {
		t.Fatalf("group status after 1 of 2 reports = %q, want in_progress", got)
	}

	late := postJSON(t, env, tok, "/api/v1/orchestration/checkout", checkoutBody("worker-B", "job-B", 1))
	if late.StatusCode != http.StatusConflict {
		t.Fatalf("late checkout status = %d, want 409; body=%s", late.StatusCode, readBodyString(late))
	}
	if code := errorCode(t, late); code != "RUN_NOT_IN_PROGRESS" {
		t.Fatalf("late checkout code = %q, want RUN_NOT_IN_PROGRESS", code)
	}

	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/no-work",
		noWorkBody("worker-B", "job-B")), http.StatusOK)
	waitReportGroupStatus(t, env, "completed")

	// Idempotent: a retried call is still 200.
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/no-work",
		noWorkBody("worker-B", "job-B")), http.StatusOK)
}

// TestNoWorkBeforeLastUploadCompletesOnUpload covers the other order: a worker
// gives up on an empty queue while another still runs the last spec. The
// group completes when the running worker's report lands.
func TestNoWorkBeforeLastUploadCompletesOnUpload(t *testing.T) {
	env, tok := startEnv(t)

	spec := "tests/a.spec.ts"
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{spec}, map[string]any{"total_reports_expected": 2})), http.StatusCreated)

	checkA := expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("worker-A", "job-A", 1)), http.StatusOK)
	if len(checkA["units"].([]any)) != 1 {
		t.Fatalf("worker A checkout: want 1 unit, got %v", checkA["units"])
	}
	idle := expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("worker-B", "job-B", 1)), http.StatusOK)
	if idle["queue_empty"] != true {
		t.Fatalf("worker B checkout: want queue_empty, got %v", idle)
	}
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/no-work",
		noWorkBody("worker-B", "job-B")), http.StatusOK)
	if got := reportGroupStatus(t, env); got != "in_progress" {
		t.Fatalf("group status with worker A still running = %q, want in_progress", got)
	}

	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("worker-A", "job-A", passResults([]string{spec}))), http.StatusOK)
	uploadWorkerReport(t, env, tok, "worker-A", "job-A")
	waitReportGroupStatus(t, env, "completed")
}

// TestNoWorkRejectedForWorkerThatLeased keeps "missing report means red" for
// a worker that ran tests: it must upload its report, it cannot opt out.
func TestNoWorkRejectedForWorkerThatLeased(t *testing.T) {
	env, tok := startEnv(t)

	spec := "tests/a.spec.ts"
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{spec}, nil)), http.StatusCreated)
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("worker-A", "job-A", 1)), http.StatusOK)
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("worker-A", "job-A", passResults([]string{spec}))), http.StatusOK)

	resp := postJSON(t, env, tok, "/api/v1/orchestration/no-work", noWorkBody("worker-A", "job-A"))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("no-work after leasing: status = %d, want 409; body=%s", resp.StatusCode, readBodyString(resp))
	}
	if code := errorCode(t, resp); code != "WORKER_HAS_LEASES" {
		t.Fatalf("no-work after leasing: code = %q, want WORKER_HAS_LEASES", code)
	}
	if got := reportGroupStatus(t, env); got != "in_progress" {
		t.Fatalf("group status with worker A's report missing = %q, want in_progress", got)
	}
}

// TestNoWorkDoesNotCountBeforeRunCompletes: an idle worker counts toward the
// expected reports only once orchestration finished every unit. Until then
// (or if the run times out) some spec has not run, so the group stays open.
func TestNoWorkDoesNotCountBeforeRunCompletes(t *testing.T) {
	env, tok := startEnv(t)

	specs := []string{"tests/a.spec.ts", "tests/b.spec.ts"}
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(specs, map[string]any{"total_reports_expected": 2})), http.StatusCreated)

	// Worker A runs one of the two specs and uploads; the other is pending.
	checkA := expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody("worker-A", "job-A", 1)), http.StatusOK)
	units := checkA["units"].([]any)
	if len(units) != 1 {
		t.Fatalf("worker A checkout: want 1 unit, got %v", units)
	}
	leased := units[0].(map[string]any)["spec_path"].(string)
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody("worker-A", "job-A", passResults([]string{leased}))), http.StatusOK)
	uploadWorkerReport(t, env, tok, "worker-A", "job-A")

	// 1 report + 1 idle worker = 2, but a spec is still pending.
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/no-work",
		noWorkBody("worker-B", "job-B")), http.StatusOK)
	if got := reportGroupStatus(t, env); got != "in_progress" {
		t.Fatalf("group status with a spec still pending = %q, want in_progress", got)
	}
}

// TestWorkerThatDiedBeforeReachingTSIOKeepsGroupIncomplete pins mattermost
// run 39451950734 red by design: dispatch-run-4 died in actions/checkout, so
// it never called checkout or no-work. Every unit still finished on the other
// workers, but nothing says that worker had nothing to do, so the group stays
// short and the reaper marks it incomplete.
func TestWorkerThatDiedBeforeReachingTSIOKeepsGroupIncomplete(t *testing.T) {
	env, tok := startEnv(t)

	spec := "tests/a.spec.ts"
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody([]string{spec}, map[string]any{"total_reports_expected": 2})), http.StatusCreated)

	// Worker A runs everything; worker B never shows up.
	runAllAndUpload(t, env, tok, "worker-A", "job-A", []string{spec})
	if got := reportGroupStatus(t, env); got != "in_progress" {
		t.Fatalf("group status with worker B missing = %q, want in_progress", got)
	}

	reapStaleReportGroups(t, env)
	waitReportGroupStatus(t, env, "incomplete")
}

// TestWorkerThatLeasedAndDiedBeforeUploadKeepsGroupIncomplete: a worker that
// ran tests and died before uploading leaves the group short, even when every
// unit completed and the other workers either uploaded or left without work.
func TestWorkerThatLeasedAndDiedBeforeUploadKeepsGroupIncomplete(t *testing.T) {
	env, tok := startEnv(t)

	specs := []string{"tests/a.spec.ts", "tests/b.spec.ts"}
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/begin",
		beginRunBody(specs, map[string]any{"total_reports_expected": 3})), http.StatusCreated)

	// A and B each run one spec; only A uploads. B dies before its upload.
	for _, w := range []struct{ name, id string }{{"worker-A", "job-A"}, {"worker-B", "job-B"}} {
		check := expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
			checkoutBody(w.name, w.id, 1)), http.StatusOK)
		units := check["units"].([]any)
		if len(units) != 1 {
			t.Fatalf("%s checkout: want 1 unit, got %v", w.name, units)
		}
		leased := units[0].(map[string]any)["spec_path"].(string)
		expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
			completeBody(w.name, w.id, passResults([]string{leased}))), http.StatusOK)
	}
	uploadWorkerReport(t, env, tok, "worker-A", "job-A")

	// C starts late and records no work: 1 report + 1 no-work of 3 expected.
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/no-work",
		noWorkBody("worker-C", "job-C")), http.StatusOK)
	if got := reportGroupStatus(t, env); got != "in_progress" {
		t.Fatalf("group status with worker B's report missing = %q, want in_progress", got)
	}

	reapStaleReportGroups(t, env)
	waitReportGroupStatus(t, env, "incomplete")
}

// reapStaleReportGroups runs the reports staleness reaper (not started by
// testenv) with a near-zero window, so any group still in_progress is marked
// incomplete the way production marks it after an hour without uploads.
func reapStaleReportGroups(t *testing.T, env *testenv.Env) {
	t.Helper()
	reaper := &reports.Reaper{Pool: env.Pool, Timeout: time.Millisecond, Interval: 50 * time.Millisecond}
	if err := reaper.Start(context.Background()); err != nil {
		t.Fatalf("start reports reaper: %v", err)
	}
	t.Cleanup(reaper.Stop)
}

// noWorkBody composes a no-work body for the given worker.
func noWorkBody(jobName, jobID string) map[string]any {
	return merge(identity(nil), map[string]any{
		"gh_job_name": jobName,
		"gh_job_id":   jobID,
	})
}

// runAllAndUpload drains every spec as one worker, then uploads its report
// and waits for the report to reach `complete`.
func runAllAndUpload(t *testing.T, env *testenv.Env, tok, jobName, jobID string, specs []string) {
	t.Helper()
	check := expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/checkout",
		checkoutBody(jobName, jobID, len(specs))), http.StatusOK)
	if got := len(check["units"].([]any)); got != len(specs) {
		t.Fatalf("%s checkout: got %d units, want %d", jobName, got, len(specs))
	}
	expectStatus(t, postJSON(t, env, tok, "/api/v1/orchestration/complete",
		completeBody(jobName, jobID, passResults(specs))), http.StatusOK)
	uploadWorkerReport(t, env, tok, jobName, jobID)
}

// uploadWorkerReport registers and uploads one worker's shard report into the
// run's report group, the way the dispatch-run action does at queue-empty,
// and waits for the report to reach `complete`.
func uploadWorkerReport(t *testing.T, env *testenv.Env, tok, jobName, jobID string) {
	t.Helper()
	id := identity(nil)
	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
		"repository":     id["repository"],
		"commit":         id["commit_sha"],
		"gh_run_id":      id["gh_run_id"],
		"gh_run_attempt": id["gh_run_attempt"],
		"framework":      id["framework"],
		"name":           id["name"],
		"branch":         id["branch"],
		"gh_job_id":      jobID,
		"gh_job_name":    jobName,
		"json_files": []any{
			map[string]any{"path": "results.json", "size": len(minimalPlaywrightJSON)},
		},
	})
	if reg.StatusCode != http.StatusOK {
		t.Fatalf("register %s: status = %d; body=%s", jobName, reg.StatusCode, reg.Body)
	}
	var ids struct {
		ReportID string `json:"report_id"`
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(reg.Body, &ids); err != nil {
		t.Fatalf("decode register response: %v (body=%s)", err, reg.Body)
	}

	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	part, err := w.CreateFormFile("files", "results.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(minimalPlaywrightJSON)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/api/v1/reports/upload/%s/%s/json", env.ServerURL, ids.ReportID, ids.UploadID), buf)
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload %s: %v", jobName, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload %s: status = %d; body=%s", jobName, resp.StatusCode, readBodyString(resp))
	}

	pollUntil(t, 5*time.Second, 50*time.Millisecond, jobName+"'s report to complete", func() bool {
		var status string
		if err := env.Pool.QueryRow(context.Background(),
			`SELECT status FROM reports WHERE id = $1`, ids.UploadID).Scan(&status); err != nil {
			t.Fatalf("read report status: %v", err)
		}
		return status == "complete"
	})
}

// reportGroupStatus reads the status of the report group seeded for the
// default run identity.
func reportGroupStatus(t *testing.T, env *testenv.Env) string {
	t.Helper()
	id := identity(nil)
	var status string
	if err := env.Pool.QueryRow(context.Background(), `
		SELECT status FROM report_groups
		 WHERE repository = $1 AND commit_sha = $2 AND gh_run_id = $3 AND name = $4 AND gh_run_attempt = $5
	`, id["repository"], id["commit_sha"], id["gh_run_id"], id["name"], id["gh_run_attempt"]).Scan(&status); err != nil {
		t.Fatalf("read report group status: %v", err)
	}
	return status
}

// waitReportGroupStatus polls until the default run's report group reaches
// want. Group completion on upload runs after the upload response returns.
func waitReportGroupStatus(t *testing.T, env *testenv.Env, want string) {
	t.Helper()
	pollUntil(t, 5*time.Second, 50*time.Millisecond, "report group to be "+want, func() bool {
		return reportGroupStatus(t, env) == want
	})
}
