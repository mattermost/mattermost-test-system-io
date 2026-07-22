//go:build e2e
// +build e2e

// Package reportse2e exercises the stateless report upload lifecycle
// (register + upload) end to end against a real Postgres testcontainer.
package reportse2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const (
	defaultRepository  = "mattermost/reports-e2e"
	defaultRepoOwner   = "mattermost"
	defaultOIDCSubject = "repo:mattermost/reports-e2e:ref:refs/heads/main"
)

// slowStore delays every Get by a fixed duration. The initial multipart
// write only uses Put, so this makes just the background re-fetch step
// observably slow.
type slowStore struct {
	storage.ObjectStore
	delay time.Duration
}

func (s *slowStore) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectMeta, error) {
	time.Sleep(s.delay)
	return s.ObjectStore.Get(ctx, key)
}

// minimalPlaywrightJSON is a small but valid Playwright JSON report — one
// suite, one spec, one passing test — enough for ingest.Extract to produce a
// real suite row.
const minimalPlaywrightJSON = `{
  "suites": [
    {
      "title": "example.spec.ts",
      "file": "example.spec.ts",
      "specs": [
        {
          "title": "does something",
          "tests": [
            {
              "projectName": "chrome",
              "status": "expected",
              "results": [
                {"status": "passed", "duration": 100, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z"}
              ]
            }
          ]
        }
      ]
    }
  ]
}`

// TestUploadJSON_RespondsBeforeExtractionCompletes asserts UploadJSON's
// response doesn't wait on suite extraction: it injects a delay into the
// store's Get (used only by the background extraction step), checks the
// HTTP response returns well under that delay, then polls until extraction
// has landed the suite row anyway.
func TestUploadJSON_RespondsBeforeExtractionCompletes(t *testing.T) {
	const getDelay = 400 * time.Millisecond

	store := &slowStore{ObjectStore: testenv.NewFakeStore(), delay: getDelay}
	env := testenv.Start(t, testenv.WithStore(store))
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
		"repository": defaultRepository,
		"gh_job_id":  "job-async-1",
		"json_files": []any{
			map[string]any{"path": "results.json", "size": len(minimalPlaywrightJSON)},
		},
	})
	if reg.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, want 200 (body=%s)", reg.StatusCode, reg.Body)
	}
	var regBody struct {
		ReportID string `json:"report_id"`
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(reg.Body, &regBody); err != nil {
		t.Fatalf("unmarshal register response: %v (body=%s)", err, reg.Body)
	}
	if regBody.ReportID == "" || regBody.UploadID == "" {
		t.Fatalf("register did not return report_id/upload_id; body=%s", reg.Body)
	}

	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	part, err := w.CreateFormFile("files", "results.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(minimalPlaywrightJSON)); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	uploadURL := fmt.Sprintf("%s/api/v1/reports/upload/%s/%s/json", env.ServerURL, regBody.ReportID, regBody.UploadID)
	req, err := http.NewRequest(http.MethodPost, uploadURL, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	elapsed := time.Since(start)
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, want 200 (body=%s)", resp.StatusCode, respBody)
	}

	// A synchronous handler would take at least getDelay (one delayed Get
	// call). Half the delay leaves headroom for CI jitter while still
	// catching a regression.
	if elapsed >= getDelay/2 {
		t.Fatalf("upload response took %s, want well under the %s background extraction delay — "+
			"looks like UploadJSON is blocking on suite extraction again", elapsed, getDelay)
	}

	reportUUID, err := uuid.Parse(regBody.UploadID)
	if err != nil {
		t.Fatalf("parse upload_id: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var suiteCount int
	for {
		if err := env.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM suites WHERE report_id = $1`, reportUUID).Scan(&suiteCount); err != nil {
			t.Fatalf("count suites: %v", err)
		}
		if suiteCount > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background suite extraction did not complete within 5s (suites=%d)", suiteCount)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
