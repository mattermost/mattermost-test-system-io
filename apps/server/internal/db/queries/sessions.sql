-- name: InsertSession :one
INSERT INTO sessions (token_hash, user_id, expires_at, ip, user_agent)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now() LIMIT 1;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = $1;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = now() WHERE id = $1;
