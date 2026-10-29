//go:build e2e
// +build e2e

package reportse2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// A shard is complete once its results land; screenshots are attachments and
// never hold it (or its group) open. Before, a screenshot upload that timed out
// at the gateway left the shard `processing` and the group `incomplete`, though
// every result had arrived (desktop CMT v6.3.2-rc.2, run 40651298431).

// completed is both a finished group's status and a landed upload's.
const completed = "completed"

type shard struct {
	env      *testenv.Env
	token    string
	groupID  string
	uploadID string
}

func registerShard(t *testing.T) shard {
	t.Helper()
	env := testenv.Start(t, testenv.WithStore(testenv.NewFakeStore()), testenv.WithReaperDisabled())
	env.InsertPolicy(t, "allow-reports-e2e", 1, string(policy.RoleUploader), map[string]string{
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
	reg := env.RegisterStatelessUpload(t, "Bearer "+tok, map[string]any{
		"repository":  defaultRepository,
		"json_files":  []any{map[string]any{"path": "results.json", "size": len(minimalPlaywrightJSON)}},
		"screenshots": []any{map[string]any{"path": "example-does-something/test-failed-1.png", "size": 4}},
	})
	if reg.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, want 200 (body=%s)", reg.StatusCode, reg.Body)
	}
	var body struct {
		ReportID string `json:"report_id"`
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(reg.Body, &body); err != nil {
		t.Fatalf("unmarshal register response: %v (body=%s)", err, reg.Body)
	}
	return shard{env: env, token: tok, groupID: body.ReportID, uploadID: body.UploadID}
}

// post sends one multipart file to the shard's json or screenshots endpoint.
func (s shard) post(t *testing.T, kind, name, content string) int {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	part, err := w.CreateFormFile("files", name)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return s.send(t, kind, w.FormDataContentType(), buf)
}

func (s shard) send(t *testing.T, kind, contentType string, body io.Reader) int {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/reports/upload/%s/%s/%s", s.env.ServerURL, s.groupID, s.uploadID, kind)
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// statuses returns the shard's status, its screenshot upload status and its group's status.
func (s shard) statuses(t *testing.T) (report, screenshots, group string) {
	t.Helper()
	var ss *string
	if err := s.env.Pool.QueryRow(context.Background(), `
		SELECT r.status, r.screenshots_upload_status, g.status
		  FROM reports r JOIN report_groups g ON g.id = r.report_group_id
		 WHERE r.id = $1`, s.uploadID).Scan(&report, &ss, &group); err != nil {
		t.Fatalf("read statuses: %v", err)
	}
	if ss != nil {
		screenshots = *ss
	}
	return report, screenshots, group
}

// waitComplete polls until the shard is complete (JSON extraction runs after the response).
func (s shard) waitComplete(t *testing.T) (screenshots, group string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		report, ss, g := s.statuses(t)
		if report == "complete" {
			return ss, g
		}
		if time.Now().After(deadline) {
			t.Fatalf("shard is %q (screenshots %q, group %q), want complete within 5s", report, ss, g)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestShardCompletesOnResultsWhenScreenshotsNeverArrive(t *testing.T) {
	s := registerShard(t)
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	screenshots, group := s.waitComplete(t)
	if screenshots != "started" {
		t.Fatalf("screenshots = %q, want started: they never arrived", screenshots)
	}
	if group != completed {
		t.Fatalf("group = %q, want completed: every shard's results are in", group)
	}
}

func TestFailedScreenshotUploadDoesNotFailTheShard(t *testing.T) {
	s := registerShard(t)
	if code := s.send(t, "screenshots", "text/plain", strings.NewReader("not multipart")); code != http.StatusBadRequest {
		t.Fatalf("broken screenshot upload status = %d, want 400", code)
	}
	report, screenshots, _ := s.statuses(t)
	if report != "processing" || screenshots != "failed" {
		t.Fatalf("after a failed screenshot upload: shard %q, screenshots %q; want processing, failed", report, screenshots)
	}
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	if _, group := s.waitComplete(t); group != completed {
		t.Fatalf("group = %q, want completed", group)
	}
}

func TestLateScreenshotsLeaveTheShardComplete(t *testing.T) {
	s := registerShard(t)
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	s.waitComplete(t)
	if code := s.post(t, "screenshots", "example-does-something/test-failed-1.png", "\x89PNG"); code != http.StatusOK {
		t.Fatalf("late screenshot upload status = %d, want 200", code)
	}
	report, screenshots, group := s.statuses(t)
	if report != "complete" || screenshots != completed || group != completed {
		t.Fatalf("after late screenshots: shard %q, screenshots %q, group %q; want complete, completed, completed", report, screenshots, group)
	}
}
