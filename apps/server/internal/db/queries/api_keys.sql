-- name: InsertAPIKey :one
INSERT INTO api_keys (name, key_prefix, key_hash, status)
VALUES ($1, $2, $3, 'active')
RETURNING *;

-- name: GetAPIKeyByPrefix :one
SELECT * FROM api_keys WHERE key_prefix = $1 LIMIT 1;

-- name: GetAPIKeyByID :one
SELECT * FROM api_keys WHERE id = $1 LIMIT 1;

-- name: ListAPIKeys :many
SELECT * FROM api_keys
WHERE ($1::text IS NULL OR status = $1)
ORDER BY created_at DESC;

-- name: TouchAPIKeyLastUsed :exec
UPDATE api_keys SET last_used_at = now() WHERE id = $1;

-- name: MarkAPIKeyRotating :exec
UPDATE api_keys SET status = 'rotating' WHERE id = $1 AND status = 'active';

-- name: RevokeAPIKey :exec
UPDATE api_keys SET status = 'revoked', revoked_at = now() WHERE id = $1;
