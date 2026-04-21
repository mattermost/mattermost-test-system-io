//go:build e2e
// +build e2e

// Package perf contains performance smoke harnesses.
//
//   - TestUploadVisibilityP95 (T088): measures POST /reports → /reports/{id}=ready
//     latency at p95. Target per SC-002: ≤ 5 s.
//   - TestDashboardListP95 (T089): measures GET /reports at p95 against 10k
//     seeded reports. Target per SC-003: ≤ 1 s.
//
// These are harnesses, not rigorous benchmarks. They run under `-tags=e2e`
// and `go test -count=1` (never cached). Tune the SAMPLE_COUNT envs when
// taking real measurements.

package perf

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestUploadVisibilityP95(t *testing.T) {
	n := envInt("TSIO_PERF_UPLOAD_SAMPLES", 20)
	targetMs := int64(5000) // SC-002

	env := testenv.Start(t)
	env.DefaultReportGroup(t)
	apiKey := env.IssueAPIKey(t, "perf")

	latencies := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		body, ct := fixture(t, representativeReportJSON())
		start := time.Now()
		req, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/api/v1/reports", body)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("X-API-Key", apiKey)
		req.Header.Set("X-Report-Source", "perf")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		var rep struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&rep)
		resp.Body.Close()
		if rep.Status != "ready" {
			t.Fatalf("iteration %d: status=%q, expected ready", i, rep.Status)
		}
		latencies = append(latencies, time.Since(start))
	}

	p50, p95 := percentiles(latencies)
	t.Logf("upload→visibility: n=%d p50=%s p95=%s target_p95=%dms",
		n, p50, p95, targetMs)
	if p95.Milliseconds() > targetMs {
		t.Errorf("p95 = %s exceeds SC-002 target of %d ms", p95, targetMs)
	}
}

func TestDashboardListP95(t *testing.T) {
	seed := envInt("TSIO_PERF_SEED_REPORTS", 1000) // default 1k, raise to 10000 for real SC-003
	samples := envInt("TSIO_PERF_LIST_SAMPLES", 50)
	targetMs := int64(1000) // SC-003

	env := testenv.Start(t)
	groupID := env.DefaultReportGroup(t)
	apiKey := env.IssueAPIKey(t, "perf")

	// Seed reports. Bulk-insert via raw SQL, skipping the full ingest pipeline.
	seedReports(t, env, groupID, seed)

	latencies := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		req, _ := http.NewRequest(http.MethodGet, env.ServerURL+"/api/v1/reports?limit=50", nil)
		req.Header.Set("X-API-Key", apiKey)
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		latencies = append(latencies, time.Since(start))
	}

	p50, p95 := percentiles(latencies)
	t.Logf("dashboard list: seeded=%d samples=%d p50=%s p95=%s target_p95=%dms",
		seed, samples, p50, p95, targetMs)
	if p95.Milliseconds() > targetMs {
		t.Errorf("p95 = %s exceeds SC-003 target of %d ms", p95, targetMs)
	}
}

// ---------- helpers ----------

func envInt(key string, dflt int) int {
	v := os.Getenv(key)
	if v == "" {
		return dflt
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return dflt
	}
	return n
}

func percentiles(d []time.Duration) (p50, p95 time.Duration) {
	if len(d) == 0 {
		return 0, 0
	}
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 = sorted[len(sorted)*50/100]
	p95 = sorted[len(sorted)*95/100]
	return p50, p95
}

func representativeReportJSON() string {
	return `{
		"suites": [
			{"title": "perf", "file": "perf.spec.ts", "specs": [
				{"title": "case-0", "tests": [{"results": [{"status":"passed","duration":10}]}]},
				{"title": "case-1", "tests": [{"results": [{"status":"passed","duration":10}]}]},
				{"title": "case-2", "tests": [{"results": [{"status":"passed","duration":10}]}]}
			]}
		],
		"stats": {"duration": 30}
	}`
}

func fixture(t *testing.T, reportJSON string) (*bytes.Buffer, string) {
	t.Helper()
	zipBuf := &bytes.Buffer{}
	zw := zip.NewWriter(zipBuf)
	f, _ := zw.CreateHeader(&zip.FileHeader{Name: "report.json", Method: zip.Store})
	_, _ = f.Write([]byte(reportJSON))
	_ = zw.Close()

	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	part, _ := w.CreateFormFile("bundle", "report.zip")
	_, _ = part.Write(zipBuf.Bytes())
	_ = w.Close()
	return buf, w.FormDataContentType()
}

func seedReports(t *testing.T, env *testenv.Env, groupID string, n int) {
	t.Helper()
	batch := 500
	for start := 0; start < n; start += batch {
		end := start + batch
		if end > n {
			end = n
		}
		// Multi-row insert using UNNEST for speed.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		_, err := env.Pool.Exec(ctx, `
			INSERT INTO reports (report_group_id, source, status, uploaded_by_oidc_subject,
			                     total_cases, passed_cases, failed_cases, skipped_cases, flaky_cases,
			                     ingested_at)
			SELECT $1, 'perf-seed', 'ready', 'seed-subject', 10, 10, 0, 0, 0, now()
			FROM generate_series($2::int, $3::int)
		`, groupID, start, end-1)
		cancel()
		if err != nil {
			t.Fatalf("seed batch %d-%d: %v", start, end, err)
		}
	}
}
