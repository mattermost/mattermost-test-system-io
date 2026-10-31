package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

type fakeReads struct {
	consolidated string
	summaryCode  int
}

func (f fakeReads) Consolidated(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, f.consolidated)
}

func (f fakeReads) History(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, `{"observations":[]}`)
}

func (f fakeReads) HistorySummary(w http.ResponseWriter, _ *http.Request) {
	if f.summaryCode != 0 {
		w.WriteHeader(f.summaryCode)
		_, _ = io.WriteString(w, `{"code":"RUN_NOT_FOUND","message":"no report group for this run"}`)
		return
	}
	_, _ = io.WriteString(w, `{"tests":[]}`)
}

type fakeStore struct{ objects map[string][]byte }

func (s fakeStore) Put(context.Context, string, io.Reader, string, int64) error { return nil }
func (s fakeStore) Delete(context.Context, string) error                        { return nil }
func (s fakeStore) List(context.Context, string) ([]string, error)              { return nil, nil }
func (s fakeStore) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (s fakeStore) Get(_ context.Context, key string) (io.ReadCloser, storage.ObjectMeta, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, storage.ObjectMeta{}, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), storage.ObjectMeta{}, nil
}

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func connect(t *testing.T, d Deps) *mcp.ClientSession {
	t.Helper()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := NewServer(d).Connect(t.Context(), serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestToolsAreReadOnly(t *testing.T) {
	cs := connect(t, Deps{Reads: fakeReads{}, Store: fakeStore{}})
	list, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
	}
	if strings.Join(names, ",") != "find_runs,get_run_failures,get_screenshot,get_test_history,get_test_runs" {
		t.Errorf("tools = %v", names)
	}
}

func TestRunFailuresKeepsFailuresCleansAndCaps(t *testing.T) {
	specs := []map[string]any{
		{"full_title": "A › passes", "status": "passed", "history": []any{}},
		{"full_title": "A › flakes", "status": "flaky", "error_message": "\x1b[31mError: timed out\x1b[39m", "history": []any{
			map[string]any{"status": "failed", "run_attempt": 1, "created_at": "2026-10-30T11:45:00Z", "error_message": "Error: timed out",
				"screenshots": []any{map[string]any{"file_path": "reports/r1/screenshots/a.png"}}},
			map[string]any{"status": "passed", "run_attempt": 1, "created_at": "2026-10-30T11:46:00Z"},
		}},
	}
	for i := range maxFailures + 5 {
		specs = append(specs, map[string]any{"full_title": fmt.Sprintf("B › fails %03d", i), "status": "failed", "error_message": strings.Repeat("x", maxErrorChars+10), "history": []any{}})
	}
	report, _ := json.Marshal(map[string]any{"overall_status": "failed", "failed": maxFailures + 5, "flaky": 1, "specs": specs})
	cs := connect(t, Deps{Reads: fakeReads{consolidated: string(report)}, Store: fakeStore{}})

	res := callTool(t, cs, "get_run_failures", map[string]any{"repository": "mattermost", "branch": "master", "name": "lane", "commit": "e8d166d"})
	var out RunFailuresOutput
	if err := json.Unmarshal([]byte(text(res)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Failures) != maxFailures || !out.Truncated || out.OverallStatus != "failed" {
		t.Fatalf("%d failures, truncated %v; want %d and truncated", len(out.Failures), out.Truncated, maxFailures)
	}
	flaky := out.Failures[0]
	if flaky.FullTitle != "A › flakes" || flaky.Error != "Error: timed out" || len(flaky.Tries) != 2 ||
		flaky.Tries[0].Screenshots[0] != "reports/r1/screenshots/a.png" {
		t.Errorf("flaky test = %+v; want ANSI stripped and tries with screenshot keys", flaky)
	}
	if r := []rune(out.Failures[1].Error); len(r) != maxErrorChars+1 {
		t.Errorf("error length = %d runes; want capped at %d plus an ellipsis", len(r), maxErrorChars)
	}
}

func TestScreenshot(t *testing.T) {
	store := fakeStore{objects: map[string][]byte{
		"reports/r1/screenshots/ok.png":   pngBytes,
		"reports/r1/screenshots/fake.png": []byte("not an image at all"),
		"reports/r1/screenshots/big.png":  append(pngBytes, make([]byte, maxScreenshotSize)...),
	}}
	cs := connect(t, Deps{Reads: fakeReads{}, Store: store})

	res := callTool(t, cs, "get_screenshot", map[string]any{"key": "reports/r1/screenshots/ok.png"})
	img, ok := res.Content[0].(*mcp.ImageContent)
	if res.IsError || !ok || img.MIMEType != "image/png" || !bytes.Equal(img.Data, pngBytes) {
		t.Fatalf("result = %+v; want the PNG", res)
	}
	for _, key := range []string{
		"reports/r1/../../secrets.png", "reports/r1/report.json", "uploads/r1/a.png", "/reports/r1/a.png",
		"reports/r1/screenshots/fake.png", "reports/r1/screenshots/big.png", "reports/r1/screenshots/missing.png",
	} {
		if res := callTool(t, cs, "get_screenshot", map[string]any{"key": key}); !res.IsError {
			t.Errorf("%s: served; want a tool error", key)
		}
	}
}

func TestHandlerErrorsBecomeToolErrors(t *testing.T) {
	cs := connect(t, Deps{Reads: fakeReads{summaryCode: http.StatusNotFound}, Store: fakeStore{}})
	res := callTool(t, cs, "get_test_history", map[string]any{
		"repository": "mattermost", "branch": "master", "name": "lane", "commit": "e8d166d", "gh_run_id": "1", "gh_run_attempt": "1",
	})
	if !res.IsError || !strings.Contains(text(res), "RUN_NOT_FOUND") {
		t.Errorf("result = %+v; want a tool error naming RUN_NOT_FOUND", res)
	}
	res = callTool(t, cs, "get_test_history", map[string]any{"repository": "mattermost", "branch": "master", "name": "lane", "commit": "e8d166d"})
	if !res.IsError || !strings.Contains(text(res), "find_runs") {
		t.Errorf("without a run ID: %+v; want a tool error pointing at find_runs", res)
	}
}

func TestToolCallsAreAudited(t *testing.T) {
	var logs bytes.Buffer
	cs := connect(t, Deps{Reads: fakeReads{}, Store: fakeStore{}, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	callTool(t, cs, "get_screenshot", map[string]any{"key": "reports/x/nope.txt"})

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("log = %q: %v", logs.String(), err)
	}
	if entry["msg"] != "mcp tool call" || entry["tool"] != "get_screenshot" || entry["error"] != true ||
		!strings.Contains(entry["arguments"].(string), "nope.txt") {
		t.Errorf("audit entry = %v", entry)
	}
}
