package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the actual route tree with the shipped request validator. Passing an
// upload key or forging internal headers must not reach a mutating handler.
func TestTriageWritesUseDedicatedAuthority(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	h := Build(Deps{Logger: slog.Default(), OpenAPISpecPath: "../../api/openapi.yaml", TriageAPIKey: "actual-triage-key"})
	for _, tc := range []struct{ path, body string }{
		{"/triage/assessments", `{"repository":"mattermost/mattermost","commit_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","gh_run_id":"1","gh_run_attempt":"1","name":"suite"}`},
		{"/triage/repairs/enqueue", `{"report_group_id":"` + id + `","stable_key":"MM-T1","owner":"@owner","ticket":"https://example.com/ticket"}`},
		{"/triage/repairs/claim", `{"repository":"mattermost/mattermost","worker":"worker-1"}`},
		{"/triage/repairs/" + id + "/heartbeat", `{"lease_token":"` + id + `"}`},
		{"/triage/repairs/" + id + "/complete", `{"lease_token":"` + id + `","outcome":"failed","account":"reproduction failed"}`},
		{"/triage/repairs/" + id + "/defect", `{"lease_token":"` + id + `","summary":"product suspect","description":"observed evidence"}`},
		{"/triage/quarantine", `{"repair_id":"` + id + `","owner":"@owner","ticket":"https://example.com/ticket","expires_at":"2026-09-10T12:00:00Z"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1"+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-API-Key", "ordinary-upload-key")
			r.Header.Set("X-TSIO-Triage-Actor", "forged-author")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d, body=%s", w.Code, w.Body)
			}
		})
	}
}
