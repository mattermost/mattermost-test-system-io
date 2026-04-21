-- name: InsertReportGroup :one
INSERT INTO report_groups (slug, display_name, description)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetReportGroupByID :one
SELECT * FROM report_groups WHERE id = $1 LIMIT 1;

-- name: GetReportGroupBySlug :one
SELECT * FROM report_groups WHERE slug = $1 LIMIT 1;

-- name: ListReportGroups :many
SELECT * FROM report_groups ORDER BY created_at DESC;
