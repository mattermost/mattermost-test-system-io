package middleware

import (
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
	h := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, testHost+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("POST %s: got %d, want %d; body=%s", tc.path, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
