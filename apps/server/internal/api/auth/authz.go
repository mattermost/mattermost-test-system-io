package authapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	apiroot "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
)

// AuthzMode controls what a failed role or repository check does.
type AuthzMode string

// Authorization modes (TSIO_AUTHZ_MODE).
const (
	AuthzOff     AuthzMode = "off"      // skip checks
	AuthzLogOnly AuthzMode = "log-only" // log failures, allow the request
	AuthzEnforce AuthzMode = "enforce"  // log failures, reject with 403
)

// RouteMinRole is the minimum role for every route behind RequireAuth, keyed
// "METHOD pattern" as chi reports the full pattern. server's parity test keeps
// it in sync with the router.
var RouteMinRole = map[string]policy.Role{
	"GET /api/v1/artifacts/{id}": policy.RoleViewer,

	"POST /api/v1/reports":                                policy.RoleUploader,
	"POST /api/v1/reports/begin":                          policy.RoleUploader,
	"POST /api/v1/reports/register":                       policy.RoleUploader,
	"POST /api/v1/reports/upload/{rid}/{uid}/json":        policy.RoleUploader,
	"POST /api/v1/reports/upload/{rid}/{uid}/screenshots": policy.RoleUploader,

	"POST /api/v1/orchestration/begin":       policy.RoleUploader,
	"POST /api/v1/orchestration/checkout":    policy.RoleUploader,
	"POST /api/v1/orchestration/complete":    policy.RoleUploader,
	"POST /api/v1/orchestration/no-work":     policy.RoleUploader,
	"POST /api/v1/orchestration/screenshots": policy.RoleUploader,

	"DELETE /api/v1/reports/{id}": policy.RoleAdmin,
}

// MinRoleForRoute returns the minimum role for a route and whether it is
// listed. Unlisted routes fail closed: writes need admin, reads need viewer.
func MinRoleForRoute(method, pattern string) (policy.Role, bool) {
	if role, ok := RouteMinRole[method+" "+pattern]; ok {
		return role, true
	}
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return policy.RoleAdmin, false
	}
	return policy.RoleViewer, false
}

// RequireAuthz checks the subject's role against MinRoleForRoute. Mount it
// right after RequireAuth on an r.Group: group middleware wraps the matched
// endpoint, so chi's route context already holds the full pattern.
func RequireAuthz(mode AuthzMode, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mode == AuthzOff {
				next.ServeHTTP(w, r)
				return
			}
			var pattern string
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				pattern = rctx.RoutePattern()
			}
			need, _ := MinRoleForRoute(r.Method, pattern)
			sub, _ := SubjectFromContext(r.Context())
			if !sub.Role.AtLeast(need) {
				logger.Warn("authz: role below route minimum",
					slog.String("mode", string(mode)),
					slog.String("route", r.Method+" "+pattern),
					slog.String("subject_kind", sub.Kind),
					slog.String("role", string(sub.Role)),
					slog.String("required_role", string(need)))
				if mode == AuthzEnforce {
					apiroot.WriteError(w, r, apiroot.ErrForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func trimGitSuffix(repo string) string {
	repo = strings.TrimSpace(repo)
	if len(repo) > 4 && strings.EqualFold(repo[len(repo)-4:], ".git") {
		return repo[:len(repo)-4]
	}
	return repo
}

// CheckRepoAccess binds OIDC tokens to their repository. Admins and AuthzOff
// bypass. A mismatch is logged and, in AuthzEnforce, returns
// apiroot.ErrForbidden. Call it before any DB write.
func CheckRepoAccess(mode AuthzMode, logger *slog.Logger, sub Subject, targetRepo string) error {
	if mode == AuthzOff || sub.Role == policy.RoleAdmin || sub.Kind != "oidc" {
		return nil
	}
	subRepo := trimGitSuffix(sub.Repository)
	target := trimGitSuffix(targetRepo)
	if subRepo != "" && strings.EqualFold(subRepo, target) {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("authz: token repository mismatch",
		slog.String("mode", string(mode)),
		slog.String("subject_kind", sub.Kind),
		slog.String("token_repository", sub.Repository),
		slog.String("target_repository", targetRepo))
	if mode == AuthzEnforce {
		return apiroot.ErrForbidden
	}
	return nil
}
