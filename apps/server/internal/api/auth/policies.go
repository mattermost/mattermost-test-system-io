package authapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	apiroot "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// pgErrUniqueViolation is the SQLSTATE code for unique_violation. Defined here
// so callers can map it to a 409 without pulling pgconn into every package.
const pgErrUniqueViolation = "23505"

// CreateOIDCPolicy serves POST /api/v1/auth/oidc-policies. Admin-key gated
// (X-Admin-Key header). Installs a github_oidc_policies row from a
// repository_pattern shorthand.
//
// Body: {repository_pattern, role, description?}.
//
// Pattern handling:
//
//	"owner/*"      → match_repository_owner=owner
//	"owner/repo"   → match_repository="owner/repo"
//	"*"            → no matchers (allow everything; dev only)
//
// Role aliases: contributor→uploader; viewer/editor/admin pass through; anything
// else returns 400.
func (h *Handlers) CreateOIDCPolicy(w http.ResponseWriter, r *http.Request) {
	if h.AdminKey == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Key")), []byte(h.AdminKey)) != 1 {
		apiroot.WriteError(w, r, apiroot.ErrUnauthorized)
		return
	}
	var body struct {
		RepositoryPattern string  `json:"repository_pattern"`
		Role              string  `json:"role"`
		Description       *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apiroot.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	role, ok := mapPolicyRole(body.Role)
	if !ok {
		apiroot.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
			"role must be uploader|contributor|viewer|editor|admin")
		return
	}
	matchRepo, matchOwner, ok := parseRepositoryPattern(body.RepositoryPattern)
	if !ok {
		apiroot.WriteErrorCode(w, http.StatusBadRequest, "BAD_REQUEST",
			"repository_pattern must be 'owner/repo', 'owner/*', or '*'")
		return
	}
	name := body.RepositoryPattern
	if name == "" {
		name = "policy"
	}

	id, err := insertOIDCPolicy(r.Context(), h.Pool, name, matchRepo, matchOwner, role)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
			apiroot.WriteErrorCode(w, http.StatusConflict, "ALREADY_EXISTS",
				"policy with this repository_pattern already exists")
			return
		}
		apiroot.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":   id.String(),
		"role": role,
	})
}

func insertOIDCPolicy(ctx context.Context, pool *pgxpool.Pool, name string, matchRepo, matchOwner *string, role string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO github_oidc_policies (name, enabled, priority,
		    match_repository, match_repository_owner, match_workflow, match_ref, match_environment,
		    grant_role)
		VALUES ($1, true, 100, $2, $3, NULL, NULL, NULL, $4)
		RETURNING id
	`, name, matchRepo, matchOwner, role).Scan(&id)
	return id, err
}

func parseRepositoryPattern(p string) (matchRepo, matchOwner *string, ok bool) {
	p = strings.TrimSpace(p)
	if p == "" || p == "*" {
		return nil, nil, true
	}
	if strings.HasSuffix(p, "/*") {
		owner := strings.TrimSuffix(p, "/*")
		if owner == "" || strings.ContainsRune(owner, '/') {
			return nil, nil, false
		}
		return nil, &owner, true
	}
	if strings.Count(p, "/") == 1 {
		s := p
		return &s, nil, true
	}
	return nil, nil, false
}

func mapPolicyRole(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "contributor", "uploader":
		return "uploader", true
	case "viewer":
		return "viewer", true
	case "editor":
		return "editor", true
	case "admin":
		return "admin", true
	default:
		return "", false
	}
}

// BootstrapPolicies parses the comma-separated `pattern=role` list (typically
// from TSIO_BOOTSTRAP_OIDC_POLICIES) and inserts each as a github_oidc_policies
// row. ON CONFLICT DO NOTHING on `name`, so callers may invoke this on every
// app startup safely; existing rows are not modified. Designed for ephemeral
// staging stacks where the database is recreated on each deploy and the org-
// wide CI policy needs to be re-seeded.
//
// Empty input is a no-op. Malformed entries log a warning and are skipped so a
// single typo never blocks startup.
func BootstrapPolicies(ctx context.Context, pool *pgxpool.Pool, spec string, logger *slog.Logger) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	for _, raw := range strings.Split(spec, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		eq := strings.LastIndex(entry, "=")
		if eq < 1 || eq == len(entry)-1 {
			logger.Warn("bootstrap oidc policy: malformed entry, skipping",
				slog.String("entry", entry))
			continue
		}
		pattern := strings.TrimSpace(entry[:eq])
		roleRaw := strings.TrimSpace(entry[eq+1:])
		role, ok := mapPolicyRole(roleRaw)
		if !ok {
			logger.Warn("bootstrap oidc policy: unknown role, skipping",
				slog.String("pattern", pattern),
				slog.String("role", roleRaw))
			continue
		}
		matchRepo, matchOwner, ok := parseRepositoryPattern(pattern)
		if !ok {
			logger.Warn("bootstrap oidc policy: malformed pattern, skipping",
				slog.String("pattern", pattern))
			continue
		}
		if err := upsertOIDCPolicy(ctx, pool, pattern, matchRepo, matchOwner, role); err != nil {
			return fmt.Errorf("bootstrap oidc policy %q=%q: %w", pattern, role, err)
		}
		logger.Info("bootstrap oidc policy ensured",
			slog.String("pattern", pattern),
			slog.String("role", role))
	}
	return nil
}

// upsertOIDCPolicy inserts or no-ops on `name` conflict. Distinct from the
// HTTP-handler insertOIDCPolicy which intentionally fails on duplicate so the
// caller learns about the collision.
func upsertOIDCPolicy(ctx context.Context, pool *pgxpool.Pool, name string, matchRepo, matchOwner *string, role string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO github_oidc_policies (name, enabled, priority,
		    match_repository, match_repository_owner, match_workflow, match_ref, match_environment,
		    grant_role)
		VALUES ($1, true, 100, $2, $3, NULL, NULL, NULL, $4)
		ON CONFLICT (name) DO NOTHING
	`, name, matchRepo, matchOwner, role)
	return err
}
