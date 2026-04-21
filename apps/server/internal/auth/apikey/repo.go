package apikey

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status is the apikey row status.
type Status string

// API key lifecycle statuses.
const (
	StatusActive   Status = "active"
	StatusRotating Status = "rotating"
	StatusRevoked  Status = "revoked"
)

// Errors surfaced by the repo.
var (
	ErrNotFound = errors.New("apikey: not found")
	ErrRevoked  = errors.New("apikey: revoked")
	ErrInvalid  = errors.New("apikey: invalid credential")
)

// Row is the api_keys table row we need in Go.
type Row struct {
	ID         uuid.UUID
	Name       string
	KeyPrefix  string
	KeyHash    string
	Status     Status
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Repo is the storage interface for api_keys.
type Repo struct {
	Pool *pgxpool.Pool
}

// Insert stores a newly-issued key.
func (r *Repo) Insert(ctx context.Context, name string, iss Issued) (Row, error) {
	const q = `
		INSERT INTO api_keys (name, key_prefix, key_hash, status)
		VALUES ($1, $2, $3, 'active')
		RETURNING id, name, key_prefix, key_hash, status, created_at, last_used_at, revoked_at
	`
	return scanRow(r.Pool.QueryRow(ctx, q, name, iss.Prefix, iss.Hash))
}

// ByPrefix narrows an authentication lookup by prefix.
func (r *Repo) ByPrefix(ctx context.Context, prefix string) (Row, error) {
	const q = `
		SELECT id, name, key_prefix, key_hash, status, created_at, last_used_at, revoked_at
		FROM api_keys WHERE key_prefix = $1 LIMIT 1
	`
	row, err := scanRow(r.Pool.QueryRow(ctx, q, prefix))
	if errors.Is(err, pgx.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	return row, err
}

// List returns all keys, optionally filtered by status.
func (r *Repo) List(ctx context.Context, statusFilter *Status) ([]Row, error) {
	const q = `
		SELECT id, name, key_prefix, key_hash, status, created_at, last_used_at, revoked_at
		FROM api_keys
		WHERE ($1::text IS NULL OR status = $1::text)
		ORDER BY created_at DESC
	`
	var sf *string
	if statusFilter != nil {
		s := string(*statusFilter)
		sf = &s
	}
	rows, err := r.Pool.Query(ctx, q, sf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// TouchLastUsed updates the last_used_at column; best-effort (ignores errors at the call site if desired).
func (r *Repo) TouchLastUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.Pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, id)
	return err
}

// MarkRotating sets status=rotating; no-op if already rotating/revoked.
func (r *Repo) MarkRotating(ctx context.Context, id uuid.UUID) error {
	_, err := r.Pool.Exec(ctx,
		`UPDATE api_keys SET status = 'rotating' WHERE id = $1 AND status = 'active'`, id)
	return err
}

// Revoke marks a key revoked.
func (r *Repo) Revoke(ctx context.Context, id uuid.UUID) error {
	_, err := r.Pool.Exec(ctx,
		`UPDATE api_keys SET status = 'revoked', revoked_at = now() WHERE id = $1`, id)
	return err
}

type rowScanner interface {
	Scan(dst ...any) error
}

func scanRow(s rowScanner) (Row, error) {
	var r Row
	var status string
	if err := s.Scan(&r.ID, &r.Name, &r.KeyPrefix, &r.KeyHash, &status, &r.CreatedAt, &r.LastUsedAt, &r.RevokedAt); err != nil {
		return Row{}, err
	}
	r.Status = Status(status)
	return r, nil
}
