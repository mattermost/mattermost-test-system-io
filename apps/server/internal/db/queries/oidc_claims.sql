-- name: InsertOIDCClaims :one
INSERT INTO oidc_claims (
    report_id, issuer, subject, audience,
    repository, repository_owner, workflow, ref, environment, raw_claims
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: ListOIDCClaimsBySubject :many
SELECT * FROM oidc_claims WHERE subject = $1 ORDER BY verified_at DESC LIMIT $2;
