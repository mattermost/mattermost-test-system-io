-- name: ListEnabledOIDCPolicies :many
SELECT * FROM github_oidc_policies WHERE enabled = true ORDER BY priority ASC;

-- name: ListOIDCPolicies :many
SELECT * FROM github_oidc_policies ORDER BY priority ASC;

-- name: GetOIDCPolicyByID :one
SELECT * FROM github_oidc_policies WHERE id = $1 LIMIT 1;

-- name: InsertOIDCPolicy :one
INSERT INTO github_oidc_policies (
    name, enabled, priority,
    match_repository, match_repository_owner, match_workflow, match_ref, match_environment,
    grant_role
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateOIDCPolicy :one
UPDATE github_oidc_policies
SET name = $2, enabled = $3, priority = $4,
    match_repository = $5, match_repository_owner = $6,
    match_workflow = $7, match_ref = $8, match_environment = $9,
    grant_role = $10, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteOIDCPolicy :exec
DELETE FROM github_oidc_policies WHERE id = $1;
