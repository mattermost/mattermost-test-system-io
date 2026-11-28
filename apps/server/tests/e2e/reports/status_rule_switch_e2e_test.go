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
	"github.com/google/uuid"

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
	// Both exclude skipped from SSR pass rate: legacy 2/(2+5)=28.5%, shared (1+1)/(1+1+2)=50%.
	orchStore := &orchestration.Store{Pool: env.Pool}
	legacyPage, _, err := htmlpage.BuildRunPage(ctx, env.Pool, orchStore, htmlpage.SiteInfo{}, defaultRepository, branch, commitSHA, orchRunName, htmlpage.RunPageOpts{
		GHRunID: orchRunID, GHRunAttempt: runAttempt,
	})
	if err != nil {
		t.Fatalf("BuildRunPage legacy: %v", err)
	}
	if legacyPage.TestPassed != 2 || legacyPage.TestFailed != 5 || legacyPage.TestFlaky != 0 ||
		legacyPage.TestSkipped != 1 || legacyPage.TestTotal != 8 || legacyPage.PassRate != "28.5" {
		t.Fatalf("legacy SSR counts = %d/%d/%d/%d/%d rate=%q, want 2/5/0/1/8 rate=28.5",
			legacyPage.TestPassed, legacyPage.TestFailed, legacyPage.TestFlaky, legacyPage.TestSkipped, legacyPage.TestTotal, legacyPage.PassRate)
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
	if body := serveHTML(teststatus.RuleLegacy); !strings.Contains(body, ">8</span> total") || !strings.Contains(body, "28.5%") {
		t.Fatalf("legacy ServeRun missing total=8 or 28.5%%")
	}
	if body := serveHTML(teststatus.RuleShared); !strings.Contains(body, ">6</span> total") || !strings.Contains(body, "50%") {
		t.Fatalf("shared ServeRun missing total=6 or 50%%")
	}

	// Verify completed shardless orchestration-backed group (orchRunID, refreshed under legacy)
	// switches counts and pass rate on Home/Repo projections and HTML handlers.
	// First ensure orchGroup's projection is populated (refreshed under legacy).
	if err := reports.RefreshGroupSummaryWithRule(ctx, env.Pool, uuid.MustParse(orchGroup.ReportID), teststatus.RuleLegacy); err != nil {
		t.Fatalf("RefreshGroupSummaryWithRule(legacy): %v", err)
	}
	homeOrchLeg, err := reports.LoadHomePageDataWithRule(ctx, env.Pool, 50, 0, defaultRepository, "", teststatus.RuleLegacy)
	if err != nil {
		t.Fatalf("LoadHomePageDataWithRule(legacy): %v", err)
	}
	homeOrchShr, err := reports.LoadHomePageDataWithRule(ctx, env.Pool, 50, 0, defaultRepository, "", teststatus.RuleShared)
	if err != nil {
		t.Fatalf("LoadHomePageDataWithRule(shared): %v", err)
	}
	findStatic := func(rows []reports.HomeStaticRun, runID string) (reports.HomeStaticRun, bool) {
		for _, r := range rows {
			if r.GHRunID == runID {
				return r, true
			}
		}
		return reports.HomeStaticRun{}, false
	}
	if r, ok := findStatic(homeOrchLeg.Static, orchRunID); !ok || r.Total != 8 || r.Passed != 2 || r.Failed != 2 || r.Flaky != 0 || r.Skipped != 1 || r.PassRate == nil || *r.PassRate != 25 {
		t.Fatalf("legacy orch home static = (%+v, %v), want 2/2/0/1/8 rate=25", r, ok)
	}
	if r, ok := findStatic(homeOrchShr.Static, orchRunID); !ok || r.Total != 6 || r.Passed != 1 || r.Failed != 2 || r.Flaky != 1 || r.Skipped != 2 || r.PassRate == nil || *r.PassRate != 50 {
		t.Fatalf("shared orch home static = (%+v, %v), want 1/2/1/2/6 rate=50", r, ok)
	}

	// Seed live-prod Row 1 shape (1483 passed, 0 failed, 2 flaky, 65 skipped) and all-skipped (0/0/0/4)
	// to verify SSR excludes skipped and hides 0/0 under BOTH legacy and shared rules.
	const (
		prodRow1RunID = "run-switch-prod-row1"
		prodRow1Name  = "pw-prod-row1"
		allSkipRunID  = "run-switch-all-skipped"
		allSkipName   = "pw-all-skipped"
	)
	for _, seed := range []struct {
		runID, name                         string
		passed, failed, flaky, skipped, tot int
	}{
		{prodRow1RunID, prodRow1Name, 1483, 0, 2, 65, 1550},
		{allSkipRunID, allSkipName, 0, 0, 0, 4, 4},
	} {
		statsJSON, _ := json.Marshal(map[string]int{
			"TotalCases": seed.tot, "Passed": seed.passed, "Failed": seed.failed, "Flaky": seed.flaky, "Skipped": seed.skipped,
		})
		if _, err := env.Pool.Exec(ctx, `
			INSERT INTO report_groups (
				framework, name, status, repository, branch, commit_sha,
				gh_run_id, gh_run_attempt, total_reports_expected, test_stats_json
			) VALUES ('playwright', $1, 'completed', $2, $3, $4, $5, $6, 1, $7::jsonb)
		`, seed.name, defaultRepository, branch, commitSHA, seed.runID, runAttempt, statsJSON); err != nil {
			t.Fatalf("seed %s insert projection: %v", seed.runID, err)
		}
	}

	// Verify HTML Home/Repo page builders and HTTP handlers apply rule and exclude skipped from SSR pass rate.
	homeHTMLLeg, err := htmlpage.BuildHomePageWithRule(ctx, env.Pool, htmlpage.SiteInfo{}, 50, 0, defaultRepository, "", teststatus.RuleLegacy)
	if err != nil {
		t.Fatalf("BuildHomePageWithRule(legacy): %v", err)
	}
	homeHTMLShr, err := htmlpage.BuildHomePageWithRule(ctx, env.Pool, htmlpage.SiteInfo{}, 50, 0, defaultRepository, "", teststatus.RuleShared)
	if err != nil {
		t.Fatalf("BuildHomePageWithRule(shared): %v", err)
	}
	findRow := func(rows []htmlpage.HomeRunRow, runID string) (htmlpage.HomeRunRow, bool) {
		for _, r := range rows {
			if r.GHRunID == runID {
				return r, true
			}
		}
		return htmlpage.HomeRunRow{}, false
	}
	if r, ok := findRow(homeHTMLLeg.StaticRuns, uploadRunID); !ok || r.PassRate != "90" {
		t.Fatalf("legacy html home upload row = (%+v, %v), want rate=90 (skipped excluded)", r, ok)
	}
	if r, ok := findRow(homeHTMLShr.StaticRuns, uploadRunID); !ok || r.PassRate != "90" {
		t.Fatalf("shared html home upload row = (%+v, %v), want rate=90", r, ok)
	}
	for _, page := range []struct {
		name string
		rows []htmlpage.HomeRunRow
	}{
		{"legacy", homeHTMLLeg.StaticRuns},
		{"shared", homeHTMLShr.StaticRuns},
	} {
		if r, ok := findRow(page.rows, prodRow1RunID); !ok || r.PassRate != "100" || r.PassRateClass != "rate-perfect" {
			t.Fatalf("%s html home prodRow1 = (%+v, %v), want PassRate=100 PassRateClass=rate-perfect", page.name, r, ok)
		}
		if r, ok := findRow(page.rows, allSkipRunID); !ok || r.PassRate != "" {
			t.Fatalf("%s html home allSkipped = (%+v, %v), want empty PassRate", page.name, r, ok)
		}
	}
	if r, ok := findRow(homeHTMLLeg.StaticRuns, orchRunID); !ok || r.Total != 8 || r.PassRate != "28.5" {
		t.Fatalf("legacy html home orch row = (%+v, %v), want total=8 rate=28.5", r, ok)
	}
	if r, ok := findRow(homeHTMLShr.StaticRuns, orchRunID); !ok || r.Total != 6 || r.PassRate != "50" {
		t.Fatalf("shared html home orch row = (%+v, %v), want total=6 rate=50", r, ok)
	}

	repoHTMLShr, err := htmlpage.BuildRepoPageWithRule(ctx, env.Pool, htmlpage.SiteInfo{}, defaultRepository, 50, 0, "", teststatus.RuleShared)
	if err != nil {
		t.Fatalf("BuildRepoPageWithRule(shared): %v", err)
	}
	if r, ok := findRow(repoHTMLShr.StaticRuns, orchRunID); !ok || r.Total != 6 || r.PassRate != "50" {
		t.Fatalf("shared html repo orch row = (%+v, %v), want total=6 rate=50", r, ok)
	}

	// Verify live in-progress orchestration run switches counts and pass rate on BuildHomeLiveRowsWithRule & ServeHomeLiveFragment.
	const liveRunID = "run-switch-live-1"
	const liveRunName = "pw-live-switch"
	if c, b := postAPI("/api/v1/orchestration/begin", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": liveRunID, "name": liveRunName, "gh_run_attempt": runAttempt,
		"branch": branch, "framework": "playwright", "playwright_project": "default",
		"total_reports_expected": 2,
		"dispatch_units": []map[string]any{
			{"spec_path": "specs/live-a.spec.ts"},
			{"spec_path": "specs/live-b.spec.ts"},
		},
	}); c != http.StatusCreated {
		t.Fatalf("live orch begin = %d (%s)", c, b)
	}
	if c, b := postAPI("/api/v1/orchestration/checkout", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": liveRunID, "name": liveRunName, "gh_run_attempt": runAttempt,
		"gh_job_name": "worker-1", "gh_job_id": "job-sw-live", "batch_size": 1,
	}); c != http.StatusOK {
		t.Fatalf("live orch checkout = %d (%s)", c, b)
	}
	if c, b := postAPI("/api/v1/orchestration/complete", map[string]any{
		"repository": defaultRepository, "commit_sha": commitSHA,
		"gh_run_id": liveRunID, "name": liveRunName, "gh_run_attempt": runAttempt,
		"gh_job_name": "worker-1", "gh_job_id": "job-sw-live",
		"results": []map[string]any{{
			"spec_path": "specs/live-a.spec.ts", "status": "failed", "actual_duration_ms": 20,
			"test_cases": []map[string]any{
				{"title": "p", "full_title": "live > p", "status": "passed", "retry_count": 0, "duration_ms": 10, "ordinal": 0},
				{"title": "f", "full_title": "live > f", "status": "failed", "retry_count": 0, "duration_ms": 10, "ordinal": 1},
				{"title": "s1", "full_title": "live > s1", "status": "skipped", "retry_count": 0, "duration_ms": 0, "ordinal": 2},
				{"title": "s2", "full_title": "live > s2", "status": "skipped", "retry_count": 0, "duration_ms": 0, "ordinal": 3},
				{"title": "unk", "full_title": "live > unk", "status": "unknown", "retry_count": 0, "duration_ms": 5, "ordinal": 4},
			},
		}},
	}); c != http.StatusOK {
		t.Fatalf("live orch complete = %d (%s)", c, b)
	}
	c, b = postAPI("/api/v1/reports/begin", map[string]any{
		"repository": defaultRepository, "commit": commitSHA,
		"gh_run_id": liveRunID, "gh_run_attempt": runAttempt,
		"framework": "playwright", "name": liveRunName, "branch": branch,
		"total_reports_expected": 2,
	})
	if c != http.StatusOK {
		t.Fatalf("live reports/begin = %d (%s)", c, b)
	}
	var liveGroup struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal(b, &liveGroup); err != nil {
		t.Fatalf("unmarshal live begin: %v", err)
	}
	if err := reports.RefreshGroupSummaryWithRule(ctx, env.Pool, uuid.MustParse(liveGroup.ReportID), teststatus.RuleLegacy); err != nil {
		t.Fatalf("RefreshGroupSummaryWithRule(live): %v", err)
	}

	liveRowsLeg, err := htmlpage.BuildHomeLiveRowsWithRule(ctx, env.Pool, defaultRepository, "", teststatus.RuleLegacy)
	if err != nil {
		t.Fatalf("BuildHomeLiveRowsWithRule(legacy): %v", err)
	}
	liveRowsShr, err := htmlpage.BuildHomeLiveRowsWithRule(ctx, env.Pool, defaultRepository, "", teststatus.RuleShared)
	if err != nil {
		t.Fatalf("BuildHomeLiveRowsWithRule(shared): %v", err)
	}
	// Unclassified "unknown" counts as failed in pass rate under both rules (Failed=1+1rem / Failed=2, Passed=1 -> 33.3%).
	if r, ok := findRow(liveRowsLeg, liveRunID); !ok || r.PassRate != "33.3" {
		t.Fatalf("legacy live row = (%+v, %v), want rate=33.3", r, ok)
	}
	if r, ok := findRow(liveRowsShr, liveRunID); !ok || r.PassRate != "33.3" {
		t.Fatalf("shared live row = (%+v, %v), want rate=33.3", r, ok)
	}

	serveLiveFragment := func(rule teststatus.Rule) string {
		h := &htmlpage.Handler{Pool: env.Pool, Orch: orchStore, StatusRule: rule}
		rec := httptest.NewRecorder()
		h.ServeHomeLiveFragment(rec, httptest.NewRequest(http.MethodGet, "/reports/fragment/home-live?repository="+url.QueryEscape(defaultRepository), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeHomeLiveFragment(%q) = %d", rule, rec.Code)
		}
		var payload struct {
			HTML string `json:"html"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode live fragment: %v", err)
		}
		return payload.HTML
	}
	if frag := serveLiveFragment(teststatus.RuleLegacy); !strings.Contains(frag, "33.3%") {
		t.Fatalf("legacy live fragment missing 33.3%%: %s", frag)
	}
	if frag := serveLiveFragment(teststatus.RuleShared); !strings.Contains(frag, "33.3%") {
		t.Fatalf("shared live fragment missing 33.3%%: %s", frag)
	}
}
