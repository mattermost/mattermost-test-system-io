package reports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

const (
	uploadKindAPIKey = "apikey"
	uploadKindOIDC   = "oidc"
)

// Token refresh changes iat/jti, not ownership. Bind a CI upload to its verified
// workflow/run/ref instead of the broad repo:ref subject shared by many jobs.
func uploadPrincipal(sub authapi.Subject) (string, error) {
	switch sub.Kind {
	case uploadKindAPIKey:
		return "apikey:" + sub.APIKeyID.String(), nil
	case "session":
		return "session:" + sub.UserID.String(), nil
	case uploadKindOIDC:
		if sub.OIDCClaims == nil {
			return "", api.ErrForbidden
		}
		c := sub.OIDCClaims
		var raw map[string]any
		if err := json.Unmarshal(c.Raw, &raw); err != nil {
			return "", api.ErrForbidden
		}
		parts := []any{c.Issuer, c.Subject, c.Repository, c.Ref}
		for _, key := range []string{"run_id", "run_attempt", "sha", "workflow_ref", "job_workflow_ref"} {
			parts = append(parts, raw[key])
		}
		data, err := json.Marshal(parts)
		if err != nil {
			return "", err
		}
		hash := sha256.Sum256(data)
		return "oidc:" + hex.EncodeToString(hash[:]), nil
	default:
		return "", api.ErrForbidden
	}
}

func persistReportClaims(ctx context.Context, tx pgx.Tx, id uuid.UUID, sub authapi.Subject) error {
	if sub.Kind != uploadKindOIDC || sub.OIDCClaims == nil {
		return nil
	}
	c := sub.OIDCClaims
	_, err := tx.Exec(ctx, `INSERT INTO oidc_claims(report_id,issuer,subject,audience,repository,repository_owner,workflow,ref,environment,raw_claims)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, c.Issuer, c.Subject, c.Audience, c.Repository, c.RepositoryOwner, c.Workflow, c.Ref, c.Environment, c.Raw)
	return err
}

// A matching verified registration cannot vouch for content replaced later by
// another uploader. Authorize before streaming any bytes to object storage.
func requireReportOwner(ctx context.Context, pool *pgxpool.Pool, groupID, reportID uuid.UUID) error {
	sub, err := authapi.SubjectFromContext(ctx)
	if err != nil {
		return api.ErrUnauthorized
	}
	principal, err := uploadPrincipal(sub)
	if err != nil {
		return err
	}
	var stored *string
	var keyID *uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT upload_principal,uploaded_by_api_key_id FROM reports WHERE id=$1 AND report_group_id=$2`, reportID, groupID).Scan(&stored, &keyID); err != nil {
		if err == pgx.ErrNoRows {
			return api.ErrNotFound
		}
		return err
	}
	if stored != nil && *stored == principal {
		return nil
	}
	// Existing API-key uploads retain their original immutable key identity.
	// Legacy OIDC/session rows have no sufficiently specific owner to upgrade.
	if stored == nil && sub.Kind == uploadKindAPIKey && keyID != nil && *keyID == sub.APIKeyID {
		return nil
	}
	return fmt.Errorf("%w: report belongs to a different upload principal", api.ErrForbidden)
}
