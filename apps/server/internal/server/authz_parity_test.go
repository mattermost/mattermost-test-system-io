package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

// TestProtectedRoutesMatchRouteMinRole fails when a route behind RequireAuth
// is missing from authapi.RouteMinRole, a table entry is not behind
// RequireAuth, or a protected route skips RequireAuthz or runs it first.
func TestProtectedRoutesMatchRouteMinRole(t *testing.T) {
	// With nil auth deps and enforce mode, an anonymous probe is answered 401
	// by RequireAuth and 403 by RequireAuthz; other middleware passes through.
	h := Build(Deps{Logger: slog.New(slog.DiscardHandler), AuthzMode: authapi.AuthzEnforce})

	protected := map[string]bool{}
	err := chi.Walk(h, func(method, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		authIdx, authzIdx := -1, -1
		for i, mw := range mws {
			switch probe(mw) {
			case http.StatusUnauthorized:
				authIdx = i
			case http.StatusForbidden:
				authzIdx = i
			}
		}
		if (authIdx < 0) != (authzIdx < 0) || authzIdx < authIdx {
			t.Errorf("%s %s: RequireAuth at %d, RequireAuthz at %d; want both, Authz after Auth", method, route, authIdx, authzIdx)
		}
		if authIdx >= 0 {
			protected[method+" "+route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}

	listed := map[string]bool{}
	for k := range authapi.RouteMinRole {
		listed[k] = true
	}
	for _, k := range missing(protected, listed) {
		t.Errorf("route behind RequireAuth missing from authapi.RouteMinRole: %s", k)
	}
	for _, k := range missing(listed, protected) {
		t.Errorf("authapi.RouteMinRole entry not registered behind RequireAuth: %s", k)
	}
}

// probe runs one anonymous GET through mw alone and returns the status it
// wrote, or 0 when it passed the request on (or panicked).
func probe(mw func(http.Handler) http.Handler) (status int) {
	defer func() {
		if recover() != nil {
			status = 0
		}
	}()
	passed := false
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { passed = true })).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if passed {
		return 0
	}
	return rec.Code
}
