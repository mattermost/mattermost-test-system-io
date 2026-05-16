// Package session manages opaque server-side sessions and single-use rotating
// refresh tokens for human users signed in via GitHub OAuth.
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

// RefreshManager handles single-use rotating refresh tokens.
type RefreshManager struct {
	Pool *pgxpool.Pool
	TTL  time.Duration
}

// ErrReuseDetected is returned when a token that was already used is presented again.
// Callers MUST treat this as an attack signal and revoke the full session chain.
var ErrReuseDetected = errors.New("refresh: reuse of spent token detected")

// Issue mints a new refresh token bound to the given session.
func (m *RefreshManager) Issue(ctx context.Context, sessionID uuid.UUID) (string, error) {
	tok, hash, err := newRefreshToken()
	if err != nil {
		return "", err
	}
	_, err = m.Pool.Exec(ctx,
		`INSERT INTO refresh_tokens (session_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		sessionID, hash, time.Now().UTC().Add(m.TTL))
	if err != nil {
		return "", fmt.Errorf("insert refresh: %w", err)
	}
	return tok, nil
}

// Rotate atomically marks the presented token used, and issues a replacement bound
// to the same session. Returns (newToken, sessionID). On reuse, returns ErrReuseDetected.
func (m *RefreshManager) Rotate(ctx context.Context, presented string) (string, uuid.UUID, error) {
	hash := hashRefresh(presented)
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return "", uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id, sessionID uuid.UUID
	var usedAt *time.Time
	var expiresAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, session_id, used_at, expires_at FROM refresh_tokens WHERE token_hash = $1 LIMIT 1`,
		hash).Scan(&id, &sessionID, &usedAt, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", uuid.Nil, ErrNotFound
	}
	if err != nil {
		return "", uuid.Nil, err
	}
	if usedAt != nil {
		// Revoke the whole session chain as an attack response.
		_, _ = m.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1`, sessionID)
		return "", uuid.Nil, ErrReuseDetected
	}
	if time.Now().After(expiresAt) {
		return "", uuid.Nil, ErrNotFound
	}

	newTok, newHash, err := newRefreshToken()
	if err != nil {
		return "", uuid.Nil, err
	}
	var newID uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO refresh_tokens (session_id, token_hash, expires_at) VALUES ($1,$2,$3) RETURNING id`,
		sessionID, newHash, time.Now().UTC().Add(m.TTL)).Scan(&newID); err != nil {
		return "", uuid.Nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET used_at = now(), replaced_by = $2 WHERE id = $1`,
		id, newID); err != nil {
		return "", uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", uuid.Nil, err
	}
	return newTok, sessionID, nil
}

func newRefreshToken() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	tok := hex.EncodeToString(buf)
	return tok, hashRefresh(tok), nil
}

func hashRefresh(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}
