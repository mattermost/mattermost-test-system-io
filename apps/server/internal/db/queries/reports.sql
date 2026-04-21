-- name: InsertReport :one
INSERT INTO reports (
    report_group_id, source, commit_sha, branch, status,
    uploaded_by_api_key_id, uploaded_by_oidc_subject, idempotency_key
) VALUES ($1, $2, $3, $4, 'ingesting', $5, $6, $7)
RETURNING *;

-- name: GetReportByID :one
SELECT * FROM reports WHERE id = $1 LIMIT 1;

-- name: ListReportsByGroup :many
SELECT * FROM reports
WHERE ($1::uuid IS NULL OR report_group_id = $1)
ORDER BY created_at DESC
LIMIT $2;

-- name: MarkReportReady :exec
UPDATE reports
SET status = 'ready',
    total_suites = $2,
    total_cases = $3,
    passed_cases = $4,
    failed_cases = $5,
    skipped_cases = $6,
    flaky_cases = $7,
    duration_ms = $8,
    ingested_at = now()
WHERE id = $1;

-- name: MarkReportFailed :exec
UPDATE reports SET status = 'failed', ingest_error = $2 WHERE id = $1;

-- name: DeleteReport :exec
DELETE FROM reports WHERE id = $1;
