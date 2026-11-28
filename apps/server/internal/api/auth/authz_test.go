package authapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	apiroot "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
)

var discard = slog.New(slog.DiscardHandler)

func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		role, min policy.Role
		want      bool
	}{
		{policy.RoleViewer, policy.RoleViewer, true},
		{policy.RoleViewer, policy.RoleUploader, false},
		{policy.RoleUploader, policy.RoleViewer, true},
		{policy.RoleUploader, policy.RoleEditor, false},
		{policy.RoleEditor, policy.RoleUploader, true},
		{policy.RoleEditor, policy.RoleAdmin, false},
		{policy.RoleAdmin, policy.RoleAdmin, true},
		{policy.RoleDenied, policy.RoleViewer, false},
		{"", policy.RoleViewer, false},
		{"superuser", policy.RoleViewer, false},
		// An unknown or denied minimum fails closed, even for admin.
		{policy.RoleAdmin, policy.RoleDenied, false},
		{policy.RoleAdmin, "", false},
		{policy.RoleViewer, "superuser", false},
	}
	for _, c := range cases {
		if got := c.role.AtLeast(c.min); got != c.want {
			t.Errorf("%q.AtLeast(%q) = %v, want %v", c.role, c.min, got, c.want)
		}
	}
}

func TestMinRoleForRoute(t *testing.T) {
	cases := []struct {
		method, pattern string
		want            policy.Role
		listed          bool
	}{
		{http.MethodPost, "/api/v1/reports/begin", policy.RoleUploader, true},
		{http.MethodDelete, "/api/v1/reports/{id}", policy.RoleAdmin, true},
		{http.MethodGet, "/api/v1/artifacts/{id}", policy.RoleViewer, true},
		{http.MethodPost, "/api/v1/unlisted", policy.RoleAdmin, false},
		{http.MethodPut, "/api/v1/unlisted", policy.RoleAdmin, false},
		{http.MethodPatch, "/api/v1/unlisted", policy.RoleAdmin, false},
		{http.MethodDelete, "", policy.RoleAdmin, false},
		{http.MethodGet, "/api/v1/unlisted", policy.RoleViewer, false},
	}
	for _, c := range cases {
		got, listed := MinRoleForRoute(c.method, c.pattern)
		if got != c.want || listed != c.listed {
			t.Errorf("MinRoleForRoute(%s %s) = (%q, %v), want (%q, %v)", c.method, c.pattern, got, listed, c.want, c.listed)
		}
	}
}

func withRole(role policy.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), subjectKey, Subject{Kind: "apikey", Role: role})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

func noContent(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// newAuthzRouter mirrors server.Build: a /api/v1 subrouter whose protected
// group injects role (in place of RequireAuth) and then runs RequireAuthz.
func newAuthzRouter(mode AuthzMode, role policy.Role, logger *slog.Logger) chi.Router {
	root := chi.NewRouter()
	root.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(withRole(role))
			r.Use(RequireAuthz(mode, logger))
			r.Post("/reports/upload/{rid}/{uid}/json", noContent)
			r.Delete("/reports/{id}", noContent)
			r.Get("/artifacts/{id}", noContent)
			for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
				r.MethodFunc(m, "/unlisted", noContent)
			}
		})
	})
	return root
}

func TestRequireAuthz(t *testing.T) {
	const upload = "/api/v1/reports/upload/r1/u1/json"
	cases := []struct {
		mode         AuthzMode
		role         policy.Role
		method, path string
		want         int
	}{
		{AuthzEnforce, policy.RoleUploader, http.MethodPost, upload, http.StatusNoContent},
		{AuthzEnforce, policy.RoleViewer, http.MethodPost, upload, http.StatusForbidden},
		// chi routes on RawPath; the pattern must come from the same match.
		{AuthzEnforce, policy.RoleUploader, http.MethodPost, "/api/v1/reports/upload/a%2Fb/u1/json", http.StatusNoContent},
		{AuthzEnforce, policy.RoleUploader, http.MethodDelete, "/api/v1/reports/r1", http.StatusForbidden},
		{AuthzEnforce, policy.RoleUploader, http.MethodDelete, "/api/v1/reports/a%2Fb", http.StatusForbidden},
		{AuthzEnforce, policy.RoleEditor, http.MethodDelete, "/api/v1/reports/r1", http.StatusForbidden},
		{AuthzEnforce, policy.RoleAdmin, http.MethodDelete, "/api/v1/reports/r1", http.StatusNoContent},
		{AuthzEnforce, policy.RoleViewer, http.MethodGet, "/api/v1/artifacts/a1", http.StatusNoContent},
		{AuthzEnforce, policy.RoleDenied, http.MethodGet, "/api/v1/artifacts/a1", http.StatusForbidden},
		{AuthzLogOnly, policy.RoleViewer, http.MethodDelete, "/api/v1/reports/r1", http.StatusNoContent},
		{AuthzLogOnly, policy.RoleDenied, http.MethodPost, upload, http.StatusNoContent},
		{AuthzOff, "", http.MethodDelete, "/api/v1/reports/r1", http.StatusNoContent},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		newAuthzRouter(c.mode, c.role, discard).ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s role=%q %s %s: status = %d, want %d", c.mode, c.role, c.method, c.path, rec.Code, c.want)
		}
	}
}

// Unlisted write verbs fail closed to admin through the real chi router.
func TestRequireAuthzUnlistedWriteVerbsAdminOnly(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for role, want := range map[policy.Role]int{
			policy.RoleUploader: http.StatusForbidden,
			policy.RoleEditor:   http.StatusForbidden,
			policy.RoleAdmin:    http.StatusNoContent,
		} {
			rec := httptest.NewRecorder()
			newAuthzRouter(AuthzEnforce, role, discard).ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/unlisted", nil))
			if rec.Code != want {
				t.Errorf("%s role=%s: status = %d, want %d", method, role, rec.Code, want)
			}
		}
	}
}

// A nil logger must not panic, and a request without a chi route context
// falls back to the fail-closed defaults.
func TestRequireAuthzNilLoggerAndNoRouteContext(t *testing.T) {
	for _, mode := range []AuthzMode{AuthzEnforce, AuthzLogOnly} {
		want := http.StatusNoContent
		if mode == AuthzEnforce {
			want = http.StatusForbidden
		}
		rec := httptest.NewRecorder()
		newAuthzRouter(mode, policy.RoleViewer, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/reports/r1", nil))
		if rec.Code != want {
			t.Errorf("%s router: status = %d, want %d", mode, rec.Code, want)
		}
	}

	bare := withRole(policy.RoleViewer)(RequireAuthz(AuthzEnforce, nil)(http.HandlerFunc(noContent)))
	for method, want := range map[string]int{http.MethodGet: http.StatusNoContent, http.MethodPost: http.StatusForbidden} {
		rec := httptest.NewRecorder()
		bare.ServeHTTP(rec, httptest.NewRequest(method, "/x", nil))
		if rec.Code != want {
			t.Errorf("no route context %s: status = %d, want %d", method, rec.Code, want)
		}
	}
}

func TestCheckRepoAccess(t *testing.T) {
	oidc := func(repo string) Subject {
		return Subject{Kind: "oidc", Role: policy.RoleUploader, Repository: repo}
	}
	cases := []struct {
		name   string
		mode   AuthzMode
		sub    Subject
		target string
		want   error
	}{
		{"off skips mismatch", AuthzOff, oidc("org/a"), "org/b", nil},
		{"admin bypasses", AuthzEnforce, Subject{Kind: "oidc", Role: policy.RoleAdmin, Repository: "org/a"}, "org/b", nil},
		{"oidc match ignores case and space", AuthzEnforce, oidc("Org/A"), " org/a ", nil},
		{"oidc mismatch enforce", AuthzEnforce, oidc("org/a"), "org/b", apiroot.ErrForbidden},
		{"oidc mismatch log-only", AuthzLogOnly, oidc("org/a"), "org/b", nil},
		{"oidc empty claim, empty target", AuthzEnforce, oidc(""), "", apiroot.ErrForbidden},
		{"oidc empty claim, blank target", AuthzEnforce, oidc(""), " ", apiroot.ErrForbidden},
		{"oidc empty claim, real target", AuthzEnforce, oidc(""), "org/a", apiroot.ErrForbidden},
		{"apikey unbound", AuthzEnforce, Subject{Kind: "apikey", Role: policy.RoleUploader}, "org/b", nil},
		{"session unbound", AuthzEnforce, Subject{Kind: "session", Role: policy.RoleViewer}, "org/b", nil},
	}
	for _, c := range cases {
		if err := CheckRepoAccess(c.mode, discard, c.sub, c.target); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}

	// A nil logger must not panic on a mismatch.
	for _, mode := range []AuthzMode{AuthzEnforce, AuthzLogOnly} {
		if err := CheckRepoAccess(mode, nil, oidc("org/a"), "org/b"); (mode == AuthzEnforce) != (err != nil) {
			t.Errorf("nil logger %s: err = %v", mode, err)
		}
	}
}
