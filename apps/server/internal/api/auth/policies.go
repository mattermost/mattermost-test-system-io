package authapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apiroot "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

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
	if h.AdminKey == "" || r.Header.Get("X-Admin-Key") != h.AdminKey {
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
