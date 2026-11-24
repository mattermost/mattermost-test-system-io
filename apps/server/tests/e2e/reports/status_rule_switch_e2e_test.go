//go:build e2e
// +build e2e

package reportse2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/apikey"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/session"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/htmlpage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/server"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestStatusRuleSwitch_DefaultLegacyVsShared(t *testing.T) {
	// Default testenv uses TSIO_STATUS_RULE=legacy (zero-value StatusRule).
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled())
	env.InsertPolicy(t, "allow-status-switch", 1, string(policy.RoleUploader), map[string]string{
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

	const (
		commitSHA   = "1111222233334444555566667777888899990000"
		branch      = "main"
		runAttempt  = "1"
		orchRunID   = "run-switch-orch-1"
		orchRunName = "pw-switch"
		emptyRunID  = "run-switch-empty-1"
		emptyName   = "pw-empty-fail"
		uploadRunID = "run-switch-upload-1"
		uploadName  = "pw-upload-passrate"
	)

	// 1. Seed orchestrated run with edge cases that diverge between legacy and shared.
	if c, b := postAPI("/api/v1/orchestration/begin", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": orchRunID, "name": orchRunName, "gh_run_attempt": runAttempt,
		"branch": branch, "framework": "playwright", "playwright_project": "default",
		"total_reports_expected": 1,
		"dispatch_units":         []map[string]any{{"spec_path": "specs/switch.spec.ts"}},
	}); c != http.StatusCreated {
		t.Fatalf("orch begin = %d (%s)", c, b)
	}
	if c, b := postAPI("/api/v1/orchestration/checkout", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": orchRunID, "name": orchRunName, "gh_run_attempt": runAttempt,
		"gh_job_name": "worker-1", "gh_job_id": "job-sw-1", "batch_size": 1,
	}); c != http.StatusOK {
		t.Fatalf("orch checkout = %d (%s)", c, b)
	}
	if c, b := postAPI("/api/v1/orchestration/complete", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": orchRunID, "name": orchRunName, "gh_run_attempt": runAttempt,
		"gh_job_name": "worker-1", "gh_job_id": "job-sw-1",
		"results": []map[string]any{{
			"spec_path": "specs/switch.spec.ts", "status": "failed", "actual_duration_ms": 50,
			"test_cases": []map[string]any{
				{"title": "passed", "full_title": "switch > passed", "status": "passed", "retry_count": 0, "duration_ms": 10, "ordinal": 0},
				{"title": "flaky", "full_title": "switch > flaky-only", "status": "flaky", "retry_count": 0, "duration_ms": 10, "ordinal": 1},
				{"title": "pending", "full_title": "switch > pending", "status": "pending", "retry_count": 0, "duration_ms": 0, "ordinal": 2},
				{"title": "unknown", "full_title": "switch > unknown", "status": "unknown", "retry_count": 0, "duration_ms": 10, "ordinal": 3},
				{"title": "skipped", "full_title": "switch > skipped", "status": "skipped", "retry_count": 0, "duration_ms": 0, "ordinal": 4},
				{"title": "failed", "full_title": "switch > failed", "status": "failed", "retry_count": 0, "duration_ms": 10, "ordinal": 5},
				{"title": "blank", "full_title": "   ", "status": "failed", "retry_count": 0, "duration_ms": 1, "ordinal": 6},
				{"title": "nostatus", "full_title": "switch > nostatus", "status": "", "retry_count": 0, "duration_ms": 1, "ordinal": 7},
			},
		}},
	}); c != http.StatusOK {
		t.Fatalf("orch complete = %d (%s)", c, b)
	}
	c, b := postAPI("/api/v1/reports/begin", map[string]any{
		"repository": defaultRepository, "commit": commitSHA,
		"gh_run_id": orchRunID, "gh_run_attempt": runAttempt,
		"framework": "playwright", "name": orchRunName, "branch": branch,
		"total_reports_expected": 1,
	})
	if c != http.StatusOK {
		t.Fatalf("reports/begin = %d (%s)", c, b)
	}
	var orchGroup struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal(b, &orchGroup); err != nil {
		t.Fatalf("unmarshal begin: %v", err)
	}

	// 2. Seed an empty-spec failed run (CompletedFail=1, zero test_cases).
	if c, b := postAPI("/api/v1/orchestration/begin", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": emptyRunID, "name": emptyName, "gh_run_attempt": runAttempt,
		"branch": branch, "framework": "playwright", "playwright_project": "default",
		"total_reports_expected": 1,
		"dispatch_units":         []map[string]any{{"spec_path": "specs/empty.spec.ts"}},
	}); c != http.StatusCreated {
		t.Fatalf("empty orch begin = %d (%s)", c, b)
	}
	if c, b := postAPI("/api/v1/orchestration/checkout", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": emptyRunID, "name": emptyName, "gh_run_attempt": runAttempt,
		"gh_job_name": "worker-1", "gh_job_id": "job-sw-empty", "batch_size": 1,
	}); c != http.StatusOK {
		t.Fatalf("empty orch checkout = %d (%s)", c, b)
	}
	if c, b := postAPI("/api/v1/orchestration/complete", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": emptyRunID, "name": emptyName, "gh_run_attempt": runAttempt,
		"gh_job_name": "worker-1", "gh_job_id": "job-sw-empty",
		"results": []map[string]any{{
			"spec_path": "specs/empty.spec.ts", "status": "failed",
			"actual_duration_ms": 0, "error_message": "spec-did-not-run", "test_cases": []any{},
		}},
	}); c != http.StatusOK {
		t.Fatalf("empty orch complete = %d (%s)", c, b)
	}

	// 3. Upload a Playwright shard with skipped tests to verify JSON/home pass-rate switching (60% legacy vs 90% shared).
	var specsJSON []string
	for i := range 8 {
		specsJSON = append(specsJSON, fmt.Sprintf(`{"title": "p%d", "tests": [{"status": "expected", "results": [{"status": "passed", "retry": 0, "duration": 10}]}]}`, i))
	}
	specsJSON = append(specsJSON,
		`{"title": "fl", "tests": [{"status": "flaky", "results": [{"status": "failed", "retry": 0, "duration": 10}, {"status": "passed", "retry": 1, "duration": 10}]}]}`,
		`{"title": "f", "tests": [{"status": "unexpected", "results": [{"status": "failed", "retry": 0, "duration": 10}]}]}`,
	)
	for i := range 5 {
		specsJSON = append(specsJSON, fmt.Sprintf(`{"title": "s%d", "tests": [{"status": "skipped", "results": [{"status": "skipped", "retry": 0, "duration": 0}]}]}`, i))
	}
	pwUpload := fmt.Sprintf(`{"suites": [{"title": "passrate.spec.ts", "file": "specs/passrate.spec.ts", "specs": [%s]}]}`, strings.Join(specsJSON, ","))

	if c, b := postAPI("/api/v1/reports/begin", map[string]any{
		"repository": defaultRepository, "commit": commitSHA,
		"gh_run_id": uploadRunID, "gh_run_attempt": runAttempt,
		"framework": "playwright", "name": uploadName, "branch": branch,
		"total_reports_expected": 1,
	}); c != http.StatusOK {
		t.Fatalf("upload begin = %d (%s)", c, b)
	}
	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
		"repository": defaultRepository, "commit": commitSHA,
		"gh_run_id": uploadRunID, "gh_run_attempt": runAttempt,
		"framework": "playwright", "name": uploadName, "branch": branch,
		"gh_job_id": "job-sw-upload", "gh_job_name": "worker-1",
		"json_files": []any{map[string]any{"path": "results.json", "size": len(pwUpload)}},
	})
	if reg.StatusCode != http.StatusOK {
		t.Fatalf("upload register = %d (%s)", reg.StatusCode, reg.Body)
	}
	var regBody struct {
		ReportID string `json:"report_id"`
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(reg.Body, &regBody); err != nil {
		t.Fatalf("unmarshal upload register: %v", err)
	}
	sh := shard{env: env, token: tok, groupID: regBody.ReportID, uploadID: regBody.UploadID}
	if code := sh.post(t, "json", "results.json", pwUpload); code != http.StatusOK {
		t.Fatalf("upload json = %d, want 200", code)
	}
	sh.waitComplete(t)

	// Wait until the uploaded group's projection is ready on LoadHomePageData.
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		homeLegacy, err := reports.LoadHomePageData(ctx, env.Pool, 50, 0, defaultRepository, "")
		if err != nil {
			t.Fatalf("LoadHomePageData: %v", err)
		}
		var found bool
		for _, row := range homeLegacy.Static {
			if row.GHRunID == uploadRunID && row.Total == 15 {
				if row.PassRate == nil || *row.PassRate != 60 {
					t.Fatalf("default legacy home PassRate = %v, want 60", row.PassRate)
				}
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for uploaded home projection; static=%+v", homeLegacy.Static)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Build a second server pointing at the same Postgres DB with TSIO_STATUS_RULE=shared.
	sharedSrv := httptest.NewServer(server.Build(server.Deps{
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		Pool:               env.Pool,
		Store:              env.Store,
		APIKeys:            &apikey.Repo{Pool: env.Pool},
		Sessions:           &session.Manager{Pool: env.Pool, TTL: time.Hour},
		Refresh:            &session.RefreshManager{Pool: env.Pool, TTL: time.Hour},
		Policy:             &policy.Engine{Pool: env.Pool},
		Hub:                events.NewHub(),
		OrchestrationStore: env.Orchestrator,
		HTMLViewEnabled:    true,
		UploadTimeoutMs:    3_600_000,
		SearchMinLength:    3,
		MaxUploadBytes:     1 << 24,
		MaxArtifactBytes:   1 << 22,
		PresignTTL:         5 * time.Minute,
		StatusRule:         teststatus.RuleShared,
	}))
	t.Cleanup(sharedSrv.Close)

	type counts struct {
		Passed  int `json:"passed"`
		Failed  int `json:"failed"`
		Flaky   int `json:"flaky"`
		Skipped int `json:"skipped"`
		Total   int `json:"total"`
	}
	assertOrchAndReports := func(baseURL string, want counts) {
		t.Helper()
		statusURL := fmt.Sprintf(
			"%s/api/v1/orchestration/status?repository=%s&commit_sha=%s&gh_run_id=%s&name=%s&gh_run_attempt=%s",
			baseURL, url.QueryEscape(defaultRepository), commitSHA, orchRunID, orchRunName, runAttempt,
		)
		resp, err := http.Get(statusURL)
		if err != nil {
			t.Fatalf("GET orchestration/status: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		var st struct {
			Tests counts `json:"tests"`
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Fatalf("unmarshal orchestration/status: %v", err)
		}
		if st.Tests != want {
			t.Fatalf("%s orchestration/status tests = %+v, want %+v", baseURL, st.Tests, want)
		}

		dResp, err := http.Get(baseURL + "/api/v1/reports/" + orchGroup.ReportID)
		if err != nil {
			t.Fatalf("GET reports/{id}: %v", err)
		}
		defer func() { _ = dResp.Body.Close() }()
		dRaw, _ := io.ReadAll(dResp.Body)
		var det struct {
			Orchestration struct {
				Tests counts `json:"tests"`
			} `json:"orchestration"`
		}
		if err := json.Unmarshal(dRaw, &det); err != nil {
			t.Fatalf("unmarshal reports/{id}: %v", err)
		}
		if det.Orchestration.Tests != want {
			t.Fatalf("%s reports/{id} orchestration.tests = %+v, want %+v", baseURL, det.Orchestration.Tests, want)
		}

		gResp, err := http.Get(baseURL + "/api/v1/reports/grouped?limit=10")
		if err != nil {
			t.Fatalf("GET reports/grouped: %v", err)
		}
		defer func() { _ = gResp.Body.Close() }()
		gRaw, _ := io.ReadAll(gResp.Body)
		var grouped struct {
			Groups []struct {
				Runs []struct {
					GHRunID       string `json:"gh_run_id"`
					Orchestration *struct {
						Tests counts `json:"tests"`
					} `json:"orchestration"`
				} `json:"runs"`
			} `json:"groups"`
		}
		if err := json.Unmarshal(gRaw, &grouped); err != nil {
			t.Fatalf("unmarshal reports/grouped: %v", err)
		}
		var foundGrouped bool
		for _, grp := range grouped.Groups {
			for _, run := range grp.Runs {
				if run.GHRunID == orchRunID && run.Orchestration != nil {
					if run.Orchestration.Tests != want {
						t.Fatalf("%s reports/grouped orchestration.tests = %+v, want %+v", baseURL, run.Orchestration.Tests, want)
					}
					foundGrouped = true
				}
			}
		}
		if !foundGrouped {
			t.Fatalf("%s reports/grouped missing orchRunID %s", baseURL, orchRunID)
		}
	}

	// Assert default (legacy) vs shared on orchestration/status & reports/{id}.
	assertOrchAndReports(env.ServerURL, counts{Passed: 2, Failed: 2, Flaky: 0, Skipped: 1, Total: 8})
	assertOrchAndReports(sharedSrv.URL, counts{Passed: 1, Failed: 2, Flaky: 1, Skipped: 2, Total: 6})

	// Assert Home projection shared PassRate = 90 (vs legacy 60 checked above).
	homeShared, err := reports.LoadHomePageDataWithRule(ctx, env.Pool, 50, 0, defaultRepository, "", teststatus.RuleShared)
	if err != nil {
		t.Fatalf("LoadHomePageDataWithRule(shared): %v", err)
	}
	var foundShared bool
	for _, row := range homeShared.Static {
		if row.GHRunID == uploadRunID {
			if row.PassRate == nil || *row.PassRate != 90 {
				t.Fatalf("shared home PassRate = %v, want 90", row.PassRate)
			}
			foundShared = true
		}
	}
	if !foundShared {
		t.Fatalf("shared home projection missing uploadRunID %s", uploadRunID)
	}

	// Assert SSR Run Page default (legacy) vs shared.
	orchStore := &orchestration.Store{Pool: env.Pool}
	legacyPage, _, err := htmlpage.BuildRunPage(ctx, env.Pool, orchStore, htmlpage.SiteInfo{}, defaultRepository, branch, commitSHA, orchRunName, htmlpage.RunPageOpts{
		GHRunID: orchRunID, GHRunAttempt: runAttempt,
	})
	if err != nil {
		t.Fatalf("BuildRunPage legacy: %v", err)
	}
	if legacyPage.TestPassed != 2 || legacyPage.TestFailed != 5 || legacyPage.TestFlaky != 0 ||
		legacyPage.TestSkipped != 1 || legacyPage.TestTotal != 8 {
		t.Fatalf("legacy SSR counts = %d/%d/%d/%d/%d, want 2/5/0/1/8",
			legacyPage.TestPassed, legacyPage.TestFailed, legacyPage.TestFlaky, legacyPage.TestSkipped, legacyPage.TestTotal)
	}

	sharedPage, _, err := htmlpage.BuildRunPage(ctx, env.Pool, orchStore, htmlpage.SiteInfo{}, defaultRepository, branch, commitSHA, orchRunName, htmlpage.RunPageOpts{
		GHRunID: orchRunID, GHRunAttempt: runAttempt, StatusRule: teststatus.RuleShared,
	})
	if err != nil {
		t.Fatalf("BuildRunPage shared: %v", err)
	}
	if sharedPage.TestPassed != 1 || sharedPage.TestFailed != 2 || sharedPage.TestFlaky != 1 ||
		sharedPage.TestSkipped != 2 || sharedPage.TestTotal != 6 || sharedPage.PassRate != "50" {
		t.Fatalf("shared SSR counts = %d/%d/%d/%d/%d rate=%q, want 1/2/1/2/6 rate=50",
			sharedPage.TestPassed, sharedPage.TestFailed, sharedPage.TestFlaky, sharedPage.TestSkipped, sharedPage.TestTotal, sharedPage.PassRate)
	}

	// Empty-spec failed run SSR overall status: legacy=passed, shared=failed.
	legacyEmpty, _, err := htmlpage.BuildRunPage(ctx, env.Pool, orchStore, htmlpage.SiteInfo{}, defaultRepository, branch, commitSHA, emptyName, htmlpage.RunPageOpts{
		GHRunID: emptyRunID, GHRunAttempt: runAttempt,
	})
	if err != nil || legacyEmpty.TestStatus != "passed" {
		t.Fatalf("legacy empty-spec TestStatus = (%q, %v), want (passed, nil)", legacyEmpty.TestStatus, err)
	}
	sharedEmpty, _, err := htmlpage.BuildRunPage(ctx, env.Pool, orchStore, htmlpage.SiteInfo{}, defaultRepository, branch, commitSHA, emptyName, htmlpage.RunPageOpts{
		GHRunID: emptyRunID, GHRunAttempt: runAttempt, StatusRule: teststatus.RuleShared,
	})
	if err != nil || sharedEmpty.TestStatus != "failed" {
		t.Fatalf("shared empty-spec TestStatus = (%q, %v), want (failed, nil)", sharedEmpty.TestStatus, err)
	}

	// Verify ServeRun HTTP handler respects StatusRule on HTML output.
	repoSeg := strings.TrimPrefix(defaultRepository, defaultRepoOwner+"/")
	serveHTML := func(rule teststatus.Rule) string {
		h := &htmlpage.Handler{Pool: env.Pool, Orch: orchStore, StatusRule: rule}
		r := chi.NewRouter()
		r.Get("/reports/{repo}/{branch}/{sha}/{name}", h.ServeRun)
		rec := httptest.NewRecorder()
		target := fmt.Sprintf("/reports/%s/%s/%s/%s?gh_run_id=%s&gh_run_attempt=%s",
			repoSeg, branch, commitSHA, orchRunName, orchRunID, runAttempt)
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeRun(%q) = %d", rule, rec.Code)
		}
		return rec.Body.String()
	}
	if body := serveHTML(teststatus.RuleLegacy); !strings.Contains(body, ">8</span> total") {
		t.Fatalf("legacy ServeRun missing total=8")
	}
	if body := serveHTML(teststatus.RuleShared); !strings.Contains(body, ">6</span> total") || !strings.Contains(body, "50%") {
		t.Fatalf("shared ServeRun missing total=6 or 50%%")
	}
}
