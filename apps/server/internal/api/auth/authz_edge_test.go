package authapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
)

var discard = slog.New(slog.DiscardHandler)

// newAuthzRouterWithLogger is newAuthzRouter with a caller-chosen logger and
// every unlisted write verb registered.
func newAuthzRouterWithLogger(mode AuthzMode, role policy.Role, logger *slog.Logger) chi.Router {
	root := chi.NewRouter()
	root.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					ctx := context.WithValue(req.Context(), subjectKey, Subject{Kind: "apikey", Role: role})
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
			r.Use(RequireAuthz(mode, logger, root))
			ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
			r.Delete("/reports/{id}", ok)
			for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
				r.MethodFunc(m, "/unlisted", ok)
			}
		})
	})
	return root
}

// An OIDC token with an empty repository claim can never match a target repo,
// including an empty or whitespace-only one.
func TestCheckRepoAccessEmptyOIDCRepository(t *testing.T) {
	sub := Subject{Kind: "oidc", Role: policy.RoleUploader}
	for _, target := range []string{"", " ", "org/a"} {
		if err := CheckRepoAccess(AuthzEnforce, discard, sub, target); err == nil {
			t.Errorf("target %q: empty-claim oidc token was allowed", target)
		}
	}
}

// Unlisted write verbs fail closed to admin through the real chi router.
func TestRequireAuthzUnlistedWriteVerbsAdminOnly(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodPost} {
		for role, want := range map[policy.Role]int{
			policy.RoleUploader: http.StatusForbidden,
			policy.RoleEditor:   http.StatusForbidden,
			policy.RoleAdmin:    http.StatusNoContent,
		} {
			rec := httptest.NewRecorder()
			newAuthzRouterWithLogger(AuthzEnforce, role, discard).
				ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/unlisted", nil))
			if rec.Code != want {
				t.Errorf("%s role=%s: status = %d, want %d", method, role, rec.Code, want)
			}
		}
	}
}

// A nil logger must not panic when a check fails: server.Build tolerates a nil
// Deps.Logger elsewhere, so the authz path has to as well.
func TestRequireAuthzNilLoggerDoesNotPanic(t *testing.T) {
	for _, mode := range []AuthzMode{AuthzEnforce, AuthzLogOnly} {
		root := newAuthzRouterWithLogger(mode, policy.RoleViewer, nil)
		rec := httptest.NewRecorder()
		root.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/reports/r1", nil))
		want := http.StatusNoContent
		if mode == AuthzEnforce {
			want = http.StatusForbidden
		}
		if rec.Code != want {
			t.Errorf("%s: status = %d, want %d", mode, rec.Code, want)
		}
	}
}

func TestCheckRepoAccessNilLoggerDoesNotPanic(t *testing.T) {
	sub := Subject{Kind: "oidc", Role: policy.RoleUploader, Repository: "org/a"}
	for _, mode := range []AuthzMode{AuthzEnforce, AuthzLogOnly} {
		if err := CheckRepoAccess(mode, nil, sub, "org/b"); (mode == AuthzEnforce) != (err != nil) {
			t.Errorf("%s: err = %v", mode, err)
		}
	}
}
