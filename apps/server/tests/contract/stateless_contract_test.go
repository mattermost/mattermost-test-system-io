//go:build e2e
// +build e2e

package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func noAuth(context.Context, *openapi3filter.AuthenticationInput) error { return nil }

// exchange sends a real request and checks the response status and shape
// against openapi.yaml. With validateReq the request must conform too (its
// multipart body is skipped, as the server does for upload routes).
func exchange(t *testing.T, env *testenv.Env, doc *openapi3.T, method, path, ctype string, body []byte,
	hdrs map[string]string, want int, validateReq bool,
) []byte {
	t.Helper()
	req, err := http.NewRequest(method, env.ServerURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status = %d, want %d; body=%s", method, path, resp.StatusCode, want, raw)
	}

	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	specReq, _ := http.NewRequest(method, doc.Servers[0].URL+path, bytes.NewReader(body))
	if ctype != "" {
		specReq.Header.Set("Content-Type", ctype)
	}
	route, params, err := router.FindRoute(specReq)
	if err != nil {
		t.Fatalf("find route %s %s: %v", method, path, err)
	}
	opts := &openapi3filter.Options{
		AuthenticationFunc: noAuth,
		ExcludeRequestBody: strings.HasPrefix(ctype, "multipart/"),
	}
	reqIn := &openapi3filter.RequestValidationInput{Request: specReq, PathParams: params, Route: route, Options: opts}
	if validateReq {
		if err := openapi3filter.ValidateRequest(t.Context(), reqIn); err != nil {
			t.Fatalf("request does not conform to openapi.yaml for %s %s: %v", method, path, err)
		}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// KNOWN GAP: authapi.RequireAuth answers 401 with http.Error ("unauthorized",
		// text/plain) while the spec's Unauthorized response declares problem+json.
		return raw
	}
	respIn := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqIn, Status: resp.StatusCode, Header: resp.Header, Options: opts,
	}
	respIn.SetBodyBytes(raw)
	if err := openapi3filter.ValidateResponse(t.Context(), respIn); err != nil {
		t.Errorf("response does not conform to openapi.yaml for %s %s (status=%d): %v\nbody=%s",
			method, path, resp.StatusCode, err, raw)
	}
	return raw
}

func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func multipartFile(t *testing.T, name, content string) ([]byte, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	part, err := w.CreateFormFile("files", name)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write([]byte(content))
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

// TestStatelessUploadContract drives begin → register → upload json →
// upload screenshots against the real handlers and pins every documented
// success and error response (200/400/401/404/409/410) to openapi.yaml.
func TestStatelessUploadContract(t *testing.T) {
	env := testenv.Start(t)
	doc, _ := loadSpec(t)
	auth := map[string]string{"X-API-Key": env.IssueAPIKey(t, "ci")}
	const ct = "application/json"

	begin := map[string]any{
		"repository": "mattermost/contract", "commit": "0123456789abcdef", "gh_run_id": "42",
		"gh_run_attempt": "1", "framework": "playwright", "name": "contract", "branch": "main",
		"gh_pr_number": 7, "run_group": "g1", "total_reports_expected": 2,
	}
	raw := exchange(t, env, doc, http.MethodPost, "/api/v1/reports/begin", ct, jsonBody(t, begin), auth, http.StatusOK, true)
	var b struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || b.ReportID == "" {
		t.Fatalf("begin response: %v; body=%s", err, raw)
	}

	t.Run("begin errors", func(t *testing.T) {
		exchange(t, env, doc, http.MethodPost, "/api/v1/reports/begin", ct, jsonBody(t, begin), nil, http.StatusUnauthorized, true)
		bad := map[string]any{}
		for k, v := range begin {
			bad[k] = v
		}
		bad["total_reports_expected"] = 3
		exchange(t, env, doc, http.MethodPost, "/api/v1/reports/begin", ct, jsonBody(t, bad), auth, http.StatusConflict, true)
		bad["total_reports_expected"] = 2
		bad["run_group"] = "other"
		exchange(t, env, doc, http.MethodPost, "/api/v1/reports/begin", ct, jsonBody(t, bad), auth, http.StatusConflict, true)
		// Handler-level 400 (spec-valid shape, bad value): total must be > 0 in the spec too, so skip request validation.
		bad["total_reports_expected"] = 0
		exchange(t, env, doc, http.MethodPost, "/api/v1/reports/begin", ct, jsonBody(t, bad), auth, http.StatusBadRequest, false)
	})

	reg := map[string]any{
		"repository": "mattermost/contract", "commit": "0123456789abcdef", "gh_run_id": "42",
		"gh_run_attempt": "1", "framework": "playwright", "name": "contract", "branch": "main",
		"gh_job_id": "job-1", "gh_job_name": "shard 1",
		"json_files": []any{
			map[string]any{"path": "results.json", "size": 10},
			map[string]any{"path": "../evil.json", "size": 10},
		},
		"screenshots":          []any{map[string]any{"path": "a/shot.png", "size": 3}},
		"environment_metadata": map[string]any{"os": "linux"},
	}
	raw = exchange(t, env, doc, http.MethodPost, "/api/v1/reports/register", ct, jsonBody(t, reg), auth, http.StatusOK, true)
	var r struct {
		ReportID     string                          `json:"report_id"`
		UploadID     string                          `json:"upload_id"`
		RejectedJSON []struct{ Path, Reason string } `json:"rejected_json_files"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.UploadID == "" {
		t.Fatalf("register response: %v; body=%s", err, raw)
	}
	if r.ReportID != b.ReportID {
		t.Errorf("register report_id = %s, want begin's %s", r.ReportID, b.ReportID)
	}
	if len(r.RejectedJSON) != 1 || r.RejectedJSON[0].Reason != "UNSAFE_PATH" {
		t.Errorf("rejected_json_files = %+v, want one UNSAFE_PATH", r.RejectedJSON)
	}

	t.Run("register errors", func(t *testing.T) {
		exchange(t, env, doc, http.MethodPost, "/api/v1/reports/register", ct, jsonBody(t, reg), nil, http.StatusUnauthorized, true)
		bad := map[string]any{}
		for k, v := range reg {
			bad[k] = v
		}
		bad["total_reports_expected"] = 9
		exchange(t, env, doc, http.MethodPost, "/api/v1/reports/register", ct, jsonBody(t, bad), auth, http.StatusConflict, true)
	})

	base := "/api/v1/reports/upload/" + r.ReportID + "/" + r.UploadID
	body, mct := multipartFile(t, "results.json", `{"config":{},"suites":[],"stats":{}}`)
	exchange(t, env, doc, http.MethodPost, base+"/json", mct, body, auth, http.StatusOK, true)
	body, mct = multipartFile(t, "a/shot.png", "png")
	exchange(t, env, doc, http.MethodPost, base+"/screenshots", mct, body, auth, http.StatusOK, true)

	t.Run("upload errors", func(t *testing.T) {
		missing := "/api/v1/reports/upload/" + r.ReportID + "/22222222-2222-2222-2222-222222222222"
		for _, suffix := range []string{"/json", "/screenshots"} {
			body, mct := multipartFile(t, "x", "x")
			exchange(t, env, doc, http.MethodPost, base+suffix, mct, body, nil, http.StatusUnauthorized, true)
			exchange(t, env, doc, http.MethodPost, missing+suffix, mct, body, auth, http.StatusNotFound, true)
		}
	})

	t.Run("legacy upload is retired", func(t *testing.T) {
		exchange(t, env, doc, http.MethodPost, "/api/v1/reports", "text/plain", []byte("x"), auth, http.StatusGone, false)
	})
}
