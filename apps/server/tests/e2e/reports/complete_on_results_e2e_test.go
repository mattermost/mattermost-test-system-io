//go:build e2e
// +build e2e

package reportse2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// A shard is complete once its results land; screenshots are attachments and
// never hold it (or its group) open. Before, a screenshot upload that timed out
// at the gateway left the shard `processing` and the group `incomplete`, though
// every result had arrived (desktop CMT v6.3.2-rc.2, run 40651298431).

// completed is both a finished group's status and a landed upload's; processing is a shard still in flight.
const (
	completed  = "completed"
	complete   = "complete"
	processing = "processing"
)

type shard struct {
	env      *testenv.Env
	token    string
	groupID  string
	uploadID string
}

func registerShard(t *testing.T) shard {
	t.Helper()
	return registerShardWithStore(t, testenv.NewFakeStore())
}

func registerShardWithStore(t *testing.T, store storage.ObjectStore) shard {
	t.Helper()
	env := testenv.Start(t, testenv.WithStore(store), testenv.WithReaperDisabled())
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

// waitComplete polls until the shard and its group are complete (JSON extraction runs after the
// response, and the group follows the shard).
func (s shard) waitComplete(t *testing.T) (screenshots, group string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		report, ss, g := s.statuses(t)
		if report == complete && g == completed {
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
	if report != processing || screenshots != "failed" {
		t.Fatalf("after a failed screenshot upload: shard %q, screenshots %q; want processing, failed", report, screenshots)
	}
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	if _, group := s.waitComplete(t); group != completed {
		t.Fatalf("group = %q, want completed", group)
	}
	// A retry that lands clears the failure it replaces.
	if code := s.post(t, "screenshots", "example-does-something/test-failed-1.png", "\x89PNG"); code != http.StatusOK {
		t.Fatalf("screenshot retry status = %d, want 200", code)
	}
	var reason *string
	if err := s.env.Pool.QueryRow(context.Background(), `SELECT error_message FROM reports WHERE id = $1`, s.uploadID).Scan(&reason); err != nil {
		t.Fatalf("read error_message: %v", err)
	}
	if reason != nil {
		t.Fatalf("error_message = %q after a successful screenshot retry, want none", *reason)
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
	if report != complete || screenshots != completed || group != completed {
		t.Fatalf("after late screenshots: shard %q, screenshots %q, group %q; want complete, completed, completed", report, screenshots, group)
	}
}

// gatedStore holds every read until open is closed, so extraction is still running for as long as a test needs.
type gatedStore struct {
	storage.ObjectStore
	open chan struct{}
}

func (g *gatedStore) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectMeta, error) {
	select {
	case <-g.open:
	case <-ctx.Done():
		return nil, storage.ObjectMeta{}, ctx.Err()
	}
	return g.ObjectStore.Get(ctx, key)
}

func TestScreenshotsDuringExtractionDoNotCompleteTheShardEarly(t *testing.T) {
	// The results take a moment to be persisted after the upload returns; screenshots that land meanwhile
	// (an upload action that sends results first) must not complete the shard before its tests exist.
	store := &gatedStore{ObjectStore: testenv.NewFakeStore(), open: make(chan struct{})}
	s := registerShardWithStore(t, store)
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	if code := s.post(t, "screenshots", "example-does-something/test-failed-1.png", "\x89PNG"); code != http.StatusOK {
		t.Fatalf("screenshot upload status = %d, want 200", code)
	}
	if report, _, _ := s.statuses(t); report != processing {
		t.Fatalf("shard is %q while its results are still being persisted, want processing", report)
	}
	close(store.open)
	s.waitComplete(t)
	var suites int
	if err := s.env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM suites WHERE report_id = $1`, s.uploadID).Scan(&suites); err != nil {
		t.Fatalf("count suites: %v", err)
	}
	if suites == 0 {
		t.Fatal("shard completed with no suites persisted")
	}
}

// unreadableStore stores files but cannot read them back, as when the object store fails mid-extraction.
type unreadableStore struct{ storage.ObjectStore }

func (unreadableStore) Get(context.Context, string) (io.ReadCloser, storage.ObjectMeta, error) {
	return nil, storage.ObjectMeta{}, errors.New("store unavailable")
}

func TestShardWhoseResultsCannotBeReadIsNotComplete(t *testing.T) {
	s := registerShardWithStore(t, unreadableStore{testenv.NewFakeStore()})
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var reason *string
		if err := s.env.Pool.QueryRow(context.Background(), `SELECT error_message FROM reports WHERE id = $1`, s.uploadID).Scan(&reason); err != nil {
			t.Fatalf("read error_message: %v", err)
		}
		if reason != nil {
			if !strings.Contains(*reason, "1 of 1 result files could not be read") {
				t.Fatalf("error_message = %q, want the unread file named", *reason)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("extraction did not record the unread results within 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if report, _, group := s.statuses(t); report != processing || group == completed {
		t.Fatalf("shard %q, group %q with no results read; want processing and the group still open", report, group)
	}
}

// blipStore fails the first reads, as a store does for a moment, then serves them.
type blipStore struct {
	storage.ObjectStore
	failures atomic.Int32
}

func (b *blipStore) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectMeta, error) {
	if b.failures.Add(-1) >= 0 {
		return nil, storage.ObjectMeta{}, errors.New("store unavailable for a moment")
	}
	return b.ObjectStore.Get(ctx, key)
}

func TestExtractionRetriesAStoreBlip(t *testing.T) {
	store := &blipStore{ObjectStore: testenv.NewFakeStore()}
	store.failures.Store(1)
	s := registerShardWithStore(t, store)
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if report, _, group := s.statuses(t); report == complete && group == completed {
			return
		}
		if time.Now().After(deadline) {
			report, _, group := s.statuses(t)
			t.Fatalf("shard %q, group %q after a store blip; want complete, completed", report, group)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// resultsReason waits for the shard's error_message to be set and returns it.
func (s shard) resultsReason(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var reason *string
		if err := s.env.Pool.QueryRow(context.Background(), `SELECT error_message FROM reports WHERE id = $1`, s.uploadID).Scan(&reason); err != nil {
			t.Fatalf("read error_message: %v", err)
		}
		if reason != nil {
			return *reason
		}
		if time.Now().After(deadline) {
			t.Fatal("no error_message recorded within 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestScreenshotOutcomesKeepTheResultsReason(t *testing.T) {
	s := registerShardWithStore(t, unreadableStore{testenv.NewFakeStore()})
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	s.resultsReason(t)
	if code := s.send(t, "screenshots", "text/plain", strings.NewReader("not multipart")); code != http.StatusBadRequest {
		t.Fatalf("broken screenshot upload status = %d, want 400", code)
	}
	if reason := s.resultsReason(t); !strings.HasPrefix(reason, "results could not be read") {
		t.Fatalf("after a failed screenshot upload the reason is %q, want the results one kept", reason)
	}
	if code := s.post(t, "screenshots", "example-does-something/test-failed-1.png", "\x89PNG"); code != http.StatusOK {
		t.Fatalf("screenshot upload status = %d, want 200", code)
	}
	if reason := s.resultsReason(t); !strings.HasPrefix(reason, "results could not be read") {
		t.Fatalf("after a successful screenshot upload the reason is %q, want the results one kept", reason)
	}
}

func TestReaperRetriesResultsOnceTheStoreIsBack(t *testing.T) {
	store := &blipStore{ObjectStore: testenv.NewFakeStore()}
	store.failures.Store(3) // the first read and both inline retries
	s := registerShardWithStore(t, store)
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	s.resultsReason(t)
	if report, _, _ := s.statuses(t); report != processing {
		t.Fatalf("shard is %q after the store stayed down, want processing", report)
	}
	if _, err := s.env.Pool.Exec(context.Background(), `UPDATE reports SET updated_at = now() - interval '2 minutes' WHERE id = $1`, s.uploadID); err != nil {
		t.Fatalf("age the shard: %v", err)
	}
	reaper := &reports.Reaper{Pool: s.env.Pool, Logger: slog.Default(),
		Results: &reports.Handlers{Pool: s.env.Pool, Store: store, Publisher: &events.Publisher{Hub: events.NewHub()}}}

	// Another finalization holding the shard (an upload, or a reaper on another server): this one skips it.
	held, err := s.env.Pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := held.Exec(context.Background(), `SELECT pg_advisory_lock($1, hashtext($2))`, int32(0x54534952), s.uploadID); err != nil {
		t.Fatalf("hold the shard: %v", err)
	}
	reaper.RetryStalledResults(context.Background())
	if report, _, _ := s.statuses(t); report != processing {
		t.Fatalf("shard is %q while another finalization holds it, want processing", report)
	}
	if _, err := held.Exec(context.Background(), `SELECT pg_advisory_unlock($1, hashtext($2))`, int32(0x54534952), s.uploadID); err != nil {
		t.Fatalf("release the shard: %v", err)
	}
	held.Release()

	reaper.RetryStalledResults(context.Background())
	if report, _, group := s.statuses(t); report != complete || group != completed {
		t.Fatalf("after the reaper's retry: shard %q, group %q; want complete, completed", report, group)
	}
	var reason *string
	if err := s.env.Pool.QueryRow(context.Background(), `SELECT error_message FROM reports WHERE id = $1`, s.uploadID).Scan(&reason); err != nil {
		t.Fatalf("read error_message: %v", err)
	}
	if reason != nil {
		t.Fatalf("error_message = %q on a completed shard, want the stale results reason cleared", *reason)
	}
}
