//go:build e2e
// +build e2e

// Package contract asserts that representative requests against the real
// handler tree produce responses that conform to apps/server/api/openapi.yaml.
// Serves as the automated contract-parity gate.
//
// Approach: start a testenv (real Postgres + real handlers), issue real calls,
// validate both the request AND response with kin-openapi's filter.

package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

func loadSpec(t *testing.T) (*openapi3.T, *openapi3filter.Options) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	// this file → apps/server/tests/contract/ → up 2 = apps/server
	serverDir := filepath.Dir(filepath.Dir(filepath.Dir(filename)))
	spec := filepath.Join(serverDir, "api", "openapi.yaml")

	loader := &openapi3.Loader{Context: t.Context()}
	doc, err := loader.LoadFromFile(spec)
	if err != nil {
		t.Fatalf("load openapi: %v", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("openapi.yaml self-validation: %v", err)
	}
	opts := &openapi3filter.Options{
		AuthenticationFunc: func(_ context.Context, _ *openapi3filter.AuthenticationInput) error { return nil },
	}
	return doc, opts
}

func mustRecordedResponse(t *testing.T, env *testenv.Env, method, path string, hdrs map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, env.ServerURL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

func validateResponse(t *testing.T, doc *openapi3.T, opts *openapi3filter.Options,
	method, path string, status int, header http.Header, body []byte,
) {
	t.Helper()
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	// kin-openapi's gorillamux router matches against the full URL including
	// the host (per doc.Servers). Build a request whose URL prefix matches the
	// first declared server so route lookup succeeds.
	var serverURL string
	if len(doc.Servers) > 0 {
		serverURL = doc.Servers[0].URL
	}
	req, _ := http.NewRequest(method, serverURL+path, nil)
	route, params, err := router.FindRoute(req)
	if err != nil {
		t.Fatalf("find route %s %s: %v", method, path, err)
	}
	reqIn := &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: params,
		Route:      route,
		Options:    opts,
	}
	respIn := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqIn,
		Status:                 status,
		Header:                 header,
		Options:                opts,
	}
	if len(body) > 0 {
		respIn.SetBodyBytes(body)
	}
	if err := openapi3filter.ValidateResponse(t.Context(), respIn); err != nil {
		t.Errorf("response does not conform to openapi.yaml for %s %s (status=%d): %v\nbody=%s",
			method, path, status, err, string(body))
	}
}

func TestHealthEndpointContract(t *testing.T) {
	env := testenv.Start(t)
	doc, opts := loadSpec(t)

	status, hdr, body := mustRecordedResponse(t, env, http.MethodGet, "/health", nil)
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	validateResponse(t, doc, opts, http.MethodGet, "/health", status, hdr, body)
}

func TestListReportsContract_empty(t *testing.T) {
	env := testenv.Start(t)
	env.DefaultReportGroup(t)
	apiKey := env.IssueAPIKey(t, "ci")
	doc, opts := loadSpec(t)

	status, hdr, body := mustRecordedResponse(t, env,
		http.MethodGet, "/api/v1/reports",
		map[string]string{"X-API-Key": apiKey},
	)
	if status != 200 {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	validateResponse(t, doc, opts, http.MethodGet, "/api/v1/reports", status, hdr, body)

	// Sanity: the reports array is present and empty (no shards under the
	// seeded group yet). Field name intentionally asserted — the web pages
	// depend on it.
	var parsed struct {
		Reports []any `json:"reports"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if parsed.Reports == nil {
		t.Error("reports should be non-nil array")
	}
}

// postRecordedResponse is the POST counterpart of mustRecordedResponse: the
// history endpoint carries its file list in a body, so it cannot be exercised by
// the GET-only helper above.
func postRecordedResponse(t *testing.T, env *testenv.Env, path string, payload any) (int, http.Header, []byte) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(env.ServerURL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

// validatePostResponse mirrors validateResponse for a request that has a body.
// The request has to carry one: the route declares it required, so validating
// the response means validating a request the router accepts first.
func validatePostResponse(t *testing.T, doc *openapi3.T, opts *openapi3filter.Options,
	path string, payload any, status int, header http.Header, body []byte,
) {
	t.Helper()
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	var serverURL string
	if len(doc.Servers) > 0 {
		serverURL = doc.Servers[0].URL
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, serverURL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	route, params, err := router.FindRoute(req)
	if err != nil {
		t.Fatalf("find route POST %s: %v", path, err)
	}
	reqIn := &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: params,
		Route:      route,
		Options:    opts,
	}
	// The request is validated too: a body the spec rejects would make the
	// response validation below meaningless.
	if err := openapi3filter.ValidateRequest(t.Context(), reqIn); err != nil {
		t.Fatalf("request does not conform to openapi.yaml for POST %s: %v", path, err)
	}
	respIn := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqIn,
		Status:                 status,
		Header:                 header,
		Options:                opts,
	}
	if len(body) > 0 {
		respIn.SetBodyBytes(body)
	}
	if err := openapi3filter.ValidateResponse(t.Context(), respIn); err != nil {
		t.Errorf("response does not conform to openapi.yaml for POST %s (status=%d): %v\nbody=%s",
			path, status, err, string(body))
	}
}

// TestReportsHistoryContract covers both ways of naming the set of runs. The
// count mode matters most here: it answers with a null `since`, and this is what
// pins that the schema actually permits it.
//
// The spec declares openapi 3.1.0, but kin-openapi — which both serves and
// validates it — implements 3.0 semantics: a 3.1 type array (`type: [string,
// "null"]`) fails the document's own self-validation with "unsupported 'type'
// value null", so nullability has to be spelled `nullable: true`, as it is in
// every other nullable field here. This test is what keeps that from being a
// guess in either direction.
func TestReportsHistoryContract(t *testing.T) {
	env := testenv.Start(t)
	doc, opts := loadSpec(t)
	const path = "/api/v1/reports/history"

	for _, tc := range []struct {
		name    string
		payload map[string]any
	}{
		{"window mode", map[string]any{"repository": "mattermost/contract", "files": []string{"a.spec.ts"}}},
		{"count mode", map[string]any{"repository": "mattermost/contract", "files": []string{"a.spec.ts"}, "runs": 5}},
		{"count mode, branch scoped", map[string]any{"repository": "mattermost/contract", "files": []string{"a.spec.ts"}, "branch": "master", "runs": 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, hdr, body := postRecordedResponse(t, env, path, tc.payload)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", status, body)
			}
			validatePostResponse(t, doc, opts, path, tc.payload, status, hdr, body)
		})
	}
}
