-- name: InsertReportJSONFile :one
INSERT INTO report_json_files (report_id, object_key, size_bytes, sha256)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetReportJSONFileByReport :one
SELECT * FROM report_json_files WHERE report_id = $1 LIMIT 1;
