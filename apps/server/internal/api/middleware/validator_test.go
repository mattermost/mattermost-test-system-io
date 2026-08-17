package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
)

// baseURL must match one of the `servers` entries in openapi.yaml — kin-openapi's
// gorillamux router matches host as well as path, so a request built with
// httptest's default "example.com" host resolves to no operation and would
// silently pass validation instead of failing it.
const baseURL = "http://localhost:8080"

// specPath resolves apps/server/api/openapi.yaml from this test file's location:
// internal/api/middleware → up 3 = apps/server.
func specPath(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	serverDir := filepath.Join(filepath.Dir(filename), "..", "..", "..")
	return filepath.Join(serverDir, "api", "openapi.yaml")
}

// TestSpecCompiles is the cheap guard the contract test cannot be: the contract
// suite is behind the e2e build tag and needs Docker, so a malformed spec would
// otherwise only surface at server start (where NewOpenAPIValidator fails soft
// and silently disables request validation).
func TestSpecCompiles(t *testing.T) {
	if _, err := NewOpenAPIValidator(specPath(t)); err != nil {
		t.Fatalf("openapi.yaml does not compile: %v", err)
	}
}

// TestSpecRoutesResolve pins the routes that carry query-parameter contracts the
// validator enforces at runtime. A typo in a path would make the validator skip
// the route entirely (unmatched requests pass through), so an unresolvable path
// silently disables validation rather than failing loudly.
func TestSpecRoutesResolve(t *testing.T) {
	v, err := NewOpenAPIValidator(specPath(t))
	if err != nil {
		t.Fatalf("openapi.yaml does not compile: %v", err)
	}

	routes := []struct {
		method string
		target string
	}{
		{http.MethodGet, baseURL + "/api/v1/tests/history?test_id=MM-T1&repo=mattermost/mattermost-mobile"},
		{http.MethodGet, baseURL + "/api/v1/tests/flakiness?repo=mattermost-mobile"},
		{http.MethodGet, baseURL + "/api/v1/tests/failing-elsewhere?test_id=MM-T1&repo=mattermost-mobile"},
		{http.MethodGet, baseURL + "/api/v1/triage/verdicts?repo=mattermost-mobile"},
		{http.MethodPost, baseURL + "/api/v1/triage/verdicts"},
		{http.MethodPost, baseURL + "/api/v1/triage/verdicts/018f0000-0000-7000-8000-000000000000/correction"},
		{http.MethodGet, baseURL + "/api/v1/triage/amnesty?test_id=MM-T1&repo=mattermost-mobile"},
		{http.MethodGet, baseURL + "/api/v1/triage/accuracy?repo=mattermost-mobile"},
		{http.MethodGet, baseURL + "/api/v1/triage/evidence?group_id=018f0000-0000-7000-8000-000000000000"},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.target, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.target, nil)
			if _, _, err := v.router.FindRoute(req); err != nil {
				t.Fatalf("route not found in spec: %v", err)
			}
		})
	}
}

// TestWindowParamRejectsGarbage proves the pattern constraint on the window
// parameter is actually enforced, rather than being decorative YAML.
func TestWindowParamRejectsGarbage(t *testing.T) {
	v, err := NewOpenAPIValidator(specPath(t))
	if err != nil {
		t.Fatalf("openapi.yaml does not compile: %v", err)
	}

	called := false
	h := v.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		baseURL+"/api/v1/tests/history?test_id=MM-T1&repo=mattermost-mobile&window=forever", nil)
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("handler was reached with an invalid window; spec constraint is not enforced")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
