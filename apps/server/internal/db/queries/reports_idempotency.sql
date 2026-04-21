-- name: ClaimIdempotencyKey :one
-- Tries to insert the idempotency mapping. If the key already exists, returns
-- the existing report_id. The caller decides whether the row was freshly claimed
-- by comparing report_id against what it just inserted.
INSERT INTO reports_idempotency (idempotency_key, report_id)
VALUES ($1, $2)
ON CONFLICT (idempotency_key) DO UPDATE SET report_id = reports_idempotency.report_id
RETURNING report_id;

-- name: GetIdempotencyMapping :one
SELECT * FROM reports_idempotency WHERE idempotency_key = $1 LIMIT 1;
