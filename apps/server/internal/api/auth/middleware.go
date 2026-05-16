package authapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

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
				http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	// Each block returns the resolved Subject when its credential validates;
	// invalid credentials fall through to the next method so a stale
	// X-API-Key alongside a valid session cookie still authenticates.
	// "None of the presented credentials worked" lands on the final
	// `return false` at the bottom of the function (mapped to 401 by the
	// caller).

	// 1) X-API-Key
	if raw := r.Header.Get("X-API-Key"); raw != "" && apiKeys != nil {
		if prefix, _, ok := apikey.ParsePlaintext(raw); ok {
			row, err := apiKeys.ByPrefix(r.Context(), prefix)
			if err == nil && row.Status != apikey.StatusRevoked && apikey.Verify(raw, row.KeyHash) {
				return Subject{Kind: "apikey", APIKeyID: row.ID, Role: policy.RoleUploader}, true
			}
		}
	}

	// 2) Authorization: Bearer <jwt> (GitHub Actions OIDC)
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") && oidcV != nil && pol != nil {
		raw := strings.TrimPrefix(auth, "Bearer ")
		if claims, err := oidcV.Verify(r.Context(), raw); err == nil {
			if role, err := pol.Evaluate(r.Context(), claims); err == nil {
				return Subject{
					Kind:        "oidc",
					OIDCSubject: claims.Subject,
					OIDCClaims:  &claims,
					Role:        role,
				}, true
			}
		}
	}

	// 3) Session cookie (humans)
	if c, err := r.Cookie(session.CookieName); err == nil && sessions != nil {
		if sess, err := sessions.Verify(r.Context(), c.Value); err == nil {
			return Subject{Kind: "session", UserID: sess.UserID, Role: policy.RoleViewer}, true
		}
	}

	return Subject{}, false
}
