-- name: InsertArtifact :one
INSERT INTO artifacts (test_case_id, kind, content_type, object_key, size_bytes, sha256)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetArtifactByID :one
SELECT * FROM artifacts WHERE id = $1 LIMIT 1;

-- name: ListArtifactsByCase :many
SELECT * FROM artifacts WHERE test_case_id = $1 ORDER BY created_at;
