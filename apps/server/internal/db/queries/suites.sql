-- name: InsertSuite :one
INSERT INTO suites (report_id, parent_suite_id, title, file, line, col, duration_ms, ordinal)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListSuitesByReport :many
SELECT * FROM suites WHERE report_id = $1 ORDER BY parent_suite_id NULLS FIRST, ordinal;
