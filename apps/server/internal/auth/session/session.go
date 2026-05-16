package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CookieName is the cookie that carries the opaque session token.
const CookieName = "tsio_session"

// TokenPrefix is the literal marker carried at the start of every opaque
// session token. Its purpose is to give secret-scanning tooling a stable,
// recognizable shape so leaks are caught before they propagate. Both Issue
// and Verify use the full prefixed token, so existing tokens minted before
// this prefix existed are intentionally invalidated.
//
//nolint:gosec // G101: public session token prefix for scanners, not a secret.
const TokenPrefix = "tsio_sess_"

// Errors.
var (
	ErrNotFound = errors.New("session: not found or expired")
)

// Session is a server-side authenticated session.
type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	IssuedAt   time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// Manager issues and verifies sessions.
type Manager struct {
	Pool *pgxpool.Pool
	TTL  time.Duration
}

// Issue creates a session row and returns (opaqueCookieToken, Session).
// The caller sets the cookie with HttpOnly, Secure, SameSite=Lax.
func (m *Manager) Issue(ctx context.Context, userID uuid.UUID, ip, userAgent string) (string, Session, error) {
	token, hash, err := newOpaqueToken()
	if err != nil {
		return "", Session{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(m.TTL)

	const q = `
		INSERT INTO sessions (token_hash, user_id, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''))
		RETURNING id, user_id, issued_at, expires_at, last_seen_at
	`
	var s Session
	if err := m.Pool.QueryRow(ctx, q, hash, userID, expires, ip, userAgent).
		Scan(&s.ID, &s.UserID, &s.IssuedAt, &s.ExpiresAt, &s.LastSeenAt); err != nil {
		return "", Session{}, fmt.Errorf("insert session: %w", err)
	}
	return token, s, nil
}

// Verify resolves an opaque cookie value to a live Session. Returns ErrNotFound
// if the session is missing, revoked, or expired.
func (m *Manager) Verify(ctx context.Context, token string) (Session, error) {
	hash := hashToken(token)
	const q = `
		SELECT id, user_id, issued_at, expires_at, last_seen_at
		FROM sessions
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		LIMIT 1
	`
	var s Session
	err := m.Pool.QueryRow(ctx, q, hash).
		Scan(&s.ID, &s.UserID, &s.IssuedAt, &s.ExpiresAt, &s.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	// Best-effort liveness touch.
	_, _ = m.Pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1`, s.ID)
	return s, nil
}

// Revoke marks the session revoked so subsequent Verify calls fail.
func (m *Manager) Revoke(ctx context.Context, id uuid.UUID) error {
	_, err := m.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1`, id)
	return err
}

// newOpaqueToken returns (cookieValue, sha256HexHash). The cookie value is
// the literal TokenPrefix concatenated with 32 hex chars of random material;
// the hash covers the full prefixed string.
func newOpaqueToken() (string, string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	tok := TokenPrefix + hex.EncodeToString(buf)
	return tok, hashToken(tok), nil
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}
