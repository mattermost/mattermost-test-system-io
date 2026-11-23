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

// newAuthzRouter mirrors server.Build: a /api/v1 subrouter whose protected
// group injects role (in place of RequireAuth) and then runs RequireAuthz.
func newAuthzRouter(mode AuthzMode, role policy.Role) chi.Router {
	root := chi.NewRouter()
	root.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					ctx := context.WithValue(req.Context(), subjectKey, Subject{Kind: "apikey", Role: role})
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
			r.Use(RequireAuthz(mode, slog.New(slog.DiscardHandler), root))
			ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
			r.Post("/reports/upload/{rid}/{uid}/json", ok)
			r.Delete("/reports/{id}", ok)
			r.Get("/artifacts/{id}", ok)
			r.Post("/unlisted", ok)
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
		{AuthzEnforce, policy.RoleUploader, http.MethodDelete, "/api/v1/reports/r1", http.StatusForbidden},
		{AuthzEnforce, policy.RoleEditor, http.MethodDelete, "/api/v1/reports/r1", http.StatusForbidden},
		{AuthzEnforce, policy.RoleAdmin, http.MethodDelete, "/api/v1/reports/r1", http.StatusNoContent},
		{AuthzEnforce, policy.RoleEditor, http.MethodPost, "/api/v1/unlisted", http.StatusForbidden},
		{AuthzEnforce, policy.RoleAdmin, http.MethodPost, "/api/v1/unlisted", http.StatusNoContent},
		{AuthzEnforce, policy.RoleViewer, http.MethodGet, "/api/v1/artifacts/a1", http.StatusNoContent},
		{AuthzEnforce, policy.RoleDenied, http.MethodGet, "/api/v1/artifacts/a1", http.StatusForbidden},
		{AuthzLogOnly, policy.RoleViewer, http.MethodDelete, "/api/v1/reports/r1", http.StatusNoContent},
		{AuthzLogOnly, policy.RoleDenied, http.MethodPost, upload, http.StatusNoContent},
		{AuthzOff, "", http.MethodDelete, "/api/v1/reports/r1", http.StatusNoContent},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		newAuthzRouter(c.mode, c.role).ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s role=%q %s %s: status = %d, want %d", c.mode, c.role, c.method, c.path, rec.Code, c.want)
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
		{"oidc without repository fails", AuthzEnforce, oidc(""), "", apiroot.ErrForbidden},
		{"apikey without repository unbound", AuthzEnforce, Subject{Kind: "apikey", Role: policy.RoleUploader}, "org/b", nil},
		{"apikey with repository bound", AuthzEnforce, Subject{Kind: "apikey", Role: policy.RoleUploader, Repository: "org/a"}, "org/b", apiroot.ErrForbidden},
		{"session unbound", AuthzEnforce, Subject{Kind: "session", Role: policy.RoleViewer}, "org/b", nil},
	}
	for _, c := range cases {
		err := CheckRepoAccess(c.mode, slog.New(slog.DiscardHandler), c.sub, c.target)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}
