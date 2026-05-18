// Package authapi mounts the /api/v1/auth/* endpoints and the RequireAuth
// middleware dispatcher that accepts api-key, OIDC bearer, or session cookie.
package authapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	apiroot "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oauth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/session"
)

const (
	oauthStateCookie = "tsio_oauth_state"
	refreshCookie    = "tsio_refresh"
)

// Handlers wires the /auth/* endpoints.
type Handlers struct {
	Flow              *oauth.Flow
	Sessions          *session.Manager
	Refresher         *session.RefreshManager
	Pool              *pgxpool.Pool
	PostLoginRedirect string // where to send the browser after successful sign-in

	// AdminKey gates POST /admin/oidc-policies and any other /admin/* endpoints
	// added in the future. When empty those endpoints always return 401.
	AdminKey string
}

// StartRedirect serves GET /api/v1/auth/github. Redirects the browser to the
// GitHub authorization URL. The React client hits this via window.location.
func (h *Handlers) StartRedirect(w http.ResponseWriter, r *http.Request) {
	if h.Flow == nil {
		apiroot.WriteErrorCode(w, http.StatusServiceUnavailable, "OAUTH_DISABLED", "GitHub OAuth is not configured")
		return
	}
	authorizeURL, state, err := h.Flow.Start()
	if err != nil {
		apiroot.WriteError(w, r, apiroot.ErrInternal)
		return
	}
	h.setStateCookie(w, state)
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// Start serves POST /api/v1/auth/github/start. Returns the authorize URL in JSON
// so programmatic clients can drive the flow themselves.
func (h *Handlers) Start(w http.ResponseWriter, r *http.Request) {
	if h.Flow == nil {
		apiroot.WriteErrorCode(w, http.StatusServiceUnavailable, "OAUTH_DISABLED", "GitHub OAuth is not configured")
		return
	}
	authorizeURL, state, err := h.Flow.Start()
	if err != nil {
		apiroot.WriteError(w, r, apiroot.ErrInternal)
		return
	}
	h.setStateCookie(w, state)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"authorize_url": authorizeURL,
		"state":         state,
	})
}

// Callback handles the GitHub redirect back to us.
func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request) {
	if h.Flow == nil {
		apiroot.WriteErrorCode(w, http.StatusServiceUnavailable, "OAUTH_DISABLED", "GitHub OAuth is not configured")
		return
	}
	if h.Sessions == nil {
		apiroot.WriteErrorCode(w, http.StatusServiceUnavailable, "SESSION_DISABLED", "session manager is not configured")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		apiroot.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "missing code or state")
		return
	}
	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil || cookie.Value != state {
		apiroot.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid oauth state")
		return
	}

	_, gu, err := h.Flow.Exchange(r.Context(), code)
	if err != nil {
		apiroot.WriteErrorCode(w, http.StatusBadRequest, "OAUTH_EXCHANGE_FAILED", "oauth exchange failed")
		return
	}
	u, err := oauth.Upsert(r.Context(), h.Pool, *gu)
	if err != nil {
		apiroot.WriteError(w, r, apiroot.ErrInternal)
		return
	}

	cookieVal, sess, err := h.Sessions.Issue(r.Context(), u.ID, r.RemoteAddr, r.UserAgent())
	if err != nil {
		apiroot.WriteError(w, r, apiroot.ErrInternal)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    cookieVal,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(h.Sessions.TTL),
	})

	if h.Refresher != nil {
		if refreshTok, err := h.Refresher.Issue(r.Context(), sess.ID); err == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     refreshCookie,
				Value:    refreshTok,
				Path:     "/api/v1/auth/",
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteLaxMode,
				Expires:  time.Now().Add(h.Refresher.TTL),
			})
		}
	}

	// Expire the oauth state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:   oauthStateCookie,
		Value:  "",
		Path:   "/api/v1/auth/",
		MaxAge: -1,
	})

	redirect := h.PostLoginRedirect
	if redirect == "" {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}

// Me serves GET /api/v1/auth/me. Returns {user: null} for anonymous callers and
// {user: {...}} for signed-in sessions. Never returns 401 — the React client
// treats 401 as a hard error and this endpoint is polled unconditionally.
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"user": user})
}

// Refresh serves POST /api/v1/auth/refresh. Rotates the refresh token and issues
// a fresh session cookie.
func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) {
	if h.Refresher == nil || h.Sessions == nil {
		apiroot.WriteError(w, r, apiroot.ErrSessionExpired)
		return
	}
	c, err := r.Cookie(refreshCookie)
	if err != nil || c.Value == "" {
		apiroot.WriteError(w, r, apiroot.ErrSessionExpired)
		return
	}
	newTok, sessionID, err := h.Refresher.Rotate(r.Context(), c.Value)
	if err != nil {
		apiroot.WriteError(w, r, apiroot.ErrSessionExpired)
		return
	}

	// Issue a fresh session token for the same user. We look up the session's
	// user_id from the existing row so the new session cookie stays bound to
	// the same principal.
	var userID uuid.UUID
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT user_id FROM sessions WHERE id = $1 AND revoked_at IS NULL LIMIT 1`,
		sessionID).Scan(&userID); err != nil {
		apiroot.WriteError(w, r, apiroot.ErrSessionExpired)
		return
	}
	sessionVal, _, err := h.Sessions.Issue(r.Context(), userID, r.RemoteAddr, r.UserAgent())
	if err != nil {
		apiroot.WriteError(w, r, apiroot.ErrInternal)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    sessionVal,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(h.Sessions.TTL),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookie,
		Value:    newTok,
		Path:     "/api/v1/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(h.Refresher.TTL),
	})
	w.WriteHeader(http.StatusNoContent)
}

// Logout clears the current session.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	// Session-side revoke is a best-effort step; if the session manager isn't
	// wired (partial deployment, test harness) the cookie wipe still happens.
	if h.Sessions != nil {
		c, err := r.Cookie(session.CookieName)
		if err == nil {
			if sess, err := h.Sessions.Verify(r.Context(), c.Value); err == nil {
				_ = h.Sessions.Revoke(r.Context(), sess.ID)
			}
		}
	}
	http.SetCookie(w, &http.Cookie{Name: session.CookieName, Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: "", Path: "/api/v1/auth/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

// currentUser resolves the /auth/me response body. Returns nil when the caller
// is anonymous or the session is invalid.
func (h *Handlers) currentUser(r *http.Request) map[string]any {
	if h.Sessions == nil {
		return nil
	}
	c, err := r.Cookie(session.CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	sess, err := h.Sessions.Verify(r.Context(), c.Value)
	if err != nil {
		return nil
	}
	const q = `
		SELECT id, github_login, display_name, avatar_url, role
		FROM users WHERE id = $1 LIMIT 1
	`
	var (
		id          string
		login       string
		displayName *string
		avatarURL   *string
		role        string
	)
	if err := h.Pool.QueryRow(r.Context(), q, sess.UserID).
		Scan(&id, &login, &displayName, &avatarURL, &role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return nil
	}
	out := map[string]any{
		"id":       id,
		"username": login,
		"role":     role,
	}
	if displayName != nil {
		out["display_name"] = *displayName
	}
	if avatarURL != nil {
		out["avatar_url"] = *avatarURL
	}
	return out
}

func (h *Handlers) setStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/api/v1/auth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(10 * time.Minute),
	})
}
