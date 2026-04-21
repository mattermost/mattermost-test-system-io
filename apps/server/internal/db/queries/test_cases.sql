-- name: InsertTestCase :one
INSERT INTO test_cases (
    suite_id, title, status, retry_count, duration_ms,
    error_message, error_stack, annotations, ordinal
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListTestCasesBySuite :many
SELECT * FROM test_cases WHERE suite_id = $1 ORDER BY ordinal;

-- name: ListTestCasesByReport :many
SELECT tc.*
FROM test_cases tc
JOIN suites s ON s.id = tc.suite_id
WHERE s.report_id = $1
  AND ($2::text IS NULL OR tc.status = $2)
ORDER BY s.ordinal, tc.ordinal;
