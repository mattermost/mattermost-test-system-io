//go:build e2e
// +build e2e

package reportse2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/htmlpage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestStatusParity_GoSQLAPISSRAndUpload(t *testing.T) {
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled(), testenv.WithStatusRule(teststatus.RuleShared))
	env.InsertPolicy(t, "allow-status-parity", 1, string(policy.RoleUploader), map[string]string{
		"repository_owner": defaultRepoOwner,
	})
	tok := env.Mock.IssueToken(t, oidcmock.Claims{
		Subject:         defaultOIDCSubject,
		Audience:        "tsio",
		Repository:      defaultRepository,
		RepositoryOwner: defaultRepoOwner,
		Workflow:        "ci",
		Ref:             "refs/heads/main",
	})

	const (
		commitSHA  = "abcdef1234567890abcdef1234567890abcdef12"
		ghRunID    = "run-status-parity-1"
		runAttempt = "1"
		runName    = "playwright-parity"
		branch     = "main"
		jobID      = "99887766"
		jobName    = "worker-1"

		wantPassed   = 1
		wantFailed   = 4
		wantFlaky    = 3
		wantSkipped  = 2
		wantTotal    = 10
		wantPassRate = 50
	)

	// 1. Go teststatus verification on the canonical fixture.
	type fixtureTest struct {
		title          string
		attempts       []string
		frameworkFlaky bool
		wantVerdict    string
	}
	fixture := []fixtureTest{
		{title: "passed", attempts: []string{"passed"}, wantVerdict: teststatus.Passed},
		{title: "flaky fail+pass", attempts: []string{"failed", "passed"}, wantVerdict: teststatus.Flaky},
		{title: "flaky pass+fail", attempts: []string{"passed", "failed"}, frameworkFlaky: true, wantVerdict: teststatus.Flaky},
		{title: "flaky framework", attempts: []string{"flaky"}, frameworkFlaky: true, wantVerdict: teststatus.Flaky},
		{title: "failed", attempts: []string{"failed"}, wantVerdict: teststatus.Failed},
		{title: "timedOut", attempts: []string{"timedOut"}, wantVerdict: teststatus.Failed},
		{title: "interrupted", attempts: []string{"interrupted"}, wantVerdict: teststatus.Failed},
		{title: "skipped", attempts: []string{"skipped"}, wantVerdict: teststatus.Skipped},
		{title: "pending", attempts: []string{"pending"}, wantVerdict: teststatus.Skipped},
		{title: "unknown", attempts: []string{"unknown"}, wantVerdict: teststatus.Failed},
		{title: "missing attempts", attempts: nil, wantVerdict: ""},
	}
	var goP, goF, goFl, goS, goT int
	for _, ft := range fixture {
		got := teststatus.TestVerdict(ft.attempts, ft.frameworkFlaky)
		if got != ft.wantVerdict {
			t.Fatalf("Go TestVerdict(%q) = %q, want %q", ft.title, got, ft.wantVerdict)
		}
		switch got {
		case teststatus.Passed:
			goP++
		case teststatus.Failed:
			goF++
		case teststatus.Flaky:
			goFl++
		case teststatus.Skipped:
			goS++
		}
		if got != "" {
			goT++
		}
	}
	if goP != wantPassed || goF != wantFailed || goFl != wantFlaky || goS != wantSkipped || goT != wantTotal {
		t.Fatalf("Go counts = %d/%d/%d/%d/%d, want %d/%d/%d/%d/%d",
			goP, goF, goFl, goS, goT, wantPassed, wantFailed, wantFlaky, wantSkipped, wantTotal)
	}
	if pct := teststatus.PassRatePercent(goP, goF, goFl); pct != wantPassRate {
		t.Fatalf("Go PassRatePercent = %d, want %d", pct, wantPassRate)
	}
	if sv := teststatus.SpecVerdict(nil); sv != teststatus.Failed {
		t.Fatalf("Go SpecVerdict(nil) = %q, want failed (spec-did-not-run)", sv)
	}

	// 2. SQL tsio_test_verdict() verification across all attempt shapes (1-arg and 2-arg framework_flaky).
	ctx := context.Background()
	for _, ft := range fixture {
		var sqlVerdict *string
		if ft.attempts == nil {
			err := env.Pool.QueryRow(ctx, `SELECT tsio_test_verdict('{}'::text[], $1::boolean)`, ft.frameworkFlaky).Scan(&sqlVerdict)
			if err != nil {
				t.Fatalf("SQL tsio_test_verdict empty: %v", err)
			}
			if sqlVerdict != nil {
				t.Fatalf("SQL tsio_test_verdict('{}') = %q, want NULL", *sqlVerdict)
			}
			continue
		}
		err := env.Pool.QueryRow(ctx, `SELECT tsio_test_verdict($1::text[], $2::boolean)`, ft.attempts, ft.frameworkFlaky).Scan(&sqlVerdict)
		if err != nil {
			t.Fatalf("SQL tsio_test_verdict(%v, %v): %v", ft.attempts, ft.frameworkFlaky, err)
		}
		if sqlVerdict == nil || *sqlVerdict != ft.wantVerdict {
			t.Fatalf("SQL tsio_test_verdict(%v, %v) = %v, want %q", ft.attempts, ft.frameworkFlaky, sqlVerdict, ft.wantVerdict)
		}
	}
	// Verify framework_flaky=true promotes a single 'passed' attempt to 'flaky', matching Go TestVerdict.
	var fwFlakyVerdict *string
	if err := env.Pool.QueryRow(ctx, `SELECT tsio_test_verdict(ARRAY['passed'], true)`).Scan(&fwFlakyVerdict); err != nil || fwFlakyVerdict == nil || *fwFlakyVerdict != teststatus.Flaky {
		t.Fatalf("SQL tsio_test_verdict(['passed'], true) = (%v, %v), want flaky", fwFlakyVerdict, err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT tsio_test_verdict(ARRAY['passed'])`).Scan(&fwFlakyVerdict); err != nil || fwFlakyVerdict == nil || *fwFlakyVerdict != teststatus.Passed {
		t.Fatalf("SQL tsio_test_verdict(['passed']) = (%v, %v), want passed", fwFlakyVerdict, err)
	}
	var nullVerdict *string
	if err := env.Pool.QueryRow(ctx, `SELECT tsio_test_verdict(NULL::text[])`).Scan(&nullVerdict); err != nil || nullVerdict != nil {
		t.Fatalf("SQL tsio_test_verdict(NULL) = (%v, %v), want (nil, nil)", nullVerdict, err)
	}
	if err := env.Pool.QueryRow(ctx, `SELECT tsio_test_verdict(NULL::text[], true)`).Scan(&nullVerdict); err != nil || nullVerdict != nil {
		t.Fatalf("SQL tsio_test_verdict(NULL, true) = (%v, %v), want (nil, nil)", nullVerdict, err)
	}

	// 3. Orchestration begin -> checkout -> complete (with empty-spec + edge test_cases).
	postAPI := func(path string, body any) (int, []byte) {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s: %v", path, err)
		}
		req, err := http.NewRequest(http.MethodPost, env.ServerURL+path, bytes.NewReader(b))
		if err != nil {
			t.Fatalf("new req %s: %v", path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw
	}

	code, raw := postAPI("/api/v1/orchestration/begin", map[string]any{
		"repository":             defaultRepository,
		"commit_sha":             commitSHA,
		"gh_run_id":              ghRunID,
		"name":                   runName,
		"gh_run_attempt":         runAttempt,
		"branch":                 branch,
		"framework":              "playwright",
		"playwright_project":     "default",
		"total_reports_expected": 1,
		"dispatch_units": []map[string]any{
			{"spec_path": "specs/parity.spec.ts"},
			{"spec_path": "specs/empty.spec.ts"},
		},
	})
	if code != http.StatusCreated {
		t.Fatalf("orch begin = %d (%s)", code, raw)
	}

	code, raw = postAPI("/api/v1/orchestration/checkout", map[string]any{
		"repository":     defaultRepository,
		"commit_sha":     commitSHA,
		"gh_run_id":      ghRunID,
		"name":           runName,
		"gh_run_attempt": runAttempt,
		"gh_job_name":    jobName,
		"gh_job_id":      jobID,
		"batch_size":     2,
	})
	if code != http.StatusOK {
		t.Fatalf("orch checkout = %d (%s)", code, raw)
	}

	orchCases := []map[string]any{
		{"title": "passed", "full_title": "parity > passed", "status": "passed", "retry_count": 0, "duration_ms": 10, "ordinal": 0},
		{"title": "flaky1", "full_title": "parity > flaky fail+pass", "status": "failed", "retry_count": 0, "duration_ms": 10, "ordinal": 1},
		{"title": "flaky1", "full_title": "parity > flaky fail+pass", "status": "passed", "retry_count": 1, "duration_ms": 10, "ordinal": 2},
		{"title": "flaky2", "full_title": "parity > flaky pass+fail", "status": "passed", "retry_count": 0, "duration_ms": 10, "ordinal": 3},
		{"title": "flaky2", "full_title": "parity > flaky pass+fail", "status": "failed", "retry_count": 1, "duration_ms": 10, "ordinal": 4},
		{"title": "flaky3", "full_title": "parity > flaky framework", "status": "flaky", "retry_count": 0, "duration_ms": 10, "ordinal": 5},
		{"title": "failed", "full_title": "parity > failed", "status": "failed", "retry_count": 0, "duration_ms": 10, "ordinal": 6},
		{"title": "timedOut", "full_title": "parity > timedOut", "status": "timedOut", "retry_count": 0, "duration_ms": 10, "ordinal": 7},
		{"title": "interrupted", "full_title": "parity > interrupted", "status": "interrupted", "retry_count": 0, "duration_ms": 10, "ordinal": 8},
		{"title": "skipped", "full_title": "parity > skipped", "status": "skipped", "retry_count": 0, "duration_ms": 0, "ordinal": 9},
		{"title": "pending", "full_title": "parity > pending", "status": "pending", "retry_count": 0, "duration_ms": 0, "ordinal": 10},
		{"title": "unknown", "full_title": "parity > unknown", "status": "unknown", "retry_count": 0, "duration_ms": 10, "ordinal": 11},
		// Edge cases: empty/whitespace full_title & missing status must not be counted.
		{"title": "empty-title", "full_title": "   ", "status": "passed", "retry_count": 0, "duration_ms": 1, "ordinal": 12},
		{"title": "tab_only", "full_title": "\t\n", "status": "failed", "retry_count": 0, "duration_ms": 1, "ordinal": 13},
		{"title": "nbsp-title", "full_title": "\u00a0", "status": "failed", "retry_count": 0, "duration_ms": 1, "ordinal": 14},
		{"title": "vtab-title", "full_title": "\v", "status": "failed", "retry_count": 0, "duration_ms": 1, "ordinal": 15},
		{"title": "missing-status", "full_title": "parity > missing-status", "status": "", "retry_count": 0, "duration_ms": 1, "ordinal": 16},
	}
	code, raw = postAPI("/api/v1/orchestration/complete", map[string]any{
		"repository":     defaultRepository,
		"commit_sha":     commitSHA,
		"gh_run_id":      ghRunID,
		"name":           runName,
		"gh_run_attempt": runAttempt,
		"gh_job_name":    jobName,
		"gh_job_id":      jobID,
		"results": []map[string]any{
			{
				"spec_path":          "specs/parity.spec.ts",
				"status":             "failed",
				"actual_duration_ms": 100,
				"test_cases":         orchCases,
			},
			{
				"spec_path":          "specs/empty.spec.ts",
				"status":             "failed",
				"actual_duration_ms": 0,
				"error_message":      "spec-did-not-run",
				"test_cases":         []any{},
			},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("orch complete = %d (%s)", code, raw)
	}

	// Check GET /api/v1/orchestration/status
	statusURL := fmt.Sprintf(
		"%s/api/v1/orchestration/status?repository=%s&commit_sha=%s&gh_run_id=%s&name=%s&gh_run_attempt=%s",
		env.ServerURL, url.QueryEscape(defaultRepository), commitSHA, ghRunID, runName, runAttempt,
	)
	resp, err := http.Get(statusURL)
	if err != nil {
		t.Fatalf("get orchestration/status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	statusRaw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("orchestration/status = %d (%s)", resp.StatusCode, statusRaw)
	}
	var orchStatus struct {
		Status string `json:"status"`
		Tests  struct {
			Passed  int `json:"passed"`
			Failed  int `json:"failed"`
			Flaky   int `json:"flaky"`
			Skipped int `json:"skipped"`
			Total   int `json:"total"`
		} `json:"tests"`
		Counts struct {
			CompletedFail int `json:"completed_fail"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(statusRaw, &orchStatus); err != nil {
		t.Fatalf("unmarshal orchestration/status: %v", err)
	}
	if orchStatus.Counts.CompletedFail != 2 {
		t.Fatalf("orchestration completed_fail = %d, want 2 (incl spec-did-not-run)", orchStatus.Counts.CompletedFail)
	}
	if orchStatus.Tests.Passed != wantPassed || orchStatus.Tests.Failed != wantFailed ||
		orchStatus.Tests.Flaky != wantFlaky || orchStatus.Tests.Skipped != wantSkipped ||
		orchStatus.Tests.Total != wantTotal {
		t.Fatalf("orchestration/status tests = %+v, want %d/%d/%d/%d/%d",
			orchStatus.Tests, wantPassed, wantFailed, wantFlaky, wantSkipped, wantTotal)
	}

	// 4. Real multipart upload via POST /api/v1/reports/begin -> register -> upload/{rid}/{uid}/json.
	pwPayload := `{
		"suites": [
			{
				"title": "parity.spec.ts",
				"file": "specs/parity.spec.ts",
				"specs": [
					{"title": "passed", "tests": [{"status": "expected", "results": [{"status": "passed", "retry": 0, "duration": 10}]}]},
					{"title": "flaky fail+pass", "tests": [{"status": "flaky", "results": [{"status": "failed", "retry": 0, "duration": 10}, {"status": "passed", "retry": 1, "duration": 10}]}]},
					{"title": "flaky pass+fail", "tests": [{"status": "flaky", "results": [{"status": "passed", "retry": 0, "duration": 10}, {"status": "failed", "retry": 1, "duration": 10}]}]},
					{"title": "flaky framework", "tests": [{"status": "flaky", "results": [{"status": "flaky", "retry": 0, "duration": 10}]}]},
					{"title": "failed", "tests": [{"status": "unexpected", "results": [{"status": "failed", "retry": 0, "duration": 10}]}]},
					{"title": "timedOut", "tests": [{"status": "unexpected", "results": [{"status": "timedOut", "retry": 0, "duration": 10}]}]},
					{"title": "interrupted", "tests": [{"status": "unexpected", "results": [{"status": "interrupted", "retry": 0, "duration": 10}]}]},
					{"title": "skipped", "tests": [{"status": "skipped", "results": [{"status": "skipped", "retry": 0, "duration": 0}]}]},
					{"title": "pending", "tests": [{"status": "skipped", "results": [{"status": "pending", "retry": 0, "duration": 0}]}]},
					{"title": "unknown", "tests": [{"status": "unexpected", "results": [{"status": "unknown", "retry": 0, "duration": 10}]}]},
					{"title": "missing attempts", "tests": [{"status": "expected", "results": []}]}
				]
			},
			{
				"title": "empty.spec.ts",
				"file": "specs/empty.spec.ts",
				"specs": []
			}
		]
	}`

	code, raw = postAPI("/api/v1/reports/begin", map[string]any{
		"repository":             defaultRepository,
		"commit":                 commitSHA,
		"gh_run_id":              ghRunID,
		"gh_run_attempt":         runAttempt,
		"framework":              "playwright",
		"name":                   runName,
		"branch":                 branch,
		"total_reports_expected": 1,
	})
	if code != http.StatusOK {
		t.Fatalf("reports/begin = %d (%s)", code, raw)
	}
	var beginResp struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal(raw, &beginResp); err != nil {
		t.Fatalf("unmarshal begin: %v", err)
	}

	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
		"repository":     defaultRepository,
		"commit":         commitSHA,
		"gh_run_id":      ghRunID,
		"gh_run_attempt": runAttempt,
		"framework":      "playwright",
		"name":           runName,
		"branch":         branch,
		"gh_job_id":      jobID,
		"gh_job_name":    jobName,
		"json_files":     []any{map[string]any{"path": "results.json", "size": len(pwPayload)}},
	})
	if reg.StatusCode != http.StatusOK {
		t.Fatalf("reports/register = %d (%s)", reg.StatusCode, reg.Body)
	}
	var regBody struct {
		ReportID string `json:"report_id"`
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(reg.Body, &regBody); err != nil {
		t.Fatalf("unmarshal register: %v", err)
	}

	sh := shard{env: env, token: tok, groupID: regBody.ReportID, uploadID: regBody.UploadID}
	if code := sh.post(t, "json", "results.json", pwPayload); code != http.StatusOK {
		t.Fatalf("upload json = %d, want 200", code)
	}
	sh.waitComplete(t)

	// Verify GET /api/v1/reports/{id} returns matching counts.
	detailResp, err := http.Get(env.ServerURL + "/api/v1/reports/" + beginResp.ReportID)
	if err != nil {
		t.Fatalf("get reports/{id}: %v", err)
	}
	defer func() { _ = detailResp.Body.Close() }()
	detailRaw, _ := io.ReadAll(detailResp.Body)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("reports/{id} = %d (%s)", detailResp.StatusCode, detailRaw)
	}
	var detail struct {
		TestStats struct {
			Passed  int `json:"passed"`
			Failed  int `json:"failed"`
			Flaky   int `json:"flaky"`
			Skipped int `json:"skipped"`
			Total   int `json:"total"`
		} `json:"test_stats"`
		Orchestration struct {
			Tests struct {
				Passed  int `json:"passed"`
				Failed  int `json:"failed"`
				Flaky   int `json:"flaky"`
				Skipped int `json:"skipped"`
				Total   int `json:"total"`
			} `json:"tests"`
		} `json:"orchestration"`
	}
	if err := json.Unmarshal(detailRaw, &detail); err != nil {
		t.Fatalf("unmarshal reports/{id}: %v", err)
	}
	if detail.TestStats.Passed != wantPassed || detail.TestStats.Failed != wantFailed ||
		detail.TestStats.Flaky != wantFlaky || detail.TestStats.Skipped != wantSkipped ||
		detail.TestStats.Total != wantTotal {
		t.Fatalf("reports/{id} test_stats = %+v, want %d/%d/%d/%d/%d",
			detail.TestStats, wantPassed, wantFailed, wantFlaky, wantSkipped, wantTotal)
	}
	if detail.Orchestration.Tests != detail.TestStats {
		t.Fatalf("reports/{id} orchestration.tests = %+v, want %+v", detail.Orchestration.Tests, detail.TestStats)
	}

	// Verify Home projection (LoadHomePageDataWithRule shared) includes PassRate = 50 (skipped excluded).
	var homeRow reports.HomeStaticRun
	deadline := time.Now().Add(5 * time.Second)
	for {
		homeData, err := reports.LoadHomePageDataWithRule(ctx, env.Pool, 20, 0, defaultRepository, "", teststatus.RuleShared)
		if err != nil {
			t.Fatalf("LoadHomePageDataWithRule: %v", err)
		}
		if len(homeData.Static) == 1 && homeData.Static[0].Total == wantTotal {
			homeRow = homeData.Static[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("home static rows = %+v, want 1 row with total=%d", homeData.Static, wantTotal)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if homeRow.Passed != wantPassed || homeRow.Failed != wantFailed || homeRow.Flaky != wantFlaky ||
		homeRow.Skipped != wantSkipped || homeRow.Total != wantTotal {
		t.Fatalf("home projection counts = %d/%d/%d/%d/%d, want %d/%d/%d/%d/%d",
			homeRow.Passed, homeRow.Failed, homeRow.Flaky, homeRow.Skipped, homeRow.Total,
			wantPassed, wantFailed, wantFlaky, wantSkipped, wantTotal)
	}
	if homeRow.PassRate == nil || *homeRow.PassRate != wantPassRate {
		t.Fatalf("home projection PassRate = %v, want %d", homeRow.PassRate, wantPassRate)
	}

	// 5. SSR Run Page (htmlpage.BuildRunPage + ServeRun HTTP handler).
	orchStore := &orchestration.Store{Pool: env.Pool}
	page, redir, err := htmlpage.BuildRunPage(ctx, env.Pool, orchStore, htmlpage.SiteInfo{}, defaultRepository, branch, commitSHA, runName, htmlpage.RunPageOpts{
		GHRunID:      ghRunID,
		GHRunAttempt: runAttempt,
		StatusRule:   teststatus.RuleShared,
	})
	if err != nil || len(redir) != 0 {
		t.Fatalf("BuildRunPage = (%v, %v), want nil err/redir", err, redir)
	}
	if page.TestPassed != wantPassed || page.TestFailed != wantFailed || page.TestFlaky != wantFlaky ||
		page.TestSkipped != wantSkipped || page.TestTotal != wantTotal {
		t.Fatalf("SSR RunPage counts = %d/%d/%d/%d/%d, want %d/%d/%d/%d/%d",
			page.TestPassed, page.TestFailed, page.TestFlaky, page.TestSkipped, page.TestTotal,
			wantPassed, wantFailed, wantFlaky, wantSkipped, wantTotal)
	}
	if page.PassRate != "50" {
		t.Fatalf("SSR RunPage PassRate = %q, want \"50\"", page.PassRate)
	}
	if page.TestStatus != "failed" || page.UnitFail != 2 {
		t.Fatalf("SSR RunPage TestStatus=%q UnitFail=%d, want failed and 2", page.TestStatus, page.UnitFail)
	}

	h := &htmlpage.Handler{Pool: env.Pool, Orch: orchStore, StatusRule: teststatus.RuleShared}
	r := chi.NewRouter()
	r.Get("/reports/{repo}/{branch}/{sha}/{name}", h.ServeRun)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, homeRow.Href, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", homeRow.Href, rec.Code)
	}
	bodyStr := rec.Body.String()
	for _, want := range []string{"50%", ">10</span> total", "4 failed", "3 flaky", ">2</span> skipped"} {
		if !strings.Contains(bodyStr, want) {
			t.Fatalf("SSR ServeRun HTML missing %q", want)
		}
	}

	// 6. Regression guard: literal "v" title must be retained while vertical tab "\v" is trimmed.
	const vRunID = "run-status-parity-vtab"
	if c, b := postAPI("/api/v1/orchestration/begin", map[string]any{
		"repository":             defaultRepository,
		"commit_sha":             commitSHA,
		"gh_run_id":              vRunID,
		"name":                   runName,
		"gh_run_attempt":         runAttempt,
		"branch":                 branch,
		"framework":              "playwright",
		"playwright_project":     "default",
		"total_reports_expected": 1,
		"dispatch_units":         []map[string]any{{"spec_path": "specs/v.spec.ts"}},
	}); c != http.StatusCreated {
		t.Fatalf("v-check orch begin = %d (%s)", c, b)
	}
	if c, b := postAPI("/api/v1/orchestration/checkout", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": vRunID, "name": runName, "gh_run_attempt": runAttempt,
		"gh_job_name": jobName, "gh_job_id": jobID + "-v", "batch_size": 1,
	}); c != http.StatusOK {
		t.Fatalf("v-check orch checkout = %d (%s)", c, b)
	}
	if c, b := postAPI("/api/v1/orchestration/complete", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": vRunID, "name": runName, "gh_run_attempt": runAttempt,
		"gh_job_name": jobName, "gh_job_id": jobID + "-v",
		"results": []map[string]any{{
			"spec_path": "specs/v.spec.ts", "status": "passed", "actual_duration_ms": 10,
			"test_cases": []map[string]any{
				{"title": "v", "full_title": "v", "status": "passed", "retry_count": 0, "duration_ms": 5, "ordinal": 0},
				{"title": "vtab", "full_title": "\v", "status": "failed", "retry_count": 0, "duration_ms": 5, "ordinal": 1},
			},
		}},
	}); c != http.StatusOK {
		t.Fatalf("v-check orch complete = %d (%s)", c, b)
	}
	c, b := postAPI("/api/v1/reports/begin", map[string]any{
		"repository": defaultRepository, "commit": commitSHA,
		"gh_run_id": vRunID, "gh_run_attempt": runAttempt,
		"framework": "playwright", "name": runName, "branch": branch,
		"total_reports_expected": 1,
	})
	if c != http.StatusOK {
		t.Fatalf("v-check reports/begin = %d (%s)", c, b)
	}
	var vBegin struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal(b, &vBegin); err != nil {
		t.Fatalf("unmarshal v-check begin: %v", err)
	}
	vStatusURL := fmt.Sprintf(
		"%s/api/v1/orchestration/status?repository=%s&commit_sha=%s&gh_run_id=%s&name=%s&gh_run_attempt=%s",
		env.ServerURL, url.QueryEscape(defaultRepository), commitSHA, vRunID, runName, runAttempt,
	)
	vStatusResp, err := http.Get(vStatusURL)
	if err != nil {
		t.Fatalf("get v-check orchestration/status: %v", err)
	}
	defer func() { _ = vStatusResp.Body.Close() }()
	vStatusRaw, _ := io.ReadAll(vStatusResp.Body)
	if vStatusResp.StatusCode != http.StatusOK {
		t.Fatalf("v-check orchestration/status = %d (%s)", vStatusResp.StatusCode, vStatusRaw)
	}
	var vOrchStatus struct {
		Tests struct {
			Passed int `json:"passed"`
			Failed int `json:"failed"`
			Total  int `json:"total"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(vStatusRaw, &vOrchStatus); err != nil {
		t.Fatalf("unmarshal v-check orchestration/status: %v", err)
	}
	if vOrchStatus.Tests.Passed != 1 || vOrchStatus.Tests.Failed != 0 || vOrchStatus.Tests.Total != 1 {
		t.Fatalf("v-check orchestration/status tests = %+v, want passed=1 failed=0 total=1", vOrchStatus.Tests)
	}
	vDetailResp, err := http.Get(env.ServerURL + "/api/v1/reports/" + vBegin.ReportID)
	if err != nil {
		t.Fatalf("get v-check reports/{id}: %v", err)
	}
	defer func() { _ = vDetailResp.Body.Close() }()
	vDetailRaw, _ := io.ReadAll(vDetailResp.Body)
	if vDetailResp.StatusCode != http.StatusOK {
		t.Fatalf("v-check reports/{id} = %d (%s)", vDetailResp.StatusCode, vDetailRaw)
	}
	var vDetail struct {
		Orchestration struct {
			Tests struct {
				Passed int `json:"passed"`
				Failed int `json:"failed"`
				Total  int `json:"total"`
			} `json:"tests"`
		} `json:"orchestration"`
	}
	if err := json.Unmarshal(vDetailRaw, &vDetail); err != nil {
		t.Fatalf("unmarshal v-check reports/{id}: %v", err)
	}
	if vDetail.Orchestration.Tests.Passed != 1 || vDetail.Orchestration.Tests.Failed != 0 || vDetail.Orchestration.Tests.Total != 1 {
		t.Fatalf("v-check reports/{id} orchestration.tests = %+v, want passed=1 failed=0 total=1", vDetail.Orchestration.Tests)
	}
}
