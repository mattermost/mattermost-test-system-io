package authapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	apiroot "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/apikey"
	authoidc "github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oidc"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/session"
)

// Subject is the resolved authenticated principal.
type Subject struct {
	Kind        string // "apikey" | "oidc" | "session"
	APIKeyID    uuid.UUID
	UserID      uuid.UUID
	OIDCSubject string
	OIDCClaims  *authoidc.Claims
	Role        policy.Role
}

// ErrNotAuthenticated indicates neither an X-API-Key, a Bearer token, nor a
// session cookie was present (or all were invalid).
var ErrNotAuthenticated = errors.New("not authenticated")

type ctxKey string

const subjectKey ctxKey = "auth.subject"

// RequireAuth is a middleware that tries each auth method and rejects with 401
// if none succeeds. Handlers SHOULD call Subject(ctx) after this to read state.
func RequireAuth(
	apiKeys *apikey.Repo,
	sessions *session.Manager,
	oidcV *authoidc.Verifier,
	pol *policy.Engine,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sub, ok := resolve(r, apiKeys, sessions, oidcV, pol)
			if !ok {
				apiroot.WriteError(w, r, apiroot.ErrUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), subjectKey, sub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SubjectFromContext retrieves the resolved subject, or returns ErrNotAuthenticated.
func SubjectFromContext(ctx context.Context) (Subject, error) {
	if s, ok := ctx.Value(subjectKey).(Subject); ok {
		return s, nil
	}
	return Subject{}, ErrNotAuthenticated
}

func resolve(
	r *http.Request,
	apiKeys *apikey.Repo,
	sessions *session.Manager,
	oidcV *authoidc.Verifier,
	pol *policy.Engine,
) (Subject, bool) {
	// 1) X-API-Key
	if raw := r.Header.Get("X-API-Key"); raw != "" {
		prefix, _, ok := apikey.ParsePlaintext(raw)
		if !ok {
			return Subject{}, false
		}
		row, err := apiKeys.ByPrefix(r.Context(), prefix)
		if err != nil || row.Status == apikey.StatusRevoked {
			return Subject{}, false
		}
		if !apikey.Verify(raw, row.KeyHash) {
			return Subject{}, false
		}
		return Subject{Kind: "apikey", APIKeyID: row.ID, Role: policy.RoleUploader}, true
	}

	// 2) Authorization: Bearer <jwt> (GitHub Actions OIDC)
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") && oidcV != nil {
		raw := strings.TrimPrefix(auth, "Bearer ")
		claims, err := oidcV.Verify(r.Context(), raw)
		if err != nil {
			return Subject{}, false
		}
		role, err := pol.Evaluate(r.Context(), claims)
		if err != nil {
			return Subject{}, false
		}
		return Subject{
			Kind:        "oidc",
			OIDCSubject: claims.Subject,
			OIDCClaims:  &claims,
			Role:        role,
		}, true
	}

	// 3) Session cookie (humans)
	if c, err := r.Cookie(session.CookieName); err == nil && sessions != nil {
		sess, err := sessions.Verify(r.Context(), c.Value)
		if err == nil {
			return Subject{Kind: "session", UserID: sess.UserID, Role: sessionRole(r, sessions, sess.UserID)}, true
		}
	}

	return Subject{}, false
}

// sessionRole resolves the persisted authorization role for a signed-in user.
// Unknown or missing roles fall back to viewer (read-only), never elevating.
func sessionRole(r *http.Request, sessions *session.Manager, userID uuid.UUID) policy.Role {
	if sessions == nil || sessions.Pool == nil {
		return policy.RoleViewer
	}
	var role string
	if err := sessions.Pool.QueryRow(r.Context(),
		`SELECT role FROM users WHERE id = $1 LIMIT 1`, userID).Scan(&role); err != nil {
		return policy.RoleViewer
	}
	switch policy.Role(role) {
	case policy.RoleUploader, policy.RoleEditor, policy.RoleAdmin, policy.RoleViewer:
		return policy.Role(role)
	default:
		return policy.RoleViewer
	}
}

// RequireWrite rejects authenticated subjects that lack write capability. It
// MUST run after RequireAuth. Human sessions default to the viewer role and are
// therefore blocked from mutations; CI credentials (API keys and OIDC tokens
// granted an uploader/editor/admin role) pass through.
func RequireWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, err := SubjectFromContext(r.Context())
		if err != nil || !sub.Role.CanWrite() {
			apiroot.WriteError(w, r, apiroot.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}
