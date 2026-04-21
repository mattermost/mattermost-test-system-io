-- name: InsertRefreshToken :one
INSERT INTO refresh_tokens (session_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens WHERE token_hash = $1 LIMIT 1;

-- name: MarkRefreshTokenUsed :exec
UPDATE refresh_tokens SET used_at = now(), replaced_by = $2 WHERE id = $1;
