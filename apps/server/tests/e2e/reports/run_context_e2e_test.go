//go:build e2e
// +build e2e

package reportse2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/migrations"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestRunContext_MigrationBackfillAndAppsSeed(t *testing.T) {
	env := testenv.Start(t, testenv.WithReaperDisabled())

	var gotApps map[string]string
	rows, err := env.Pool.Query(t.Context(), `SELECT repository, app FROM apps`)
	if err != nil {
		t.Fatalf("query apps: %v", err)
	}
	gotApps = make(map[string]string)
	for rows.Next() {
		var repo, app string
		if err := rows.Scan(&repo, &app); err != nil {
			t.Fatalf("scan apps: %v", err)
		}
		gotApps[repo] = app
	}
	rows.Close()
	wantApps := map[string]string{
		"mattermost/mattermost":        "mattermost",
		"mattermost/mattermost-mobile": "mobile",
		"mattermost/desktop":           "desktop",
	}
	for repo, want := range wantApps {
		if gotApps[repo] != want {
			t.Fatalf("apps[%q] = %q, want %q", repo, gotApps[repo], want)
		}
	}

	downSQL, err := migrations.FS.ReadFile("000032_run_context.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	upSQL, err := migrations.FS.ReadFile("000032_run_context.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	if _, err := env.Pool.Exec(t.Context(), string(downSQL)); err != nil {
		t.Fatalf("exec down migration: %v", err)
	}

	// Seed pre-000032 report_groups rows and verify backfill SQL classifies every case.
	type seedRow struct {
		name   string
		branch string
		pr     *int
		want   string
	}
	prNum := 42
	zero := 0
	seeds := []seedRow{
		{"pr-num", "feat/anything", &prNum, "pr"},
		{"pr-branch", "pr-123", nil, "pr"},
		{"pr-ref", "refs/heads/pr-007", nil, "pr"},
		{"pr-zero", "pr-0", &zero, "other"},
		{"main-bare", "main", nil, "main"},
		{"master-ref", "refs/heads/master", nil, "main"},
		{"master-upper", "MASTER", nil, "main"},
		{"release-branch", "release-10.5", nil, "release"},
		{"release-bare", "release", nil, "other"},
		{"feature-other", "feat/run-context", nil, "other"},
	}
	for i, s := range seeds {
		_, err := env.Pool.Exec(t.Context(), `
			INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt, gh_pr_number)
			VALUES ('playwright', $1, 'mattermost/mattermost', $2, $3, '1', '1', $4)
		`, s.name, s.branch, "sha-"+string(rune('a'+i)), s.pr)
		if err != nil {
			t.Fatalf("insert pre-migration row %s: %v", s.name, err)
		}
	}

	if _, err := env.Pool.Exec(t.Context(), string(upSQL)); err != nil {
		t.Fatalf("re-exec up migration: %v", err)
	}

	for _, s := range seeds {
		var gotStage string
		if err := env.Pool.QueryRow(t.Context(),
			`SELECT stage FROM report_groups WHERE name = $1`, s.name).Scan(&gotStage); err != nil {
			t.Fatalf("query backfilled stage for %s: %v", s.name, err)
		}
		if gotStage != s.want {
			t.Fatalf("row %s (branch=%q, pr=%v): backfilled stage = %q, want %q",
				s.name, s.branch, s.pr, gotStage, s.want)
		}
	}
}

func postJSONWithAPIKey(t *testing.T, serverURL, apiKey, path string, payload any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new req %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestRunContext_PersistenceMergeAndValidation(t *testing.T) {
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled())
	apiKey := env.IssueAPIKey(t, "ci")

	postJSON := func(path string, payload any) (int, []byte) {
		return postJSONWithAPIKey(t, env.ServerURL, apiKey, path, payload)
	}

	// 1. Orchestration begin resolves stage='nightly' from event_name='schedule' on branch='main'.
	if code, body := postJSON("/api/v1/orchestration/begin", map[string]any{
		"repository": "mattermost/mattermost-mobile", "commit_sha": "1234567890abcdef",
		"gh_run_id": "rc-1", "gh_run_attempt": "1", "framework": "detox",
		"name": "detox-nightly", "branch": "main", "total_reports_expected": 2,
		"dispatch_units": []map[string]any{{"spec_path": "a.e2e.ts"}},
		"event_name":     "schedule", "server_version": "10.5.0",
		"app_version": "2.25.0", "edition": "enterprise",
	}); code != http.StatusCreated {
		t.Fatalf("orchestration/begin status=%d body=%s", code, body)
	}

	// 2. Subsequent /reports/begin without event_name must NOT overwrite stage='nightly' or existing fields.
	code, beginBody := postJSON("/api/v1/reports/begin", map[string]any{
		"repository": "mattermost/mattermost-mobile", "commit": "1234567890abcdef",
		"gh_run_id": "rc-1", "gh_run_attempt": "1", "framework": "detox",
		"name": "detox-nightly", "branch": "main", "total_reports_expected": 2,
	})
	if code != http.StatusOK {
		t.Fatalf("reports/begin status=%d body=%s", code, beginBody)
	}
	var beginResp struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal(beginBody, &beginResp); err != nil {
		t.Fatalf("decode begin: %v", err)
	}

	// 3. Per-shard /reports/register stores reports.platform while preserving group context.
	code, regBody := postJSON("/api/v1/reports/register", map[string]any{
		"repository": "mattermost/mattermost-mobile", "commit": "1234567890abcdef",
		"gh_run_id": "rc-1", "gh_run_attempt": "1", "framework": "detox",
		"name": "detox-nightly", "branch": "main", "platform": "ios",
		"gh_job_id": "job-ios", "gh_job_name": "detox-ios",
		"json_files": []any{}, "screenshots": []any{},
	})
	if code != http.StatusOK {
		t.Fatalf("reports/register status=%d body=%s", code, regBody)
	}
	var regResp struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(regBody, &regResp); err != nil {
		t.Fatalf("decode register: %v", err)
	}

	var stage, eventName, serverVer, appVer, edition, groupPlatform, shardPlatform string
	if err := env.Pool.QueryRow(t.Context(), `
		SELECT stage, COALESCE(event_name,''), COALESCE(server_version,''), COALESCE(app_version,''),
		       COALESCE(edition,''), COALESCE(platform,'')
		  FROM report_groups WHERE id = $1
	`, beginResp.ReportID).Scan(&stage, &eventName, &serverVer, &appVer, &edition, &groupPlatform); err != nil {
		t.Fatalf("query report_groups: %v", err)
	}
	if stage != "nightly" || eventName != "schedule" || serverVer != "10.5.0" ||
		appVer != "2.25.0" || edition != "enterprise" || groupPlatform != "ios" {
		t.Fatalf("unexpected group context: stage=%q event=%q sv=%q av=%q ed=%q plat=%q",
			stage, eventName, serverVer, appVer, edition, groupPlatform)
	}

	if err := env.Pool.QueryRow(t.Context(), `
		SELECT COALESCE(platform,'') FROM reports WHERE id = $1
	`, regResp.UploadID).Scan(&shardPlatform); err != nil {
		t.Fatalf("query reports.platform: %v", err)
	}
	if shardPlatform != "ios" {
		t.Fatalf("reports.platform = %q, want ios", shardPlatform)
	}

	// 4. Empty branch starts at stage='other', then /reports/register with branch='main' upgrades to 'main'.
	code, b2 := postJSON("/api/v1/reports/begin", map[string]any{
		"repository": "mattermost/mattermost", "commit": "aaaaaaaaaaaaaaaa",
		"gh_run_id": "rc-2", "gh_run_attempt": "1", "framework": "playwright",
		"name": "pw-upgrade", "branch": "", "total_reports_expected": 1,
	})
	if code != http.StatusOK {
		t.Fatalf("begin empty branch status=%d body=%s", code, b2)
	}
	var b2Resp struct {
		ReportID string `json:"report_id"`
	}
	_ = json.Unmarshal(b2, &b2Resp)
	if code, r2 := postJSON("/api/v1/reports/register", map[string]any{
		"repository": "mattermost/mattermost", "commit": "aaaaaaaaaaaaaaaa",
		"gh_run_id": "rc-2", "gh_run_attempt": "1", "framework": "playwright",
		"name": "pw-upgrade", "branch": "main", "gh_job_id": "j2", "gh_job_name": "j2",
		"json_files": []any{}, "screenshots": []any{},
	}); code != http.StatusOK {
		t.Fatalf("register upgrade status=%d body=%s", code, r2)
	}
	var upgradedStage string
	if err := env.Pool.QueryRow(t.Context(), `SELECT stage FROM report_groups WHERE id = $1`, b2Resp.ReportID).Scan(&upgradedStage); err != nil {
		t.Fatalf("query upgraded stage: %v", err)
	}
	if upgradedStage != "main" {
		t.Fatalf("upgraded stage = %q, want main", upgradedStage)
	}

	// 5. Invalid explicit stage returns 400 on all three write endpoints.
	for _, tc := range []struct {
		path string
		body map[string]any
	}{
		{"/api/v1/reports/begin", map[string]any{
			"repository": "mattermost/mattermost", "commit": "bbbbbbbbbbbbbbbb",
			"gh_run_id": "rc-bad", "framework": "playwright", "name": "bad",
			"total_reports_expected": 1, "stage": "invalid",
		}},
		{"/api/v1/reports/register", map[string]any{
			"repository": "mattermost/mattermost", "commit": "bbbbbbbbbbbbbbbb",
			"gh_run_id": "rc-bad", "framework": "playwright", "name": "bad",
			"gh_job_id": "j", "gh_job_name": "j", "stage": "invalid",
		}},
		{"/api/v1/orchestration/begin", map[string]any{
			"repository": "mattermost/mattermost", "commit_sha": "bbbbbbbbbbbbbbbb",
			"gh_run_id": "rc-bad", "name": "bad", "total_reports_expected": 1,
			"dispatch_units": []map[string]any{{"spec_path": "a.spec.ts"}}, "stage": "invalid",
		}},
	} {
		if c, body := postJSON(tc.path, tc.body); c != http.StatusBadRequest {
			t.Fatalf("%s invalid stage status=%d, want 400; body=%s", tc.path, c, body)
		}
	}
}

func TestRunContext_ConflictRollback(t *testing.T) {
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled())
	apiKey := env.IssueAPIKey(t, "ci")

	code, beginBody := postJSONWithAPIKey(t, env.ServerURL, apiKey, "/api/v1/reports/begin", map[string]any{
		"repository": "mattermost/mattermost", "commit": "cccccccccccccccc",
		"gh_run_id": "rc-conflict", "gh_run_attempt": "1", "framework": "playwright",
		"name": "pw-conflict", "run_group": "rg-1", "branch": "", "total_reports_expected": 2,
	})
	if code != http.StatusOK {
		t.Fatalf("seed begin status=%d body=%s", code, beginBody)
	}
	var seedResp struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal(beginBody, &seedResp); err != nil {
		t.Fatalf("decode seed begin: %v", err)
	}

	assertUnchanged := func(label string) {
		t.Helper()
		var (
			stage, eventName, baseRef, headRef, platform, serverVer, appVer, edition, branch, runGroup string
			prNum, totalExpected                                                                       *int
			envMeta                                                                                    []byte
			reportsCount                                                                               int
		)
		if err := env.Pool.QueryRow(t.Context(), `
			SELECT stage, COALESCE(event_name,''), COALESCE(base_ref,''), COALESCE(head_ref,''),
			       COALESCE(platform,''), COALESCE(server_version,''), COALESCE(app_version,''),
			       COALESCE(edition,''), branch, COALESCE(run_group,''), gh_pr_number,
			       total_reports_expected, environment_metadata
			  FROM report_groups WHERE id = $1
		`, seedResp.ReportID).Scan(
			&stage, &eventName, &baseRef, &headRef,
			&platform, &serverVer, &appVer, &edition,
			&branch, &runGroup, &prNum, &totalExpected, &envMeta,
		); err != nil {
			t.Fatalf("%s: query report_groups: %v", label, err)
		}
		if stage != "other" || eventName != "" || baseRef != "" || headRef != "" ||
			platform != "" || serverVer != "" || appVer != "" || edition != "" ||
			branch != "" || runGroup != "rg-1" || prNum != nil ||
			totalExpected == nil || *totalExpected != 2 || len(envMeta) != 0 {
			t.Fatalf("%s: report_groups mutated on 409: stage=%q event=%q base=%q head=%q plat=%q sv=%q av=%q ed=%q branch=%q rg=%q pr=%v total=%v env=%s",
				label, stage, eventName, baseRef, headRef, platform, serverVer, appVer, edition, branch, runGroup, prNum, totalExpected, envMeta)
		}
		if err := env.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM reports WHERE report_group_id = $1`, seedResp.ReportID,
		).Scan(&reportsCount); err != nil {
			t.Fatalf("%s: query reports count: %v", label, err)
		}
		if reportsCount != 0 {
			t.Fatalf("%s: reports count = %d, want 0", label, reportsCount)
		}
	}

	// 1. /reports/begin with mismatched total_reports_expected must 409 and roll back all context changes.
	if c, b := postJSONWithAPIKey(t, env.ServerURL, apiKey, "/api/v1/reports/begin", map[string]any{
		"repository": "mattermost/mattermost", "commit": "cccccccccccccccc",
		"gh_run_id": "rc-conflict", "gh_run_attempt": "1", "framework": "playwright",
		"name": "pw-conflict", "run_group": "rg-1", "branch": "main", "gh_pr_number": 99,
		"total_reports_expected": 3, "stage": "nightly", "event_name": "schedule",
		"base_ref": "main", "head_ref": "feat/x", "platform": "linux",
		"server_version": "10.5.0", "app_version": "2.25.0", "edition": "enterprise",
	}); c != http.StatusConflict {
		t.Fatalf("begin total mismatch status=%d, want 409; body=%s", c, b)
	}
	assertUnchanged("begin total mismatch")

	// 2. /reports/register with mismatched run_group must 409 and roll back context + metadata changes.
	if c, b := postJSONWithAPIKey(t, env.ServerURL, apiKey, "/api/v1/reports/register", map[string]any{
		"repository": "mattermost/mattermost", "commit": "cccccccccccccccc",
		"gh_run_id": "rc-conflict", "gh_run_attempt": "1", "framework": "playwright",
		"name": "pw-conflict", "run_group": "rg-other", "branch": "main", "gh_pr_number": 99,
		"gh_job_id": "job-1", "gh_job_name": "shard-1",
		"json_files": []any{}, "screenshots": []any{},
		"environment_metadata": map[string]any{"os": "ubuntu"},
		"stage":                "nightly", "event_name": "schedule", "platform": "linux",
		"server_version": "10.5.0", "app_version": "2.25.0", "edition": "enterprise",
	}); c != http.StatusConflict {
		t.Fatalf("register run_group mismatch status=%d, want 409; body=%s", c, b)
	}
	assertUnchanged("register run_group mismatch")

	// 3. /reports/register with mismatched total_reports_expected must 409 and roll back.
	if c, b := postJSONWithAPIKey(t, env.ServerURL, apiKey, "/api/v1/reports/register", map[string]any{
		"repository": "mattermost/mattermost", "commit": "cccccccccccccccc",
		"gh_run_id": "rc-conflict", "gh_run_attempt": "1", "framework": "playwright",
		"name": "pw-conflict", "branch": "main", "total_reports_expected": 5,
		"gh_job_id": "job-1", "gh_job_name": "shard-1",
		"json_files": []any{}, "screenshots": []any{},
		"platform": "linux", "server_version": "10.5.0",
	}); c != http.StatusConflict {
		t.Fatalf("register total mismatch status=%d, want 409; body=%s", c, b)
	}
	assertUnchanged("register total mismatch")
}

func TestRunContext_OrchestrationBeginReplayMerge(t *testing.T) {
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled())
	apiKey := env.IssueAPIKey(t, "ci")

	// First call: 201 Created with initial server_version and empty branch (stage='other').
	c1, b1 := postJSONWithAPIKey(t, env.ServerURL, apiKey, "/api/v1/orchestration/begin", map[string]any{
		"repository": "mattermost/mattermost", "commit_sha": "dddddddddddddddd",
		"gh_run_id": "rc-replay", "gh_run_attempt": "1", "framework": "playwright",
		"name": "pw-replay", "branch": "", "total_reports_expected": 2,
		"dispatch_units": []map[string]any{{"spec_path": "a.spec.ts"}},
		"server_version": "10.4.0",
	})
	if c1 != http.StatusCreated {
		t.Fatalf("first orchestration/begin status=%d, want 201; body=%s", c1, b1)
	}
	var firstResp struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(b1, &firstResp); err != nil {
		t.Fatalf("decode first begin: %v", err)
	}

	// Idempotent replay with missing context fields + conflicting server_version:
	// must return 200 OK, keep existing server_version='10.4.0', and merge missing fields.
	c2, b2 := postJSONWithAPIKey(t, env.ServerURL, apiKey, "/api/v1/orchestration/begin", map[string]any{
		"repository": "mattermost/mattermost", "commit_sha": "dddddddddddddddd",
		"gh_run_id": "rc-replay", "gh_run_attempt": "1", "framework": "playwright",
		"name": "pw-replay", "branch": "", "total_reports_expected": 2,
		"dispatch_units": []map[string]any{{"spec_path": "a.spec.ts"}},
		"event_name":     "schedule", "base_ref": "main", "head_ref": "feat/replay",
		"platform": "linux", "server_version": "10.5.0",
		"app_version": "2.25.0", "edition": "enterprise",
	})
	if c2 != http.StatusOK {
		t.Fatalf("replay orchestration/begin status=%d, want 200; body=%s", c2, b2)
	}
	var secondResp struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(b2, &secondResp); err != nil {
		t.Fatalf("decode replay begin: %v", err)
	}
	if secondResp.RunID != firstResp.RunID {
		t.Fatalf("replay run_id = %q, want %q", secondResp.RunID, firstResp.RunID)
	}

	var stage, eventName, baseRef, headRef, platform, serverVer, appVer, edition string
	if err := env.Pool.QueryRow(t.Context(), `
		SELECT stage, COALESCE(event_name,''), COALESCE(base_ref,''), COALESCE(head_ref,''),
		       COALESCE(platform,''), COALESCE(server_version,''), COALESCE(app_version,''),
		       COALESCE(edition,'')
		  FROM report_groups
		 WHERE repository = 'mattermost/mattermost' AND commit_sha = 'dddddddddddddddd'
		   AND gh_run_id = 'rc-replay' AND name = 'pw-replay' AND gh_run_attempt = '1'
	`).Scan(&stage, &eventName, &baseRef, &headRef, &platform, &serverVer, &appVer, &edition); err != nil {
		t.Fatalf("query report_groups: %v", err)
	}
	if stage != "nightly" || eventName != "schedule" || baseRef != "main" || headRef != "feat/replay" ||
		platform != "linux" || serverVer != "10.4.0" || appVer != "2.25.0" || edition != "enterprise" {
		t.Fatalf("unexpected merged context on replay: stage=%q event=%q base=%q head=%q plat=%q sv=%q av=%q ed=%q",
			stage, eventName, baseRef, headRef, platform, serverVer, appVer, edition)
	}

	var runCount, groupCount int
	if err := env.Pool.QueryRow(t.Context(), `
		SELECT count(*) FROM orchestration_runs
		 WHERE repository = 'mattermost/mattermost' AND commit_sha = 'dddddddddddddddd'
		   AND gh_run_id = 'rc-replay' AND name = 'pw-replay' AND gh_run_attempt = '1'
	`).Scan(&runCount); err != nil {
		t.Fatalf("count orchestration_runs: %v", err)
	}
	if err := env.Pool.QueryRow(t.Context(), `
		SELECT count(*) FROM report_groups
		 WHERE repository = 'mattermost/mattermost' AND commit_sha = 'dddddddddddddddd'
		   AND gh_run_id = 'rc-replay' AND name = 'pw-replay' AND gh_run_attempt = '1'
	`).Scan(&groupCount); err != nil {
		t.Fatalf("count report_groups: %v", err)
	}
	if runCount != 1 || groupCount != 1 {
		t.Fatalf("counts run=%d group=%d, want 1/1", runCount, groupCount)
	}
}
