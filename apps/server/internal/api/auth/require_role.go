package authapi

import (
	"net/http"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
)

// WriteRoles may upload reports, drive shard orchestration and delete reports. A viewer — a browser
// session, an OIDC policy granting viewer, or a viewer API key — authenticates but cannot write.
var WriteRoles = []policy.Role{policy.RoleUploader, policy.RoleEditor, policy.RoleAdmin}

// ReadRoles may use the protected reads (reads too costly to leave anonymous). CI's uploader keys
// write only, so a leaked CI key can't use them.
var ReadRoles = []policy.Role{policy.RoleViewer, policy.RoleEditor, policy.RoleAdmin}

// RequireRole rejects a request whose authenticated subject has none of the allowed roles: 403 for a
// subject with another role, 401 when RequireAuth didn't run or found no subject.
func RequireRole(allowed ...policy.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sub, err := SubjectFromContext(r.Context())
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !slices.Contains(allowed, sub.Role) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ParseKeyRole parses the role of an API key. Keys are uploader (CI), viewer (reads), editor or admin;
// "denied" exists only for OIDC policies.
func ParseKeyRole(s string) (policy.Role, bool) {
	switch r := policy.Role(strings.ToLower(strings.TrimSpace(s))); r {
	case policy.RoleUploader, policy.RoleViewer, policy.RoleEditor, policy.RoleAdmin:
		return r, true
	default:
		return "", false
	}
}
