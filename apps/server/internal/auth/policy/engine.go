// Package policy evaluates GitHub Actions OIDC claims against a configurable
// ruleset to decide authorization.
package policy

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oidc"
)

// Role is a resolved authorization outcome.
type Role string

// Authorization roles granted by policy rules.
const (
	RoleUploader Role = "uploader"
	RoleViewer   Role = "viewer"
	RoleEditor   Role = "editor"
	RoleAdmin    Role = "admin"
	RoleDenied   Role = "denied"
)

// ErrDenied is returned when no matching enabled rule grants access.
var ErrDenied = errors.New("policy: denied")

// Rule is the in-memory representation of a github_oidc_policies row.
type Rule struct {
	ID                   uuid.UUID
	Name                 string
	Enabled              bool
	Priority             int
	MatchRepository      *string
	MatchRepositoryOwner *string
	MatchWorkflow        *string
	MatchRef             *string
	MatchEnvironment     *string
	GrantRole            Role
}

// Engine loads rules from Postgres and evaluates claims.
type Engine struct {
	Pool *pgxpool.Pool
}

// Evaluate loads enabled rules and evaluates them against claims.
func (e *Engine) Evaluate(ctx context.Context, c oidc.Claims) (Role, error) {
	rules, err := e.load(ctx)
	if err != nil {
		return "", err
	}
	return Match(rules, c)
}

// Match applies rules (already ordered by priority) to claims and returns the
// first granted role, or ErrDenied. Exported for testability — no DB access.
func Match(rules []Rule, c oidc.Claims) (Role, error) {
	for _, r := range rules {
		if matchStr(r.MatchRepository, c.Repository) &&
			matchStr(r.MatchRepositoryOwner, c.RepositoryOwner) &&
			matchStr(r.MatchWorkflow, c.Workflow) &&
			matchStr(r.MatchRef, c.Ref) &&
			matchStr(r.MatchEnvironment, c.Environment) {
			if r.GrantRole == RoleDenied {
				return "", ErrDenied
			}
			return r.GrantRole, nil
		}
	}
	return "", ErrDenied
}

func (e *Engine) load(ctx context.Context) ([]Rule, error) {
	const q = `
		SELECT id, name, enabled, priority,
		       match_repository, match_repository_owner, match_workflow, match_ref, match_environment,
		       grant_role
		FROM github_oidc_policies
		WHERE enabled = true
		ORDER BY priority ASC
	`
	rows, err := e.Pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		var grant string
		if err := rows.Scan(
			&r.ID, &r.Name, &r.Enabled, &r.Priority,
			&r.MatchRepository, &r.MatchRepositoryOwner, &r.MatchWorkflow, &r.MatchRef, &r.MatchEnvironment,
			&grant,
		); err != nil {
			return nil, err
		}
		r.GrantRole = Role(grant)
		out = append(out, r)
	}
	return out, rows.Err()
}

// matchStr returns true if rule is nil (wildcard) or equal to value.
// Future: extend to regex/glob matching when a use case emerges.
func matchStr(rule *string, value string) bool {
	if rule == nil {
		return true
	}
	return *rule == value
}
