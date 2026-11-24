package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSpecPath = "../../../api/openapi.yaml"

// testHost is the spec's local server: the router only matches declared hosts.
const testHost = "http://localhost:8080"

const testRID = "11111111-1111-1111-1111-111111111111"

func TestValidatorBodyValidation(t *testing.T) {
	v, err := NewOpenAPIValidator(testSpecPath)
	if err != nil {
		t.Fatalf("NewOpenAPIValidator: %v", err)
	}
	var got []byte // body the downstream handler saw
	h := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))

	upload := "/api/v1/reports/upload/" + testRID + "/" + testRID
	tests := []struct {
		name, path, contentType, body string
		want                          int
	}{
		// Upload routes: the body is not validated, so a wrong content type passes.
		{"upload json skips body", upload + "/json", "text/plain", "not multipart", http.StatusNoContent},
		{"upload screenshots skips body", upload + "/screenshots", "text/plain", "not multipart", http.StatusNoContent},
		{"orchestration screenshots skips body", "/api/v1/orchestration/screenshots", "text/plain", "not multipart", http.StatusNoContent},
		{"legacy upload skips body", "/api/v1/reports", "text/plain", "not multipart", http.StatusNoContent},
		// Non-upload routes still validate the body.
		{"begin rejects missing fields", "/api/v1/reports/begin", "application/json", "{}", http.StatusBadRequest},
		{"begin rejects wrong content type", "/api/v1/reports/begin", "text/plain", "{}", http.StatusBadRequest},
		{"begin accepts valid body", "/api/v1/reports/begin", "application/json",
			`{"repository":"o/r","commit":"abc","gh_run_id":"1","framework":"playwright","name":"n","total_reports_expected":2}`,
			http.StatusNoContent},
		{"begin accepts valid run context", "/api/v1/reports/begin", "application/json",
			`{"repository":"o/r","commit":"abc","gh_run_id":"1","framework":"playwright","name":"n","total_reports_expected":2,"stage":"nightly","event_name":"schedule","base_ref":"main","head_ref":"feat/x","platform":"linux","server_version":"10.5","app_version":"2.25","edition":"enterprise"}`,
			http.StatusNoContent},
		{"begin rejects invalid stage", "/api/v1/reports/begin", "application/json",
			`{"repository":"o/r","commit":"abc","gh_run_id":"1","framework":"playwright","name":"n","total_reports_expected":2,"stage":"bogus"}`,
			http.StatusBadRequest},
		{"orchestration begin accepts run context", "/api/v1/orchestration/begin", "application/json",
			`{"repository":"o/r","commit_sha":"abcdef1","gh_run_id":"1","gh_run_attempt":"1","framework":"playwright","name":"n","lease_timeout_ms":300000,"idle_timeout_ms":600000,"retest_on_fail":false,"retest_budget":1,"total_reports_expected":1,"dispatch_units":[{"spec_path":"a.spec.ts"}],"stage":"manual","event_name":"workflow_dispatch","platform":"ios"}`,
			http.StatusNoContent},
		{"orchestration begin rejects invalid stage", "/api/v1/orchestration/begin", "application/json",
			`{"repository":"o/r","commit_sha":"abcdef1","gh_run_id":"1","name":"n","total_reports_expected":1,"dispatch_units":[{"spec_path":"a.spec.ts"}],"stage":"invalid"}`,
			http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, testHost+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			rec := httptest.NewRecorder()
			got = nil
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("POST %s: got %d, want %d; body=%s", tc.path, rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusNoContent && string(got) != tc.body {
				t.Fatalf("POST %s: handler saw body %q, want %q", tc.path, got, tc.body)
			}
		})
	}
}

// Parameter validation survives the upload opt-out: it is per operation, and
// kin-openapi does not enforce `format: uuid`, so the handlers still parse
// {rid}/{uid} themselves. Query constraints are the observable proof.
func TestValidatorParamValidation(t *testing.T) {
	v, err := NewOpenAPIValidator(testSpecPath)
	if err != nil {
		t.Fatalf("NewOpenAPIValidator: %v", err)
	}
	h := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for q, want := range map[string]int{"limit=0": http.StatusBadRequest, "limit=201": http.StatusBadRequest, "limit=5": http.StatusNoContent} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, testHost+"/api/v1/reports?"+q, nil))
		if rec.Code != want {
			t.Errorf("GET /api/v1/reports?%s: got %d, want %d", q, rec.Code, want)
		}
	}
}
