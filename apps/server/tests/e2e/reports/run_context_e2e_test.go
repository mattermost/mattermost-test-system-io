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

func TestRunContext_PersistenceMergeAndValidation(t *testing.T) {
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled())
	apiKey := env.IssueAPIKey(t, "ci")

	postJSON := func(path string, payload any) (int, []byte) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", path, err)
		}
		req, err := http.NewRequest(http.MethodPost, env.ServerURL+path, bytes.NewReader(raw))
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
