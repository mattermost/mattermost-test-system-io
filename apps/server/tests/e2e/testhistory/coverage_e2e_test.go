//go:build e2e

package testhistory

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func TestHistory_LimitDoesNotHideWindowSummary(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)
	for i := 0; i < 6; i++ {
		status := "failed"
		if i == 0 {
			status = "passed"
		}
		ingestDiag(t, env, tok, "master", sha("ab", i), fmt.Sprintf("window-%d", i), status, nil)
	}
	q := url.Values{"repo": {diagRepo}, "test_id": {diagKey}, "branch": {"master"}, "window": {"180d"}, "limit": {"5"}}
	got := getJSON(t, env, "/api/v1/tests/history?"+q.Encode())
	summary := got["summary"].(map[string]any)
	if summary["runs"] != float64(6) || summary["last_pass_commit"] != sha("ab", 0) || got["total_entries"] != float64(6) || got["truncated"] != true || len(got["entries"].([]any)) != 5 {
		t.Fatalf("bounded page hid the passing run from its summary: %v", got)
	}
	entry := got["entries"].([]any)[0].(map[string]any)
	for _, field := range []string{"group_id", "repository", "framework", "gh_run_attempt"} {
		if entry[field] == nil || entry[field] == "" {
			t.Errorf("entry missing %s", field)
		}
	}
	pack := getJSON(t, env, "/api/v1/tests/evidence?group_id="+entry["group_id"].(string))
	if pack["group"].(map[string]any)["commit_sha"] != entry["commit"] {
		t.Fatal("history identity resolved a different run")
	}
}

func TestHistory_TrustedBaselineRequiresCompleteNonPRRuns(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)
	ingestDiag(t, env, tok, "master", sha("ac", 0), "complete-master", "passed", nil)
	pr := 1234
	ingestDiag(t, env, tok, "master", sha("ac", 1), "fork-master", "failed", &pr)
	ingestDiag(t, env, tok, "master", sha("ac", 2), "incomplete-master", "failed", nil)
	if _, err := env.Pool.Exec(context.Background(), `UPDATE report_groups SET status='incomplete', total_reports_expected=2 WHERE gh_run_id='incomplete-master'`); err != nil {
		t.Fatal(err)
	}
	q := url.Values{"repo": {diagRepo}, "test_id": {diagKey}, "baseline": {"true"}, "name": {diagJobName}, "framework": {"playwright"}}
	got := getJSON(t, env, "/api/v1/tests/history?"+q.Encode())
	summary := got["summary"].(map[string]any)
	if summary["runs"] != float64(1) || summary["passed"] != float64(1) || got["baseline"] != true {
		t.Fatalf("untrusted baseline: %v", got)
	}
	q.Set("repo", "mattermost")
	resp, err := http.Get(env.ServerURL + "/api/v1/tests/history?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short repo baseline status=%d, want 400", resp.StatusCode)
	}
}

func TestEvidence_CompletenessRequiresEveryDeclaredReport(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)
	body := diagReport("failed")
	identity := map[string]any{
		"repository": diagRepo, "framework": "playwright", "name": diagJobName,
		"branch": "master", "commit": sha("ad", 1), "gh_run_id": "coverage-run", "gh_run_attempt": "1",
		"gh_job_id": "coverage-1", "gh_job_name": "worker1", "total_reports_expected": 2,
		"json_files":           []any{map[string]any{"path": "results.json", "size": len(body)}},
		"environment_metadata": map[string]any{"server_image_digest": "example/server@sha256:abcdef"},
	}
	uploadDiagReport(t, env, tok, identity, body)
	path := "/api/v1/tests/evidence?" + url.Values{"repository": {diagRepo}, "commit_sha": {sha("ad", 1)}, "gh_run_id": {"coverage-run"}, "name": {diagJobName}}.Encode()
	got := getJSON(t, env, path)
	if got["truncated"] != false || got["complete"] != false {
		t.Fatalf("partial upload considered complete: %v", got)
	}
	group := got["group"].(map[string]any)
	if group["reports_registered"] != float64(1) || group["total_reports_expected"] != float64(2) {
		t.Fatalf("wrong coverage: %v", group)
	}
	identity["gh_job_id"], identity["gh_job_name"] = "coverage-2", "worker2"
	uploadDiagReport(t, env, tok, identity, body)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got = getJSON(t, env, path)
		if got["complete"] == true {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("all reports uploaded but incomplete: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	group = got["group"].(map[string]any)
	if group["reports_complete"] != float64(2) {
		t.Fatalf("wrong completed report count: %v", group)
	}
	q := url.Values{"repo": {diagRepo}, "test_id": {diagKey}, "baseline": {"true"}, "name": {diagJobName}}
	history := getJSON(t, env, "/api/v1/tests/history?"+q.Encode())
	entry := history["entries"].([]any)[0].(map[string]any)
	if entry["environment_metadata"].(map[string]any)["server_image_digest"] != "example/server@sha256:abcdef" {
		t.Fatal("baseline image metadata did not survive")
	}
}
