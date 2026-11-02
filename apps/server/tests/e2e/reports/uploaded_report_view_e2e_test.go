//go:build e2e
// +build e2e

package reportse2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/htmlpage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
)

const reportViewMarker = "existing-report-view"

// An uploaded report with no dispatch record opens the existing report view, not an empty page.
func TestUploadedReportWithoutDispatchOpensReportView(t *testing.T) {
	s := registerShard(t)
	if code := s.post(t, "json", "results.json", minimalPlaywrightJSON); code != http.StatusOK {
		t.Fatalf("json upload status = %d, want 200", code)
	}
	s.waitComplete(t)

	// The home page row for this run carries its results and its link.
	var run reports.HomeStaticRun
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := reports.LoadHomePageData(context.Background(), s.env.Pool, 50, 0, defaultRepository, "")
		if err != nil {
			t.Fatalf("load home: %v", err)
		}
		if len(data.Static) == 1 && data.Static[0].Total > 0 {
			run = data.Static[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("home rows = %+v, want one run with results", data.Static)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if run.Passed != 1 || run.Total != 1 {
		t.Fatalf("run results = passed %d total %d, want 1/1", run.Passed, run.Total)
	}
	if run.Orch != nil {
		t.Fatalf("run has orchestration %+v, want none", run.Orch)
	}

	h := &htmlpage.Handler{
		Pool: s.env.Pool,
		Orch: &orchestration.Store{Pool: s.env.Pool},
		Fallback: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, reportViewMarker)
		}),
	}
	r := chi.NewRouter()
	r.Get("/reports/{repo}/{branch}/{sha}/{name}", h.ServeRun)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, run.Href, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", run.Href, rec.Code)
	}
	if body := rec.Body.String(); body != reportViewMarker {
		t.Fatalf("GET %s served %q, want the existing report view", run.Href, firstLine(body))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
